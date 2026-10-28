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
	s.notBuilt(w, r)
}

func (s *Server) CreateEnvironment(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteEnvironment(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateEnvironment(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.notBuilt(w, r)
}

func (s *Server) ListGroupMappings(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) CreateGroupMapping(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) DeleteGroupMapping(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.notBuilt(w, r)
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
	s.notBuilt(w, r)
}

func (s *Server) CreateProject(w http.ResponseWriter, r *http.Request) {
	s.notBuilt(w, r)
}

func (s *Server) GetAdminProject(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) UpdateProject(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	s.notBuilt(w, r)
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
	s.notBuilt(w, r)
}

func (s *Server) RemoveProjectMember(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath, userId api.UserIdPath) {
	s.notBuilt(w, r)
}

func (s *Server) SetProjectMember(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath, userId api.UserIdPath) {
	s.notBuilt(w, r)
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
	s.notBuilt(w, r)
}
