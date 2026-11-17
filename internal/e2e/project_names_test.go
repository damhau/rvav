package e2e

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/seed"
)

// TestProjectNames: the Administration pages that pick or name projects
// list them by name for whoever holds the page's own permission for all of
// rvav, without reading projects' settings (#126): references only,
// archived projects on request.
func TestProjectNames(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, picker = "mkeller", "npicker"
		if _, err := h.store.Pool.Exec(ctx, `insert into users (id, subject, name, email) values ($1, $2, $3, $4)`,
			seed.UserID(picker), "seed:"+picker, "Nina Picker", picker+"@corp.example"); err != nil {
			t.Fatal(err)
		}
		var old api.AdminProject
		h.json("POST", "/api/v1/admin/projects", admin, api.AdminProjectCreate{Code: "old", Name: "Old Platform",
			Environments: []api.EnvironmentKey{"dev"}}, 201, &old)
		h.json("PATCH", "/api/v1/admin/projects/"+old.Id, admin, api.AdminProjectUpdate{Archived: new(true)}, 200, nil)

		names := func(user, query string, want int) []map[string]any {
			t.Helper()
			var raw struct{ Items []map[string]any }
			h.json("GET", "/api/v1/admin/project-names"+query, user, nil, want, &raw)
			return raw.Items
		}
		// hold gives the picker a new role holding these permissions for all of rvav.
		roles := 0
		hold := func(perms ...string) string {
			t.Helper()
			roles++
			key := fmt.Sprintf("picker-%d", roles)
			h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: key, Name: key, Permissions: perms}, 201, nil)
			var a api.RoleAssignment
			h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID(picker), Role: key}, 201, &a)
			return deref(a.Id)
		}

		for _, perm := range []string{"project.settings.read", "access.read", "audit.read", "infrastructure.read",
			"infrastructure.manage", "policies.read", "policies.manage", "extensibility.read", "extensibility.manage",
			"import.manage"} {
			perms := []string{perm}
			// A role granting something in a project sees it too, which names no project.
			if info, _ := auth.Info(auth.Permission(perm)); info.Project {
				perms = append(perms, "project.see")
			}
			given := hold(perms...)
			if perm != "project.settings.read" {
				h.json("GET", "/api/v1/admin/projects", picker, nil, 403, nil)
			}
			var got []string
			for _, p := range names(picker, "", 200) {
				if keys := slices.Sorted(maps.Keys(p)); !slices.Equal(keys, []string{"archived", "code", "id", "name"}) {
					t.Fatalf("with %s, a project named with %v", perm, keys)
				}
				if p["archived"] != false {
					t.Fatalf("with %s, an archived project without asking: %v", perm, p)
				}
				got = append(got, p["name"].(string))
			}
			if want := []string{"Data Engineering", "Finance Apps", "Infra Core", "Web Platform"}; !slices.Equal(got, want) {
				t.Fatalf("with %s, projects named %v, want %v", perm, got, want)
			}
			all := names(picker, "?archived=true", 200)
			i := slices.IndexFunc(all, func(p map[string]any) bool { return p["id"] == old.Id })
			if len(all) != 5 || i < 0 || all[i]["archived"] != true || all[i]["code"] != "old" || all[i]["name"] != "Old Platform" {
				t.Fatalf("with %s and archived projects: %v", perm, all)
			}
			h.json("DELETE", "/api/v1/admin/role-assignments/"+given, admin, nil, 204, nil)
		}

		// None of them, or one held in a single project only: refused.
		given := hold("project.see", "catalog.manage")
		names(picker, "", 403)
		h.json("DELETE", "/api/v1/admin/role-assignments/"+given, admin, nil, 204, nil)
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "web-auditor", Name: "Web auditor",
			Permissions: []string{"project.see", "project.settings.read", "audit.read"}}, 201, nil)
		h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID(picker), admin,
			api.ProjectMemberWrite{Roles: &[]string{"web-auditor"}}, 200, nil)
		names(picker, "", 403)
		names(picker, "?archived=true", 403)
		names("lmartin", "", 403)

		// An access reader explains someone's access in a project named there.
		hold("access.read")
		var refs api.ProjectNameList
		h.json("GET", "/api/v1/admin/project-names", picker, nil, 200, &refs)
		i := slices.IndexFunc(refs.Items, func(p api.ProjectName) bool { return p.Name == "Data Engineering" })
		var e api.AccessExplanation
		h.json("GET", "/api/v1/admin/access/explain?user_id="+seed.UserID("sbrunner")+"&project_id="+refs.Items[i].Id,
			picker, nil, 200, &e)
		if e.Project == nil || e.Project.Id != "prj_data" {
			t.Fatalf("explained in %+v", e.Project)
		}
	})
}
