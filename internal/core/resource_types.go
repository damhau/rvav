package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// Custom resource types (ADR-0056): administrators define them; auditors
// read them.

func presentResourceType(t store.ResourceType, use store.ResourceTypeUse) api.ResourceType {
	out := api.ResourceType{Key: t.Key, Title: t.Title, Create: presentDriver(t.Lifecycle.Create), Delete: presentDriver(t.Lifecycle.Delete),
		Actions: nonNilStrings(t.Actions), Enabled: t.Enabled, Resources: use.Resources, UsedBy: nonNilStrings(use.UsedBy),
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, PropertySchema: map[string]any{}}
	if t.Description != "" {
		out.Description = &t.Description
	}
	if d := t.Lifecycle.Update; d != nil {
		out.Update = new(presentDriver(*d))
	}
	if d := t.Lifecycle.Read; d != nil {
		out.Read = new(presentDriver(*d))
	}
	_ = json.Unmarshal(t.PropertySchema, &out.PropertySchema)
	return out
}

func presentDriver(d store.ResourceDriver) api.ResourceTypeDriver {
	return api.ResourceTypeDriver{Kind: api.ResourceTypeDriverKind(d.Kind), Name: d.Name}
}

// ResourceTypes lists the types, with what uses them.
func (s *Service) ResourceTypes(ctx context.Context, p *auth.Principal) ([]api.ResourceType, error) {
	if !p.Everywhere(auth.ExtensibilityRead) {
		return nil, ErrForbidden
	}
	list, err := s.Store.ResourceTypes(ctx)
	if err != nil {
		return nil, err
	}
	uses, err := s.Store.ResourceTypeUses(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.ResourceType, len(list))
	for i, t := range list {
		out[i] = presentResourceType(t, uses[t.Key])
	}
	return out, nil
}

func (s *Service) GetResourceType(ctx context.Context, p *auth.Principal, key string) (api.ResourceType, error) {
	if !p.Everywhere(auth.ExtensibilityRead) {
		return api.ResourceType{}, ErrForbidden
	}
	return s.resourceType(ctx, key)
}

func (s *Service) resourceType(ctx context.Context, key string) (api.ResourceType, error) {
	t, err := s.Store.GetResourceType(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return api.ResourceType{}, ErrNotFound
	} else if err != nil {
		return api.ResourceType{}, err
	}
	uses, err := s.Store.ResourceTypeUses(ctx)
	if err != nil {
		return api.ResourceType{}, err
	}
	return presentResourceType(t, uses[key]), nil
}

// SaveResourceType defines a type (create) or changes one; its key never
// changes.
func (s *Service) SaveResourceType(ctx context.Context, p *auth.Principal, key string, create bool, w api.ResourceTypeWrite) (api.ResourceType, error) {
	if !p.Everywhere(auth.ExtensibilityManage) {
		return api.ResourceType{}, ErrForbidden
	}
	var errs []api.FieldError
	if create {
		key = strings.TrimSpace(deref(w.Key))
		if !workflowKey.MatchString(key) {
			errs = append(errs, fieldErr("/key", "pattern", "2 to 41 lower-case letters, digits and dashes, starting with a letter."))
		} else if _, err := s.Store.GetResourceType(ctx, key); err == nil {
			return api.ResourceType{}, &ConflictError{Msg: "A resource type is defined as " + key + "."}
		} else if !errors.Is(err, store.ErrNotFound) {
			return api.ResourceType{}, err
		}
	} else if _, err := s.Store.GetResourceType(ctx, key); errors.Is(err, store.ErrNotFound) {
		return api.ResourceType{}, ErrNotFound
	} else if err != nil {
		return api.ResourceType{}, err
	}
	t := store.ResourceType{Key: key, Title: strings.TrimSpace(w.Title), Description: strings.TrimSpace(deref(w.Description)),
		Enabled: w.Enabled == nil || *w.Enabled, PropertySchema: json.RawMessage(`{"type": "object"}`)}
	if t.Title == "" {
		errs = append(errs, fieldErr("/title", "required", "Give it a title."))
	}
	drivers := []struct {
		phase string
		in    *api.ResourceTypeDriver
		out   **store.ResourceDriver
	}{{"create", &w.Create, nil}, {"delete", &w.Delete, nil}, {"update", w.Update, &t.Lifecycle.Update}, {"read", w.Read, &t.Lifecycle.Read}}
	for _, d := range drivers {
		if d.in == nil {
			continue
		}
		driver, derrs, err := s.checkDriver(ctx, "/"+d.phase, *d.in)
		if err != nil {
			return api.ResourceType{}, err
		}
		errs = append(errs, derrs...)
		switch d.phase {
		case "create":
			t.Lifecycle.Create = driver
		case "delete":
			t.Lifecycle.Delete = driver
		default:
			*d.out = &driver
		}
	}
	if w.PropertySchema != nil {
		raw, _ := json.Marshal(*w.PropertySchema)
		if err := checkJSONSchema(raw); err != nil {
			errs = append(errs, fieldErr("/property_schema", "invalid", "Not a JSON Schema rvav can use: "+err.Error()))
		} else if typ, ok := (*w.PropertySchema)["type"]; ok && typ != "object" {
			errs = append(errs, fieldErr("/property_schema/type", "invalid", "A resource's properties are an object."))
		}
		t.PropertySchema = raw
	}
	actions, aerrs, err := s.checkTypeActions(ctx, derefSlice(w.Actions))
	if err != nil {
		return api.ResourceType{}, err
	}
	t.Actions, errs = actions, append(errs, aerrs...)
	if len(errs) > 0 {
		return api.ResourceType{}, &ValidationError{Errors: errs}
	}
	if err := s.Store.SaveResourceType(ctx, t); err != nil {
		return api.ResourceType{}, err
	}
	action := map[bool]string{true: "resource_type.create", false: "resource_type.update"}[create]
	s.audit(ctx, p, action, "resource_type", key, nil, map[string]any{"create": t.Lifecycle.Create, "delete": t.Lifecycle.Delete,
		"actions": t.Actions, "enabled": t.Enabled})
	return s.resourceType(ctx, key)
}

