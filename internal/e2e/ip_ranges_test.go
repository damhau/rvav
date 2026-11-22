package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

const (
	rangeVCenter = "vc01.corp.example"
	// rangeNetwork is a dev network the test takes from EfficientIP.
	rangeNetwork = "dvpg-db-dev-vl320"
)

func labRange(name, start, end string, excluded ...string) api.IpRangeWrite {
	return api.IpRangeWrite{Name: name, Vcenter: rangeVCenter, Network: rangeNetwork, Cidr: "10.32.20.0/24",
		StartAddress: start, EndAddress: end, Gateway: "10.32.20.1", DnsServers: &[]string{"10.32.20.53"},
		DnsSearchDomains: &[]string{"corp.example"}, DnsDomain: new("lab.corp.example"), Excluded: &excluded}
}

// refusedFields are the fields a 422 names, with their codes.
func refusedFields(t *testing.T, b []byte) string {
	t.Helper()
	var p struct {
		Errors []api.FieldError `json:"errors"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	var out []string
	for _, e := range p.Errors {
		out = append(out, e.Field+" "+deref(e.Code))
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}

// TestIPRanges covers I1.2's ranges (ADR-0063): an administrator adds,
// changes and removes them, refused when they disagree with the network's
// other ranges, overlap, or would strand an address a VM holds; the network
// mappings show the network's source; concurrent allocations never get one
// address twice and never an excluded one.
func TestIPRanges(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"

		var a api.IpRange
		h.json("POST", "/api/v1/admin/ip-ranges", admin, labRange("Lab", "10.32.20.10", "10.32.20.20", "10.32.20.12", "10.32.20.15-10.32.20.16"), 201, &a)
		if a.Size != 8 || a.Used != 0 || a.Free != 8 || a.DnsDomain != "lab.corp.example" || !slices.Equal(a.Excluded, []string{"10.32.20.12", "10.32.20.15-10.32.20.16"}) {
			t.Fatalf("range = %+v", a)
		}
		h.json("POST", "/api/v1/admin/ip-ranges", "lmartin", labRange("Mine", "10.32.20.100", "10.32.20.110"), 403, nil)

		refused := func(method, path string, w api.IpRangeWrite, want string) {
			t.Helper()
			code, b, _ := h.do(method, path, admin, w)
			if code != 422 || refusedFields(t, b) != want {
				t.Errorf("%s %s = %d %s, want 422 %s", method, path, code, b, want)
			}
		}
		bad := labRange("", "10.32.21.10", "10.32.20.5", "10.32.20.300")
		bad.Cidr, bad.Network = "10.32.20.0/24", "dvpg-nowhere"
		refused("POST", "/api/v1/admin/ip-ranges", bad,
			"/end_address before_start, /excluded/0 invalid, /name required, /network unknown, /start_address outside")
		other := labRange("Other", "10.32.20.18", "10.32.20.30")
		other.Gateway = "10.32.20.254"
		refused("POST", "/api/v1/admin/ip-ranges", other, "/gateway differs, /start_address overlaps")
		// What is saved is what was written (a client such as Terraform
		// compares them), and a DNS server is no address to give.
		odd := labRange("Odd", "10.32.20.40", "10.32.20.60", "10.32.20.41-10.32.20.41", " 10.32.20.42 - 10.32.20.43")
		odd.DnsDomain, odd.DnsServers = new("lab.corp.example."), &[]string{"10.32.20.53"}
		refused("POST", "/api/v1/admin/ip-ranges", odd, "/dns_domain not_canonical, /excluded/0 not_canonical, /excluded/1 not_canonical")
		odd = labRange("Odd", "10.32.20.40", "10.32.20.60")
		refused("POST", "/api/v1/admin/ip-ranges", odd, "/dns_servers/0 in_range")
		code, b, _ := h.do("POST", "/api/v1/admin/ip-ranges", admin, map[string]any{"name": "x", "colour": "red"})
		if code != 400 {
			t.Errorf("an unknown field = %d %s", code, b)
		}

		var second api.IpRange
		h.json("POST", "/api/v1/admin/ip-ranges", admin, labRange("Lab 2", "10.32.20.30", "10.32.20.31"), 201, &second)
		var list api.IpRangeList
		h.json("GET", "/api/v1/admin/ip-ranges", "lmartin", nil, 403, nil)
		h.json("GET", "/api/v1/admin/ip-ranges", admin, nil, 200, &list)
		if len(list.Items) != 2 || list.Items[0].Id != a.Id {
			t.Fatalf("ranges = %+v", list.Items)
		}
		var mappings api.NetworkMappingList
		h.json("GET", "/api/v1/admin/network-mappings", admin, nil, 200, &mappings)
		var sources []string
		for _, m := range mappings.Items {
			if m.Network == rangeNetwork {
				sources = append(sources, string(m.Source)+" "+deref(m.Vcenter)+" "+m.Subnet+" "+m.DnsZone)
			}
		}
		if !slices.Equal(sources, []string{"manual  10.32.20.0/24 corp.example", "rvav " + rangeVCenter + " 10.32.20.0/24 lab.corp.example"}) {
			t.Errorf("%s's mappings = %q", rangeNetwork, sources)
		}
		h.json("DELETE", "/api/v1/admin/ip-ranges/"+second.Id, admin, nil, 204, nil)
		h.json("GET", "/api/v1/admin/ip-ranges/"+second.Id, admin, nil, 404, nil)

		// Eight adapters at once get the range's eight addresses, a ninth none.
		var wg sync.WaitGroup
		got := make([]string, 9)
		errs := make([]error, 9)
		for i := range 9 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				al, _, err := h.store.AllocateAddress(ctx, store.AllocationRequest{VCenter: rangeVCenter, Network: rangeNetwork,
					RequestID: "req_lab", DeploymentID: "dep_lab", Hostname: "lab" + string(rune('a'+i))})
				got[i], errs[i] = al.Address, err
			}()
		}
		wg.Wait()
		var given []string
		full := 0
		for i, err := range errs {
			switch {
			case errors.Is(err, store.ErrRangeFull):
				full++
			case err != nil:
				t.Fatal(err)
			default:
				given = append(given, got[i])
			}
		}
		slices.Sort(given)
		if full != 1 || !slices.Equal(given, []string{"10.32.20.10", "10.32.20.11", "10.32.20.13", "10.32.20.14", "10.32.20.17",
			"10.32.20.18", "10.32.20.19", "10.32.20.20"}) {
			t.Fatalf("given %v, %d refused", given, full)
		}
		// Which adapter is refused is the race's: ask again as one given an
		// address, and as the refused one.
		holder := slices.IndexFunc(errs, func(err error) bool { return err == nil })
		refusedOne := slices.IndexFunc(errs, func(err error) bool { return err != nil })
		ask := func(i int) (store.IPAllocation, error) {
			al, _, err := h.store.AllocateAddress(ctx, store.AllocationRequest{VCenter: rangeVCenter, Network: rangeNetwork,
				RequestID: "req_lab", DeploymentID: "dep_lab", Hostname: "lab" + string(rune('a'+i))})
			return al, err
		}
		if again, err := ask(holder); err != nil || again.Address != got[holder] {
			t.Errorf("asked again = %v %v, want %s", again.Address, err, got[holder])
		}
		if _, err := ask(refusedOne); !errors.Is(err, store.ErrRangeFull) {
			t.Errorf("the refused one asking again: %v, want %v", err, store.ErrRangeFull)
		}

		var allocs api.IpAllocationList
		h.json("GET", "/api/v1/admin/ip-ranges/"+a.Id+"/allocations", admin, nil, 200, &allocs)
		if len(allocs.Items) != 8 || allocs.Items[0].Address != "10.32.20.10" || allocs.Items[7].Address != "10.32.20.20" {
			t.Errorf("allocations = %+v", allocs.Items)
		}
		h.json("GET", "/api/v1/admin/ip-ranges/"+a.Id, admin, nil, 200, &a)
		if a.Used != 8 || a.Free != 0 {
			t.Errorf("usage = %d used, %d free", a.Used, a.Free)
		}

		// Given addresses stay: the range can't lose them or move.
		changed := labRange("Lab", "10.32.20.11", "10.32.20.20", "10.32.20.12", "10.32.20.15-10.32.20.16", "10.32.20.19")
		changed.Gateway = "10.32.20.2"
		refused("PUT", "/api/v1/admin/ip-ranges/"+a.Id, changed, "/excluded in_use, /network in_use, /start_address in_use")
		renamed := labRange("Lab apps", "10.32.20.10", "10.32.20.40", "10.32.20.12", "10.32.20.15-10.32.20.16")
		h.json("PUT", "/api/v1/admin/ip-ranges/"+a.Id, admin, renamed, 200, &a)
		if a.Name != "Lab apps" || a.Size != 28 || a.Free != 20 {
			t.Errorf("changed range = %+v", a)
		}
		code, b, _ = h.do("DELETE", "/api/v1/admin/ip-ranges/"+a.Id, admin, nil)
		if code != 409 || !strings.Contains(string(b), "VMs hold addresses from Lab apps") {
			t.Errorf("deleting a range in use = %d %s", code, b)
		}
		left, err := h.store.IPAllocations(ctx, a.Id)
		if err != nil {
			t.Fatal(err)
		}
		for _, al := range left {
			if err := h.store.ReleaseAddress(ctx, h.store.Pool, al.ID); err != nil {
				t.Fatal(err)
			}
		}
		h.json("DELETE", "/api/v1/admin/ip-ranges/"+a.Id, admin, nil, 204, nil)
		var audit struct{ Items []api.AuditEvent }
		h.json("GET", "/api/v1/audit?action=ip_range.", admin, nil, 200, &audit)
		var actions []string
		for _, e := range audit.Items {
			actions = append(actions, e.Action)
		}
		slices.Sort(actions)
		if !slices.Equal(actions, []string{"ip_range.create", "ip_range.create", "ip_range.delete", "ip_range.delete", "ip_range.update"}) {
			t.Errorf("audited %v", actions)
		}
	})
}
