package e2e

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/store"
)

// TestEfficientIPMappings covers S7's network mappings and pickers: the
// sync reads subnet class parameters, keeps mappings set by hand, and drops
// the ones EfficientIP no longer has; ipam.subnets and dns.zones list what
// EfficientIP has; VM names already in DNS are skipped.
func TestEfficientIPMappings(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		sync := &inventory.SubnetSync{Store: h.store, IPAM: h.acts.IPAM, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		mapping := func(network string) store.NetworkSubnet {
			t.Helper()
			n, err := h.store.NetworkSubnet(ctx, network)
			if err != nil {
				t.Fatalf("%s: %v", network, err)
			}
			return n
		}

		// The seeded mappings are set by hand: they stay, and get the subnet's ID.
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if n := mapping("dvpg-app-dev-vl310"); n.Source != "manual" || n.EfficientIPSubnetID == "" || n.SubnetName != "dvpg-app-dev-vl310" {
			t.Fatalf("manual mapping after a sync = %+v", n)
		}

		// A subnet with the class parameters adds a mapping; incomplete or
		// competing ones don't.
		params := map[string]string{efficientip.ParamNetwork: "dvpg-new-dev-vl330, dvpg-new-tst-vl330", efficientip.ParamGateway: "10.31.30.1",
			efficientip.ParamZone: "corp.example", efficientip.ParamDNSServers: "10.0.0.53,10.0.1.53"}
		id, _ := h.ipam.AddSubnet(efficientip.FakeSpace, "10.31.30.0/24", "new-dev", params)
		_, _ = h.ipam.AddSubnet(efficientip.FakeSpace, "10.31.31.0/24", "no-gateway", map[string]string{efficientip.ParamNetwork: "dvpg-a", efficientip.ParamZone: "corp.example"})
		_, _ = h.ipam.AddSubnet(efficientip.FakeSpace, "10.31.32.0/24", "dup-1", map[string]string{efficientip.ParamNetwork: "dvpg-dup", efficientip.ParamGateway: "10.31.32.1", efficientip.ParamZone: "corp.example"})
		_, _ = h.ipam.AddSubnet(efficientip.FakeSpace, "10.31.33.0/24", "dup-2", map[string]string{efficientip.ParamNetwork: "dvpg-dup", efficientip.ParamGateway: "10.31.33.1", efficientip.ParamZone: "corp.example"})
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		n := mapping("dvpg-new-tst-vl330")
		if n.Source != "efficientip" || n.Subnet != "10.31.30.0/24" || n.Gateway != "10.31.30.1" || n.EfficientIPSubnetID != id ||
			strings.Join(n.DNSServers, " ") != "10.0.0.53 10.0.1.53" {
			t.Fatalf("synced mapping = %+v", n)
		}
		for _, missing := range []string{"dvpg-a", "dvpg-dup"} {
			if _, err := h.store.NetworkSubnet(ctx, missing); err != store.ErrNotFound {
				t.Fatalf("%s mapped: %v", missing, err)
			}
		}

		// The subnet loses its class parameter: its mappings go, the manual ones stay.
		h.ipam.SetSubnetParams(id, nil)
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.NetworkSubnet(ctx, "dvpg-new-dev-vl330"); err != store.ErrNotFound {
			t.Fatalf("mapping left after its subnet lost the parameter: %v", err)
		}
		mapping("dvpg-app-dev-vl310")

		// Pickers.
		const web = "prj_web"
		dev := "dev"
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		if _, err := h.store.Pool.Exec(ctx, `update catalog_item_versions set ui_schema = jsonb_set(ui_schema, '{fields,subnet}',
			'{"widget": "select", "label": "Subnet", "value_source": {"kind": "builtin", "name": "ipam.subnets", "params": {"network": "${inputs.network}"}}}')
			where id = $1`, form.VersionId); err != nil {
			t.Fatal(err)
		}
		values := func(version, field string, inputs map[string]any) api.FieldValues {
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", "lmartin", api.FieldValuesQuery{VersionId: version,
				ProjectId: web, Environment: &dev, Field: field, Inputs: inputs}, 200, &out)
			return out
		}
		subnets := values(form.VersionId, "subnet", map[string]any{"network": "dvpg-app-dev-vl310"})
		if subnets.Options == nil || len(*subnets.Options) != 1 || (*subnets.Options)[0].Value != "10.31.10.0/24" ||
			(*subnets.Options)[0].Label != "10.31.10.0/24 · dvpg-app-dev-vl310" {
			t.Fatalf("ipam.subnets = %+v", subnets.Options)
		}
		h.ipam.AddZone(efficientip.FakeDNSServer, "apps.corp.example")
		var dns api.RequestForm
		h.json("GET", "/api/v1/catalog/items/dns-record/form?project_id="+web, "lmartin", nil, 200, &dns)
		zones := values(dns.VersionId, "zone", nil)
		if zones.Options == nil || len(*zones.Options) != 2 || (*zones.Options)[0].Value != "apps.corp.example" {
			t.Fatalf("dns.zones = %+v", zones.Options)
		}

		// A name already in DNS is skipped, in the preview and on submit.
		if _, err := h.acts.IPAM.CreateRecord(ctx, efficientip.Record{FQDN: "web-dev-app001.corp.example", Type: "A", Value: "10.9.9.9"}); err != nil {
			t.Fatal(err)
		}
		if name := values(form.VersionId, "vm_name", map[string]any{"role": "app", "network": "dvpg-app-dev-vl310"}); name.Value != "web-dev-app002" {
			t.Fatalf("name preview with web-dev-app001 in DNS = %v", name.Value)
		}
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		var dep api.Deployment
		h.json("GET", "/api/v1/deployments/"+(*req.DeploymentIds)[0], "tnguyen", nil, 200, &dep)
		if dep.Name != "web-dev-app002" {
			t.Fatalf("deployment with web-dev-app001 in DNS = %s", dep.Name)
		}
	})
}
