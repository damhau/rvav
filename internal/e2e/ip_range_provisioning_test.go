package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
)

// TestIPRangeProvisioning: on a network EfficientIP doesn't manage, VMs get
// their addresses from rvav's IP ranges (ADR-0063). Requests running at
// once get distinct addresses and never an excluded one; one needing more
// than the ranges have left is refused when it is evaluated; deleting, a
// rollback, a failure before the VM exists and a day-2 move of the adapter
// give them back. No DNS record is made for them, and the guest gets the
// ranges' DNS servers and search domains. A network EfficientIP manages
// works as before.
func TestIPRangeProvisioning(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, user = "mkeller", "tnguyen"
		h.json("DELETE", "/api/v1/admin/network-mappings/"+rangeNetwork, admin, nil, 204, nil)
		lab := labRange("Lab", "10.32.20.10", "10.32.20.19", "10.32.20.11", "10.32.20.15")
		lab.DnsSearchDomains = &[]string{"corp.example", "svc.corp.example"}
		var rg api.IpRange
		h.json("POST", "/api/v1/admin/ip-ranges", admin, lab, 201, &rg)
		if rg.Size != 8 {
			t.Fatalf("range = %+v", rg)
		}

		publish := func(slug string, def api.CatalogVersionDefinition) api.AdminCatalogItem {
			t.Helper()
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
				Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			return item
		}
		linux := func() api.CatalogVersionDefinition {
			return guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example",
				"cloud_init": map[string]any{"user_data": "#cloud-config\nfqdn: {{ .vm.fqdn | json }}"}})
		}
		draft := func(item api.AdminCatalogItem, role, network string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"role": role, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": network}}
		}
		submit := func(d api.RequestDraft) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", user, d, 202, &req, "Idempotency-Key", uuid.NewString())
			return req
		}
		// held are the range's addresses, by the VM's adapter holding each.
		held := func() map[string]string {
			t.Helper()
			var list api.IpAllocationList
			h.json("GET", "/api/v1/admin/ip-ranges/"+rg.Id+"/allocations", admin, nil, 200, &list)
			out := map[string]string{}
			for _, a := range list.Items {
				out[a.Hostname+deref(a.Adapter)] = a.Address
			}
			return out
		}
		addressesOf := func(requestID string) []string {
			t.Helper()
			rows, _ := h.store.Pool.Query(ctx, `select name from resources where request_id = $1 and type = 'ipam.address' order by name`, requestID)
			var out []string
			for rows.Next() {
				var ip string
				_ = rows.Scan(&ip)
				out = append(out, ip)
			}
			return out
		}
		system := func(s api.Step) string {
			if s.System == nil {
				return ""
			}
			return string(*s.System)
		}
		records := func(requestID string) int {
			var n int
			_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources where request_id = $1 and type = 'dns.record'`, requestID).Scan(&n)
			return n
		}

		// Four requests at once: four addresses, none excluded, none twice.
		ranged := publish("ranged", linux())
		var reqs []api.Request
		for range 4 {
			reqs = append(reqs, submit(draft(ranged, "lab", rangeNetwork)))
		}
		var wg sync.WaitGroup
		for _, r := range reqs {
			wg.Go(func() {
				if err := h.execute(r.Id, cloudInitGuest(h, c)); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		given := map[string]bool{}
		for _, r := range reqs {
			r = h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
			ips := addressesOf(r.Id)
			if len(ips) != 1 || given[ips[0]] || ips[0] == "10.32.20.11" || ips[0] == "10.32.20.15" || !strings.HasPrefix(ips[0], "10.32.20.1") {
				t.Fatalf("request %s got %v; given before %v", r.Number, ips, given)
			}
			given[ips[0]] = true
			steps := timelineOf(h, user, r.Id)
			if s := steps["ipam.reserve"]; system(s) != "rvav" || deref(s.Detail) != "IP ranges of "+rangeNetwork {
				t.Fatalf("reserve step = %q %q %v", s.Title, deref(s.Detail), s.System)
			}
			if _, ok := steps["dns.create"]; ok || records(r.Id) != 0 {
				t.Fatalf("request %s makes DNS records (steps %v)", r.Number, keysOf(steps))
			}
		}
		if got := held(); len(got) != 4 {
			t.Fatalf("held = %v", got)
		}
		// EfficientIP knows nothing of them.
		var ips []string
		for ip := range given {
			ips = append(ips, ip)
		}
		if addresses, recs, err := h.svc.IPAM.Find(ctx, ips, nil); err != nil || len(addresses)+len(recs) != 0 {
			t.Fatalf("EfficientIP has %v %v (%v)", addresses, recs, err)
		}

		// The guest gets the ranges' gateway, DNS servers and search domains,
		// and its name in their DNS domain.
		var moref string
		_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`, reqs[0].Id).Scan(&moref)
		info, err := guestInfoOf(ctx, c, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.StdEncoding.DecodeString(info["guestinfo.metadata"])
		var meta struct {
			Network struct {
				Ethernets map[string]struct {
					Addresses   []string
					Routes      []struct{ Via string }
					Nameservers struct{ Addresses, Search []string }
				}
			}
		}
		_ = json.Unmarshal(raw, &meta)
		nic0 := meta.Network.Ethernets["nic0"]
		if len(nic0.Routes) != 1 || nic0.Routes[0].Via != "10.32.20.1" || !slices.Equal(nic0.Nameservers.Addresses, []string{"10.32.20.53"}) ||
			!slices.Equal(nic0.Nameservers.Search, []string{"corp.example", "svc.corp.example"}) {
			t.Fatalf("metadata = %s", raw)
		}
		userData, _ := base64.StdEncoding.DecodeString(info["guestinfo.userdata"])
		if !strings.Contains(string(userData), `.lab.corp.example"`) {
			t.Fatalf("user data = %s", userData)
		}

		// Deleting a deployment gives its address back.
		deleteDeployment := func(r api.Request) api.Request {
			t.Helper()
			var del api.Request
			h.json("POST", "/api/v1/deployments/"+(*r.DeploymentIds)[0]+"/actions", user, api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
				202, &del, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(del.Id, nil)
			return h.waitStatus(del.Id, user, api.RequestStatusSucceeded)
		}
		del := deleteDeployment(reqs[3])
		if got := held(); len(got) != 3 {
			t.Fatalf("held after the delete = %v", got)
		}
		if s := timelineOf(h, user, del.Id)["ipam.release"]; system(s) != "rvav" || s.Status != api.StepStatusDone {
			t.Fatalf("release step = %+v", s)
		}

		// A request failing before its VM exists gives its address back; a
		// retry asks for one again.
		cloneFails := func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(h.acts.CloneVM, mock.Anything, mock.Anything).Return("", temporal.NewNonRetryableApplicationError(
				"vCenter refused the clone", "test", nil))
		}
		failed := submit(draft(ranged, "lab", rangeNetwork))
		h.runWorkflow(failed.Id, cloneFails)
		h.waitStatus(failed.Id, user, api.RequestStatusFailed)
		if got := held(); len(got) != 3 || len(addressesOf(failed.Id)) != 0 {
			t.Fatalf("held after the failure = %v, the request's %v", got, addressesOf(failed.Id))
		}
		h.json("POST", "/api/v1/requests/"+failed.Id+"/retry", user, nil, 202, nil)
		h.runWorkflow(failed.Id, cloudInitGuest(h, c))
		h.waitStatus(failed.Id, user, api.RequestStatusSucceeded)
		if got := held(); len(got) != 4 || len(addressesOf(failed.Id)) != 1 {
			t.Fatalf("held after the retry = %v", got)
		}

		// Rolled back, a request gives its address back.
		rb := linux()
		rb.Blueprint["rollback_on_failure"] = true
		rolled := submit(draft(publish("ranged-rollback", rb), "roll", rangeNetwork))
		h.runWorkflow(rolled.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(h.acts.PowerOn, mock.Anything, mock.Anything).Return("", temporal.NewNonRetryableApplicationError(
				"the VM doesn't start", "test", nil))
		})
		h.waitStatus(rolled.Id, user, api.RequestStatusFailed)
		if s := timelineOf(h, user, rolled.Id)["rollback"]; s.Status != api.StepStatusDone {
			t.Fatalf("rollback = %+v", s)
		}
		if got := held(); len(got) != 4 {
			t.Fatalf("held after the rollback = %v", got)
		}

		// An adapter added on the network takes an address from its ranges,
		// without a record; the first, on a network EfficientIP manages,
		// gets its address and records there as before. Moved to another
		// network by a day-2 action, the adapter gives its address back.
		two := linux()
		two.Blueprint["nics"] = []any{map[string]any{"key": "backup", "network": `"` + rangeNetwork + `"`}}
		mixed := submit(draft(publish("ranged-nic", two), "mix", "dvpg-app-dev-vl310"))
		h.runWorkflow(mixed.Id, cloudInitGuest(h, c))
		h.waitStatus(mixed.Id, user, api.RequestStatusSucceeded)
		backup := held()["web-dev-mix01backup"]
		if backup == "" || records(mixed.Id) != 2 {
			t.Fatalf("held %v, %d records", held(), records(mixed.Id))
		}
		var dnsNames []string
		rows, _ := h.store.Pool.Query(ctx, `select name from resources where request_id = $1 and type = 'dns.record' order by name`, mixed.Id)
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			dnsNames = append(dnsNames, n)
		}
		if !slices.Contains(dnsNames, "A web-dev-mix01.corp.example") || slices.ContainsFunc(dnsNames, func(n string) bool { return strings.Contains(n, "backup") }) {
			t.Fatalf("records = %v", dnsNames)
		}
		if s := timelineOf(h, user, mixed.Id)["ipam.reserve"]; system(s) != "efficientip" ||
			!strings.Contains(deref(s.Detail), "IP ranges of "+rangeNetwork) {
			t.Fatalf("reserve step = %q %q", s.Title, deref(s.Detail))
		}
		d := &vmDeployment{h: h, id: (*mixed.DeploymentIds)[0], built: mixed, viewer: user}
		d.resource = *d.actions(user)["power_off"].ResourceId
		nics := d.vm().Nics
		if len(nics) != 2 || nics[1].Network != rangeNetwork {
			t.Fatalf("nics = %+v", nics)
		}
		d.succeed(user, "nic_move", map[string]any{"nic": deref(nics[1].Label), "network": "dvpg-app-dev-vl310"})
		if got := held(); got["web-dev-mix01backup"] != "" || len(got) != 4 {
			t.Fatalf("held after the move = %v", got)
		}
		var efficientip int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources where deployment_id = $1 and type = 'ipam.address'
			and properties->>'efficientip_id' <> ''`, d.id).Scan(&efficientip)
		if efficientip != 1 {
			t.Fatalf("%d EfficientIP addresses left", efficientip)
		}

		// A request needing more addresses than the ranges have left is
		// refused when it is evaluated, on its network.
		many := draft(ranged, "many", rangeNetwork)
		many.Count = new(5)
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", user, many, 200, &pv)
		i := slices.IndexFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "no_free_address" })
		if i < 0 || pv.Errors[i].Field != "/inputs/network" ||
			pv.Errors[i].Message != "The IP ranges of "+rangeNetwork+" have 4 free addresses; this request needs 5. Pick another network, or ask an administrator to add a range." {
			t.Fatalf("preview errors = %+v", pv.Errors)
		}
		code, b, _ := h.do("POST", "/api/v1/requests", user, many, "Idempotency-Key", uuid.NewString())
		if code != 422 || !strings.Contains(string(b), "no_free_address") {
			t.Fatalf("submitted = %d %s", code, b)
		}
		many.Count = new(4)
		h.json("POST", "/api/v1/requests/preview", user, many, 200, &pv)
		taken := slices.Collect(maps.Values(held()))
		if slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "no_free_address" }) ||
			pv.Addresses == nil || len(*pv.Addresses) == 0 || slices.Contains(taken, (*pv.Addresses)[0]) {
			t.Fatalf("four = %+v, hint %v", pv.Errors, pv.Addresses)
		}
	})
}
