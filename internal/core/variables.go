package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/store"
)

// Shared variables (ADR-0057): named values that aren't secret, which
// catalog admins keep and composite steps and conditions read as
// vars.<group>.<name> each time they run, as vRO's configuration elements.

var (
	variablePart = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	variableRef  = regexp.MustCompile(`\bvars\.([a-z][a-z0-9_]*)\.([a-z][a-z0-9_]*)`)
	// A nudge, not a guard: what a name says it holds belongs in a secret,
	// which steps get masked and the run data never shows.
	secretLike = regexp.MustCompile(`password|passwd|passphrase|secret|private_key|api_key|apikey`)
)

const maxVariableValue = 16 << 10

func presentVariable(v store.Variable, users map[string][]string) api.Variable {
	key := v.Group + "." + v.Name
	out := api.Variable{Key: key, Group: v.Group, Name: v.Name, Description: v.Description, UsedBy: nonNilStrings(users[key]),
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, UpdatedBy: v.UpdatedByName}
	_ = json.Unmarshal(v.Value, &out.Value)
	return out
}

// Variables lists the shared variables, with the items that read each.
func (s *Service) Variables(ctx context.Context, p *auth.Principal) ([]api.Variable, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return nil, ErrForbidden
	}
	list, err := s.Store.Variables(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.Store.VariableUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Variable, len(list))
	for i, v := range list {
		out[i] = presentVariable(v, users)
	}
	return out, nil
}

func (s *Service) Variable(ctx context.Context, p *auth.Principal, key string) (api.Variable, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.Variable{}, ErrForbidden
	}
	return s.variable(ctx, key)
}

func (s *Service) variable(ctx context.Context, key string) (api.Variable, error) {
	group, name, _ := strings.Cut(key, ".")
	v, err := s.Store.GetVariable(ctx, group, name)
	if errors.Is(err, store.ErrNotFound) {
		return api.Variable{}, ErrNotFound
	} else if err != nil {
		return api.Variable{}, err
	}
	users, err := s.Store.VariableUsers(ctx)
	if err != nil {
		return api.Variable{}, err
	}
	return presentVariable(v, users), nil
}

// CreateVariable adds a shared variable.
func (s *Service) CreateVariable(ctx context.Context, p *auth.Principal, w api.VariableCreate) (api.Variable, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.Variable{}, ErrForbidden
	}
	v := store.Variable{Group: strings.TrimSpace(w.Group), Name: strings.TrimSpace(w.Name)}
	var errs []api.FieldError
	for _, part := range []struct{ ptr, value string }{{"/group", v.Group}, {"/name", v.Name}} {
		switch {
		case !variablePart.MatchString(part.value):
			errs = append(errs, fieldErr(part.ptr, "pattern", "1 to 41 lower-case letters, digits and underscores, starting with a letter: steps read vars.<group>.<name>."))
		case secretLike.MatchString(part.value):
			errs = append(errs, fieldErr(part.ptr, "secret", "A variable isn't masked anywhere: store a password, a token or a key as a secret instead."))
		}
	}
	errs = append(errs, checkVariable(&v, w.Value, w.Description)...)
	if len(errs) > 0 {
		return api.Variable{}, &ValidationError{Errors: errs}
	}
	key := v.Group + "." + v.Name
	switch err := s.Store.AddVariable(ctx, v, p.User.ID); {
	case errors.Is(err, store.ErrInUse):
		return api.Variable{}, &ConflictError{Msg: "There is a variable " + key + " already: change its value instead."}
	case err != nil:
		return api.Variable{}, err
	}
	s.audit(ctx, p, "variable.create", "variable", key, nil, map[string]any{"value": json.RawMessage(v.Value)})
	return s.variable(ctx, key)
}

// UpdateVariable changes a shared variable's value and description; steps
// that run from then on read the new value.
func (s *Service) UpdateVariable(ctx context.Context, p *auth.Principal, key string, w api.VariableUpdate) (api.Variable, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.Variable{}, ErrForbidden
	}
	group, name, _ := strings.Cut(key, ".")
	old, err := s.Store.GetVariable(ctx, group, name)
	if errors.Is(err, store.ErrNotFound) {
		return api.Variable{}, ErrNotFound
	} else if err != nil {
		return api.Variable{}, err
	}
	v := store.Variable{Group: group, Name: name}
	if errs := checkVariable(&v, w.Value, w.Description); len(errs) > 0 {
		return api.Variable{}, &ValidationError{Errors: errs}
	}
	if err := s.Store.SetVariable(ctx, v, p.User.ID); errors.Is(err, store.ErrNotFound) {
		return api.Variable{}, ErrNotFound
	} else if err != nil {
		return api.Variable{}, err
	}
	s.audit(ctx, p, "variable.update", "variable", key, nil, map[string]any{"value": json.RawMessage(v.Value), "was": json.RawMessage(old.Value)})
	return s.variable(ctx, key)
}

