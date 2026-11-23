package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// projectRoleHolder holds, in Web Platform only, a custom role made of
// project.see and one permission that changes a part of the project.
const projectRoleHolder = "cdubois"

// giveProjectRole makes that role and gives it to projectRoleHolder in Web
// Platform, then checks they open the project's page with it.
func giveProjectRole(h *harness, perm string) {
	h.t.Helper()
	h.json("POST", "/api/v1/admin/roles", "mkeller", api.RoleCreate{Key: "one-part", Name: "One part",
		Permissions: []string{"project.see", perm}}, 201, nil)
	h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID(projectRoleHolder), "mkeller",
		api.ProjectMemberWrite{Roles: &[]string{"one-part"}}, 200, nil)
	var project api.AdminProject
	h.json("GET", "/api/v1/admin/projects/prj_web", projectRoleHolder, nil, 200, &project)
	if project.Name != "Web Platform" || project.MyPermissions == nil || !slices.Contains(*project.MyPermissions, perm) {
		h.t.Fatalf("project = %+v", project)
	}
}

// refusedReads are what a project's settings readers, and those who change
// its placement or its policies, read: not a role that changes another part.
func refusedReads(h *harness, but ...string) {
	h.t.Helper()
	reads := []struct{ method, path string }{
		{"GET", "/api/v1/admin/projects"},
		{"GET", "/api/v1/admin/projects/prj_web/quotas"},
		{"GET", "/api/v1/admin/projects/prj_web/user-quotas"},
		{"GET", "/api/v1/admin/quotas"},
		{"GET", "/api/v1/admin/placement-rules?project_id=prj_web"},
		{"GET", "/api/v1/admin/placement-preview?project_id=prj_web&environment=dev"},
		{"GET", "/api/v1/admin/ad-placements?project_id=prj_web"},
		{"POST", "/api/v1/admin/policies/explain"},
		{"GET", "/api/v1/projects/prj_web/assignable-roles"},
	}
	for _, r := range reads {
		if slices.Contains(but, r.path) {
			continue
		}
		var body any
		if r.method == "POST" {
			body = api.PolicyExplainQuery{ProjectId: "prj_web", Environment: "dev"}
		}
		if code, b, _ := h.do(r.method, r.path, projectRoleHolder, body); code != 403 {
			h.t.Errorf("%s %s = %d, want 403: %s", r.method, r.path, code, b)
		}
	}
}

// TestQuotasRole: a custom role that sets a project's quotas, and nothing
// else, opens the project and reads its quotas, each member's and what they
// take, as it changes them (#217). Its placement, policies and members'
// roles, rvav's quotas and every project's list stay their readers'.
func TestQuotasRole(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		giveProjectRole(h, "project.quotas.manage")
		const holder = projectRoleHolder

		var limits api.QuotaLimitList
		h.json("GET", "/api/v1/admin/projects/prj_web/quotas", holder, nil, 200, &limits)
		if !slices.ContainsFunc(limits.Items, func(l api.QuotaLimit) bool { return l.Resource == api.Vcpu && l.Environment == "" }) {
			t.Fatalf("limits = %+v", limits.Items)
		}
		var use api.QuotaUsage
		h.json("GET", "/api/v1/projects/prj_web/quota", holder, nil, 200, &use)
		if len(use.Lines) == 0 {
			t.Fatalf("use = %+v", use)
		}
		each := api.UserQuotaWriteList{Items: []api.UserQuotaWrite{{Resource: api.Vcpu, Limit: new(8),
			Enforcement: api.UserQuotaWriteEnforcementHard}}}
		h.json("PUT", "/api/v1/admin/projects/prj_web/user-quotas", holder, each, 200, nil)
		var members api.UserQuotaList
		h.json("GET", "/api/v1/admin/projects/prj_web/user-quotas", holder, nil, 200, &members)
		if len(members.Items) != 1 || members.Items[0].Limit == nil || *members.Items[0].Limit != 8 {
			t.Fatalf("each member's quota = %+v", members.Items)
		}
		h.json("PUT", "/api/v1/admin/projects/prj_web/quotas", holder, limits, 200, nil)

		refusedReads(h, "/api/v1/admin/projects/prj_web/quotas", "/api/v1/admin/projects/prj_web/user-quotas")
		h.json("PATCH", "/api/v1/admin/projects/prj_web", holder, api.AdminProjectUpdate{Description: new("Mine")}, 403, nil)
		h.json("GET", "/api/v1/admin/projects/prj_data/quotas", holder, nil, 404, nil)

		// For all of rvav: every project, and rvav's own quotas, which its save replaces.
		h.json("POST", "/api/v1/admin/role-assignments", "mkeller", api.RoleAssignmentWrite{UserId: seed.UserID(holder),
			Role: "one-part"}, 201, nil)
		var all api.AdminProjectList
		h.json("GET", "/api/v1/admin/projects", holder, nil, 200, &all)
		if len(all.Items) != 4 {
			t.Fatalf("projects = %+v", all.Items)
		}
		h.json("GET", "/api/v1/admin/quotas", holder, nil, 200, nil)
		h.json("GET", "/api/v1/admin/placement-rules", holder, nil, 403, nil)
	})
}

