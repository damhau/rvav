package e2e

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/provision"
)

// TestN2Acceptance is the N2 milestone's acceptance (N2.3): a composite
// whose VM step is on its own segment and in a security group builds, and
// deletes, as a VM item does; its VM has two adapters, the second on the
// deployment's segment, addressed there.
func TestN2Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		fakeNSX := nsx.NewFakeServer()
		fakeNSX.RealizeAfter, fakeNSX.AnyVM, fakeNSX.SeeAfter = 1, true, 1
		fakeNSX.OnSegment = portGroups(t, ctx, c)
		client := useFakeNSX(h, fakeNSX)
		h.acts.NSX, h.acts.NSXTimeout = client, 20*time.Second
		segmentRule(h)
		// The VM item: its first adapter on the application network, a
		// back-end one on its deployment's segment, in a group of its own.
		rhelVariant(h, "rhel9-backend", "RHEL 9 with a back end", func(bp map[string]any) {
			bp["segments"] = []any{map[string]any{"key": "back", "title": "Back end"}}
			bp["nics"] = []any{map[string]any{"key": "back", "network": "segments.back"}}
			bp["groups"] = []any{"web"}
			bp["security_groups"] = []any{map[string]any{"key": "web", "title": "Web servers", "rules": []any{
				map[string]any{"from": []any{"any"}, "services": []any{"HTTPS"}}}}}
		})
		def := vmWithDBUser()
		def.Blueprint = map[string]any{"kind": "composite", "rollback_on_failure": true, "steps": []any{
			map[string]any{"key": "vm", "title": "The web server", "item": "rhel9-backend", "with": map[string]any{
				"role": "inputs.role", "size": "inputs.size", "cluster": "inputs.cluster", "datastore": "inputs.datastore",
				"network": "inputs.network", "post_config": []any{"linux-baseline"}}},
			map[string]any{"key": "monitoring", "awx": map[string]any{"job_template": "monitoring-agent", "title": "Monitoring"},
				"with": map[string]any{"target": "steps.vm.fqdn"}},
		}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-backend", Name: "Web server with a back end",
			Summary: "On NSX, monitored.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/web-backend/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-backend/entitlements", admin, api.CatalogEntitlements{AllProjects: true,
			ProjectIds: []string{}}, 200, nil)

		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "web", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "database": "orders", "role_name": "orders_ro"}},
			202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)

		var depID, vmName, uuidVM string
		if err := h.store.Pool.QueryRow(ctx, `select d.id, r.name, r.external_id from deployments d join resources r on r.deployment_id = d.id
			where d.created_by_request_id = $1 and r.type = 'vsphere.vm'`, req.Id).Scan(&depID, &vmName, &uuidVM); err != nil {
			t.Fatal(err)
		}
		segID, web, pol := provision.SegmentName(depID, "back"), provision.GroupName(depID, "web"), provision.PolicyName(depID)

		// Its own segment, on the project's Tier-1; the second adapter on it,
		// addressed there, the first on the application network.
		if seg, err := client.Segment(ctx, segID); err != nil || seg.ConnectivityPath != nsx.Tier1Path+"t1-gva-dev" {
			t.Fatalf("segment = %+v, %v", seg, err)
		}
		vm, err := h.vc.FindVM(ctx, vmName)
		if err != nil || len(vm.NICs) != 2 || vm.NICs[0].Network != "dvpg-app-dev-vl310" || vm.NICs[1].Network != segID {
			t.Fatalf("VM = %+v, %v", vm, err)
		}
		var onSegment, onApp int
		rows, _ := h.store.Pool.Query(ctx, `select name from resources where deployment_id = $1 and type = 'ipam.address'`, depID)
		for rows.Next() {
			var a string
			_ = rows.Scan(&a)
			switch {
			case strings.HasPrefix(a, "10.60.0."):
				onSegment++
			case strings.HasPrefix(a, "10.31.10."):
				onApp++
			}
		}
		rows.Close()
		if onSegment != 1 || onApp != 1 {
			t.Fatalf("addresses: %d on the segment, %d on the application network", onSegment, onApp)
		}

		// In its security group, with the rule into it.
		if _, err := client.Group(ctx, web); err != nil {
			t.Fatalf("group: %v", err)
		}
		if p, err := client.SecurityPolicy(ctx, pol); err != nil || len(p.Rules) != 1 {
			t.Fatalf("policy = %+v, %v", p, err)
		}
		if v, _ := fakeNSX.VM(uuidVM); !slices.Contains(v.Tags, provision.GroupTag(web)) {
			t.Fatalf("VM tags = %+v", v.Tags)
		}

		// Deleted as a VM item is: the segment, its port group, the group,
		// the rules.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		if _, err := client.Segment(ctx, segID); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("segment after delete: %v", err)
		}
		if ok, _ := h.vc.NetworkExists(ctx, segID); ok {
			t.Fatal("the segment's port group is still in vCenter")
		}
		if _, err := client.Group(ctx, web); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("group after delete: %v", err)
		}
		if _, err := client.SecurityPolicy(ctx, pol); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("policy after delete: %v", err)
		}
	}, simulator.VPX())
}
