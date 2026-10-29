package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// The form designer's preview (ADR-0019): any version of any item, as a
// chosen user in a chosen project, for catalog admins.

// previewPrincipal is who a catalog admin previews as (themselves by
// default): a member of the project, directly or through a group, so a
// preview shows what a requester there would get and no more.
func (s *Service) previewPrincipal(ctx context.Context, p *auth.Principal, asUserID *string, projectID string) (*auth.Principal, error) {
	if !canAdminCatalog(p) {
		return nil, ErrForbidden
	}
	as := p
	if asUserID != nil && *asUserID != "" && *asUserID != p.User.ID {
		var err error
		as, err = auth.PrincipalFor(ctx, s.Store, *asUserID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, &ValidationError{Errors: []api.FieldError{fieldErr("/as_user_id", "unknown", "There is no such user.")}}
		}
		if err != nil {
			return nil, err
		}
	}
	if as.ProjectRoles[projectID] == "" {
		return nil, &ValidationError{Errors: []api.FieldError{fieldErr("/as_user_id", "not_member",
			as.User.Name+" is not a member of this project: preview as one of its members.")}}
	}
	return as, nil
}

// PreviewCatalogItemVersion is the form of a version as a user would get
// it: initial values, rule results (with rules that fail), and what is
// wrong with the inputs.
func (s *Service) PreviewCatalogItemVersion(ctx context.Context, p *auth.Principal, idOrSlug, versionID string, q api.DesignerPreviewQuery) (api.DesignerPreview, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.DesignerPreview{}, err
	}
	v, _, err := s.itemVersion(ctx, it, versionID)
	if err != nil {
		return api.DesignerPreview{}, err
	}
	as, err := s.previewPrincipal(ctx, p, q.AsUserId, q.ProjectId)
	if err != nil {
		return api.DesignerPreview{}, err
	}
	proj, err := s.visibleProject(ctx, as, q.ProjectId)
	if err != nil {
		return api.DesignerPreview{}, err
	}
	out := api.DesignerPreview{Form: api.RequestForm{ItemId: it.ID, VersionId: v.ID}, Errors: []api.FieldError{}}
	if err := json.Unmarshal(v.FormSchema, &out.Form.Schema); err != nil {
		return out, err
	}
	if err := json.Unmarshal(v.UISchema, &out.Form.UiSchema); err != nil {
		return out, err
	}
	if json.Unmarshal(v.InitialValues, &out.Form.InitialValues) != nil || out.Form.InitialValues == nil {
		out.Form.InitialValues = map[string]any{}
	}
	out.Form.OneDeployment = OneDeployment(v.Blueprint)
	inputs := map[string]any{}
	for k, val := range q.Inputs {
		inputs[k] = val
	}
	env := ""
	if q.Environment != nil {
		env = *q.Environment
	}
	out.Fields = fieldStates(v.FormSchema, v.UISchema, formVars(inputs, proj, env, as))
	ruleErrs := applyStates(out.Fields, out.Form.UiSchema, out.Form.InitialValues, inputs)
	if schema, err := compileSchemaUncached(v.FormSchema); err == nil {
		b, _ := json.Marshal(inputs)
		errs, err := validateWith(schema, b)
		if err != nil {
			return out, err
		}
		out.Errors = append(out.Errors, hiddenErrors(out.Fields, errs)...)
	}
	for _, e := range ruleErrs {
		if !slices.ContainsFunc(out.Errors, func(f api.FieldError) bool { return f.Field == e.Field }) {
			out.Errors = append(out.Errors, e)
		}
	}
	return out, nil
}

// PreviewUsers are a project's members, to preview a form as one of them.
func (s *Service) PreviewUsers(ctx context.Context, p *auth.Principal, projectID string) ([]api.UserRef, []string, error) {
	if !canAdminCatalog(p) {
		return nil, nil, ErrForbidden
	}
	users, err := s.Store.ProjectMembers(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	refs, roles := make([]api.UserRef, len(users)), make([]string, len(users))
	for i, u := range users {
		refs[i] = api.UserRef{Id: u.ID, Name: u.Name}
		if as, err := auth.PrincipalFor(ctx, s.Store, u.ID); err == nil {
			roles[i] = orDefault(as.ProjectRole(projectID), "member")
		}
	}
	return refs, roles, nil
}
