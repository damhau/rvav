package e2e

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestRolesAPI: roles made of permissions, given in a project and for all of
// rvav, to users and groups, with what can't be taken away (ADR-0034).
func TestRolesAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }

		// The catalogue, for anyone signed in.
		var catalogue api.PermissionCatalogue
		h.json("GET", "/api/v1/permissions", "tnguyen", nil, 200, &catalogue)
		power := slices.IndexFunc(catalogue.Day2, func(g api.Day2PermissionGroup) bool { return g.Group == "power" })
		if !slices.ContainsFunc(catalogue.Items, func(p api.PermissionInfo) bool { return p.Id == "access.manage" && p.Scope == "rvav" }) ||
			power < 0 || !slices.ContainsFunc(catalogue.Day2[power].Actions, func(a struct {
			Key        string         `json:"key"`
			Name       string         `json:"name"`
			Permission api.Permission `json:"permission"`
		}) bool {
			return a.Permission == "day2.power:power_on"
		}) {
			t.Fatalf("catalogue = %+v", catalogue)
		}

		// The built-in roles, for those who see access; fixed.
		h.json("GET", "/api/v1/admin/roles", "tnguyen", nil, 403, nil)
		var roles api.RoleList
		h.json("GET", "/api/v1/admin/roles", admin, nil, 200, &roles)
		if len(roles.Items) != 8 || !roles.Items[0].BuiltIn {
			t.Fatalf("roles = %+v", roles.Items)
		}
		h.json("PATCH", "/api/v1/admin/roles/member", admin, api.RoleUpdate{Name: new("Members")}, 409, nil)
		h.json("DELETE", "/api/v1/admin/roles/viewer", admin, nil, 409, nil)

		// A custom role: its key, a name, permissions rvav knows, project.see with one in a project.
		operator := api.RoleCreate{Key: "vm-operator", Name: "VM operator",
			Permissions: []string{"project.see", "day2.power:power_on", "day2.power:power_off"}}
		h.json("POST", "/api/v1/admin/roles", "lmartin", operator, 403, nil)
		for _, bad := range []api.RoleCreate{
			{Key: "admin", Name: "x", Permissions: []string{"project.see"}},
			{Key: "supervisor", Name: "x", Permissions: []string{"project.see"}},
			{Key: "owner", Name: "x", Permissions: []string{"project.see"}},
			{Key: "new", Name: "x", Permissions: []string{"project.see"}},
			{Key: "Bad!", Name: "x", Permissions: []string{"project.see"}},
			{Key: "nothing", Name: "x", Permissions: []string{}},
			{Key: "unseen", Name: "x", Permissions: []string{"day2.power"}},
			{Key: "odd", Name: "x", Permissions: []string{"day2.nonsense", "rule.everything"}},
		} {
			h.json("POST", "/api/v1/admin/roles", admin, bad, 422, &problem)
		}
		var vmop api.Role
		h.json("POST", "/api/v1/admin/roles", admin, operator, 201, &vmop)
		if vmop.Scope != api.RoleScopeAny || vmop.BuiltIn || vmop.Holders != 0 || len(vmop.Permissions) != 3 {
			t.Fatalf("created %+v", vmop)
		}
		h.json("POST", "/api/v1/admin/roles", admin, operator, 409, nil)
		var reader api.Role
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "policy-reader", Name: "Policy reader",
			Permissions: []string{"policies.read"}}, 201, &reader)
		if reader.Scope != api.RoleScopeRvav {
			t.Fatalf("a role with a permission for all of rvav = %+v", reader)
		}

		// In a project: several roles at once; a project admin gives no more than they hold.
		cdubois := "/api/v1/projects/prj_web/members/" + seed.UserID("cdubois")
		withOperator := api.ProjectMemberWrite{Roles: &[]string{"viewer", "vm-operator"}}
		h.json("PUT", cdubois, "lmartin", withOperator, 403, nil)
		h.json("PUT", cdubois, admin, api.ProjectMemberWrite{Roles: &[]string{"policy-reader"}}, 422, nil)
		h.json("PUT", cdubois, admin, api.ProjectMemberWrite{Role: new("member"), Roles: &[]string{"viewer"}}, 422, nil)
		var member api.ProjectMember
		h.json("PUT", cdubois, admin, withOperator, 200, &member)
		if roleOf(member.Role) != "viewer" || !slices.Equal(member.DirectRoles, []string{"viewer", "vm-operator"}) || len(member.Roles) != 2 {
			t.Fatalf("member = %+v", member)
		}
		var me api.Me
		h.json("GET", "/api/v1/me", "cdubois", nil, 200, &me)
		web := slices.IndexFunc(me.Projects, func(p api.MeProject) bool { return p.Project.Id == "prj_web" })
		if web < 0 || !slices.Equal(roleKeys(me.Projects[web].Roles), []string{"viewer", "vm-operator"}) ||
			!slices.Contains(me.Projects[web].Permissions, "day2.power:power_on") || slices.Contains(me.Permissions, "project.see") {
			t.Fatalf("me = %+v", me)
		}
		h.json("PATCH", "/api/v1/admin/roles/vm-operator", admin, api.RoleUpdate{Permissions: &[]string{"project.see", "policies.read"}}, 422, nil)
		h.json("DELETE", "/api/v1/admin/roles/vm-operator", admin, nil, 409, nil)
		h.json("PUT", cdubois, "lmartin", api.ProjectMemberWrite{Role: new("member")}, 403, nil) // taking vm-operator away

		// A change of access ends every live stream with resync, someone else's too.
		stream := make(chan string, 16)
		sctx, stop := context.WithCancel(ctx)
		defer stop()
		go readSSE(sctx, h.srv.URL+"/api/v1/events?topics=deployments", "tnguyen", "", stream)
		time.Sleep(200 * time.Millisecond)
		h.json("PUT", cdubois, admin, api.ProjectMemberWrite{Role: new("member")}, 200, &member)
		select {
		case e := <-stream:
			if e != " resync" {
				t.Fatalf("after a change of access, the stream got %q", e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a change of access didn't end the stream")
		}

		// Groups map to any role, within its scope.
		var mapping api.GroupMapping
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "rvav-ops", ProjectId: new("prj_web"),
			Role: "vm-operator"}, 201, &mapping)
		if deref(mapping.RoleName) != "VM operator" {
			t.Fatalf("mapping = %+v", mapping)
		}
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "rvav-ops", ProjectId: new("prj_web"),
			Role: "policy-reader"}, 422, nil)
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "rvav-ops", Role: "nonsense"}, 422, nil)
		h.json("DELETE", "/api/v1/admin/roles/vm-operator", admin, nil, 409, nil)
		h.json("DELETE", "/api/v1/admin/group-mappings/"+mapping.Id, admin, nil, 204, nil)

		// For all of rvav, to users.
		var given api.RoleAssignment
		h.json("POST", "/api/v1/admin/role-assignments", "lmartin", api.RoleAssignmentWrite{UserId: seed.UserID("cdubois"), Role: "auditor"}, 403, nil)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID("cdubois"), Role: "member"}, 422, nil)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID("cdubois"), Role: "user"}, 422, nil)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID("cdubois"), Role: "auditor"}, 201, &given)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID("cdubois"), Role: "auditor"}, 409, nil)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID("mkeller"), Role: "admin"}, 409, nil)
		var list api.RoleAssignmentList
		h.json("GET", "/api/v1/admin/role-assignments?user_id="+seed.UserID("cdubois"), "cdubois", nil, 200, &list)
		if len(list.Items) != 1 || list.Items[0].Fixed || list.Items[0].Role.Key != "auditor" {
			t.Fatalf("assignments = %+v", list.Items)
		}
		h.json("GET", "/api/v1/me", "cdubois", nil, 200, &me)
		if !slices.Contains(me.Permissions, "access.read") || !slices.Contains(roleKeys(me.Roles), "auditor") {
			t.Fatalf("an auditor for all of rvav: %+v", me)
		}
		h.json("DELETE", "/api/v1/admin/role-assignments/"+deref(given.Id), admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/roles/vm-operator", admin, nil, 204, nil)

		// The last way anyone holds access.manage stays, whatever role gives it.
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "access-admin", Name: "Access admin",
			Permissions: []string{"access.manage", "access.read"}}, 201, nil)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID("cdubois"), Role: "access-admin"}, 201, &given)
		if _, err := h.store.Pool.Exec(ctx, `update users set global_roles = array_remove(global_roles, 'admin');
			delete from group_mappings where role = 'admin'`); err != nil {
			t.Fatal(err)
		}
		h.json("PATCH", "/api/v1/admin/roles/access-admin", "cdubois", api.RoleUpdate{Permissions: &[]string{"access.read"}}, 409, nil)
		h.json("DELETE", "/api/v1/admin/role-assignments/"+deref(given.Id), "cdubois", nil, 409, nil)
		h.json("PATCH", "/api/v1/admin/roles/access-admin", "cdubois", api.RoleUpdate{Description: new("Gives roles.")}, 200, nil)

		for _, action := range []string{"role.create", "role.update", "role.delete", "role_assignment.create", "role_assignment.delete",
			"member.set", "group_mapping.create", "group_mapping.delete"} {
			if h.auditCount(action) == 0 {
				t.Errorf("%s isn't audited", action)
			}
		}
	})
}