// TestMembersRole: a custom role that manages a project's members, and
// nothing else, opens the project, its members, the roles it may give and
// the people it may add, and changes them and who sees its deployments
// (#217). The project's quotas, placement and policies stay its settings
// readers'.
func TestMembersRole(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		giveProjectRole(h, "project.members.manage")
		const holder = projectRoleHolder

		var members api.ProjectMemberList
		h.json("GET", "/api/v1/projects/prj_web/members", holder, nil, 200, &members)
		if !slices.ContainsFunc(members.Items, func(m api.ProjectMember) bool { return m.User.Id == seed.UserID("tnguyen") }) {
			t.Fatalf("members = %+v", members.Items)
		}
		var offered api.AssignableRoleList
		h.json("GET", "/api/v1/projects/prj_web/assignable-roles", holder, nil, 200, &offered)
		var keys []string
		for _, r := range offered.Items {
			keys = append(keys, r.Key)
		}
		slices.Sort(keys)
		// Only roles whose permissions it holds: no one gives more than they hold.
		if !slices.Equal(keys, []string{"one-part", "viewer"}) {
			t.Fatalf("roles offered = %v", keys)
		}
		var people api.UserList
		h.json("GET", "/api/v1/users?q=Alain", holder, nil, 200, &people)
		if len(people.Items) != 1 || people.Items[0].Id != seed.UserID("afavre") {
			t.Fatalf("people = %+v", people.Items)
		}
		h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID("afavre"), holder,
			api.ProjectMemberWrite{Roles: &[]string{"viewer"}}, 200, nil)
		h.json("DELETE", "/api/v1/projects/prj_web/members/"+seed.UserID("afavre"), holder, nil, 204, nil)
		h.json("PUT", "/api/v1/projects/prj_web/deployment-sharing", holder,
			api.DeploymentSharingWrite{DeploymentSharing: api.OwnerOnly}, 200, nil)
		var project api.AdminProject
		h.json("GET", "/api/v1/admin/projects/prj_web", holder, nil, 200, &project)
		if project.DeploymentSharing == nil || *project.DeploymentSharing != api.OwnerOnly {
			t.Fatalf("project = %+v", project)
		}

		refusedReads(h, "/api/v1/projects/prj_web/assignable-roles")
		h.json("PATCH", "/api/v1/admin/projects/prj_web", holder, api.AdminProjectUpdate{Description: new("Mine")}, 403, nil)
	})
}

// TestProjectUpdateRole: a custom role that changes a project's details,
// and nothing else, opens the project with its details, and changes them
// (#217). The project's quotas, placement, policies and members' roles stay
// their readers'.
func TestProjectUpdateRole(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		giveProjectRole(h, "project.update")
		const holder = projectRoleHolder

		h.json("GET", "/api/v1/environments", holder, nil, 200, nil)
		var project api.AdminProject
		h.json("PATCH", "/api/v1/admin/projects/prj_web", holder, api.AdminProjectUpdate{Description: new("Storefront")}, 200, &project)
		if deref(project.Description) != "Storefront" {
			t.Fatalf("project = %+v", project)
		}

		refusedReads(h)
		h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID("afavre"), holder,
			api.ProjectMemberWrite{Roles: &[]string{"viewer"}}, 403, nil)
		h.json("PUT", "/api/v1/admin/projects/prj_web/quotas", holder, api.QuotaLimitList{Items: []api.QuotaLimit{}}, 403, nil)
	})
}
