package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) ListFormLibrary(w http.ResponseWriter, r *http.Request) {
	list, err := s.Core.ListFormLibrary(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, list, err)
}

func (s *Server) CreateFormLibraryEntry(w http.ResponseWriter, r *http.Request) {
	var body api.FormLibraryEntryWrite
	if !s.decode(w, r, &body) {
		return
	}
	out, err := s.Core.PutFormLibraryEntry(r.Context(), principal(r), "", body, true)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetFormLibraryEntry(w http.ResponseWriter, r *http.Request, key string, params api.GetFormLibraryEntryParams) {
	version := 0
	if params.Version != nil {
		version = *params.Version
	}
	out, err := s.Core.FormLibraryEntry(r.Context(), principal(r), key, version)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) UpdateFormLibraryEntry(w http.ResponseWriter, r *http.Request, key string) {
	var body api.FormLibraryEntryWrite
	if !s.decode(w, r, &body) {
		return
	}
	out, err := s.Core.PutFormLibraryEntry(r.Context(), principal(r), key, body, false)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteFormLibraryEntry(w http.ResponseWriter, r *http.Request, key string) {
	s.replyEmpty(w, r, s.Core.DeleteFormLibraryEntry(r.Context(), principal(r), key))
}

func (s *Server) TestFormLibraryEntry(w http.ResponseWriter, r *http.Request) {
	var body api.FormLibraryTest
	if !s.decode(w, r, &body) {
		return
	}
	out, err := s.Core.TestFormLibraryEntry(r.Context(), principal(r), body)
	s.reply(w, r, http.StatusOK, out, err)
}
