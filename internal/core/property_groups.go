package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// Property groups (ADR-0038): reusable inputs and constants, a new version
// at each save. An item's blueprint names a version of each in
// property_groups; GetVersion puts their inputs in its form, and bindings
// read their constants as propgroup.<key>.<name>.

var (
	groupKey  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{1,62}$`)
	groupProp = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)
)

// ListPropertyGroups lists the property groups at their latest version.
func (s *Service) ListPropertyGroups(ctx context.Context, p *auth.Principal) (api.PropertyGroupList, error) {
	out := api.PropertyGroupList{Items: []api.PropertyGroup{}}
	if !p.Everywhere(auth.CatalogManage) {
		return out, ErrForbidden
	}
	list, err := s.Store.PropertyGroups(ctx)
	if err != nil {
		return out, err
	}
	for _, g := range list {
		pg, err := s.presentGroup(ctx, g)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, pg)
	}
	return out, nil
}

// PropertyGroup gives a property group at a version, its latest when
// version is 0.
func (s *Service) PropertyGroup(ctx context.Context, p *auth.Principal, key string, version int) (api.PropertyGroup, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.PropertyGroup{}, ErrForbidden
	}
	g, err := s.Store.GetPropertyGroup(ctx, key, version)
	if errors.Is(err, store.ErrNotFound) {
		return api.PropertyGroup{}, ErrNotFound
	} else if err != nil {
		return api.PropertyGroup{}, err
	}
	return s.presentGroup(ctx, g)
}

// PutPropertyGroup adds a property group (create, its key in w) or saves
// one as its next version.
func (s *Service) PutPropertyGroup(ctx context.Context, p *auth.Principal, key string, w api.PropertyGroupWrite, create bool) (api.PropertyGroup, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.PropertyGroup{}, ErrForbidden
	}
	if create {
		key = deref(w.Key)
	}
	var errs []api.FieldError
	if create && !groupKey.MatchString(key) {
		errs = append(errs, fieldErr("/key", "pattern", "Letters, digits and underscores, starting with a letter: bindings read it as propgroup.<key>."))
	}
	if !create && w.Key != nil && *w.Key != key {
		errs = append(errs, fieldErr("/key", "invalid", "A property group's key doesn't change."))
	}
	if errs = append(errs, checkGroup(w)...); len(errs) > 0 {
		return api.PropertyGroup{}, &ValidationError{Errors: errs}
	}
	inputs, _ := json.Marshal(w.Inputs)
	constants, _ := json.Marshal(w.Constants)
	g := store.PropertyGroup{Key: key, Name: strings.TrimSpace(w.Name), Description: deref(w.Description), Inputs: inputs, Constants: constants}
	version, err := s.Store.AddPropertyGroupVersion(ctx, g, create, p.User.ID)
	switch {
	case errors.Is(err, store.ErrInUse) && create:
		return api.PropertyGroup{}, &ConflictError{Msg: fmt.Sprintf("The property group %q exists: save it as a new version instead.", key)}
	case errors.Is(err, store.ErrInUse):
		return api.PropertyGroup{}, &ConflictError{Msg: "This property group was saved elsewhere at the same time: open it again to see that version."}
	case errors.Is(err, store.ErrNotFound):
		return api.PropertyGroup{}, ErrNotFound
	case err != nil:
		return api.PropertyGroup{}, err
	}
	action := "property_group.update"
	if create {
		action = "property_group.create"
	}
	s.auditGroup(ctx, p, action, key, map[string]any{"version": version})
	return s.PropertyGroup(ctx, p, key, version)
}

// DeletePropertyGroup deletes a property group, every version of it, when
// no item's version but a draft names it.
func (s *Service) DeletePropertyGroup(ctx context.Context, p *auth.Principal, key string) error {
	if !p.Everywhere(auth.CatalogManage) {
		return ErrForbidden
	}
	users, err := s.Store.PropertyGroupUsers(ctx, key)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return &ConflictError{Msg: "Items use this property group: " + strings.Join(users, ", ") + "."}
	}
	if err := s.Store.DeletePropertyGroup(ctx, key); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.auditGroup(ctx, p, "property_group.delete", key, nil)
	return nil
}

func (s *Service) auditGroup(ctx context.Context, p *auth.Principal, action, key string, detail map[string]any) {
	if err := s.Store.Audit(ctx, s.Store.Pool, store.AuditEvent{ActorID: &p.User.ID, Action: action, TargetType: "property_group",
		TargetID: key, Detail: detail}); err != nil {
		s.Log.Error("audit", "action", action, "err", err)
	}
}

func (s *Service) presentGroup(ctx context.Context, g store.PropertyGroup) (api.PropertyGroup, error) {
	out := api.PropertyGroup{Key: g.Key, Name: g.Name, Description: g.Description, Version: g.Version,
		Inputs: []api.PropertyGroupInput{}, Constants: map[string]any{}, UpdatedAt: g.CreatedAt}
	if err := json.Unmarshal(g.Inputs, &out.Inputs); err != nil {
		return out, err
	}
	if err := json.Unmarshal(g.Constants, &out.Constants); err != nil {
		return out, err
	}
	var err error
	if out.Versions, err = s.Store.PropertyGroupVersions(ctx, g.Key); err != nil {
		return out, err
	}
	if out.UsedBy, err = s.Store.PropertyGroupUsers(ctx, g.Key); err != nil {
		return out, err
	}
	if out.UsedBy == nil {
		out.UsedBy = []string{}
	}
	return out, nil
}

// checkGroup checks a property group's inputs and constants.
func checkGroup(w api.PropertyGroupWrite) []api.FieldError {
	var errs []api.FieldError
	add := func(ptr, code, msg string) { errs = append(errs, fieldErr(ptr, code, msg)) }
	if strings.TrimSpace(w.Name) == "" {
		add("/name", "required", "Give the property group a name.")
	}
	names := map[string]bool{}
	for i, in := range w.Inputs {
		at := fmt.Sprintf("/inputs/%d", i)
		switch {
		case !groupProp.MatchString(in.Name):
			add(at+"/name", "pattern", "Letters, digits and underscores, starting with a letter: bindings read it as inputs.<name>.")
		case names[in.Name]:
			add(at+"/name", "duplicate", fmt.Sprintf("The group has %s twice.", in.Name))
		}
		names[in.Name] = true
		if strings.TrimSpace(in.Label) == "" {
			add(at+"/label", "required", "Give the input a label.")
		}
		str := in.Type == api.PropertyGroupInputTypeString
		switch in.Type {
		case api.PropertyGroupInputTypeString, api.PropertyGroupInputTypeInteger, api.PropertyGroupInputTypeNumber, api.PropertyGroupInputTypeBoolean:
		default:
			add(at+"/type", "invalid", "An input is a string, an integer, a number or a boolean.")
		}
		options := derefSlice(in.Options)
		if len(options) > 0 && !str {
			add(at+"/options", "invalid", "Only a string input is chosen from options.")
		}
		secret := in.Secret != nil && *in.Secret
		if secret && (!str || len(options) > 0) {
			add(at+"/secret", "invalid", "A secret input is a string typed in, without options.")
		}
		if secret && in.ExposeToInventory != nil && *in.ExposeToInventory {
			add(at+"/expose_to_inventory", "invalid", "A secret input never goes to the AWX inventory.")
		}
		if in.Default == nil {
			continue
		}
		switch {
		case secret:
			add(at+"/default", "invalid", "A secret input has no default: the group would hold its value.")
		case !ofType(in.Default, in.Type):
			add(at+"/default", "invalid", fmt.Sprintf("The default must be a %s.", in.Type))
		case len(options) > 0 && !slices.Contains(options, fmt.Sprint(in.Default)):
			add(at+"/default", "invalid", "The default must be one of the options.")
		}
	}
	for _, k := range sortedKeys(w.Constants) {
		at := "/constants/" + k
		if !groupProp.MatchString(k) {
			add(at, "pattern", "Letters, digits and underscores, starting with a letter: bindings read it as propgroup.<key>.<name>.")
			continue
		}
		switch w.Constants[k].(type) {
		case string, float64, bool:
		default:
			add(at, "invalid", "A constant is text, a number or a boolean.")
		}
	}
	return errs
}

func ofType(v any, t api.PropertyGroupInputType) bool {
	switch x := v.(type) {
	case string:
		return t == api.PropertyGroupInputTypeString
	case bool:
		return t == api.PropertyGroupInputTypeBoolean
	case float64:
		return t == api.PropertyGroupInputTypeNumber || t == api.PropertyGroupInputTypeInteger && x == math.Trunc(x)
	}
	return false
}

// checkGroupRefs checks the property groups a definition's blueprint
// names: each exists at its version, once, and adds no input the form or
// another group has.
func (s *Service) checkGroupRefs(ctx context.Context, d api.CatalogVersionDefinition, prefix string) ([]api.FieldError, error) {
	raw, ok := d.Blueprint["property_groups"]
	if !ok || raw == nil {
		return nil, nil
	}
	ptr := prefix + "/blueprint/property_groups"
	list, ok := raw.([]any)
	if !ok {
		return []api.FieldError{fieldErr(ptr, "invalid", "A list of the property groups the item uses, each a key and a version.")}, nil
	}
	var errs []api.FieldError
	props, _ := d.FormSchema["properties"].(map[string]any)
	from := map[string]string{}
	keys := map[string]bool{}
	for i, e := range list {
		at := fmt.Sprintf("%s/%d", ptr, i)
		ref, _ := e.(map[string]any)
		key, _ := ref["key"].(string)
		version, whole := wholeNumber(ref["version"])
		if key == "" || !whole || version < 1 || len(ref) != 2 {
			errs = append(errs, fieldErr(at, "invalid", "Name the group and its version, for example {key: network, version: 2}."))
			continue
		}
		if keys[key] {
			errs = append(errs, fieldErr(at+"/key", "duplicate", fmt.Sprintf("The blueprint names %s twice.", key)))
			continue
		}
		keys[key] = true
		g, err := s.Store.GetPropertyGroup(ctx, key, version)
		if errors.Is(err, store.ErrNotFound) {
			latest, err := s.Store.GetPropertyGroup(ctx, key, 0)
			switch {
			case errors.Is(err, store.ErrNotFound):
				errs = append(errs, fieldErr(at+"/key", "unknown", fmt.Sprintf("There is no property group %q.", key)))
			case err != nil:
				return nil, err
			default:
				errs = append(errs, fieldErr(at+"/version", "unknown", fmt.Sprintf("Property group %s has no version %d: its latest is %d.", key, version, latest.Version)))
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		var inputs []store.GroupInput
		_ = json.Unmarshal(g.Inputs, &inputs)
		for _, in := range inputs {
			switch other, twice := from[in.Name]; {
			case props[in.Name] != nil:
				errs = append(errs, fieldErr(at, "duplicate", fmt.Sprintf("%s is an input of the form and of property group %s: rename one.", in.Name, key)))
			case twice:
				errs = append(errs, fieldErr(at, "duplicate", fmt.Sprintf("%s is an input of property groups %s and %s: the item can use one of them.", in.Name, other, key)))
			}
			from[in.Name] = key
		}
	}
	return errs, nil
}

// groupFields are the UI fields of the inputs of the property groups d's
// blueprint names, as GetVersion adds them to its form. A group that isn't
// there adds none: checkGroupRefs says so.
func (s *Service) groupFields(ctx context.Context, d api.CatalogVersionDefinition) (map[string]api.UiField, error) {
	raw, _ := json.Marshal(d.Blueprint)
	var groups []store.PropertyGroup
	for _, r := range store.GroupRefs(raw) {
		if r.Version < 1 {
			continue
		}
		g, err := s.Store.GetPropertyGroup(ctx, r.Key, r.Version)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	if len(groups) == 0 {
		return nil, nil
	}
	var ui api.UiSchema
	if err := json.Unmarshal(store.WithGroups(store.ItemVersion{}, groups).UISchema, &ui); err != nil {
		return nil, err
	}
	return ui.Fields, nil
}

// wholeNumber reads a JSON or YAML number that is a whole one.
func wholeNumber(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), n == math.Trunc(n) && math.Abs(n) < 1e9
	case int:
		return n, true
	case int64:
		return int(n), true
	case uint64:
		return int(n), n < 1e9
	}
	return 0, false
}