// checkVariable checks and sets a variable's value and description.
func checkVariable(v *store.Variable, value any, description *string) []api.FieldError {
	var errs []api.FieldError
	raw, err := json.Marshal(value)
	switch {
	case value == nil:
		errs = append(errs, fieldErr("/value", "required", "Give it a value: text, a number, true or false, a list or an object."))
	case err != nil:
		errs = append(errs, fieldErr("/value", "invalid", "Not a value JSON can hold."))
	case len(raw) > maxVariableValue:
		errs = append(errs, fieldErr("/value", "too_long", fmt.Sprintf("At most %d KiB of JSON: keep files elsewhere.", maxVariableValue>>10)))
	}
	v.Value, v.Description = raw, strings.TrimSpace(deref(description))
	if utf8.RuneCountInString(v.Description) > 500 {
		errs = append(errs, fieldErr("/description", "too_long", "At most 500 characters."))
	}
	return errs
}

// DeleteVariable removes a shared variable no item reads.
func (s *Service) DeleteVariable(ctx context.Context, p *auth.Principal, key string) error {
	if !p.Everywhere(auth.CatalogManage) {
		return ErrForbidden
	}
	group, name, _ := strings.Cut(key, ".")
	if _, err := s.Store.GetVariable(ctx, group, name); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	users, err := s.Store.VariableUsers(ctx)
	if err != nil {
		return err
	}
	if u := users[key]; len(u) > 0 {
		return &ConflictError{Msg: "It is read by " + strings.Join(u, ", ") + ": change their steps first."}
	}
	if err := s.Store.DeleteVariable(ctx, group, name); err != nil {
		return err
	}
	s.audit(ctx, p, "variable.delete", "variable", key, nil, nil)
	return nil
}

// variableVars are the shared variables as steps read them, by group and name.
func (s *Service) variableVars(ctx context.Context) (map[string]any, error) {
	return s.Store.VariableValues(ctx)
}

// checkVariableRefs checks, to save or publish a composite, that the
// shared variables its steps read exist.
func (s *Service) checkVariableRefs(ctx context.Context, d api.CatalogVersionDefinition, prefix string) ([]api.FieldError, error) {
	raw, _ := json.Marshal(d.Blueprint)
	b, err := blueprint.Parse(raw)
	if err != nil || len(b.Steps) == 0 {
		return nil, nil
	}
	var values map[string]any
	var errs []api.FieldError
	var failed error
	eachStep(b, prefix+"/blueprint", func(st blueprint.CompositeStep, at string) {
		text, _ := json.Marshal(withoutChildren(st))
		refs := variableRef.FindAllStringSubmatch(string(text), -1)
		if len(refs) == 0 || failed != nil {
			return
		}
		if values == nil {
			if values, failed = s.variableVars(ctx); failed != nil {
				return
			}
		}
		seen := map[string]bool{}
		for _, m := range refs {
			group, _ := values[m[1]].(map[string]any)
			if _, ok := group[m[2]]; ok || seen[m[0]] {
				continue
			}
			seen[m[0]] = true
			errs = append(errs, fieldErr(at, "unknown_variable", "There is no variable "+m[0]+": add it under Administration > Variables."))
		}
	})
	return errs, failed
}

// withoutChildren is a step without the steps of its parallel group or
// branch sides, which eachStep visits on their own.
func withoutChildren(st blueprint.CompositeStep) blueprint.CompositeStep {
	st.Parallel = nil
	if st.Branch != nil {
		b := blueprint.Branch{Cases: make([]blueprint.BranchCase, len(st.Branch.Cases))}
		for i, c := range st.Branch.Cases {
			c.Steps = nil
			b.Cases[i] = c
		}
		st.Branch = &b
	}
	return st
}
