package e2e

import (
	"context"
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/vra"
	"github.com/damhau/rvav/internal/vraimport"
)

// TestVRATemplates: the sample vRA's cloud templates mapped to catalog
// items import through the catalog's own YAML import as drafts; the RHEL
// one, published, makes a valid request named by the project's vRA naming
// template, sized from vRA's flavors (P2.2), and placed by its constraint
// tags on the cluster and network tagged in vCenter (T1.2).
func TestVRATemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		tagObjects(t, ctx, c, "env/dev", "GVA-DEV-CL01")
		tagObjects(t, ctx, c, "net/app", "dvpg-app-dev-vl310")
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		e := vra.SampleEstate(time.Now())
		imported := importVRAItems(h, &e)
		if len(imported) != 4 {
			t.Fatalf("%d items imported, want 4 (the draft template skipped)", len(imported))
		}

		rhel := imported["bp-rhel9"]
		draft := struct{ Id string }{publishVRAItem(h, rhel)}
		in := api.RequestDraft{CatalogItemId: rhel.ItemId, VersionId: draft.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"size": "medium", "role": "api"}}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", in, 200, &pv, "Idempotency-Key", uuid.NewString())
		if !pv.Valid || pv.Names == nil || len(*pv.Names) != 1 || (*pv.Names)[0] != "web-dev-api001" {
			t.Fatalf("preview: valid %v, names %v, errors %+v", pv.Valid, pv.Names, pv.Errors)
		}
		// env:${input.environment} is env/dev; the gold datastore is
		// preferred (rvav-tier:gold:soft); net:app chooses the network.
		if pl := (*pv.Components)[0].Placement; deref(pl.Cluster) != "GVA-DEV-CL01" || deref(pl.Datastore) != "vsan-gva-dev-cl01" ||
			pl.Constraints == nil || len(*pl.Constraints) != 4 {
			t.Fatalf("placement = %+v", pl)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", in, 202, &req, "Idempotency-Key", uuid.NewString())
		// Its guest is cloud-init (vRA's cloudConfig): vcsim has none, so
		// the test sets the address, as the guest would.
		h.runWorkflow(req.Id, cloudInitGuest(h, c))
		done := h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		var moref string
		_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`,
			done.Id).Scan(&moref)
		var vm mo.VirtualMachine
		if err := property.DefaultCollector(c).RetrieveOne(ctx, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref},
			[]string{"name", "config.hardware"}, &vm); err != nil {
			t.Fatal(err)
		}
		if vm.Name != "web-dev-api001" || vm.Config.Hardware.NumCPU != 4 || vm.Config.Hardware.MemoryMB != 16384 {
			t.Fatalf("the VM is %s with %d vCPU, %d MB", vm.Name, vm.Config.Hardware.NumCPU, vm.Config.Hardware.MemoryMB)
		}
		placed := (&vmDeployment{h: h, id: (*done.DeploymentIds)[0], viewer: "tnguyen"}).vm()
		if deref(placed.Cluster) != "GVA-DEV-CL01" || len(placed.Nics) == 0 || placed.Nics[0].Network != "dvpg-app-dev-vl310" {
			t.Fatalf("the VM landed on %s / %+v", deref(placed.Cluster), placed.Nics)
		}
	})
}

// importVRAItems imports the estate's mapped templates through the
// catalog's YAML import, as drafts; by template ID.
func importVRAItems(h *harness, e *vra.Estate) map[string]api.CatalogImportResult {
	h.t.Helper()
	imported := map[string]api.CatalogImportResult{}
	for _, bp := range e.Blueprints {
		p := vraimport.MapTemplate(e, bp)
		if p.Status == vraimport.Skipped {
			continue
		}
		y, err := p.Doc.YAML()
		if err != nil {
			h.t.Fatal(err)
		}
		var res api.CatalogImportResult
		h.json("POST", "/api/v1/admin/catalog/import", "mkeller", rawYAML(y), 200, &res, "Content-Type", "application/yaml")
		if !res.Created || len(res.Versions) == 0 {
			h.t.Fatalf("%s imported as %+v", bp.Name, res)
		}
		imported[bp.ID] = res
	}
	return imported
}

// publishVRAItem publishes an imported item's draft for every project.
func publishVRAItem(h *harness, res api.CatalogImportResult) string {
	h.t.Helper()
	id := publishDraft(h, res)
	h.json("PUT", "/api/v1/admin/catalog/items/"+res.ItemId+"/entitlements", "mkeller",
		api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
	return id
}

// publishDraft publishes an imported item's draft, its sharing as it is.
func publishDraft(h *harness, res api.CatalogImportResult) string {
	h.t.Helper()
	draft := res.Versions[len(res.Versions)-1]
	h.json("POST", "/api/v1/admin/catalog/items/"+res.ItemId+"/versions/"+draft.Id+"/publish", "mkeller", nil, 200, nil)
	return draft.Id
}

// TestVRAReferences: a vRA template whose machines read each other, with
// dependsOn, count.index and string functions, imports as an item whose
// web servers are built after the database, with its address in their
// tags and cloud-init (T1.1).
func TestVRAReferences(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	const content = `formatVersion: 1
inputs:
  app:
    type: string
resources:
  db:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
  web:
    type: Cloud.vSphere.Machine
    dependsOn: [db]
    properties:
      image: rhel9
      flavor: small
      count: 2
      tags:
        - key: app
          value: ${to_lower(input.app)}-${count.index}
        - key: app
          value: db-${replace(resource.db.address, '.', '-')}
      cloudConfig: |
        #cloud-config
        write_files:
          - path: /etc/app.conf
            content: |
              db=${resource.db.address} node=${count.index}
`
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		e := vra.SampleEstate(time.Now())
		e.Blueprints = []vra.Blueprint{{ID: "bp-refs", Name: "Shop", Versions: []vra.BlueprintVersion{
			{Version: "1", Status: "RELEASED", Content: content}}}}
		res := importVRAItems(h, &e)["bp-refs"]
		draft := publishVRAItem(h, res)
		in := api.RequestDraft{CatalogItemId: res.ItemId, VersionId: draft, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"app": "Shop", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", in, 200, &pv)
		if !pv.Valid || pv.Components == nil || len(*pv.Components) != 2 || (*pv.Components)[1].DependsOn == nil ||
			!slices.Equal(*(*pv.Components)[1].DependsOn, []string{"db"}) {
			t.Fatalf("preview: valid %v, components %+v, errors %+v", pv.Valid, pv.Components, pv.Errors)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", in, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, cloudInitGuest(h, c))
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		var dbIP string
		_ = h.store.Pool.QueryRow(ctx, `select name from resources where request_id = $1 and type = 'ipam.address'
			and properties->>'hostname' = 'web-dev-db01'`, req.Id).Scan(&dbIP)
		if dbIP == "" {
			t.Fatal("the database has no address")
		}
		for i, name := range []string{"web-dev-web01", "web-dev-web02"} {
			var moref string
			_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1
				and type = 'vsphere.vm' and name = $2`, req.Id, name).Scan(&moref)
			want := []string{"app/db-" + strings.ReplaceAll(dbIP, ".", "-"), "app/shop-" + strconv.Itoa(i)}
			if tags, err := h.vc.VMTags(ctx, moref); err != nil || !slices.Equal(tags, want) {
				t.Fatalf("%s's tags = %v (%v), want %v", name, tags, err, want)
			}
			var vm mo.VirtualMachine
			if err := property.DefaultCollector(c).RetrieveOne(ctx, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref},
				[]string{"config.extraConfig"}, &vm); err != nil {
				t.Fatal(err)
			}
			var user string
			for _, o := range vm.Config.ExtraConfig {
				if ov := o.GetOptionValue(); ov.Key == "guestinfo.userdata" {
					b, _ := base64.StdEncoding.DecodeString(ov.Value.(string))
					user = string(b)
				}
			}
			if want := "db=" + dbIP + " node=" + strconv.Itoa(i); !strings.Contains(user, want) {
				t.Fatalf("%s's user data lacks %q:\n%s", name, want, user)
			}
		}
	})
}
