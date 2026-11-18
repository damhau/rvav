package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
)

// TestIPv6: a blueprint with ipv6 gives its VMs' first adapter an IPv6
// address beside the IPv4 one, from the IPv6 subnet linked to the network,
// with an AAAA record; the guest gets both, and deleting the deployment
// releases both (ADR-0039).
func TestIPv6(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, network, subnet6 = "mkeller", "dvpg-app-dev-vl310", "2001:db8:310::/64"
		publish := func(slug string, def api.CatalogVersionDefinition) api.AdminCatalogItem {
			t.Helper()
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
				Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			return item
		}
		draft := func(item api.AdminCatalogItem, role string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"role": role, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": network}}
		}
		run := func(item api.AdminCatalogItem, role string) (api.Request, map[string]api.Step) {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", draft(item, role), 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, cloudInitGuest(h, c))
			req = h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
			var list struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &list)
			steps := map[string]api.Step{}
			for _, s := range list.Items {
				steps[s.Key] = s
			}
			return req, steps
		}

		// IPv6 comes from EfficientIP, as the IPv4 address: not beside DHCP.
		var problem struct{ Errors []api.FieldError }
		bad := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		bad.Blueprint["ipam"], bad.Blueprint["ipv6"] = "dhcp", true
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-ipv6", Name: "b", Summary: "b",
			Category: "compute", Type: api.VmBlueprint, Definition: &bad}, 422, &problem)
		if got := fieldsOf(problem.Errors); len(got) != 1 || !strings.HasSuffix(got[0], "/blueprint/ipv6") {
			t.Fatalf("ipv6 with dhcp = %+v", problem.Errors)
		}

		linux := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example",
			"cloud_init": map[string]any{"user_data": "#cloud-config\nfinal_message: {{ .vm.ip6 | json }}"}})
		linux.Blueprint["ipv6"] = true
		item := publish("ipv6-linux", linux)

		// The network has no IPv6 subnet yet.
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft(item, "six"), 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "no_subnet6" }) {
			t.Fatalf("no IPv6 subnet: %+v", pv.Errors)
		}

		// An administrator links one by hand.
		write := api.NetworkMappingWrite{Subnet: "10.31.10.0/24", Gateway: "10.31.10.1", DnsZone: "corp.example",
			DnsServers: &[]string{"10.10.0.53", "10.10.1.53"}, Subnet6: ptr("2001:db8:310::5/64"), Gateway6: ptr("2001:db8:311::1")}
		h.json("PUT", "/api/v1/admin/network-mappings/"+network, admin, write, 422, &problem)
		if got := fieldsOf(problem.Errors); strings.Join(got, " ") != "/subnet6 /gateway6" {
			t.Fatalf("a bad IPv6 subnet = %+v", problem.Errors)
		}
		write.Subnet6, write.Gateway6 = ptr(subnet6), ptr("2001:db8:310::1")
		var mapping api.NetworkMapping
		h.json("PUT", "/api/v1/admin/network-mappings/"+network, admin, write, 200, &mapping)
		if deref(mapping.Subnet6) != subnet6 || deref(mapping.Gateway6) != "2001:db8:310::1" {
			t.Fatalf("mapping = %+v", mapping)
		}
		if _, err := h.ipam.AddSubnet6(efficientip.FakeSpace, subnet6, "app-dev-v6", map[string]string{efficientip.ParamNetwork: network,
			efficientip.ParamGateway: "2001:db8:310::1"}); err != nil {
			t.Fatal(err)
		}

		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft(item, "six"), 200, &pv)
		if !pv.Valid {
			t.Fatalf("preview = %+v", pv.Errors)
		}
		req, steps := run(item, "six")
		reserved := steps["ipam.reserve"]
		ips := strings.Split(strings.TrimPrefix(reserved.Title, "Reserved "), ", ")
		if len(ips) != 2 || !strings.HasPrefix(ips[1], "2001:db8:310::") || deref(reserved.Detail) != "Subnets 10.31.10.0/24, "+subnet6 {
			t.Fatalf("reserve step = %q %q", reserved.Title, deref(reserved.Detail))
		}
		ip6 := ips[1]
		if s := steps["dns.create"]; s.Title != "Created A, AAAA and PTR records" {
			t.Fatalf("DNS step = %q %q", s.Title, deref(s.Detail))
		}
		if s := steps["vsphere.customize"]; !strings.Contains(deref(s.Detail), " and "+ip6+"/64, through guestinfo") {
			t.Fatalf("customize step = %q %q", s.Title, deref(s.Detail))
		}
		var moref string
		_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`, req.Id).Scan(&moref)
		info, err := guestInfoOf(ctx, c, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.StdEncoding.DecodeString(info["guestinfo.metadata"])
		var meta struct {
			Network struct {
				Ethernets map[string]struct {
					Addresses []string
					Routes    []map[string]string
				} `json:"ethernets"`
			} `json:"network"`
		}
		_ = json.Unmarshal(raw, &meta)
		nic := meta.Network.Ethernets["nic0"]
		if len(nic.Addresses) != 2 || nic.Addresses[1] != ip6+"/64" || len(nic.Routes) != 2 || nic.Routes[1]["via"] != "2001:db8:310::1" {
			t.Fatalf("metadata = %s", raw)
		}
		if user, _ := base64.StdEncoding.DecodeString(info["guestinfo.userdata"]); !strings.Contains(string(user), `final_message: "`+ip6+`"`) {
			t.Fatalf("user data = %s", user)
		}

		var depID string
		if err := h.store.Pool.QueryRow(ctx, `select id from deployments where created_by_request_id = $1`, req.Id).Scan(&depID); err != nil {
			t.Fatal(err)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+depID, "tnguyen", nil, 200, &dep)
		var addresses []string
		var aaaa api.DnsRecordResource
		var vmID string
		for _, r := range dep.Resources {
			switch typ, _ := r.Discriminator(); typ {
			case "vsphere.vm":
				vm, _ := r.AsVmResource()
				vmID = vm.Id
			case "ipam.address":
				a, _ := r.AsIpAddressResource()
				addresses = append(addresses, a.Address+" "+a.Subnet)
			case "dns.record":
				if d, _ := r.AsDnsRecordResource(); d.RecordType == api.AAAA {
					aaaa = d
				}
			}
		}
		if len(addresses) != 2 || !slices.Contains(addresses, ip6+" "+subnet6) || aaaa.Value != ip6 || aaaa.Fqdn != "web-dev-six01.corp.example" {
			t.Fatalf("addresses = %v, AAAA = %+v", addresses, aaaa)
		}
		if dep.PrimaryAddress == nil || strings.Contains(dep.PrimaryAddress.Ip, ":") {
			t.Fatalf("the deployment's address = %+v", dep.PrimaryAddress)
		}
		hosts, err := h.store.InventoryHosts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, host := range hosts {
			if host.Name == "web-dev-six01" && (host.Address == nil || strings.Contains(*host.Address, ":")) {
				t.Fatalf("AWX's address = %v", host.Address)
			}
		}

		// Renaming the VM renames its IPv6 address and AAAA record too.
		var ren api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: "rename", ResourceId: &vmID,
			Inputs: map[string]any{"name": "web-dev-sixr01"}}, 202, &ren, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(ren.Id, nil)
		h.waitStatus(ren.Id, "tnguyen", api.RequestStatusSucceeded)
		h.json("GET", "/api/v1/deployments/"+depID, "tnguyen", nil, 200, &dep)
		aaaa = api.DnsRecordResource{}
		for _, r := range dep.Resources {
			if typ, _ := r.Discriminator(); typ == "dns.record" {
				if d, _ := r.AsDnsRecordResource(); d.RecordType == api.AAAA {
					aaaa = d
				}
			}
		}
		if aaaa.Fqdn != "web-dev-sixr01.corp.example" || aaaa.Value != ip6 {
			t.Fatalf("AAAA after the rename = %+v", aaaa)
		}
		if got, _ := h.acts.IPAM.Reserve(ctx, efficientip.Subnet{CIDR: subnet6}, "web-dev-sixr01.corp.example", depID); got.IP != ip6 {
			t.Fatalf("the IPv6 address is not renamed in EfficientIP: %+v", got)
		}
		if ok, _ := h.acts.IPAM.NameExists(ctx, "web-dev-six01.corp.example"); ok {
			t.Fatal("the old name is still in DNS")
		}

		// Without cloud-init, vCenter's customization sets both.
		plain := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		plain.Blueprint["ipv6"] = true
		_, steps = run(publish("ipv6-plain", plain), "sixplain")
		other := strings.Split(steps["ipam.reserve"].Title, ", ")[1]
		if s := steps["vsphere.customize"]; s.Title != "Applied guest customization" || !strings.HasSuffix(deref(s.Detail), " and "+other+"/64") {
			t.Fatalf("customize step = %q %q", s.Title, deref(s.Detail))
		}

		// Deleting the deployment releases the IPv6 address and its record.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		managed, records, err := h.acts.IPAM.Managed(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range managed {
			if a.IP == ip6 {
				t.Fatalf("%s is still reserved", ip6)
			}
		}
		for _, r := range records {
			if r.Type == "AAAA" && r.Value == ip6 {
				t.Fatalf("the AAAA record is still there: %+v", r)
			}
		}
		if !slices.ContainsFunc(managed, func(a efficientip.Address) bool { return a.IP == other }) {
			t.Fatal("the other deployment's IPv6 address is not managed")
		}
	})
}
