package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
)

// TestDHCP: a blueprint whose first adapter is on DHCP reserves no address
// and makes no DNS records, needs no EfficientIP subnet, configures the
// guest for DHCP, and shows the address the VM reports; deleting it
// releases nothing (ADR-0039).
func TestDHCP(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		publish := func(slug string, def api.CatalogVersionDefinition) api.AdminCatalogItem {
			t.Helper()
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
				Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			return item
		}
		run := func(item api.AdminCatalogItem, role string) (api.Request, map[string]api.Step) {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": role, "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 202, &req, "Idempotency-Key", uuid.NewString())
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

		var problem struct{ Errors []api.FieldError }
		bad := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		bad.Blueprint["ipam"] = "static"
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-ipam", Name: "b", Summary: "b",
			Category: "compute", Type: api.VmBlueprint, Definition: &bad}, 422, &problem)
		if got := fieldsOf(problem.Errors); len(got) != 1 || !strings.HasSuffix(got[0], "/blueprint/ipam") {
			t.Fatalf("ipam static = %+v", problem.Errors)
		}

		// No EfficientIP subnet is linked to the network: DHCP needs none.
		if _, err := h.store.Pool.Exec(ctx, `delete from network_subnets where network = 'dvpg-app-dev-vl310'`); err != nil {
			t.Fatal(err)
		}
		linux := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example",
			"cloud_init": map[string]any{"user_data": "#cloud-config\nfqdn: {{ .vm.fqdn | json }}"}})
		linux.Blueprint["ipam"] = "dhcp"
		req, steps := run(publish("dhcp-linux", linux), "dhcp")
		if _, ok := steps["ipam.reserve"]; ok {
			t.Fatal("an address step is planned")
		}
		if _, ok := steps["dns.create"]; ok {
			t.Fatal("a DNS step is planned")
		}
		if s := steps["vsphere.customize"]; !strings.Contains(deref(s.Detail), ", DHCP, through guestinfo") {
			t.Fatalf("customize step = %q %q", s.Title, deref(s.Detail))
		}
		if s := steps["vsphere.power_on"]; s.Title != "Powered on, guest IP "+dhcpLease || !strings.Contains(deref(s.Detail), "from DHCP") {
			t.Fatalf("power on step = %q %q", s.Title, deref(s.Detail))
		}
		var moref string
		_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`, req.Id).Scan(&moref)
		info, err := guestInfoOf(ctx, c, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.StdEncoding.DecodeString(info["guestinfo.metadata"])
		var meta struct {
			Hostname string `json:"hostname"`
			Network  struct {
				Ethernets map[string]map[string]any `json:"ethernets"`
			} `json:"network"`
		}
		_ = json.Unmarshal(raw, &meta)
		if nic := meta.Network.Ethernets["nic0"]; nic["dhcp4"] != true || nic["addresses"] != nil || meta.Hostname != "web-dev-dhcp01.corp.example" {
			t.Fatalf("metadata = %s", raw)
		}
		user, _ := base64.StdEncoding.DecodeString(info["guestinfo.userdata"])
		if !strings.Contains(string(user), `fqdn: "web-dev-dhcp01.corp.example"`) {
			t.Fatalf("user data = %s", user)
		}

		var depID string
		if err := h.store.Pool.QueryRow(ctx, `select id from deployments where created_by_request_id = $1`, req.Id).Scan(&depID); err != nil {
			t.Fatal(err)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+depID, "tnguyen", nil, 200, &dep)
		var vm api.VmResource
		for _, r := range dep.Resources {
			switch typ, _ := r.Discriminator(); typ {
			case "ipam.address", "dns.record":
				t.Fatalf("the deployment has a %s", typ)
			case "vsphere.vm":
				vm, _ = r.AsVmResource()
			}
		}
		if vm.Vm.Dhcp == nil || !*vm.Vm.Dhcp || vm.Vm.Nics[0].IpAddresses == nil || (*vm.Vm.Nics[0].IpAddresses)[0] != dhcpLease ||
			deref(vm.Vm.Fqdn) != "web-dev-dhcp01.corp.example" {
			t.Fatalf("the VM = %+v", vm.Vm)
		}
		hosts, err := h.store.InventoryHosts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var address string
		for _, host := range hosts {
			if host.Name == "web-dev-dhcp01" && host.Address != nil {
				address = *host.Address
			}
		}
		if address != dhcpLease {
			t.Fatalf("AWX's address = %q", address)
		}

		// Without cloud-init, vCenter's customization uses DHCP.
		plain := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		plain.Blueprint["ipam"] = "dhcp"
		_, steps = run(publish("dhcp-plain", plain), "plain")
		if s := steps["vsphere.customize"]; s.Title != "Applied guest customization" || deref(s.Detail) != "Hostname web-dev-plain01, DHCP" {
			t.Fatalf("customize step = %q %q", s.Title, deref(s.Detail))
		}

		// Deleting it releases nothing in EfficientIP.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		var list struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+del.Id+"/steps", "tnguyen", nil, 200, &list)
		for _, s := range list.Items {
			if s.System != nil && *s.System == api.StepSystemEfficientip {
				t.Fatalf("delete step %q %q", s.Title, deref(s.Detail))
			}
		}
	})
}
