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
