package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/seed"
)

// TestAccessExplanation: what a user may do, each permission with the roles
// granting it and where they come from, as the checks count them; the
// roles a project's members may be given by whoever manages them
// (ADR-0034).
func TestAccessExplanation(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		explain := func(asker, user, project string, want int) api.AccessExplanation {
			t.Helper()
			path := "/api/v1/admin/access/explain?user_id=" + seed.UserID(user)
			if project != "" {
				path += "&project_id=" + project
			}
			var out api.AccessExplanation
			h.json("GET", path, asker, nil, want, &out)
			return out
		}
		grants := func(e api.AccessExplanation, perm string) []api.AccessGrant {
			t.Helper()
			i := slices.IndexFunc(e.Permissions, func(p api.AccessPermission) bool { return p.Id == perm })
			if i < 0 {
				t.Fatalf("%s isn't explained: %+v", perm, e.Permissions)
			}
			if e.Permissions[i].Held != (len(e.Permissions[i].GrantedBy) > 0) {
				t.Fatalf("%s held = %v with %+v", perm, e.Permissions[i].Held, e.Permissions[i].GrantedBy)
			}
			return e.Permissions[i].GrantedBy
		}

		// Oneself, in the projects one sees; someone else takes access.read.
		explain("tnguyen", "mkeller", "", 403)
		explain("tnguyen", "tnguyen", "prj_data", 404)
		thomas := explain("tnguyen", "tnguyen", "web", 200)
		if thomas.Project == nil || thomas.Project.Id != "prj_web" || !slices.Equal(thomas.Groups, []string{"rvav-web"}) {
			t.Fatalf("explained %+v", thomas)
		}
		submit := grants(thomas, "request.submit")
		if len(submit) != 2 || submit[0].Through != api.AccessGrantThroughUser || submit[0].Given != api.AccessGrantGivenProject ||
			submit[1].Through != api.AccessGrantThroughGroup || deref(submit[1].Group) != "rvav-web" || submit[1].Role.Name != "Member" {
			t.Fatalf("request.submit granted by %+v", submit)
		}
		if g := grants(thomas, "project.members.manage"); len(g) != 0 {
			t.Fatalf("a member manages members: %+v", g)
		}
		if len(thomas.Permissions) != len(auth.Catalogue) {
			t.Fatalf("%d permissions explained, the catalogue has %d", len(thomas.Permissions), len(auth.Catalogue))
		}
		explain(admin, "nobody", "", 404)
		explain(admin, "tnguyen", "nowhere", 404)

		// An administrator by rvav and by a group: approval.project counts in a project only.
		marc := explain(admin, "mkeller", "", 200)
		if g := grants(marc, "access.manage"); len(g) != 2 || g[0].Through != api.AccessGrantThroughRvav || deref(g[1].Group) != "rvav-admins" {
			t.Fatalf("access.manage granted by %+v", g)
		}
		if g := grants(marc, "approval.project"); len(g) != 0 {
			t.Fatalf("approval.project for all of rvav: %+v", g)
		}
		lea := explain(admin, "lmartin", "prj_web", 200)
		if g := grants(lea, "approval.project"); len(g) != 2 || g[0].Role.Key != "project-admin" {
			t.Fatalf("a project admin's approval.project: %+v", g)
		}
		if g := grants(lea, "catalog.manage"); len(g) != 1 || g[0].Given != api.AccessGrantGivenRvav || deref(g[0].Group) != "rvav-catalog-admins" {
			t.Fatalf("catalog.manage granted by %+v", g)
		}

		// A custom role's day-2 permissions, after the catalogue.
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "vm-operator", Name: "VM operator",
			Permissions: []string{"project.see", "day2.power:power_on", "day2.power:power_off"}}, 201, nil)
		h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID("cdubois"), admin,
			api.ProjectMemberWrite{Roles: &[]string{"vm-operator"}}, 200, nil)
		claire := explain(admin, "cdubois", "prj_web", 200)
		last := claire.Permissions[len(claire.Permissions)-2:]
		if last[0].Id != "day2.power:power_off" || last[1].Id != "day2.power:power_on" || last[1].GrantedBy[0].Role.Key != "vm-operator" {
			t.Fatalf("day-2 permissions explained: %+v", last)
		}
		if g := grants(explain(admin, "cdubois", "prj_data", 200), "project.see"); len(g) != 0 {
			t.Fatalf("project.see in Data Engineering: %+v", g)
		}

		// What is explained is what the checks count.
		for _, user := range []string{"lmartin", "mkeller", "tnguyen", "sbrunner", "afavre", "cdubois"} {
			p, err := auth.ForUser(ctx, h.store, seed.UserID(user))
			if err != nil {
				t.Fatal(err)
			}
			for _, project := range []string{"", "prj_web", "prj_data"} {
				for _, perm := range explain(admin, user, project, 200).Permissions {
					held := p.Everywhere(auth.Permission(perm.Id))
					if project != "" {
						held = p.In(auth.Permission(perm.Id), project)
					}
					if held != perm.Held {
						t.Errorf("%s in %q: %s explained %v, checked %v", user, project, perm.Id, perm.Held, held)
					}
				}
			}
		}

		// The roles a project admin may give: no more than they hold there.
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "requester", Name: "Requester",
			Permissions: []string{"project.see", "request.submit"}}, 201, nil)
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "policy-reader", Name: "Policy reader",
			Permissions: []string{"policies.read"}}, 201, nil)
		keys := func(user string) []string {
			var list api.AssignableRoleList
			h.json("GET", "/api/v1/projects/prj_web/assignable-roles", user, nil, 200, &list)
			out := make([]string, len(list.Items))
			for i, r := range list.Items {
				out[i] = r.Key
			}
			return out
		}
		if got := keys("lmartin"); !slices.Equal(got, []string{"member", "project-admin", "viewer", "requester"}) {
			t.Fatalf("a project admin may give %v", got)
		}
		if got := keys(admin); !slices.Equal(got, []string{"member", "project-admin", "viewer", "requester", "vm-operator"}) {
			t.Fatalf("an administrator may give %v", got)
		}
		h.json("GET", "/api/v1/projects/prj_web/assignable-roles", "tnguyen", nil, 403, nil)
		h.json("GET", "/api/v1/projects/prj_data/assignable-roles", "lmartin", nil, 404, nil)
		h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID("afavre"), "lmartin",
			api.ProjectMemberWrite{Roles: &[]string{"viewer", "requester"}}, 200, nil)
	})
}
