package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) ListDeploymentLimitPolicies(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.DeploymentLimitPolicies(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.DeploymentLimitPolicyList{Items: items}, err)
}

func (s *Server) CreateDeploymentLimitPolicy(w http.ResponseWriter, r *http.Request) {
	var b api.DeploymentLimitPolicyWrite
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.SaveDeploymentLimitPolicy(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) UpdateDeploymentLimitPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.DeploymentLimitPolicyWrite
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.SaveDeploymentLimitPolicy(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteDeploymentLimitPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteDeploymentLimitPolicy(r.Context(), principal(r), id))
}
