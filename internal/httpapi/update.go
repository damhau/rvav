package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// PreviewDeploymentUpdate says what updating a deployment to a version and
// inputs would do (ADR-0042).
func (s *Server) PreviewDeploymentUpdate(w http.ResponseWriter, r *http.Request, deploymentID api.DeploymentIdPath) {
	var d api.UpdateDraft
	if !s.decode(w, r, &d) {
		return
	}
	pv, err := s.Core.PreviewUpdate(r.Context(), principal(r), deploymentID, d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

// UpdateDeployment asks for the update of a deployment: a request of kind
// update.
func (s *Server) UpdateDeployment(w http.ResponseWriter, r *http.Request, deploymentID api.DeploymentIdPath, params api.UpdateDeploymentParams) {
	submitOnce(s, w, r, params.IdempotencyKey.String(), func(d api.UpdateDraft) (store.RequestRow, error) {
		return s.Core.UpdateDeployment(r.Context(), principal(r), deploymentID, d)
	})
}
