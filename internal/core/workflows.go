package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// Custom workflows (ADR-0022): administrators register them; auditors read
// the registrations.

var (
	workflowKey  = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	workflowType = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*\.v[0-9]+$`)
	usages       = []string{"catalog_item", "day2_action", "subscription_target", "composite_step"}
)

func presentWorkflow(w store.WorkflowRegistration, use store.WorkflowUse) api.WorkflowRegistration {
	out := api.WorkflowRegistration{Key: w.Key, Title: w.Title, WorkflowType: w.WorkflowType, TaskQueue: w.TaskQueue,
		TimeoutSeconds: w.TimeoutSeconds, ProjectIds: nonNilStrings(w.ProjectIDs), Enabled: w.Enabled,
		Running: use.Running, UsedBy: nonNilStrings(use.UsedBy), CreatedAt: w.CreatedAt, UpdatedAt: &w.UpdatedAt,
		InputSchema: map[string]any{}, Usages: make([]api.WorkflowUsage, len(w.Usages))}
	if w.Description != "" {
		out.Description = &w.Description
	}
	_ = json.Unmarshal(w.InputSchema, &out.InputSchema)
	for i, u := range w.Usages {
		out.Usages[i] = api.WorkflowUsage(u)
	}
	if len(w.Day2) > 0 && string(w.Day2) != "null" {
		var d api.WorkflowDay2
		if json.Unmarshal(w.Day2, &d) == nil {
			out.Day2 = &d
		}
	}
	return out
}

// WorkflowRegistrations lists the registrations, with what uses them.
func (s *Service) WorkflowRegistrations(ctx context.Context, p *auth.Principal) ([]api.WorkflowRegistration, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	list, err := s.Store.WorkflowRegistrations(ctx)
	if err != nil {
		return nil, err
	}
	uses, err := s.Store.WorkflowUses(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.WorkflowRegistration, len(list))
	for i, w := range list {
		out[i] = presentWorkflow(w, uses[w.Key])
	}
	return out, nil
}

func (s *Service) GetWorkflowRegistration(ctx context.Context, p *auth.Principal, key string) (api.WorkflowRegistration, error) {
	if !p.SeesEverything() {
		return api.WorkflowRegistration{}, ErrForbidden
	}
	w, err := s.Store.GetWorkflowRegistration(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return api.WorkflowRegistration{}, ErrNotFound
	} else if err != nil {
		return api.WorkflowRegistration{}, err
	}
	uses, err := s.Store.WorkflowUses(ctx)
	if err != nil {
		return api.WorkflowRegistration{}, err
	}
	return presentWorkflow(w, uses[key]), nil
}

// SaveWorkflow registers a workflow (create) or changes a registration;
// its key never changes.
func (s *Service) SaveWorkflowRegistration(ctx context.Context, p *auth.Principal, key string, create bool, w api.WorkflowRegistrationWrite) (api.WorkflowRegistration, error) {
	if !p.IsAdmin() {
		return api.WorkflowRegistration{}, ErrForbidden
	}
	var errs []api.FieldError
	if create {
		key = strings.TrimSpace(deref(w.Key))
		if !workflowKey.MatchString(key) {
			errs = append(errs, fieldErr("/key", "pattern", "2 to 41 lower-case letters, digits and dashes, starting with a letter."))
		} else if _, err := s.Store.GetWorkflowRegistration(ctx, key); err == nil {
			return api.WorkflowRegistration{}, &ConflictError{Msg: "A workflow is registered as " + key + "."}
		} else if !errors.Is(err, store.ErrNotFound) {
			return api.WorkflowRegistration{}, err
		}
	} else if _, err := s.Store.GetWorkflowRegistration(ctx, key); errors.Is(err, store.ErrNotFound) {
		return api.WorkflowRegistration{}, ErrNotFound
	} else if err != nil {
		return api.WorkflowRegistration{}, err
	}
	reg := store.WorkflowRegistration{Key: key, Title: strings.TrimSpace(w.Title), Description: deref(w.Description),
		WorkflowType: strings.TrimSpace(w.WorkflowType), TaskQueue: strings.TrimSpace(w.TaskQueue), TimeoutSeconds: 30 * 24 * 3600,
		ProjectIDs: derefSlice(w.ProjectIds), Enabled: w.Enabled == nil || *w.Enabled, InputSchema: json.RawMessage(`{"type": "object"}`)}
	if w.TimeoutSeconds != nil {
		reg.TimeoutSeconds = *w.TimeoutSeconds
	}
	if reg.Title == "" {
		errs = append(errs, fieldErr("/title", "required", "Give it a title."))
	}
	if !workflowType.MatchString(reg.WorkflowType) {
		errs = append(errs, fieldErr("/workflow_type", "pattern",
			"The Temporal workflow type, ending with its major version: decommission.v1. A breaking change is a new type, decommission.v2."))
	}
	if reg.TaskQueue == "" {
		errs = append(errs, fieldErr("/task_queue", "required", "The task queue its worker listens on."))
	}
	if reg.TimeoutSeconds < 60 {
		errs = append(errs, fieldErr("/timeout_seconds", "range", "At least a minute."))
	}
	if w.InputSchema != nil {
		raw, _ := json.Marshal(*w.InputSchema)
		if err := checkJSONSchema(raw); err != nil {
			errs = append(errs, fieldErr("/input_schema", "invalid", "Not a JSON Schema rvav can use: "+err.Error()))
		}
		reg.InputSchema = raw
	}
	for i, id := range reg.ProjectIDs {
		if _, err := s.Store.GetProject(ctx, id); errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fieldErr("/project_ids/"+strconv.Itoa(i), "unknown", "There is no project "+id+"."))
		} else if err != nil {
			return api.WorkflowRegistration{}, err
		}
	}
	if len(w.Usages) == 0 {
		errs = append(errs, fieldErr("/usages", "required", "Say where it may be used."))
	}
	for i, u := range w.Usages {
		if !slices.Contains(usages, string(u)) {
			errs = append(errs, fieldErr("/usages/"+strconv.Itoa(i), "enum", strings.Join(usages, ", ")+"."))
		}
		reg.Usages = append(reg.Usages, string(u))
	}
	if slices.Contains(reg.Usages, "day2_action") {
		switch {
		case w.Day2 == nil:
			errs = append(errs, fieldErr("/day2", "required", "Say how it is offered as a day-2 action."))
		default:
			if strings.TrimSpace(w.Day2.Name) == "" {
				errs = append(errs, fieldErr("/day2/name", "required", "Give the action a name."))
			}
			if len(w.Day2.Types) == 0 {
				errs = append(errs, fieldErr("/day2/types", "required", "VMs, deployments or both."))
			}
			if c := deref(w.Day2.Condition); c != "" {
				if err := policy.CheckDay2(c); err != nil {
					errs = append(errs, fieldErr("/day2/condition", "invalid", "This condition is not valid CEL: "+err.Error()))
				}
			}
			reg.Day2, _ = json.Marshal(w.Day2)
		}
	}
	if len(errs) > 0 {
		return api.WorkflowRegistration{}, &ValidationError{Errors: errs}
	}
	if err := s.Store.SaveWorkflowRegistration(ctx, reg); err != nil {
		return api.WorkflowRegistration{}, err
	}
	action := map[bool]string{true: "workflow.register", false: "workflow.update"}[create]
	s.audit(ctx, p, action, "workflow", key, nil, map[string]any{"workflow_type": reg.WorkflowType, "task_queue": reg.TaskQueue,
		"usages": reg.Usages, "enabled": reg.Enabled})
	return s.GetWorkflowRegistration(ctx, p, key)
}

// DeleteWorkflow removes a registration nothing uses.
func (s *Service) DeleteWorkflowRegistration(ctx context.Context, p *auth.Principal, key string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	if _, err := s.Store.GetWorkflowRegistration(ctx, key); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	uses, err := s.Store.WorkflowUses(ctx)
	if err != nil {
		return err
	}
	switch u := uses[key]; {
	case u.Running > 0:
		return &ConflictError{Msg: "Runs of it haven't ended: disable it instead, and remove it once they have."}
	case len(u.UsedBy) > 0:
		return &ConflictError{Msg: "It is used by " + strings.Join(u.UsedBy, ", ") + ": disable it instead."}
	}
	var runs int
	if err := s.Store.Pool.QueryRow(ctx, `select count(*) from workflow_runs where registration_key = $1`, key).Scan(&runs); err != nil {
		return err
	}
	if runs > 0 {
		return &ConflictError{Msg: "Requests ran it: their history needs it. Disable it instead."}
	}
	if err := s.Store.DeleteWorkflowRegistration(ctx, key); err != nil {
		return err
	}
	s.audit(ctx, p, "workflow.delete", "workflow", key, nil, nil)
	return nil
}

// usableWorkflow is a registration a project may use one way, or why not.
func (s *Service) usableWorkflow(ctx context.Context, key, usage, projectID string) (store.WorkflowRegistration, string, error) {
	w, err := s.Store.GetWorkflowRegistration(ctx, key)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return w, "There is no custom workflow " + key + " registered.", nil
	case err != nil:
		return w, "", err
	case !w.Enabled:
		return w, "The custom workflow " + key + " is disabled.", nil
	case !slices.Contains(w.Usages, usage):
		return w, "The custom workflow " + key + " isn't registered for this use (" + usage + ").", nil
	case projectID != "" && len(w.ProjectIDs) > 0 && !slices.Contains(w.ProjectIDs, projectID):
		return w, "The custom workflow " + key + " isn't offered to this project.", nil
	}
	return w, "", nil
}

func checkJSONSchema(raw []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	c := newCompiler()
	if err := c.AddResource("urn:rvav:workflow-input", doc); err != nil {
		return err
	}
	_, err = c.Compile("urn:rvav:workflow-input")
	return err
}
