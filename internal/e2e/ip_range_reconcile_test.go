package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/store"
)

// TestIPRangeReconcile: the reconcile report compares rvav's IP ranges
// with what VMs hold (ADR-0063), with EfficientIP or without it: an
// address given to a VM no deployment has any more, which an administrator
// can then give back, and an inventory VM whose address is in a range that
// didn't give it to it.
func TestIPRangeReconcile(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, user = "mkeller", "tnguyen"
		h.json("DELETE", "/api/v1/admin/network-mappings/"+rangeNetwork, admin, nil, 204, nil)
		var rg api.IpRange
		h.json("POST", "/api/v1/admin/ip-ranges", admin, labRange("Lab", "10.32.20.10", "10.32.20.19", "10.32.20.11"), 201, &rg)

		build := func(role string) (req api.Request, ip, name string) {
			t.Helper()
			var form api.RequestForm
			h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", user, nil, 200, &form)
			var item api.CatalogItem
			h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", user, nil, 200, &item)
			h.json("POST", "/api/v1/requests", user, api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: "prj_web",
				Environment: "dev", Inputs: map[string]any{"role": role, "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": rangeNetwork, "post_config": []string{"linux-baseline"}}},
				202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			req = h.waitStatus(req.Id, user, api.RequestStatusSucceeded)
			_ = h.store.Pool.QueryRow(ctx, `select name, properties->>'hostname' from resources where request_id = $1 and type = 'ipam.address'`,
				req.Id).Scan(&ip, &name)
			return req, ip, name
		}
		report := func() api.IpamReconcile {
			t.Helper()
			var out api.IpamReconcile
			h.json("GET", "/api/v1/admin/ipam/reconcile", admin, nil, 200, &out)
			return out
		}
		inventory := func(name, ip string) {
			t.Helper()
			if err := h.store.UpsertInventoryVM(ctx, rangeVCenter, store.InvVM{Moref: "vm-" + name, Name: name, PowerState: "poweredOn",
				IPAddress: ip, Cluster: "GVA-DEV-CL01"}); err != nil {
				t.Fatal(err)
			}
		}

		_, keptIP, kept := build("kept")
		gone, goneIP, goneVM := build("gone")
		if !strings.HasPrefix(keptIP, "10.32.20.") || keptIP == goneIP {
			t.Fatalf("addresses = %s %s", keptIP, goneIP)
		}
		inventory(kept, keptIP)
		inventory(goneVM, goneIP)
		if r := report(); !r.Efficientip || len(r.OrphanAllocations)+len(r.Unallocated) != 0 {
			t.Fatalf("with two VMs = %+v", r)
		}

		// Unregistered, a VM keeps its address in the range: nothing in rvav
		// has it any more.
		var unreg api.Request
		h.json("POST", "/api/v1/deployments/"+(*gone.DeploymentIds)[0]+"/actions", admin, api.Day2Draft{Action: "unregister",
			Inputs: map[string]any{}}, 202, &unreg, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(unreg.Id, nil)
		h.waitStatus(unreg.Id, admin, api.RequestStatusSucceeded)

		// VMs rvav didn't build: one on an address the range never gave, one
		// on an address it gave another VM, one on an excluded address and
		// one outside the range.
		inventory("lab-outside01", "10.32.20.18")
		inventory("lab-clash01", keptIP)
		inventory("lab-excluded01", "10.32.20.11")
		inventory("lab-far01", "10.32.20.200")

		r := report()
		if len(r.OrphanAllocations) != 1 {
			t.Fatalf("orphans = %+v", r.OrphanAllocations)
		}
		o := r.OrphanAllocations[0]
		if o.Address != goneIP || o.Hostname != goneVM || o.RangeId != rg.Id || o.RangeName != "Lab" || o.Network != rangeNetwork ||
			o.Vcenter != rangeVCenter || deref(o.DeploymentId) != (*gone.DeploymentIds)[0] || o.Adapter != nil {
			t.Fatalf("orphan = %+v", o)
		}
		got := map[string]api.IpamUnallocated{}
		for _, u := range r.Unallocated {
			got[u.Vm] = u
		}
		if len(got) != 2 || got["lab-outside01"].Address != "10.32.20.18" || got["lab-outside01"].HeldBy != nil ||
			got["lab-outside01"].RangeName != "Lab" || deref(got["lab-clash01"].HeldBy) != kept {
			t.Fatalf("unallocated = %+v", r.Unallocated)
		}

		// An administrator gives the orphan back once vCenter no longer
		// reports its VM; the address of a VM a deployment has stays.
		base := "/api/v1/admin/ip-ranges/" + rg.Id + "/allocations/"
		h.json("DELETE", base+goneIP, user, nil, 403, nil)
		code, b, _ := h.do("DELETE", base+keptIP, admin, nil)
		if code != 409 || !strings.Contains(string(b), kept+" still holds "+keptIP) {
			t.Fatalf("releasing a held address = %d %s", code, b)
		}
		h.json("DELETE", base+"10.32.20.18", admin, nil, 404, nil)
		code, b, _ = h.do("DELETE", base+goneIP, admin, nil)
		if code != 409 || !strings.Contains(string(b), "still reports VM "+goneVM+" on "+goneIP) {
			t.Fatalf("releasing an orphan vCenter still reports = %d %s", code, b)
		}
		if _, err := h.store.DeleteInventoryVM(ctx, rangeVCenter, "vm-"+goneVM); err != nil {
			t.Fatal(err)
		}
		h.json("DELETE", base+goneIP, admin, nil, 204, nil)
		if r := report(); len(r.OrphanAllocations) != 0 || len(r.Unallocated) != 2 {
			t.Fatalf("after the release = %+v", r)
		}
		var audit struct{ Items []api.AuditEvent }
		h.json("GET", "/api/v1/audit?action=ip_range.release", admin, nil, 200, &audit)
		if len(audit.Items) != 1 {
			t.Fatalf("audit = %+v", audit.Items)
		}

		// Without EfficientIP, the ranges are compared still.
		h.svc.IPAM = efficientip.NotSet{}
		if r := report(); r.Efficientip || r.ManagedAddresses != 0 || len(r.OrphanAddresses) != 0 || len(r.Unallocated) != 2 {
			t.Fatalf("without EfficientIP = %+v", r)
		}
	})
}
