package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) ListVariables(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.Variables(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.VariableList{Items: items}, err)
}

func (s *Server) CreateVariable(w http.ResponseWriter, r *http.Request) {
	var b api.VariableCreate
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.CreateVariable(r.Context(), principal(r), b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetVariable(w http.ResponseWriter, r *http.Request, key string) {
	out, err := s.Core.Variable(r.Context(), principal(r), key)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) UpdateVariable(w http.ResponseWriter, r *http.Request, key string) {
	var b api.VariableUpdate
	if !s.decodeStrict(w, r, &b) {
		return
	}
	out, err := s.Core.UpdateVariable(r.Context(), principal(r), key, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteVariable(w http.ResponseWriter, r *http.Request, key string) {
	s.replyEmpty(w, r, s.Core.DeleteVariable(r.Context(), principal(r), key))
}
