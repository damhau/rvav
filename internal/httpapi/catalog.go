package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

func (s *Server) ListCatalogCategories(w http.ResponseWriter, r *http.Request, params api.ListCatalogCategoriesParams) {
	pr, ok := s.visibleProject(w, r, params.ProjectId)
	if !ok {
		return
	}
	list, err := s.Store.ListCategories(r.Context(), pr.ID, principal(r).Requester(pr.ID))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]api.CatalogCategory, len(list))
	for i, c := range list {
		items[i] = api.CatalogCategory{Key: c.Key, Name: c.Name, ItemCount: c.ItemCount}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) ListCatalogItems(w http.ResponseWriter, r *http.Request, params api.ListCatalogItemsParams) {
	pr, ok := s.visibleProject(w, r, params.ProjectId)
	if !ok {
		return
	}
	after, err := cursorOf(params.Cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	who := principal(r).Requester(pr.ID)
	f := store.ItemFilter{ProjectID: pr.ID, UserID: who.UserID, Groups: who.Groups, Roles: who.Roles, Category: deref(params.Category),
		Search: deref(params.Q), Limit: limitOf(params.Limit), After: after}
	if params.Type != nil {
		f.Type = string(*params.Type)
	}
	if params.Favourite != nil {
		f.Favourite = *params.Favourite
	}
	page, err := s.Store.ListItems(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := api.CatalogItemPage{Items: make([]api.CatalogItem, len(page.Items)), NextCursor: nextCursor(page.Next)}
	for i, it := range page.Items {
		out.Items[i] = present.CatalogItem(it)
	}
	writeJSON(w, http.StatusOK, out)
}

// entitledItem loads an item that is published and shared with the caller
// in a project they can see.
func (s *Server) entitledItem(w http.ResponseWriter, r *http.Request, itemID, projectID string) (store.CatalogItem, store.Project, bool) {
	pr, ok := s.visibleProject(w, r, projectID)
	if !ok {
		return store.CatalogItem{}, pr, false
	}
	it, err := s.Store.GetItem(r.Context(), itemID, principal(r).User.ID)
	if err == nil {
		var entitled bool
		entitled, err = s.Core.SharedWithRequester(r.Context(), principal(r), it.ID, pr.ID)
		if err == nil && (!entitled || it.PublishedVersionID == nil) {
			err = store.ErrNotFound
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return it, pr, false
	}
	return it, pr, true
}

func (s *Server) GetCatalogItem(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, params api.GetCatalogItemParams) {
	it, _, ok := s.entitledItem(w, r, itemID, params.ProjectId)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, present.CatalogItem(it))
}

func (s *Server) setFavourite(w http.ResponseWriter, r *http.Request, itemID string, on bool) {
	p := principal(r)
	it, err := s.Store.GetItem(r.Context(), itemID, p.User.ID)
	if err == nil {
		var shared bool
		if shared, err = s.Core.SharedAnywhere(r.Context(), p, it.ID); err == nil && !shared {
			err = store.ErrNotFound
		}
	}
	if err == nil {
		err = s.Store.SetFavourite(r.Context(), p.User.ID, it.ID, on)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) AddFavourite(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	s.setFavourite(w, r, itemID, true)
}

func (s *Server) RemoveFavourite(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	s.setFavourite(w, r, itemID, false)
}

func (s *Server) GetRequestForm(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, params api.GetRequestFormParams) {
	it, _, ok := s.entitledItem(w, r, itemID, params.ProjectId)
	if !ok {
		return
	}
	versionID := *it.PublishedVersionID
	if params.VersionId != nil && *params.VersionId != "" {
		versionID = *params.VersionId
	}
	ver, err := s.Store.GetVersion(r.Context(), versionID)
	// Drafts and retired versions are for catalog admins (ADR-0019, ADR-0046).
	if err == nil && (ver.ItemID != it.ID || !core.Requestable(it, versionID) && !principal(r).Everywhere(auth.CatalogManage)) {
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := api.RequestForm{ItemId: it.ID, VersionId: ver.ID}
	if err := json.Unmarshal(ver.FormSchema, &out.Schema); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := json.Unmarshal(ver.UISchema, &out.UiSchema); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := json.Unmarshal(ver.InitialValues, &out.InitialValues); err != nil || out.InitialValues == nil {
		out.InitialValues = map[string]any{}
	}
	out.OneDeployment = core.OneDeployment(ver.Blueprint)
	out.Topology = core.Topology(ver.Blueprint)
	names, err := s.Core.MakesDeployments(r.Context(), ver.Blueprint)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if names {
		out.NamesDeployments = &names
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ResolveFieldValues(w http.ResponseWriter, r *http.Request) {
	var q api.FieldValuesQuery
	if !s.decode(w, r, &q) {
		return
	}
	if q.Inputs == nil {
		q.Inputs = map[string]any{}
	}
	out, err := s.Core.FieldValues(r.Context(), principal(r), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
