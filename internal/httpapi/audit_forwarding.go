package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// Where audit events are sent (ADR-0058).

func (s *Server) ListAuditDestinations(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.AuditDestinations(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.AuditDestinationList{Items: items}, err)
}

func (s *Server) CreateAuditDestination(w http.ResponseWriter, r *http.Request) {
	var b api.AuditDestinationWrite
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.SaveAuditDestination(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetAuditDestination(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	out, err := s.Core.GetAuditDestination(r.Context(), principal(r), id)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) UpdateAuditDestination(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.AuditDestinationWrite
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.SaveAuditDestination(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteAuditDestination(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteAuditDestination(r.Context(), principal(r), id))
}

func (s *Server) TestAuditDestination(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	out, err := s.Core.TestAuditDestination(r.Context(), principal(r), id)
	s.reply(w, r, http.StatusOK, out, err)
}
