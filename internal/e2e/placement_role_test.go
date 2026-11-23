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

// TestPlacementRole: a custom role with a project's placement permissions
// only opens the project, its placement rules, what they select and its
// OUs, and changes a rule (#212). The project's quotas and every project's
// list stay its settings readers'.
func TestPlacementRole(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, placer = "mkeller", "cdubois"
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "placer", Name: "Placer",
			Permissions: []string{"project.see", "placement.manage"}}, 201, nil)
		h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID(placer), admin,
			api.ProjectMemberWrite{Roles: &[]string{"placer"}}, 200, nil)

		var project api.AdminProject
		h.json("GET", "/api/v1/admin/projects/prj_web", placer, nil, 200, &project)
		if project.Name != "Web Platform" || project.MyPermissions == nil || !slices.Contains(*project.MyPermissions, "placement.manage") {
			t.Fatalf("project = %+v", project)
		}
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", placer, nil, 200, &rules)
		i := slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })
		if i < 0 {
			t.Fatalf("rules = %+v", rules.Items)
		}
		var preview api.PlacementPreview
		h.json("GET", "/api/v1/admin/placement-preview?project_id=prj_web&environment=dev", placer, nil, 200, &preview)
		if len(preview.Clusters) == 0 {
			t.Fatalf("preview = %+v", preview)
		}
		h.json("GET", "/api/v1/admin/ad-placements?project_id=prj_web", placer, nil, 200, nil)
		r := rules.Items[i]
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, placer, api.PlacementRuleWrite{ProjectId: "prj_web",
			Environment: "dev", Priority: &r.Priority, Clusters: r.Clusters, Datastores: r.Datastores, Networks: r.Networks,
			Folder: r.Folder, ClusterTags: &r.ClusterTags, DatastoreTags: &r.DatastoreTags}, 200, nil)

		h.json("GET", "/api/v1/admin/projects/prj_web/quotas", placer, nil, 403, nil)
		h.json("GET", "/api/v1/admin/projects", placer, nil, 403, nil)
		h.json("GET", "/api/v1/admin/placement-rules", placer, nil, 403, nil)
	})
}
