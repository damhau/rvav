package e2e

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestUnregisterLowersCount: unregistering a VM lowers its component's
// count input, as a join raises it, so the next update doesn't build it
// again (X1.6).
func TestUnregisterLowersCount(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"
		def := webFarm(false)
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-farm", Name: "Web farm",
			Summary: "Web servers.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-farm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		v1 := item.DraftVersion.Id
		inputs := map[string]any{"app": "farm", "web_nodes": 2, "size": "S", "cluster": "GVA-DEV-CL01", "network": "dvpg-app-dev-vl310"}
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: v1, ProjectId: web,
			Environment: "dev", Inputs: inputs}, 202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, nil)
		h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		depID := (*built.DeploymentIds)[0]
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
		web01 := ""
		for _, r := range dep.Resources {
			if v, _ := r.ValueByDiscriminator(); v != nil {
				if vm, ok := v.(api.VmResource); ok && vm.Name == "web-dev-farmweb01" {
					web01 = vm.Id
				}
			}
		}

		var r api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", admin, api.Day2Draft{Action: "vm_unregister", ResourceId: &web01,
			Inputs: map[string]any{}}, 202, &r, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, admin, api.RequestStatusSucceeded)
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+r.Id+"/steps", admin, nil, 200, &steps)
		if i := slices.IndexFunc(steps.Items, func(s api.Step) bool { return s.Key == "rvav.unregister" }); i < 0 ||
			!strings.HasSuffix(deref(steps.Items[i].Detail), "; web_nodes is now 1") {
			t.Fatalf("steps = %+v", steps.Items)
		}
		h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
		if n, _ := dep.Inputs["web_nodes"].(float64); n != 1 {
			t.Fatalf("inputs after unregistering = %v", dep.Inputs)
		}

		next := maps.Clone(dep.Inputs)
		next["size"] = "M"
		var pv api.UpdatePreview
		h.json("POST", "/api/v1/deployments/"+depID+"/update/preview", owner, api.UpdateDraft{VersionId: v1, Inputs: next}, 200, &pv)
		if slices.ContainsFunc(pv.Changes, func(ch api.UpdateChange) bool { return ch.Kind == api.VmAdd }) {
			t.Fatalf("the next update builds a VM again: %+v", pv.Changes)
		}
	})
}
