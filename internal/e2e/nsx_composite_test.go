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

// segmentRule makes Web Platform's dev placement rule make NSX segments on
// t1-gva-dev, their subnets /26s from the development block.
func segmentRule(h *harness) {
	h.t.Helper()
	var rules api.PlacementRuleList
	h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", "mkeller", nil, 200, &rules)
	r := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })]
	h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, "mkeller", api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
		Priority: &r.Priority, Clusters: r.Clusters, Datastores: r.Datastores, Networks: r.Networks, Folder: r.Folder,
		ClusterTags: &r.ClusterTags, DatastoreTags: &r.DatastoreTags, Segments: &api.PlacementSegments{Tier1: "t1-gva-dev",
			IpBlock: "nsx-gva-dev", PrefixLength: 26, DnsZone: "corp.example", DnsServers: &[]string{"10.10.0.53"}}}, 200, nil)
}

// nsxItem is the seeded RHEL item on its own segment, in a group made for
// its deployment and in grp-web-servers.
func nsxItem(h *harness) (string, string) {
	h.t.Helper()
	return rhelVariant(h, "rhel9-nsx", "RHEL 9 on NSX", func(bp map[string]any) {
		bp["bindings"].(map[string]any)["network"] = "segments.app"
		bp["segments"] = []any{map[string]any{"key": "app", "title": "Application"}}
		groupBlueprint("any", "SSH")(bp)
	})
}

// TestCompositeNSX: a composite whose VM step's item is on its own NSX
// segment and in security groups (N2.1): previewed with them, planned
// under the step, made before its VMs; made again on a retry after they
// were deleted; deleted with the deployment.
func TestCompositeNSX(t *testing.T) {
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
		nsxItem(h)

		def := vmWithDBUser()
		def.Blueprint = map[string]any{"kind": "composite", "steps": []any{
			map[string]any{"key": "vm", "title": "The VM", "item": "rhel9-nsx", "with": map[string]any{
				"role": "inputs.role", "size": "inputs.size", "cluster": "inputs.cluster", "datastore": "inputs.datastore",
				"network": "inputs.network", "post_config": []any{"linux-baseline"}}},
			map[string]any{"key": "monitoring", "awx": map[string]any{"job_template": "monitoring-agent", "title": "Monitoring"},
				"with": map[string]any{"target": "steps.vm.fqdn"}},
		}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "vm-on-nsx", Name: "A VM on NSX, monitored",
			Summary: "Its own segment and groups.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/vm-on-nsx/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/vm-on-nsx/entitlements", admin, api.CatalogEntitlements{AllProjects: true,
			ProjectIds: []string{}}, 200, nil)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "web", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "database": "orders", "role_name": "orders_ro"}}

		// The preview shows the VM step: on a new segment, in both groups.
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if !pv.Valid || pv.Components == nil || len(*pv.Components) != 1 {
			t.Fatalf("preview = %+v", pv)
		}
		comp := (*pv.Components)[0]
		if comp.Key != "vm/vm" || comp.Title != "The VM" || comp.Nics[0].Network != "A new NSX segment (app) on t1-gva-dev" ||
			comp.NsxGroups == nil || !slices.Equal(*comp.NsxGroups, []string{"Web servers (new)", "grp-web-servers"}) {
			t.Fatalf("the VM step's preview = %+v %v", comp, comp.NsxGroups)
		}

		// Failing in its post-configuration: the segment and groups are made.
		h.awx.FailTask = "baseline : harden sshd"
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.awx.FailTask = ""
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusFailed)
		var depID string
		if err := h.store.Pool.QueryRow(ctx, `select id from deployments where created_by_request_id = $1`, req.Id).Scan(&depID); err != nil {
			t.Fatal(err)
		}
		segID, web, pol := provision.SegmentName(depID, "app"), provision.GroupName(depID, "web"), provision.PolicyName(depID)
		if _, err := client.Group(ctx, web); err != nil {
			t.Fatalf("group before the retry: %v", err)
		}
		// Gone meanwhile (a rollback that stopped halfway): made again.
		if err := client.DeleteSecurityPolicy(ctx, pol); err != nil {
			t.Fatal(err)
		}
		if err := client.DeleteGroup(ctx, web); err != nil {
			t.Fatal(err)
		}
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", "tnguyen", nil, 202, nil)
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)

		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &steps)
		status := map[string]api.StepStatus{}
		for _, s := range steps.Items {
			for _, ch := range s.Children {
				status[ch.Key] = ch.Status
			}
		}
		for _, key := range []string{"step:vm/nsx.segment:app", "step:vm/nsx.security", "step:vm/nsx.tags"} {
			if status[key] != api.StepStatusDone {
				t.Fatalf("%s = %q (%v)", key, status[key], status)
			}
		}
		seg, err := client.Segment(ctx, segID)
		if err != nil || seg.ConnectivityPath != nsx.Tier1Path+"t1-gva-dev" {
			t.Fatalf("segment = %+v, %v", seg, err)
		}
		if _, err := client.Group(ctx, web); err != nil {
			t.Fatalf("group after the retry: %v", err)
		}
		if p, err := client.SecurityPolicy(ctx, pol); err != nil || len(p.Rules) != 2 {
			t.Fatalf("policy after the retry = %+v, %v", p, err)
		}
		var vmName, uuidVM string
		_ = h.store.Pool.QueryRow(ctx, `select name, external_id from resources where deployment_id = $1 and type = 'vsphere.vm'`,
			depID).Scan(&vmName, &uuidVM)
		if vm, err := h.vc.FindVM(ctx, vmName); err != nil || vm.NICs[0].Network != segID {
			t.Fatalf("VM = %+v, %v", vm, err)
		}
		var addr string
		_ = h.store.Pool.QueryRow(ctx, `select name from resources where deployment_id = $1 and type = 'ipam.address'`, depID).Scan(&addr)
		if !strings.HasPrefix(addr, "10.60.0.") {
			t.Fatalf("address = %q", addr)
		}
		if vm, _ := fakeNSX.VM(uuidVM); !slices.Contains(vm.Tags, provision.GroupTag(web)) ||
			!slices.Contains(vm.Tags, nsx.Tag{Scope: "role", Tag: "web"}) {
			t.Fatalf("VM tags = %+v", vm.Tags)
		}

		// Deleted with the deployment.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		if _, err := client.Segment(ctx, segID); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("segment after delete: %v", err)
		}
		if _, err := client.Group(ctx, web); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("group after delete: %v", err)
		}
		if _, err := client.SecurityPolicy(ctx, pol); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("policy after delete: %v", err)
		}
	}, simulator.VPX())
}
