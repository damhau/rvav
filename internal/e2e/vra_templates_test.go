package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/vra"
	"github.com/damhau/rvav/internal/vraimport"
)

// TestVRATemplates: the sample vRA's cloud templates mapped to catalog
// items import through the catalog's own YAML import as drafts; the RHEL
// one, published, makes a valid request named by the project's vRA naming
// template, sized from vRA's flavors (P2.2).
func TestVRATemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		e := vra.SampleEstate(time.Now())
		imported := importVRAItems(h, &e)
		if len(imported) != 4 {
			t.Fatalf("%d items imported, want 4 (the draft template skipped)", len(imported))
		}

		rhel := imported["bp-rhel9"]
		draft := struct{ Id string }{publishVRAItem(h, rhel)}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", api.RequestDraft{CatalogItemId: rhel.ItemId, VersionId: draft.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"size": "medium", "role": "api",
				"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 200, &pv,
			"Idempotency-Key", uuid.NewString())
		if !pv.Valid || pv.Names == nil || len(*pv.Names) != 1 || (*pv.Names)[0] != "web-dev-api001" {
			t.Fatalf("preview: valid %v, names %v, errors %+v", pv.Valid, pv.Names, pv.Errors)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: rhel.ItemId, VersionId: draft.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"size": "medium", "role": "api",
				"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 202, &req,
			"Idempotency-Key", uuid.NewString())
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
	draft := res.Versions[len(res.Versions)-1]
	h.json("POST", "/api/v1/admin/catalog/items/"+res.ItemId+"/versions/"+draft.Id+"/publish", "mkeller", nil, 200, nil)
	h.json("PUT", "/api/v1/admin/catalog/items/"+res.ItemId+"/entitlements", "mkeller",
		api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
	return draft.Id
}
