package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// Administration (S9, ADR-0020): answered 501 until built.

func (s *Server) ListApprovalPolicies(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) CreateApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteApprovalPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateApprovalPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) ListDay2Policies(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) CreateDay2Policy(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteDay2Policy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateDay2Policy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) ListAdminEnvironments(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.AdminEnvironments(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.AdminEnvironmentList{Items: items}, err)
}

func (s *Server) CreateEnvironment(w http.ResponseWriter, r *http.Request) {
	var e api.AdminEnvironment
	if !s.decode(w, r, &e) {
		return
	}
	out, err := s.Core.CreateEnvironment(r.Context(), principal(r), e)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) DeleteEnvironment(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.replyEmpty(w, r, s.Core.DeleteEnvironment(r.Context(), principal(r), key))
}

func (s *Server) UpdateEnvironment(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	var u api.AdminEnvironmentUpdate
	if !s.decode(w, r, &u) {
		return
	}
	out, err := s.Core.UpdateEnvironment(r.Context(), principal(r), key, u)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListGroupMappings(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.GroupMappings(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.GroupMappingList{Items: items}, err)
}

func (s *Server) CreateGroupMapping(w http.ResponseWriter, r *http.Request) {
	var m api.GroupMappingWrite
	if !s.decode(w, r, &m) {
		return
	}
	out, err := s.Core.CreateGroupMapping(r.Context(), principal(r), m)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) DeleteGroupMapping(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteGroupMapping(r.Context(), principal(r), id))
}

func (s *Server) GetIntegrations(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) SetIntegration(w http.ResponseWriter, r *http.Request, kind api.IntegrationKindPath) {
	s.notBuilt(w, r)
}

func (s *Server) TestIntegration(w http.ResponseWriter, r *http.Request, kind api.IntegrationKindPath) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteLeasePolicy(w http.ResponseWriter, r *http.Request, params api.DeleteLeasePolicyParams) {
	s.notBuilt(w, r)
}

func (s *Server) ListLeasePolicies(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) SetLeasePolicy(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) ListNamingPolicies(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) CreateNamingPolicy(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteNamingPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateNamingPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) PreviewNaming(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) ListNetworkMappings(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteNetworkMapping(w http.ResponseWriter, r *http.Request, network string) {
	s.notBuilt(w, r)
}

func (s *Server) SetNetworkMapping(w http.ResponseWriter, r *http.Request, network string) {
	s.notBuilt(w, r)
}

func (s *Server) PreviewPlacement(w http.ResponseWriter, r *http.Request, params api.PreviewPlacementParams) {
	s.notBuilt(w, r)
}

func (s *Server) ListPlacementRules(w http.ResponseWriter, r *http.Request, params api.ListPlacementRulesParams) {
	s.notBuilt(w, r)
}

func (s *Server) CreatePlacementRule(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) DeletePlacementRule(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdatePlacementRule(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) ExplainPolicies(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) ListAdminProjects(w http.ResponseWriter, r *http.Request, params api.ListAdminProjectsParams) {
	items, err := s.Core.AdminProjects(r.Context(), principal(r), deref(params.Q), params.Archived != nil && *params.Archived)
	s.reply(w, r, http.StatusOK, api.AdminProjectList{Items: items}, err)
}

func (s *Server) CreateProject(w http.ResponseWriter, r *http.Request) {
	var c api.AdminProjectCreate
	if !s.decode(w, r, &c) {
		return
	}
	out, err := s.Core.CreateProject(r.Context(), principal(r), c)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) GetAdminProject(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	out, err := s.Core.AdminProject(r.Context(), principal(r), projectId)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) UpdateProject(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	var u api.AdminProjectUpdate
	if !s.decode(w, r, &u) {
		return
	}
	out, err := s.Core.UpdateProject(r.Context(), principal(r), projectId, u)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) GetProjectQuotas(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) SetProjectQuotas(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) ListSizePresets(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) CreateSizePreset(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteSizePreset(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateSizePreset(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.notBuilt(w, r)
}

func (s *Server) ListVmTemplates(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) CreateVmTemplate(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteVmTemplate(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateVmTemplate(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.notBuilt(w, r)
}

func (s *Server) ListAuditEvents(w http.ResponseWriter, r *http.Request, params api.ListAuditEventsParams) {
	s.notBuilt(w, r)
}

func (s *Server) ListProjectMembers(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	items, err := s.Core.ProjectMembers(r.Context(), principal(r), projectId)
	s.reply(w, r, http.StatusOK, api.ProjectMemberList{Items: items}, err)
}

func (s *Server) RemoveProjectMember(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath, userId api.UserIdPath) {
	s.replyEmpty(w, r, s.Core.RemoveProjectMember(r.Context(), principal(r), projectId, userId))
}

func (s *Server) SetProjectMember(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath, userId api.UserIdPath) {
	var m api.ProjectMemberWrite
	if !s.decode(w, r, &m) {
		return
	}
	out, err := s.Core.SetProjectMember(r.Context(), principal(r), projectId, userId, string(m.Role))
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListApiTokens(w http.ResponseWriter, r *http.Request, params api.ListApiTokensParams) {
	s.notBuilt(w, r)
}

func (s *Server) CreateApiToken(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) RevokeApiToken(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
}

func (s *Server) SearchUsers(w http.ResponseWriter, r *http.Request, params api.SearchUsersParams) {
	items, err := s.Core.SearchUsers(r.Context(), deref(params.Q), limitOf(params.Limit))
	s.reply(w, r, http.StatusOK, api.UserList{Items: items}, err)
}
