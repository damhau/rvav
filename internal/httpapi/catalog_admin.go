package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// Catalog administration (S8, ADR-0019). Handlers answer 501 until they are
// built.

var _ = api.ItemIdPath("")

// notBuilt answers 501 for an operation in the spec that is not built yet.
func (s *Server) notBuilt(w http.ResponseWriter, r *http.Request) {
	s.problem(w, r, http.StatusNotImplemented, "not-implemented", "Not built yet",
		"This operation is in the API but not built yet (S8, catalog administration).", nil)
}

func (s *Server) ListAdminCatalogItems(w http.ResponseWriter, r *http.Request, params api.ListAdminCatalogItemsParams) {
	s.notBuilt(w, r)
}

func (s *Server) CreateCatalogItem(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) GetAdminCatalogItem(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateCatalogItem(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteCatalogItem(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) CreateCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) GetCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) PublishCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) DeprecateCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) PreviewCatalogItemVersion(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, versionID api.VersionIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) CompareCatalogItemVersions(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath, params api.CompareCatalogItemVersionsParams) {
	s.notBuilt(w, r)
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
