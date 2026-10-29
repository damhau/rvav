package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// GetOpenApi serves the API's own description (S14.10), the spec this
// build was generated from.
func (s *Server) GetOpenApi(w http.ResponseWriter, r *http.Request) {
	spec, err := api.GetSwagger()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, spec)
}
