package e2e

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// TestIPRangeSkipsHeldAddresses: a range never gives an address a VM
// holds some other way (I1.6, ADR-0063). Saved over an address EfficientIP
// gave one of rvav's VMs, or one vCenter reports for a VM, it is refused;
// held once saved, the address is skipped, counted as taken, and a
// request is refused when only such addresses are left. So is a DNS
// server written into a range before this check.
func TestIPRangeSkipsHeldAddresses(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, user = "mkeller", "tnguyen"
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", user, nil, 200, &form)
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", user, nil, 200, &item)
		draft := func(role string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"role": role, "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
					"network": rangeNetwork, "post_config": []string{"linux-baseline"}}}
		}
		var old api.Request
		h.json("POST", "/api/v1/requests", user, draft("old"), 202, &old, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(old.Id, nil)
		old = h.waitStatus(old.Id, user, api.RequestStatusSucceeded)
		var given, holder, source string
		if err := h.store.Pool.QueryRow(ctx, `select name, properties->>'hostname', coalesce(properties->>'source', '') from resources
			where request_id = $1 and type = 'ipam.address'`, old.Id).Scan(&given, &holder, &source); err != nil {
			t.Fatal(err)
		}
		ip := netip.MustParseAddr(given)
		if source != "" || !strings.HasPrefix(given, "10.32.20.") {
			t.Fatalf("EfficientIP gave %s (source %q)", given, source)
		}
		at := func(n int) string {
			a := ip
			for range n {
				a = a.Next()
			}
			return a.String()
		}
		refused := func(method, path string, w api.IpRangeWrite, words ...string) {
			t.Helper()
			code, b, _ := h.do(method, path, admin, w)
			if code != 422 || refusedFields(t, b) != "/excluded held" {
				t.Fatalf("%s %s = %d %s, want 422 /excluded held", method, path, code, b)
			}
			for _, w := range words {
				if !strings.Contains(string(b), w) {
					t.Fatalf("%s %s = %s, without %q", method, path, b, w)
				}
			}
		}

		// The network moves to rvav's ranges: not over EfficientIP's address,
		// nor over one vCenter reports.
		refused("POST", "/api/v1/admin/ip-ranges", labRange("Lab", given, at(5)),
			given+" ("+holder+", a deployment's address)", "Exclude it")
		inventory := func(name, address string) {
			t.Helper()
			if err := h.store.UpsertInventoryVM(ctx, rangeVCenter, store.InvVM{Moref: "vm-" + name, Name: name, PowerState: "poweredOn",
				IPAddress: address, Cluster: "GVA-DEV-CL01"}); err != nil {
				t.Fatal(err)
			}
		}
		inventory("lab-stray01", at(2))
		refused("POST", "/api/v1/admin/ip-ranges", labRange("Lab", given, at(5)), given+" ("+holder, at(2)+" (lab-stray01, as vCenter reports)",
			"Exclude them")
		var rg api.IpRange
		h.json("POST", "/api/v1/admin/ip-ranges", admin, labRange("Lab", at(1), at(5), at(2)), 201, &rg)
		if rg.Size != 4 || rg.Taken != 0 || rg.Free != 4 {
			t.Fatalf("range = %+v", rg)
		}

		// Held after the range was saved: skipped, and the range still saves.
		inventory("lab-stray01", at(3))
		if _, err := h.store.UpsertResource(ctx, h.store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: (*old.DeploymentIds)[0],
			RequestID: &old.Id, Type: "ipam.address", Name: at(4), ExternalID: new("eip-" + at(4)),
			Properties: []byte(`{"address": "` + at(4) + `", "subnet": "10.32.20.0/24", "hostname": "` + holder + `", "efficientip_id": 99}`)}); err != nil {
			t.Fatal(err)
		}
		h.json("GET", "/api/v1/admin/ip-ranges/"+rg.Id, admin, nil, 200, &rg)
		if rg.Taken != 2 || rg.Free != 2 {
			t.Fatalf("range with two addresses held = %+v", rg)
		}
		renamed := labRange("Lab apps", at(1), at(5), at(2))
		h.json("PUT", "/api/v1/admin/ip-ranges/"+rg.Id, admin, renamed, 200, &rg)
		allocate := func(host, address string) (string, error) {
			al, _, err := h.store.AllocateAddress(ctx, store.AllocationRequest{VCenter: rangeVCenter, Network: rangeNetwork,
				RequestID: "req_lab", DeploymentID: "dep_lab", Hostname: host, Address: address})
			return al.Address, err
		}
		if _, err := allocate("lab-x0", at(4)); !errors.Is(err, store.ErrAddressHeld) {
			t.Fatalf("an address EfficientIP gave, chosen: %v", err)
		}
		var got []string
		for _, host := range []string{"lab-x1", "lab-x2"} {
			a, err := allocate(host, "")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, a)
		}
		if !slices.Equal(got, []string{at(1), at(5)}) {
			t.Fatalf("given %v, want %s and %s", got, at(1), at(5))
		}
		if _, err := allocate("lab-x3", ""); !errors.Is(err, store.ErrRangeFull) {
			t.Fatalf("with only held addresses left: %v", err)
		}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", user, draft("new"), 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "no_free_address" }) {
			t.Fatalf("a request when only held addresses are left = %+v", pv.Errors)
		}

		// A DNS server a range kept from before is never given either.
		if _, err := h.store.Pool.Exec(ctx, `update ip_ranges set dns_servers = $2 where id = $1`, rg.Id, []string{at(3)}); err != nil {
			t.Fatal(err)
		}
		inventory("lab-stray01", "10.32.20.250")
		h.json("GET", "/api/v1/admin/ip-ranges/"+rg.Id, admin, nil, 200, &rg)
		if rg.Taken != 2 || rg.Free != 0 {
			t.Fatalf("range with its DNS server inside = %+v", rg)
		}
		if _, err := allocate("lab-x3", ""); !errors.Is(err, store.ErrRangeFull) {
			t.Fatalf("with the DNS server left: %v", err)
		}
	})
}

// TestIPRangeSavesOneAtATime: two overlapping ranges of one vCenter saved
// at the same moment, on two networks: one is refused (I1.6).
func TestIPRangeSavesOneAtATime(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		a := labRange("A", "10.32.20.10", "10.32.20.20")
		b := labRange("B", "10.32.20.15", "10.32.20.25")
		b.Network = "dvpg-app-dev-vl310"
		for try := range 20 {
			var wg sync.WaitGroup
			codes := make([]int, 2)
			for i, w := range []api.IpRangeWrite{a, b} {
				wg.Go(func() { codes[i], _, _ = h.do("POST", "/api/v1/admin/ip-ranges", "mkeller", w) })
			}
			wg.Wait()
			if slices.Sort(codes); !slices.Equal(codes, []int{201, 422}) {
				t.Fatalf("try %d: saved at once = %v, want one 201 and one 422", try, codes)
			}
			if _, err := h.store.Pool.Exec(ctx, `delete from ip_ranges`); err != nil {
				t.Fatal(err)
			}
		}
	})
}
