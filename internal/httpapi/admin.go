package httpapi

import "net/http"

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
