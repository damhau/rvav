package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

func (s *Server) ListPermissions(w http.ResponseWriter, r *http.Request) {
	out, err := s.Core.Permissions(r.Context())
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListRoles(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.Roles(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.RoleList{Items: items}, err)
}

func (s *Server) CreateRole(w http.ResponseWriter, r *http.Request) {
	var c api.RoleCreate
	if !s.decode(w, r, &c) {
		return
	}
	out, err := s.Core.CreateRole(r.Context(), principal(r), c)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetRole(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	out, err := s.Core.Role(r.Context(), principal(r), key)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) UpdateRole(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	var u api.RoleUpdate
	if !s.decode(w, r, &u) {
		return
	}
	out, err := s.Core.UpdateRole(r.Context(), principal(r), key, u)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteRole(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.replyEmpty(w, r, s.Core.DeleteRole(r.Context(), principal(r), key))
}

func (s *Server) ListRoleAssignments(w http.ResponseWriter, r *http.Request, params api.ListRoleAssignmentsParams) {
	items, err := s.Core.RoleAssignments(r.Context(), principal(r), deref(params.Role), deref(params.UserId))
	s.reply(w, r, http.StatusOK, api.RoleAssignmentList{Items: items}, err)
}

func (s *Server) CreateRoleAssignment(w http.ResponseWriter, r *http.Request) {
	var a api.RoleAssignmentWrite
	if !s.decode(w, r, &a) {
		return
	}
	out, err := s.Core.CreateRoleAssignment(r.Context(), principal(r), a)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) DeleteRoleAssignment(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteRoleAssignment(r.Context(), principal(r), id))
}
