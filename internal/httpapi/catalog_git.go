package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
)

// The catalog's Git repository (ADR-0029).

func (s *Server) CatalogGitStatus(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.CatalogGitStatus(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ReadCatalogGit(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.ReadCatalogGit(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) CatalogItemGit(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	out, err := s.Core.CatalogItemGit(r.Context(), principal(r), itemID)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ResolveCatalogItemGit(w http.ResponseWriter, r *http.Request, itemID api.ItemIdPath) {
	var body api.ResolveCatalogItemGitJSONBody
	if !s.decode(w, r, &body) {
		return
	}
	out, err := s.Core.ResolveCatalogItemGit(r.Context(), principal(r), itemID, string(body.Take))
	s.reply(w, r, http.StatusOK, out, err)
}

// GitWebhook is the Git server's call when the repository changes: rvav
// reads it at once.
func (s *Server) GitWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "bad_request", "Can't read the request", err.Error(), nil)
		return
	}
	switch err := s.Core.GitWebhook(r.Context(), r.Header, body); {
	case errors.Is(err, core.ErrGitWebhook):
		s.Log.Warn("a Git webhook call was refused: not signed with RVAV_GIT_WEBHOOK_SECRET")
		s.problem(w, r, http.StatusUnauthorized, "unauthenticated", "Not a call from the catalog's Git server", "", nil)
	case errors.Is(err, core.ErrNotFound):
		s.problem(w, r, http.StatusNotFound, "not_found", "No Git webhook is set up", "", nil)
	case err != nil:
		s.fail(w, r, err)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}
