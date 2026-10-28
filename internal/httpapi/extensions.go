package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// Events, subscriptions and custom workflows (S10, ADR-0021 and ADR-0022).

func (s *Server) ListEventTopics(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.EventTopics(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.EventTopicList{Items: items}, err)
}

func (s *Server) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.Subscriptions(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.SubscriptionList{Items: items}, err)
}

func (s *Server) CreateSubscription(w http.ResponseWriter, r *http.Request) {
	var b api.SubscriptionWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveSubscription(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetSubscription(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	out, err := s.Core.GetSubscription(r.Context(), principal(r), id)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) UpdateSubscription(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.SubscriptionWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveSubscription(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteSubscription(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteSubscription(r.Context(), principal(r), id))
}

func (s *Server) TestSubscription(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	out, err := s.Core.TestSubscription(r.Context(), principal(r), id)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListSubscriptionRuns(w http.ResponseWriter, r *http.Request, id api.IdPath, params api.ListSubscriptionRunsParams) {
	status := ""
	if params.Status != nil {
		status = string(*params.Status)
	}
	items, next, err := s.Core.SubscriptionRuns(r.Context(), principal(r), id, status, deref(params.Cursor), limitOf(params.Limit))
	s.reply(w, r, http.StatusOK, api.SubscriptionRunPage{Items: items, NextCursor: next}, err)
}

func (s *Server) GetSubscriptionRun(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	out, err := s.Core.SubscriptionRun(r.Context(), principal(r), id)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) RetrySubscriptionRun(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	out, err := s.Core.RetrySubscriptionRun(r.Context(), principal(r), id)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.WorkflowRegistrations(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.WorkflowRegistrationList{Items: items}, err)
}

func (s *Server) RegisterWorkflow(w http.ResponseWriter, r *http.Request) {
	var b api.WorkflowRegistrationWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveWorkflowRegistration(r.Context(), principal(r), "", true, b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetWorkflow(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	out, err := s.Core.GetWorkflowRegistration(r.Context(), principal(r), key)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) UpdateWorkflow(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	var b api.WorkflowRegistrationWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveWorkflowRegistration(r.Context(), principal(r), key, false, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteWorkflow(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.replyEmpty(w, r, s.Core.DeleteWorkflowRegistration(r.Context(), principal(r), key))
}
