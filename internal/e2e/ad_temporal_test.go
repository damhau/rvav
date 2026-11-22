package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/temporal"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/ad/adtest"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/provision"
)

// refusedClone is vCenter refusing to clone one VM.
type refusedClone struct {
	vsphere.Driver
	name string
}

func (r refusedClone) CloneVM(ctx context.Context, spec vsphere.VMSpec) (*vsphere.VM, error) {
	if spec.Name == r.name {
		return nil, temporal.NewNonRetryableApplicationError("vCenter refused the clone", "Permanent", nil)
	}
	return r.Driver.CloneVM(ctx, spec)
}

// TestActiveDirectoryThroughTemporal: on the Temporal dev server, a VM
// gets its computer object and an address of rvav's IP ranges; a clone
// vCenter refuses gives both back; a multi-VM item's VMs get theirs. It
// records these runs' histories for TestReplayHistories (ADR-0063,
// ADR-0064).
func TestActiveDirectoryThroughTemporal(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, user, queue = "mkeller", "tnguyen", "rvav-ad-e2e"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		h.acts.VSphere = refusedClone{Driver: h.acts.VSphere, name: "web-dev-bad01"}
		web := "OU=Web," + adtest.BaseDN
		dir := adtest.Start(t, []string{web})
		h.svc.Config.AD = directoryConfig(dir)
		h.acts.Directory = func(context.Context) provision.Directory {
			return renaming{core.Directory(h.svc.Config.AD), dir}
		}
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{ProjectId: new("prj_web"), Ou: "OU=Web"}, 201, nil)
		h.json("DELETE", "/api/v1/admin/network-mappings/"+rangeNetwork, admin, nil, 204, nil)
		var rg api.IpRange
		h.json("POST", "/api/v1/admin/ip-ranges", admin, labRange("Lab", "10.32.20.10", "10.32.20.19"), 201, &rg)
		held := func() []string {
			t.Helper()
			var list api.IpAllocationList
			h.json("GET", "/api/v1/admin/ip-ranges/"+rg.Id+"/allocations", admin, nil, 200, &list)
			var out []string
			for _, a := range list.Items {
				out = append(out, a.Hostname+" "+a.Address)
			}
			return out
		}
		publish := func(slug string, def api.CatalogVersionDefinition) api.AdminCatalogItem {
			t.Helper()
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
				Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true,
				ProjectIds: []string{}}, 200, nil)
			return item
		}
		run := func(item api.AdminCatalogItem, inputs map[string]any, want api.RequestStatus, history string) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", user, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: inputs}, 202, &req, "Idempotency-Key", uuid.NewString())
			req = h.waitStatus(req.Id, user, want)
			settle(t, ctx, tc, req.Id)
			recordHistory(t, ctx, tc, req.Id, history)
			return req
		}
		computers := func() []string {
			var out []string
			for _, dn := range dir.Computers() {
				cn, _, _ := strings.Cut(strings.TrimPrefix(dn, "CN="), ",")
				out = append(out, cn)
			}
			return out
		}

		ranged := publish("ad-ranged", guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"}))
		vm := func(role string) map[string]any {
			return map[string]any{"role": role, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": rangeNetwork}
		}
		run(ranged, vm("lab"), api.RequestStatusSucceeded, "provision-ad-range")
		if got := held(); !slices.Equal(computers(), []string{"WEB-DEV-LAB01"}) || len(got) != 1 || !strings.HasPrefix(got[0], "web-dev-lab01 10.32.20.") {
			t.Fatalf("computers %v, addresses %v", computers(), got)
		}

		failed := run(ranged, vm("bad"), api.RequestStatusFailed, "provision-ad-released")
		if s := timelineOf(h, user, failed.Id)["ad.computer"]; !slices.Equal(computers(), []string{"WEB-DEV-LAB01"}) || dir.Adds() != 2 ||
			len(held()) != 1 || s.Title != "Removed computer object WEB-DEV-BAD01$" {
			t.Fatalf("after the refused clone: computers %v, %d adds, addresses %v, step %q", computers(), dir.Adds(), held(), s.Title)
		}

		shop := publish("ad-shop", shopApp())
		run(shop, map[string]any{"app": "s", "web_nodes": 2, "size": "S", "web_cluster": "GVA-DEV-CL01",
			"web_datastore": "vsan-gva-dev-cl01", "db_cluster": "GVA-DEV-CL01", "db_datastore": "nfs-gva-bronze-01",
			"network": "dvpg-app-dev-vl310", "db_post": []string{}}, api.RequestStatusSucceeded, "provision-components-ad")
		if got := computers(); !slices.Equal(got, []string{"WEB-DEV-LAB01", "WEB-DEV-SDB01", "WEB-DEV-SWEB01", "WEB-DEV-SWEB02"}) {
			t.Fatalf("computers = %v", got)
		}
	})
}
