package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Request drafts: a request saved to finish later, its author's only,
// unchecked until submitted.

func presentDraft(d store.RequestDraft) api.SavedDraft {
	out := api.SavedDraft{Id: d.ID, Name: d.Name, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		Project: api.ProjectRef{Id: d.ProjectID, Code: d.ProjectCode, Name: d.ProjectName}}
	out.CatalogItem.Id, out.CatalogItem.Slug, out.CatalogItem.Name = d.ItemID, d.ItemSlug, d.ItemName
	_ = json.Unmarshal(d.Draft, &out.Draft)
	if out.Draft.Inputs == nil {
		out.Draft.Inputs = map[string]any{}
	}
	return out
}

// RequestDrafts are the caller's drafts, last changed first.
func (s *Service) RequestDrafts(ctx context.Context, p *auth.Principal) ([]api.SavedDraft, error) {
	list, err := s.Store.RequestDrafts(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}
	out := make([]api.SavedDraft, 0, len(list))
	for _, d := range list {
		out = append(out, presentDraft(d))
	}
	return out, s.withOwners(ctx, out)
}

// withOwners names who drafts are on behalf of.
func (s *Service) withOwners(ctx context.Context, drafts []api.SavedDraft) error {
	var want []string
	for _, d := range drafts {
		if d.Draft.OnBehalfOf != nil && *d.Draft.OnBehalfOf != "" {
			want = append(want, *d.Draft.OnBehalfOf)
		}
	}
	if len(want) == 0 {
		return nil
	}
	users, err := s.Store.FindUsers(ctx, want)
	if err != nil {
		return err
	}
	for i, d := range drafts {
		if d.Draft.OnBehalfOf == nil {
			continue
		}
		if u, ok := users[*d.Draft.OnBehalfOf]; ok {
			email := openapi_types.Email(u.Email)
			drafts[i].OnBehalfOf = &api.UserRef{Id: u.ID, Name: u.Name, Email: &email}
		}
	}
	return nil
}

func (s *Service) GetRequestDraft(ctx context.Context, p *auth.Principal, id string) (api.SavedDraft, error) {
	d, err := s.Store.GetRequestDraft(ctx, p.User.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.SavedDraft{}, ErrNotFound
	}
	if err != nil {
		return api.SavedDraft{}, err
	}
	out := []api.SavedDraft{presentDraft(d)}
	return out[0], s.withOwners(ctx, out)
}

// SaveRequestDraft creates (id empty) or replaces a draft. The item, its
// version and the project must exist; values of secret fields are dropped.
func (s *Service) SaveRequestDraft(ctx context.Context, p *auth.Principal, id string, w api.SavedDraftWrite) (api.SavedDraft, error) {
	var errs []api.FieldError
	item, err := s.Store.GetItem(ctx, w.Draft.CatalogItemId, p.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		errs = append(errs, fieldErr("/draft/catalog_item_id", "unknown", "No such catalog item."))
	} else if err != nil {
		return api.SavedDraft{}, err
	}
	version, err := s.Store.GetVersion(ctx, w.Draft.VersionId)
	if errors.Is(err, store.ErrNotFound) || err == nil && version.ItemID != item.ID {
		errs = append(errs, fieldErr("/draft/version_id", "unknown", "Not a version of this item."))
	} else if err != nil {
		return api.SavedDraft{}, err
	}
	project, err := s.Store.GetProject(ctx, w.Draft.ProjectId)
	switch {
	case errors.Is(err, store.ErrNotFound) || err == nil && !p.CanRequest(project.ID):
		errs = append(errs, fieldErr("/draft/project_id", "unknown", "No such project, or not one you request in."))
	case err != nil:
		return api.SavedDraft{}, err
	}
	if w.Name != nil && len([]rune(*w.Name)) > 120 {
		errs = append(errs, fieldErr("/name", "too_long", "At most 120 characters."))
	}
	if len(errs) > 0 {
		return api.SavedDraft{}, &ValidationError{Errors: errs}
	}
	// Secrets aren't kept in drafts: fields marked secret, and secret widgets.
	secret := present.SecretFields(version.UISchema)
	var ui api.UiSchema
	_ = json.Unmarshal(version.UISchema, &ui)
	inputs := map[string]any{}
	for k, v := range w.Draft.Inputs {
		if f, ok := ui.Fields[k]; secret[k] || ok && f.Widget == api.UiWidgetSecret {
			continue
		}
		inputs[k] = v
	}
	w.Draft.Inputs, w.Draft.ProjectId = inputs, project.ID
	name := strings.TrimSpace(deref(w.Name))
	if name == "" {
		name = item.Name
	}
	if id == "" {
		id = ids.New(ids.Draft)
	} else if _, err := s.Store.GetRequestDraft(ctx, p.User.ID, id); errors.Is(err, store.ErrNotFound) {
		return api.SavedDraft{}, ErrNotFound
	} else if err != nil {
		return api.SavedDraft{}, err
	}
	body, _ := json.Marshal(w.Draft)
	if err := s.Store.SaveRequestDraft(ctx, store.RequestDraft{ID: id, UserID: p.User.ID, ItemID: item.ID,
		ProjectID: project.ID, Name: name, Draft: body}); errors.Is(err, store.ErrNotFound) {
		return api.SavedDraft{}, ErrNotFound
	} else if err != nil {
		return api.SavedDraft{}, err
	}
	return s.GetRequestDraft(ctx, p, id)
}

func (s *Service) DeleteRequestDraft(ctx context.Context, p *auth.Principal, id string) error {
	if err := s.Store.DeleteRequestDraft(ctx, p.User.ID, id); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}
