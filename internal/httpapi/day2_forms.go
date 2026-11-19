package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// Forms designed for custom day-2 actions (ADR-0050), and an action's form
// being filled in.

func (s *Server) PreviewDeploymentAction(w http.ResponseWriter, r *http.Request, deploymentID api.DeploymentIdPath) {
	var q api.Day2FormQuery
	if !s.decode(w, r, &q) {
		return
	}
	out, err := s.Core.PreviewAction(r.Context(), principal(r), deploymentID, q)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ResolveDeploymentActionValues(w http.ResponseWriter, r *http.Request, deploymentID api.DeploymentIdPath) {
	var q api.Day2FieldValuesQuery
	if !s.decode(w, r, &q) {
		return
	}
	if q.Inputs == nil {
		q.Inputs = map[string]any{}
	}
	out, err := s.Core.ActionFieldValues(r.Context(), principal(r), deploymentID, q)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListDay2Forms(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.Day2Forms(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) GetDay2Form(w http.ResponseWriter, r *http.Request, action api.Day2ActionPath) {
	out, err := s.Core.Day2Form(r.Context(), principal(r), action)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) SaveDay2Form(w http.ResponseWriter, r *http.Request, action api.Day2ActionPath) {
	var body api.Day2FormWrite
	if !s.decode(w, r, &body) {
		return
	}
	out, err := s.Core.SaveDay2Form(r.Context(), principal(r), action, body)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteDay2Form(w http.ResponseWriter, r *http.Request, action api.Day2ActionPath) {
	s.replyEmpty(w, r, s.Core.DeleteDay2Form(r.Context(), principal(r), action))
}

func (s *Server) PreviewDay2Form(w http.ResponseWriter, r *http.Request, action api.Day2ActionPath) {
	var q api.Day2FormDesignerQuery
	if !s.decode(w, r, &q) {
		return
	}
	out, err := s.Core.PreviewDay2Form(r.Context(), principal(r), action, q)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ResolveDay2FormValues(w http.ResponseWriter, r *http.Request, action api.Day2ActionPath) {
	var q api.Day2FormDesignerValuesQuery
	if !s.decode(w, r, &q) {
		return
	}
	if q.Inputs == nil {
		q.Inputs = map[string]any{}
	}
	out, err := s.Core.Day2FormValues(r.Context(), principal(r), action, q)
	s.reply(w, r, http.StatusOK, out, err)
}