// checkDriver checks what runs a step: a custom workflow registered for
// resource types, or a job template AWX has.
func (s *Service) checkDriver(ctx context.Context, ptr string, d api.ResourceTypeDriver) (store.ResourceDriver, []api.FieldError, error) {
	out := store.ResourceDriver{Kind: string(d.Kind), Name: strings.TrimSpace(d.Name)}
	if out.Name == "" {
		return out, []api.FieldError{fieldErr(ptr+"/name", "required", "Name the custom workflow or the AWX template.")}, nil
	}
	switch d.Kind {
	case api.ResourceDriverWorkflow:
		_, why, err := s.usableWorkflow(ctx, out.Name, "resource_type", "")
		if err != nil || why == "" {
			return out, nil, err
		}
		return out, []api.FieldError{fieldErr(ptr+"/name", "unknown", why)}, nil
	case api.ResourceDriverAwx:
		if s.AWX == nil {
			return out, nil, nil
		}
		if _, err := s.AWX.LookupJobTemplate(ctx, out.Name, false); err != nil {
			return out, []api.FieldError{fieldErr(ptr+"/name", "unknown", "AWX has no job template "+out.Name+".")}, nil
		}
		return out, nil, nil
	}
	return out, []api.FieldError{fieldErr(ptr+"/kind", "enum", "workflow or awx.")}, nil
}

// checkTypeActions checks the day-2 actions a type offers: custom workflows
// registered as day-2 actions and AWX actions rvav knows.
func (s *Service) checkTypeActions(ctx context.Context, keys []string) ([]string, []api.FieldError, error) {
	var out []string
	var errs []api.FieldError
	for i, k := range keys {
		k = strings.TrimSpace(k)
		ptr := "/actions/" + strconv.Itoa(i)
		if slices.Contains(out, k) {
			errs = append(errs, fieldErr(ptr, "duplicate", "Listed twice."))
			continue
		}
		out = append(out, k)
		if wf, ok := strings.CutPrefix(k, "wf."); ok {
			w, err := s.Store.GetWorkflowRegistration(ctx, wf)
			switch {
			case errors.Is(err, store.ErrNotFound):
				errs = append(errs, fieldErr(ptr, "unknown", "There is no custom workflow "+wf+" registered."))
			case err != nil:
				return nil, nil, err
			default:
				if _, ok := workflowDef(w); !ok {
					errs = append(errs, fieldErr(ptr, "unknown", "The custom workflow "+wf+" isn't registered as a day-2 action."))
				}
			}
			continue
		}
		if !strings.HasPrefix(k, "awx.") {
			errs = append(errs, fieldErr(ptr, "invalid", "A custom workflow (wf.<key>) or an AWX action (awx.<template>)."))
			continue
		}
		a, err := s.Store.GetAWXAction(ctx, k)
		switch {
		case errors.Is(err, store.ErrNotFound) || err == nil && a.Gone:
			errs = append(errs, fieldErr(ptr, "unknown", "There is no AWX action "+k+"."))
		case err != nil:
			return nil, nil, err
		}
	}
	return out, errs, nil
}

// DeleteResourceType removes a type no item uses and no deployment has.
func (s *Service) DeleteResourceType(ctx context.Context, p *auth.Principal, key string) error {
	if !p.Everywhere(auth.ExtensibilityManage) {
		return ErrForbidden
	}
	if _, err := s.Store.GetResourceType(ctx, key); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	uses, err := s.Store.ResourceTypeUses(ctx)
	if err != nil {
		return err
	}
	switch u := uses[key]; {
	case u.Resources > 0:
		return &ConflictError{Msg: "Deployments have " + plural(u.Resources, "resource") + " of it: disable it instead, and remove it once they are deleted."}
	case len(u.UsedBy) > 0:
		return &ConflictError{Msg: "It is used by " + strings.Join(u.UsedBy, ", ") + ": disable it instead."}
	}
	if err := s.Store.DeleteResourceType(ctx, key); err != nil {
		return err
	}
	s.audit(ctx, p, "resource_type.delete", "resource_type", key, nil, nil)
	return nil
}
