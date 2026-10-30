package httpapi

import (
	"io"
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// The vRA importer (ADR-0027, P2.5).

func (s *Server) GetVraImport(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.VRAImportStatus(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ReadVra(w http.ResponseWriter, r *http.Request) {
	// vra-export's file: a large vRA's deployments with their resources.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<20))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "bad-request", "The file could not be read", err.Error(), nil)
		return
	}
	out, err := s.Core.ReadVRA(r.Context(), principal(r), body)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetVraPlan(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.VRAPlan(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) MapVraProject(w http.ResponseWriter, r *http.Request, vraProjectId string) {
	var b api.VraProjectMapping
	if !s.decode(w, r, &b) {
		return
	}
	if err := s.Core.MapVRAProject(r.Context(), principal(r), vraProjectId, b); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ImportVra(w http.ResponseWriter, r *http.Request) {
	var b api.VraImportRequest
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.ImportVRA(r.Context(), principal(r), b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) GetVroTriage(w http.ResponseWriter, r *http.Request) {
	md, err := s.Core.VROTriageMarkdown(r.Context(), principal(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="vro-triage.md"`)
	_, _ = w.Write([]byte(md))
}
