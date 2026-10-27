package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Day-2 actions, retry and rollback (ADR-0013). The console is in console.go.

func (s *Server) ListDeploymentActions(w http.ResponseWriter, r *http.Request, deploymentID api.DeploymentIdPath) {
	items, err := s.Core.Actions(r.Context(), principal(r), deploymentID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) RunDeploymentAction(w http.ResponseWriter, r *http.Request, deploymentID api.DeploymentIdPath, params api.RunDeploymentActionParams) {
	submitOnce(s, w, r, params.IdempotencyKey.String(), func(d api.Day2Draft) (store.RequestRow, error) {
		return s.Core.RunAction(r.Context(), principal(r), deploymentID, d)
	})
}

// ListDeploymentHistory lists the requests that created and changed a
// deployment, to whoever may see the deployment.
func (s *Server) ListDeploymentHistory(w http.ResponseWriter, r *http.Request, deploymentID api.DeploymentIdPath, params api.ListDeploymentHistoryParams) {
	p := principal(r)
	d, err := s.Store.GetDeployment(r.Context(), deploymentID)
	if err == nil && !p.CanSeeProject(d.ProjectID) && d.OwnerID != p.User.ID {
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	after, err := cursorOf(params.Cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.Store.ListRequests(r.Context(), store.RequestFilter{DeploymentID: d.ID, Limit: limitOf(params.Limit), After: after})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := api.RequestPage{Items: make([]api.Request, len(page.Items)), NextCursor: nextCursor(page.Next)}
	for i, row := range page.Items {
		out.Items[i] = present.Request(row, s.secretFields(r, row.VersionID))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) RetryRequest(w http.ResponseWriter, r *http.Request, requestID api.RequestIdPath) {
	row, err := s.Core.Retry(r.Context(), principal(r), requestID)
	s.accepted(w, r, row, err)
}

func (s *Server) RollbackRequest(w http.ResponseWriter, r *http.Request, requestID api.RequestIdPath) {
	row, err := s.Core.RollbackRequest(r.Context(), principal(r), requestID)
	s.accepted(w, r, row, err)
}

// accepted answers 202 with the request, or the error.
func (s *Server) accepted(w http.ResponseWriter, r *http.Request, row store.RequestRow, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, present.Request(row, s.secretFields(r, row.VersionID)))
}
