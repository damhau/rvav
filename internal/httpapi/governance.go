package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
)

// Administration (S9, ADR-0020): answered 501 until built.

func (s *Server) ListApprovalPolicies(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.ApprovalPolicies(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.ApprovalPolicyList{Items: items}, err)
}

func (s *Server) CreateApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	var b api.ApprovalPolicyWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveApprovalPolicy(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) DeleteApprovalPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteApprovalPolicy(r.Context(), principal(r), id))
}

func (s *Server) UpdateApprovalPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.ApprovalPolicyWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveApprovalPolicy(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListDay2Policies(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.Day2Policies(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.Day2PolicyList{Items: items}, err)
}

func (s *Server) CreateDay2Policy(w http.ResponseWriter, r *http.Request) {
	var b api.Day2PolicyWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveDay2Policy(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) DeleteDay2Policy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteDay2Policy(r.Context(), principal(r), id))
}

func (s *Server) UpdateDay2Policy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.Day2PolicyWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveDay2Policy(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, out, err)
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
	items, err := s.Core.Integrations(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.IntegrationList{Items: items}, err)
}

func (s *Server) SetIntegration(w http.ResponseWriter, r *http.Request, kind api.IntegrationKindPath) {
	var b api.IntegrationWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SetIntegration(r.Context(), principal(r), string(kind), b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) TestIntegration(w http.ResponseWriter, r *http.Request, kind api.IntegrationKindPath) {
	var b *api.IntegrationWrite
	if r.ContentLength != 0 {
		b = &api.IntegrationWrite{}
		if !s.decode(w, r, b) {
			return
		}
	}
	out, err := s.Core.TestIntegration(r.Context(), principal(r), string(kind), b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) DeleteLeasePolicy(w http.ResponseWriter, r *http.Request, params api.DeleteLeasePolicyParams) {
	s.replyEmpty(w, r, s.Core.DeleteLeaseOverride(r.Context(), principal(r), params.Environment, params.ProjectId))
}

func (s *Server) ListLeasePolicies(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.LeasePolicies(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.LeasePolicyList{Items: items}, err)
}

func (s *Server) SetLeasePolicy(w http.ResponseWriter, r *http.Request) {
	var b api.LeasePolicy
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SetLeasePolicy(r.Context(), principal(r), b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListNamingPolicies(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.NamingPolicies(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.NamingPolicyList{Items: items}, err)
}

func (s *Server) CreateNamingPolicy(w http.ResponseWriter, r *http.Request) {
	var b api.NamingPolicyWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveNamingPolicy(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) DeleteNamingPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteNamingPolicy(r.Context(), principal(r), id))
}

func (s *Server) UpdateNamingPolicy(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.NamingPolicyWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveNamingPolicy(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) PreviewNaming(w http.ResponseWriter, r *http.Request) {
	var q api.NamingPreviewQuery
	if !s.decode(w, r, &q) {
		return
	}
	out, err := s.Core.PreviewNaming(r.Context(), principal(r), q)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListNetworkMappings(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.NetworkMappings(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.NetworkMappingList{Items: items}, err)
}

func (s *Server) DeleteNetworkMapping(w http.ResponseWriter, r *http.Request, network string) {
	s.replyEmpty(w, r, s.Core.DeleteNetworkMapping(r.Context(), principal(r), network))
}

func (s *Server) SetNetworkMapping(w http.ResponseWriter, r *http.Request, network string) {
	var b api.NetworkMappingWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SetNetworkMapping(r.Context(), principal(r), network, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) PreviewPlacement(w http.ResponseWriter, r *http.Request, params api.PreviewPlacementParams) {
	out, err := s.Core.PlacementPreview(r.Context(), principal(r), params.ProjectId, params.Environment)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListPlacementRules(w http.ResponseWriter, r *http.Request, params api.ListPlacementRulesParams) {
	items, err := s.Core.PlacementRules(r.Context(), principal(r), deref(params.ProjectId))
	s.reply(w, r, http.StatusOK, api.PlacementRuleList{Items: items}, err)
}

func (s *Server) CreatePlacementRule(w http.ResponseWriter, r *http.Request) {
	var b api.PlacementRuleWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SavePlacementRule(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) DeletePlacementRule(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeletePlacementRule(r.Context(), principal(r), id))
}

func (s *Server) UpdatePlacementRule(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.PlacementRuleWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SavePlacementRule(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ExplainPolicies(w http.ResponseWriter, r *http.Request) {
	var q api.PolicyExplainQuery
	if !s.decode(w, r, &q) {
		return
	}
	out, err := s.Core.ExplainPolicies(r.Context(), principal(r), q)
	s.reply(w, r, http.StatusOK, out, err)
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
	items, err := s.Core.ProjectQuotas(r.Context(), principal(r), projectId)
	s.reply(w, r, http.StatusOK, api.QuotaLimitList{Items: items}, err)
}

func (s *Server) SetProjectQuotas(w http.ResponseWriter, r *http.Request, projectId api.ProjectIdPath) {
	var b api.QuotaLimitList
	if !s.decode(w, r, &b) {
		return
	}
	items, err := s.Core.SetProjectQuotas(r.Context(), principal(r), projectId, b.Items)
	s.reply(w, r, http.StatusOK, api.QuotaLimitList{Items: items}, err)
}

func (s *Server) ListSizePresets(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.SizePresets(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.SizePresetList{Items: items}, err)
}

func (s *Server) CreateSizePreset(w http.ResponseWriter, r *http.Request) {
	var b api.SizePresetWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveSizePreset(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) DeleteSizePreset(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.replyEmpty(w, r, s.Core.DeleteSizePreset(r.Context(), principal(r), key))
}

func (s *Server) UpdateSizePreset(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	var b api.SizePresetWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveSizePreset(r.Context(), principal(r), key, b)
	s.reply(w, r, http.StatusOK, out, err)
}

func (s *Server) ListVmTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := s.Core.VMTemplates(r.Context(), principal(r))
	s.reply(w, r, http.StatusOK, api.VmTemplateList{Items: items}, err)
}

func (s *Server) CreateVmTemplate(w http.ResponseWriter, r *http.Request) {
	var b api.VmTemplateWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveVMTemplate(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) DeleteVmTemplate(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	s.replyEmpty(w, r, s.Core.DeleteVMTemplate(r.Context(), principal(r), key))
}

func (s *Server) UpdateVmTemplate(w http.ResponseWriter, r *http.Request, key api.KeyPath) {
	var b api.VmTemplateWrite
	if !s.decode(w, r, &b) {
		return
	}
	out, err := s.Core.SaveVMTemplate(r.Context(), principal(r), key, b)
	s.reply(w, r, http.StatusOK, out, err)
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
