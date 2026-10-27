package e2e

import (
	"context"
	"errors"
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

		// A second subnet claiming a mapped network, or the subnet losing its
		// gateway, holds the mapping as it is until fixed.
		dup, _ := h.ipam.AddSubnet(efficientip.FakeSpace, "10.31.34.0/24", "dup-new", map[string]string{efficientip.ParamNetwork: "dvpg-new-tst-vl330",
			efficientip.ParamGateway: "10.31.34.1", efficientip.ParamZone: "corp.example"})
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if n := mapping("dvpg-new-tst-vl330"); n.Subnet != "10.31.30.0/24" {
			t.Fatalf("mapping after a competing claim = %+v", n)
		}
		h.ipam.SetSubnetParams(dup, nil)
		h.ipam.SetSubnetParams(id, map[string]string{efficientip.ParamNetwork: params[efficientip.ParamNetwork], efficientip.ParamZone: "corp.example"})
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if n := mapping("dvpg-new-dev-vl330"); n.Gateway != "10.31.30.1" {
			t.Fatalf("mapping after its subnet lost the gateway = %+v", n)
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

// TestIPAMReconcile: the report lists what EfficientIP has tagged for rvav
// that no deployment owns, and what a deployment owns that EfficientIP lost.
func TestIPAMReconcile(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)

		report := func() api.IpamReconcile {
			var out api.IpamReconcile
			h.json("GET", "/api/v1/admin/ipam/reconcile", "mkeller", nil, 200, &out)
			return out
		}
		if r := report(); r.ManagedAddresses != 1 || r.ManagedRecords != 2 || len(r.OrphanAddresses)+len(r.OrphanRecords)+len(r.Missing) != 0 {
			t.Fatalf("after one VM = %+v", r)
		}

		// A failed run left an address and a record behind; someone deleted
		// the deployment's PTR record in EfficientIP.
		left, err := h.acts.IPAM.Reserve(ctx, efficientip.Subnet{CIDR: "10.31.10.0/24"}, "web-dev-app099.corp.example", "dep_gone")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.acts.IPAM.CreateRecord(ctx, efficientip.Record{FQDN: "web-dev-app099.corp.example", Type: "A", Value: left.IP, Zone: "corp.example"}); err != nil {
			t.Fatal(err)
		}
		var ptr store.IPAMResource
		owned, _ := h.store.IPAMResources(ctx)
		for _, r := range owned {
			if strings.HasPrefix(r.Name, "PTR ") {
				ptr = r
			}
		}
		if err := h.acts.IPAM.DeleteRecord(ctx, efficientip.Record{ID: *ptr.ExternalID}); err != nil {
			t.Fatal(err)
		}

		r := report()
		if len(r.OrphanAddresses) != 1 || r.OrphanAddresses[0].Ip != left.IP || r.OrphanAddresses[0].Name != "web-dev-app099.corp.example" ||
			r.OrphanAddresses[0].Subnet != "10.31.10.0/24" {
			t.Fatalf("orphan addresses = %+v", r.OrphanAddresses)
		}
		if len(r.OrphanRecords) != 1 || r.OrphanRecords[0].Fqdn != "web-dev-app099.corp.example" || r.OrphanRecords[0].Type != "A" {
			t.Fatalf("orphan records = %+v", r.OrphanRecords)
		}
		if len(r.Missing) != 1 || r.Missing[0].ResourceId != ptr.ID || r.Missing[0].DeploymentName != "web-dev-app001" {
			t.Fatalf("missing = %+v", r.Missing)
		}

		h.json("GET", "/api/v1/admin/ipam/reconcile", "tnguyen", nil, 403, nil)

		// An address already recorded for a deployment is never moved to another.
		var address store.IPAMResource
		for _, r := range owned {
			if r.Type == "ipam.address" {
				address = r
			}
		}
		_, err = h.store.UpsertResource(ctx, h.store.Pool, store.Resource{ID: "res_taken", DeploymentID: "dep_other", Type: "ipam.address",
			Name: "10.31.10.99", ExternalID: address.ExternalID, Properties: []byte(`{}`)})
		if !errors.Is(err, store.ErrTaken) {
			t.Fatalf("recording another deployment's address = %v", err)
		}
	})
}
