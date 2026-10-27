package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// reply writes v with status, or the error.
func (s *Server) reply(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

// replyEmpty answers 204, or the error.
func (s *Server) replyEmpty(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Catalog administration (S8, ADR-0019). Handlers answer 501 until they are
// built.

// notBuilt answers 501 for an operation in the spec that is not built yet.
func (s *Server) notBuilt(w http.ResponseWriter, r *http.Request) {
	s.problem(w, r, http.StatusNotImplemented, "not-implemented", "Not built yet",
		"This operation is in the API but not built yet (S8, catalog administration).", nil)
}

func (s *Server) ListAdminCatalogItems(w http.ResponseWriter, r *http.Request, params api.ListAdminCatalogItemsParams) {
	after, err := cursorOf(params.Cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f := store.AdminItemFilter{Search: deref(params.Q), Category: deref(params.Category), Limit: limitOf(params.Limit), After: after}
	if params.Source != nil {
		f.Source = string(*params.Source)
	}
	if params.State != nil {
		f.State = string(*params.State)
	}
	page, err := s.Core.AdminCatalogItems(r.Context(), principal(r), f)
	s.reply(w, r, http.StatusOK, page, err)
}

func (s *Server) CreateCatalogItem(w http.ResponseWriter, r *http.Request) {
	var c api.CatalogItemCreate
	if !s.decode(w, r, &c) {
		return
	}
	it, err := s.Core.CreateCatalogItem(r.Context(), principal(r), c)
	s.reply(w, r, http.StatusCreated, it, err)
}

func (s *Server) GetAdminCatalogItem(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	it, err := s.Core.AdminCatalogItem(r.Context(), principal(r), itemID)
	s.reply(w, r, http.StatusOK, it, err)
}

func (s *Server) UpdateCatalogItem(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	var u api.CatalogItemUpdate
	if !s.decode(w, r, &u) {
		return
	}
	it, err := s.Core.UpdateCatalogItem(r.Context(), principal(r), itemID, u)
	s.reply(w, r, http.StatusOK, it, err)
}

func (s *Server) DeleteCatalogItem(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	s.replyEmpty(w, r, s.Core.DeleteCatalogItem(r.Context(), principal(r), itemID))
}

func (s *Server) CreateCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	var c api.CatalogItemVersionCreate
	if !s.decode(w, r, &c) {
		return
	}
	v, err := s.Core.CreateCatalogItemVersion(r.Context(), principal(r), itemID, c)
	s.reply(w, r, http.StatusCreated, v, err)
}

func (s *Server) GetCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	v, err := s.Core.CatalogItemVersion(r.Context(), principal(r), itemID, versionID)
	s.reply(w, r, http.StatusOK, v, err)
}

func (s *Server) UpdateCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	var u api.CatalogItemVersionUpdate
	if !s.decode(w, r, &u) {
		return
	}
	v, err := s.Core.UpdateCatalogItemVersion(r.Context(), principal(r), itemID, versionID, u)
	s.reply(w, r, http.StatusOK, v, err)
}

func (s *Server) DeleteCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	s.replyEmpty(w, r, s.Core.DeleteCatalogItemVersion(r.Context(), principal(r), itemID, versionID))
}

func (s *Server) PublishCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	var body api.CatalogItemVersionPublish
	if r.ContentLength > 0 && !s.decode(w, r, &body) {
		return
	}
	it, err := s.Core.PublishCatalogItemVersion(r.Context(), principal(r), itemID, versionID, body.Changelog)
	s.reply(w, r, http.StatusOK, it, err)
}

func (s *Server) DeprecateCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	it, err := s.Core.DeprecateCatalogItemVersion(r.Context(), principal(r), itemID, versionID)
	s.reply(w, r, http.StatusOK, it, err)
}

func (s *Server) PreviewCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) CompareCatalogItemVersions(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, params api.CompareCatalogItemVersionsParams) {
	diff, err := s.Core.CompareCatalogItemVersions(r.Context(), principal(r), itemID, params.From, params.To)
	s.reply(w, r, http.StatusOK, diff, err)
}

func (s *Server) GetCatalogItemEntitlements(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) SetCatalogItemEntitlements(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) ExportCatalogItem(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, params api.ExportCatalogItemParams) {
	s.notBuilt(w, r)
}

func (s *Server) ImportCatalogItem(w http.ResponseWriter, r *http.Request, params api.ImportCatalogItemParams) {
	s.notBuilt(w, r)
}

func (s *Server) ListAdminCatalogCategories(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) CreateCatalogCategory(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) UpdateCatalogCategory(w http.ResponseWriter, r *http.Request, key string) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteCatalogCategory(w http.ResponseWriter, r *http.Request, key string) {
	s.notBuilt(w, r)
}

func (s *Server) ListCatalogIcons(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) ListSecrets(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) CreateSecret(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) UpdateSecret(w http.ResponseWriter, r *http.Request, name string) { s.notBuilt(w, r) }

func (s *Server) DeleteSecret(w http.ResponseWriter, r *http.Request, name string) { s.notBuilt(w, r) }
