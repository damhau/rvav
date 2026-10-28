package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// Events, subscriptions and custom workflows (S10, ADR-0021 and ADR-0022):
// answered 501 until built.

func (s *Server) ListEventTopics(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) ListSubscriptions(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) CreateSubscription(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) GetSubscription(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateSubscription(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteSubscription(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) TestSubscription(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) ListSubscriptionRuns(w http.ResponseWriter, r *http.Request, id api.IdPath, params api.ListSubscriptionRunsParams) {
	s.notBuilt(w, r)
}

func (s *Server) GetSubscriptionRun(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) RetrySubscriptionRun(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) ListWorkflows(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) RegisterWorkflow(w http.ResponseWriter, r *http.Request) { s.notBuilt(w, r) }

func (s *Server) GetWorkflow(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateWorkflow(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteWorkflow(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.notBuilt(w, r)
}
