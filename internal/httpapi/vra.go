package httpapi

import (
	"errors"
	"net/http"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
)

// The vRA importer (ADR-0027, P2.5).

func (s *Server) GetVraImport(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.VRAImportStatus(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

// maxEstate is the largest vra-export file taken: a vRA of some 100,000
// deployments. The API needs about four times it in memory to take it.
const maxEstate = 64 << 20

func (s *Server) ReadVra(w http.ResponseWriter, r *http.Request) {
	// Only administrators get their file read at all.
	if p := principal(r); !p.IsAdmin() {
		s.fail(w, r, core.ErrForbidden)
		return
	}
	out, err := s.Core.ReadVRA(r.Context(), principal(r), http.MaxBytesReader(w, r.Body, maxEstate))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		s.problem(w, r, http.StatusRequestEntityTooLarge, "too-large", "The file is too large",
			"rvav takes vra-export files of up to 64 MB.", nil)
		return
	}
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

func (s *Server) SetVraCutover(w http.ResponseWriter, r *http.Request, vraProjectId string) {
	var b api.VraCutoverWrite
	if !s.decode(w, r, &b) {
		return
	}
	if err := s.Core.SetVRACutover(r.Context(), principal(r), vraProjectId, b); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
