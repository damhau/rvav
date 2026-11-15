package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestProjectCreateNamesItsAdmins: project.create lets its holder name the
// new project's project admins, themselves included, and its catalogue
// entry, which the role editor shows, says so (ADR-0034).
func TestProjectCreateNamesItsAdmins(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, maker, other = "mkeller", "cdubois", "afavre"
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "project-maker", Name: "Project maker",
			Permissions: []string{"project.create"}}, 201, nil)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID(maker),
			Role: "project-maker"}, 201, nil)

		var catalogue api.PermissionCatalogue
		h.json("GET", "/api/v1/permissions", maker, nil, 200, &catalogue)
		i := slices.IndexFunc(catalogue.Items, func(p api.PermissionInfo) bool { return p.Id == "project.create" })
		if i < 0 || !strings.Contains(catalogue.Items[i].Summary, "project admins, themselves included") {
			t.Fatalf("project.create's summary doesn't say its holder names the project admins: %+v", catalogue.Items)
		}

		var made api.AdminProject
		h.json("POST", "/api/v1/admin/projects", maker, api.AdminProjectCreate{Code: "own", Name: "Own",
			Environments: []string{"dev", "prd"}, ProjectAdmins: &[]string{seed.UserID(maker), seed.UserID(other)}}, 201, &made)
		for _, user := range []string{maker, other} {
			var me api.Me
			h.json("GET", "/api/v1/me", user, nil, 200, &me)
			j := slices.IndexFunc(me.Projects, func(p api.MeProject) bool { return p.Project.Id == made.Id })
			if j < 0 {
				t.Fatalf("%s doesn't see the project %s made: %+v", user, maker, me.Projects)
			}
			p := me.Projects[j]
			if len(p.Roles) != 1 || p.Roles[0].Key != "project-admin" ||
				!slices.Contains(p.Permissions, "project.members.manage") || !slices.Contains(p.Permissions, "approval.project") {
				t.Fatalf("%s in the project %s made: roles %+v, permissions %v", user, maker, p.Roles, p.Permissions)
			}
		}
	})
}
