package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) ListApprovals(w http.ResponseWriter, r *http.Request, params api.ListApprovalsParams) {
	page, err := s.Core.ListApprovals(r.Context(), principal(r), params)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) GetApproval(w http.ResponseWriter, r *http.Request, approvalID api.ApprovalIdPath) {
	a, err := s.Core.GetApproval(r.Context(), principal(r), approvalID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) DecideApproval(w http.ResponseWriter, r *http.Request, approvalID api.ApprovalIdPath) {
	var d api.ApprovalDecision
	if !s.decode(w, r, &d) {
		return
	}
	a, err := s.Core.DecideApproval(r.Context(), principal(r), approvalID, d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) DelegateApproval(w http.ResponseWriter, r *http.Request, approvalID api.ApprovalIdPath) {
	var d api.ApprovalDelegation
	if !s.decode(w, r, &d) {
		return
	}
	a, err := s.Core.DelegateApproval(r.Context(), principal(r), approvalID, d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) ListRequestApprovals(w http.ResponseWriter, r *http.Request, requestID api.RequestIdPath) {
	items, err := s.Core.RequestApprovals(r.Context(), principal(r), requestID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
