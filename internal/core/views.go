package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// Saved views: a list's query (filters, sort, layout) under a name, its
// owner's only. A view isn't shared with a project: its address already
// shares it, and shows each person what they may see.

// MaxSavedViews is how many views of one list a person may keep.
const MaxSavedViews = 100

func presentView(v store.SavedView) api.SavedView {
	out := api.SavedView{Id: v.ID, List: api.ViewList(v.List), Name: v.Name, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	_ = json.Unmarshal(v.Query, &out.Query)
	if out.Query == nil {
		out.Query = map[string]string{}
	}
	return out
}

// SavedViews are the caller's views of a list, by name.
func (s *Service) SavedViews(ctx context.Context, p *auth.Principal, list api.ViewList) ([]api.SavedView, error) {
	if !list.Valid() {
		return nil, &BadRequestError{Msg: fmt.Sprintf("No list %q has views: name deployments or resources.", list)}
	}
	views, err := s.Store.SavedViews(ctx, p.User.ID, string(list))
	if err != nil {
		return nil, err
	}
	out := make([]api.SavedView, len(views))
	for i, v := range views {
		out[i] = presentView(v)
	}
	return out, nil
}

// SaveView creates (id empty) or replaces one of the caller's views.
func (s *Service) SaveView(ctx context.Context, p *auth.Principal, id string, w api.SavedViewWrite) (api.SavedView, error) {
	var errs []api.FieldError
	name := strings.TrimSpace(w.Name)
	switch {
	case name == "":
		errs = append(errs, fieldErr("/name", "required", "Give the view a name."))
	case len([]rune(name)) > 60:
		errs = append(errs, fieldErr("/name", "too_long", "At most 60 characters."))
	}
	if !w.List.Valid() {
		errs = append(errs, fieldErr("/list", "unknown", "Views are of deployments or resources."))
	}
	if len(w.Query) > 20 {
		errs = append(errs, fieldErr("/query", "too_many", "At most 20 filters."))
	}
	for k, v := range w.Query {
		switch {
		case utf8.RuneCountInString(k) > 40:
			errs = append(errs, fieldErr("/query", "too_long", "A filter's name is 40 characters at most."))
		case utf8.RuneCountInString(v) > 400:
			errs = append(errs, fieldErr("/query/"+k, "too_long", "At most 400 characters."))
		}
	}
	if len(errs) > 0 {
		return api.SavedView{}, &ValidationError{Errors: errs}
	}
	if id == "" {
		n, err := s.Store.CountSavedViews(ctx, p.User.ID, string(w.List))
		if err != nil {
			return api.SavedView{}, err
		}
		if n >= MaxSavedViews {
			return api.SavedView{}, &ValidationError{Errors: []api.FieldError{fieldErr("/name", "too_many",
				fmt.Sprintf("You have %d saved views of this list: delete one first.", n))}}
		}
		id = ids.New(ids.View)
	} else if _, err := s.Store.GetSavedView(ctx, p.User.ID, id); errors.Is(err, store.ErrNotFound) {
		return api.SavedView{}, ErrNotFound
	} else if err != nil {
		return api.SavedView{}, err
	}
	query := w.Query
	if query == nil {
		query = map[string]string{}
	}
	body, _ := json.Marshal(query)
	err := s.Store.SaveView(ctx, store.SavedView{ID: id, UserID: p.User.ID, List: string(w.List), Name: name, Query: body})
	switch {
	case store.IsUniqueViolation(err):
		return api.SavedView{}, &ValidationError{Errors: []api.FieldError{fieldErr("/name", "taken",
			"You have a view with this name: pick another, or update that one.")}}
	case errors.Is(err, store.ErrNotFound):
		return api.SavedView{}, ErrNotFound
	case err != nil:
		return api.SavedView{}, err
	}
	v, err := s.Store.GetSavedView(ctx, p.User.ID, id)
	if err != nil {
		return api.SavedView{}, err
	}
	return presentView(v), nil
}

func (s *Service) DeleteSavedView(ctx context.Context, p *auth.Principal, id string) error {
	if err := s.Store.DeleteSavedView(ctx, p.User.ID, id); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}
