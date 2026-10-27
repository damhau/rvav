package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) ReconcileIpam(w http.ResponseWriter, r *http.Request) {
	report, err := s.Core.ReconcileIPAM(r.Context(), principal(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) ListAwxTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.Core.AWXTemplates(r.Context(), principal(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) SyncAwxTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.Core.SyncAWXTemplates(r.Context(), principal(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) GetAnsibleInventory(w http.ResponseWriter, r *http.Request) {
	inv, err := s.Core.AnsibleInventory(r.Context(), principal(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) ListAwxActions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Core.AWXActions(r.Context(), principal(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) UpdateAwxAction(w http.ResponseWriter, r *http.Request, key string) {
	var u api.AwxActionUpdate
	if !s.decode(w, r, &u) {
		return
	}
	a, err := s.Core.UpdateAWXAction(r.Context(), principal(r), key, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
