package httpapi

import "net/http"

func (s *Server) GetServiceNowCmdb(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.ServiceNowCmdb(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ReconcileServiceNowCmdb(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.ReconcileServiceNowCmdb(r.Context(), principal(r))
	s.reply(w, r, http.StatusAccepted, out, err)
}
