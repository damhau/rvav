package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) GetProjectUserQuotas(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	items, err := s.Core.UserQuotas(r.Context(), principal(r), projectId)
	s.reply(w, r, http.StatusOK, api.UserQuotaList{Items: items}, err)
}

func (s *Server) SetProjectUserQuotas(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	var b api.UserQuotaWriteList
	if !s.decode(w, r, &b) {
		return
	}
	items, err := s.Core.SetUserQuotas(r.Context(), principal(r), projectId, b.Items)
	s.reply(w, r, http.StatusOK, api.UserQuotaList{Items: items}, err)
}

func (s *Server) GetGlobalQuotas(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.GlobalQuotas(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.GlobalQuotaList{Items: items}, err)
}

func (s *Server) SetGlobalQuotas(w http.ResponseWriter, r *http.Request) {
	var b api.GlobalQuotaList
	if !s.decode(w, r, &b) {
		return
	}
	items, err := s.Core.SetGlobalQuotas(r.Context(), principal(r), b.Items)
	s.reply(w, r, http.StatusOK, api.GlobalQuotaList{Items: items}, err)
}
