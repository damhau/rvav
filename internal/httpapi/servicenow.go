package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) GetServiceNowCmdb(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.ServiceNowCmdb(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ReconcileServiceNowCmdb(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.ReconcileServiceNowCmdb(r.Context(), principal(r))
	s.reply(w, r, http.StatusAccepted, out, err)
}

func (s *Server) ListServiceNowCatalog(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.ServiceNowCatalog(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) GetServiceNowEntitlements(w http.ResponseWriter, r *http.Request, params api.GetServiceNowEntitlementsParams) {
	out, err := s.Core.ServiceNowEntitlements(r.Context(), principal(r), params.User)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) SubmitServiceNowRequest(w http.ResponseWriter, r *http.Request) {
	var d api.ServiceNowRequestDraft
	if !s.decode(w, r, &d) {
		return
	}
	out, made, err := s.Core.SubmitServiceNowRequest(r.Context(), principal(r), d)
	status := http.StatusOK
	if made {
		status = http.StatusAccepted
	}
	s.reply(w, r, status, out, err)
}

func (s *Server) GetServiceNowRequest(w http.ResponseWriter, r *http.Request, ritmSysID string) {
	out, err := s.Core.ServiceNowRequest(r.Context(), principal(r), ritmSysID)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) GetServiceNowToken(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.ServiceNowToken(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) MakeServiceNowToken(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.MakeServiceNowToken(r.Context(), principal(r))
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) RevokeServiceNowToken(w http.ResponseWriter, r *http.Request) {
	s.replyEmpty(w, r, s.Core.RevokeServiceNowToken(r.Context(), principal(r)))
}
