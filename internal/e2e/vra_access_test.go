package e2e

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/vra"
)

// TestVRAGuestAccess: a vRA template's remote access, DHCP and IPv6
// address import as an item that saves, publishes and is requested with
// them: the database's IPv6 address needs its network's IPv6 subnet, and
// the web server on DHCP needs no address (T1.6).
func TestVRAGuestAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	const content = `formatVersion: 1
inputs:
  dbPassword:
    type: string
    encrypted: true
resources:
  web:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
      remoteAccess:
        authentication: generatedPublicPrivateKey
        username: ops
      networks:
        - network: ${resource.app.id}
          assignment: dynamic
  db:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
      remoteAccess:
        authentication: usernamePassword
        username: dba
        password: ${input.dbPassword}
      networks:
        - network: ${resource.app.id}
          assignIPv6Address: true
  app:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
`
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const network, subnet6, gateway6 = "dvpg-app-dev-vl310", "2001:db8:310::/64", "2001:db8:310::1"
		e := vra.SampleEstate(time.Now())
		e.Blueprints = []vra.Blueprint{{ID: "bp-access", Name: "Shop", Versions: []vra.BlueprintVersion{
			{Version: "1", Status: "RELEASED", Content: content}}}}
		res := importVRAItems(h, &e)["bp-access"]
		version := publishVRAItem(h, res)
		in := api.RequestDraft{CatalogItemId: res.ItemId, VersionId: version, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"dbPassword": "Correct-Horse-9", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": network}}

		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", in, 200, &pv)
		if pv.Valid || len(pv.Errors) != 1 || deref(pv.Errors[0].Code) != "no_subnet6" {
			t.Fatalf("no IPv6 subnet: valid %v, errors %+v", pv.Valid, pv.Errors)
		}
		h.json("PUT", "/api/v1/admin/network-mappings/"+network, "mkeller", api.NetworkMappingWrite{Subnet: "10.31.10.0/24",
			Gateway: "10.31.10.1", DnsZone: "corp.example", DnsServers: &[]string{"10.10.0.53"}, Subnet6: ptr(subnet6),
			Gateway6: ptr(gateway6)}, 200, nil)
		if _, err := h.ipam.AddSubnet6(efficientip.FakeSpace, subnet6, "app-dev-v6", map[string]string{efficientip.ParamNetwork: network,
			efficientip.ParamGateway: gateway6}); err != nil {
			t.Fatal(err)
		}

		h.json("POST", "/api/v1/requests/preview", "tnguyen", in, 200, &pv)
		if !pv.Valid || pv.Components == nil {
			t.Fatalf("preview: valid %v, errors %+v", pv.Valid, pv.Errors)
		}
		comps := *pv.Components
		db := slices.IndexFunc(comps, func(c api.PreviewComponent) bool { return c.Key == "db" })
		web := slices.IndexFunc(comps, func(c api.PreviewComponent) bool { return c.Key == "web" })
		if db < 0 || web < 0 {
			t.Fatalf("components = %+v", comps)
		}
		if nic := comps[db].Nics[0]; nic.Ipam != api.PreviewComponentNicsIpam("efficientip") || deref(nic.Subnet) != "10.31.10.0/24 and "+subnet6 {
			t.Fatalf("db's adapter = %+v", nic)
		}
		if nic := comps[web].Nics[0]; nic.Ipam != api.PreviewComponentNicsIpam("dhcp") || nic.Subnet != nil {
			t.Fatalf("web's adapter = %+v", nic)
		}
	})
}
