package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestCustomRoles: an operator who may only power VMs on and off in one
// project, and an auditor made through the API who sees everything and
// changes nothing (ADR-0034).
func TestCustomRoles(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, operator, reviewer = "mkeller", "cdubois", "rreview"
		d := buildVM(h, model)

		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "vm-operator", Name: "VM operator",
			Permissions: []string{"project.see", "day2.power:power_on", "day2.power:power_off"}}, 201, nil)
		h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID(operator), admin,
			api.ProjectMemberWrite{Roles: &[]string{"vm-operator"}}, 200, nil)

		// The operator sees Web Platform and its deployments, nothing else.
		var deployments api.DeploymentPage
		h.json("GET", "/api/v1/deployments", operator, nil, 200, &deployments)
		if !slices.ContainsFunc(deployments.Items, func(x api.Deployment) bool { return x.Id == d.id }) ||
			slices.ContainsFunc(deployments.Items, func(x api.Deployment) bool { return x.Project.Id != "prj_web" }) {
			t.Fatalf("the operator's deployments = %+v", deployments.Items)
		}
		h.json("GET", "/api/v1/projects/prj_data", operator, nil, 404, nil)
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", operator, nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", operator, nil, 200, &form)
		h.json("POST", "/api/v1/requests", operator, api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{}}, 403, nil, "Idempotency-Key", uuid.NewString())

		// Power off and on: no day-2 policy matches, the role allows them; nothing else.
		a := d.actions(operator)
		if !a["power_off"].Enabled || a["reset"].Enabled || deref(a["reset"].DisabledReason) != "Your role in Web Platform does not allow Reset." {
			t.Fatalf("the operator's actions = %+v", a)
		}
		for _, other := range []string{"resize", "delete", "change_owner"} {
			if a[other].Enabled {
				t.Errorf("the operator may %s", other)
			}
		}
		d.succeed(operator, "power_off", nil)
		d.run(operator, "reset", nil, 403)
		d.succeed(operator, "power_on", nil)

		// A day-2 policy names the role: it decides first, and the role stays while it does.
		var deny api.Day2Policy
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "Operators don't power off", Priority: new(5),
			Actions: &[]string{"power_off"}, Roles: &[]string{"vm-operator"}, Effect: api.Day2PolicyWriteEffectDeny,
			Reason: new("Ask the owner.")}, 201, &deny)
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "x", Roles: &[]string{"auditor"},
			Effect: api.Day2PolicyWriteEffectAllow}, 422, nil)
		var day2 api.Day2PolicyList
		h.json("GET", "/api/v1/admin/day2-policies", admin, nil, 200, &day2)
		if !slices.Contains(day2.PolicyRoles, api.RoleRef{Key: "vm-operator", Name: "VM operator"}) {
			t.Fatalf("the roles a day-2 policy may name = %+v", day2.PolicyRoles)
		}
		if r := d.actions(operator)["power_off"]; r.Enabled || deref(r.DisabledReason) != "Ask the owner." {
			t.Fatalf("power_off under a policy denying it = %+v", r)
		}
		h.json("DELETE", "/api/v1/admin/roles/vm-operator", admin, nil, 409, nil)
		h.json("DELETE", "/api/v1/admin/day2-policies/"+deny.Id, admin, nil, 204, nil)

		// Conditions see the custom roles and the permissions held in the project.
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "Operators reset", Priority: new(5),
			Actions: &[]string{"reset"}, Condition: new(`"vm-operator" in user.roles && "day2.power:power_off" in user.permissions`),
			Effect: api.Day2PolicyWriteEffectAllow}, 201, nil)
		if r := d.actions(operator)["reset"]; !r.Enabled {
			t.Fatalf("reset under a policy for operators: %+v", r)
		}

		// An auditor of rvav's own making, for all of rvav.
		if _, err := h.store.Pool.Exec(ctx, `insert into users (id, subject, name, email) values
			('usr_rreview', 'seed:rreview', 'Rita Review', 'rita.review@corp.example')`); err != nil {
			t.Fatal(err)
		}
		var auditor api.Role
		h.json("GET", "/api/v1/admin/roles/auditor", admin, nil, 200, &auditor)
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "reviewer", Name: "Reviewer",
			Permissions: auditor.Permissions}, 201, nil)
		h.json("PUT", "/api/v1/projects/prj_web/members/usr_rreview", admin, api.ProjectMemberWrite{Roles: &[]string{"reviewer"}}, 422, nil)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: "usr_rreview", Role: "reviewer"}, 201, nil)

		var me api.Me
		h.json("GET", "/api/v1/me", reviewer, nil, 200, &me)
		if !slices.Equal(me.Permissions, slices.Sorted(slices.Values(auditor.Permissions))) || len(me.Projects) != 4 {
			t.Fatalf("the reviewer's /me = %+v", me)
		}
		h.json("GET", "/api/v1/deployments", reviewer, nil, 200, &deployments)
		if !slices.ContainsFunc(deployments.Items, func(x api.Deployment) bool { return x.Id == d.id }) {
			t.Fatalf("the reviewer's deployments = %+v", deployments.Items)
		}
		for _, path := range []string{"/api/v1/deployments/" + d.id, "/api/v1/requests/" + d.built.Id, "/api/v1/admin/projects",
			"/api/v1/admin/projects/prj_fin", "/api/v1/admin/environments", "/api/v1/admin/group-mappings", "/api/v1/admin/roles",
			"/api/v1/admin/lease-policies", "/api/v1/admin/day2-policies", "/api/v1/admin/templates", "/api/v1/admin/subscriptions",
			"/api/v1/admin/workflows", "/api/v1/audit", "/api/v1/approvals?view=all", "/api/v1/ansible/inventory",
			"/api/v1/reports?project_id=prj_core"} {
			h.json("GET", path, reviewer, nil, 200, nil)
		}
		for _, path := range []string{"/api/v1/admin/system", "/api/v1/admin/integrations", "/api/v1/admin/catalog/items",
			"/api/v1/admin/vra"} {
			h.json("GET", path, reviewer, nil, 403, nil)
		}
		h.json("PATCH", "/api/v1/admin/projects/prj_web", reviewer, api.AdminProjectUpdate{Name: new("Web")}, 403, nil)
		h.json("PUT", "/api/v1/admin/lease-policies", reviewer, api.LeasePolicy{Environment: "dev", DefaultDays: new(30)}, 403, nil)
		h.json("POST", "/api/v1/admin/roles", reviewer, api.RoleCreate{Key: "mine", Name: "Mine", Permissions: []string{"access.manage"}}, 403, nil)
		h.json("POST", "/api/v1/requests/"+d.built.Id+"/cancel", reviewer, nil, 403, nil)
		h.json("POST", "/api/v1/requests", reviewer, api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{}}, 403, nil, "Idempotency-Key", uuid.NewString())
		if a := d.actions(reviewer); a["power_off"].Enabled || a["reset"].Enabled {
			t.Fatalf("the reviewer's actions = %+v", a)
		}
		d.run(reviewer, "power_off", nil, 403)
	})
}
