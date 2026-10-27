package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// The approvals API is specified first (ADR-0006); the handlers follow.

func (s *Server) notYet(w http.ResponseWriter, r *http.Request) {
	s.problem(w, r, http.StatusNotImplemented, "not-implemented", "Not available yet", "Approvals are being built.", nil)
}

func (s *Server) ListApprovals(w http.ResponseWriter, r *http.Request, _ api.ListApprovalsParams) {
	s.notYet(w, r)
}

func (s *Server) GetApproval(w http.ResponseWriter, r *http.Request, _ api.ApprovalIdPath) {
	s.notYet(w, r)
}

func (s *Server) DecideApproval(w http.ResponseWriter, r *http.Request, _ api.ApprovalIdPath) {
	s.notYet(w, r)
}

func (s *Server) DelegateApproval(w http.ResponseWriter, r *http.Request, _ api.ApprovalIdPath) {
	s.notYet(w, r)
}

func (s *Server) ListRequestApprovals(w http.ResponseWriter, r *http.Request, _ api.RequestIdPath) {
	s.notYet(w, r)
}