// TestCustomRoleAdministration: the administration's checks are the
// permissions of roles, given in a project or for all of rvav (ADR-0034).
func TestCustomRoleAdministration(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, user = "mkeller", "cdubois"
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "placement-steward", Name: "Placement steward",
			Permissions: []string{"project.see", "project.settings.read", "placement.manage", "project.quotas.manage", "audit.read"}}, 201, nil)
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "policy-reader", Name: "Policy reader",
			Permissions: []string{"policies.read", "infrastructure.read"}}, 201, nil)
		for _, path := range []string{"/api/v1/admin/lease-policies", "/api/v1/admin/templates", "/api/v1/admin/projects/prj_web",
			"/api/v1/admin/placement-rules?project_id=prj_web", "/api/v1/audit"} {
			if code, _, _ := h.do("GET", path, user, nil); code != 403 && code != 404 {
				t.Errorf("GET %s before any role = %d", path, code)
			}
		}
		h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID(user), admin,
			api.ProjectMemberWrite{Roles: &[]string{"placement-steward"}}, 200, nil)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID(user), Role: "policy-reader"}, 201, nil)

		// For all of rvav: read, not change.
		h.json("GET", "/api/v1/admin/lease-policies", user, nil, 200, nil)
		h.json("GET", "/api/v1/admin/templates", user, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/lease-policies", user, api.LeasePolicy{Environment: "dev", DefaultDays: new(30)}, 403, nil)
		h.json("GET", "/api/v1/admin/subscriptions", user, nil, 403, nil)
		h.json("GET", "/api/v1/admin/system", user, nil, 403, nil)

		// In Web Platform: its administration, its placement and quotas, its audit log; not its details.
		var project api.Project
		h.json("GET", "/api/v1/projects/prj_web", user, nil, 200, &project)
		if mine := *project.MyPermissions; !slices.Contains(mine, "placement.manage") || slices.Contains(mine, "request.submit") {
			t.Fatalf("my permissions in Web Platform = %v", project.MyPermissions)
		}
		h.json("GET", "/api/v1/projects/prj_data", user, nil, 404, nil)
		h.json("GET", "/api/v1/admin/projects/prj_web", user, nil, 200, nil)
		h.json("PATCH", "/api/v1/admin/projects/prj_web", user, api.AdminProjectUpdate{Name: new("Web")}, 403, nil)
		h.json("GET", "/api/v1/admin/projects/prj_data", user, nil, 404, nil)
		h.json("GET", "/api/v1/admin/projects", user, nil, 403, nil)
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", user, nil, 200, &rules)
		r := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })]
		rule := api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev", Priority: &r.Priority, Clusters: r.Clusters,
			Datastores: r.Datastores, Networks: r.Networks, Folder: r.Folder, ClusterTags: &r.ClusterTags, DatastoreTags: &r.DatastoreTags}
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, user, rule, 200, nil)
		rule.ProjectId = "prj_data"
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, user, rule, 403, nil)
		h.json("POST", "/api/v1/admin/placement-rules", user, rule, 403, nil)
		h.json("PUT", "/api/v1/admin/projects/prj_web/quotas", user, api.QuotaLimitList{Items: []api.QuotaLimit{
			{Environment: "", Resource: api.Vcpu, Limit: new(200), Enforcement: api.QuotaLimitEnforcementHard}}}, 200, nil)
		h.json("PUT", "/api/v1/admin/projects/prj_data/quotas", user, api.QuotaLimitList{Items: []api.QuotaLimit{}}, 403, nil)
		var page api.AuditEventPage
		h.json("GET", "/api/v1/audit", user, nil, 200, &page)
		if len(page.Items) == 0 || slices.ContainsFunc(page.Items, func(e api.AuditEvent) bool { return e.Project == nil || e.Project.Id != "prj_web" }) {
			t.Fatalf("Web Platform's audit log = %+v", page.Items)
		}
		h.json("GET", "/api/v1/audit?project_id=prj_data", user, nil, 200, &page)
		if len(page.Items) != 0 {
			t.Fatalf("Data Engineering's audit log = %+v", page.Items)
		}
	})
}
