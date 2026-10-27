package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// Day-2 actions, history, console, retry and rollback are specified first
// (ADR-0006, ADR-0013); the handlers follow.

func (s *Server) day2NotYet(w http.ResponseWriter, r *http.Request) {
	s.problem(w, r, http.StatusNotImplemented, "not-implemented", "Not available yet", "Day-2 actions are being built.", nil)
}

func (s *Server) ListDeploymentActions(w http.ResponseWriter, r *http.Request, _ api.DeploymentIdPath) {
	s.day2NotYet(w, r)
}

func (s *Server) RunDeploymentAction(w http.ResponseWriter, r *http.Request, _ api.DeploymentIdPath, _ api.RunDeploymentActionParams) {
	s.day2NotYet(w, r)
}

func (s *Server) ListDeploymentHistory(w http.ResponseWriter, r *http.Request, _ api.DeploymentIdPath, _ api.ListDeploymentHistoryParams) {
	s.day2NotYet(w, r)
}

func (s *Server) OpenConsole(w http.ResponseWriter, r *http.Request, _ api.DeploymentIdPath, _ string) {
	s.day2NotYet(w, r)
}

func (s *Server) ConsoleStream(w http.ResponseWriter, r *http.Request, _ string) {
	s.day2NotYet(w, r)
}

func (s *Server) RetryRequest(w http.ResponseWriter, r *http.Request, _ api.RequestIdPath) {
	s.day2NotYet(w, r)
}

func (s *Server) RollbackRequest(w http.ResponseWriter, r *http.Request, _ api.RequestIdPath) {
	s.day2NotYet(w, r)
}
