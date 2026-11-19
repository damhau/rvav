package e2e

import (
	"context"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
)

// TestUpdateAdoptedWithoutInputs: a deployment adopted without inputs is
// compared as if requested with its version's initial values, and a
// placement it never recorded isn't refused where its VM already is
// (X1.6). Elsewhere, it is.
func TestUpdateAdoptedWithoutInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner = "mkeller", "tnguyen"
		app01 := existingVM(t, ctx, h, c, model, "legacy-app01", "10.31.10.211")
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var rhel api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", owner, nil, 200, &rhel)
		var res api.OnboardingResult
		h.json("POST", "/api/v1/admin/onboarding", admin, api.OnboardingDraft{ProjectId: "prj_web", Environment: "dev",
			OwnerId: "usr_tnguyen", CatalogItemId: rhel.Id, Vms: []vmRefT{{Moref: app01, Vcenter: "vc01.corp.example"}}}, 200, &res)
		dep := res.Deployments[0].Id
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", owner, nil, 200, &form)

		// The form's initial values, placed where the VM is: nothing to
		// change, and the update records them.
		in := maps.Clone(form.InitialValues)
		maps.Copy(in, map[string]any{"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"})
		var pv api.UpdatePreview
		h.json("POST", "/api/v1/deployments/"+dep+"/update/preview", owner, api.UpdateDraft{VersionId: form.VersionId, Inputs: in}, 200, &pv)
		if len(pv.Refused) != 0 || !pv.Allowed || len(pv.Changes) != 0 || pv.Unchanged {
			t.Fatalf("where the VM is: allowed %v, unchanged %v, refused %+v, changes %+v", pv.Allowed, pv.Unchanged, pv.Refused, pv.Changes)
		}

		elsewhere := maps.Clone(in)
		elsewhere["network"] = "dvpg-db-dev-vl320"
		h.json("POST", "/api/v1/deployments/"+dep+"/update/preview", owner, api.UpdateDraft{VersionId: form.VersionId, Inputs: elsewhere}, 200, &pv)
		if len(pv.Refused) != 1 || deref(pv.Refused[0].Setting) != "network" {
			t.Fatalf("elsewhere: refused %+v, errors %+v", pv.Refused, pv.Errors)
		}

		var req api.Request
		h.json("POST", "/api/v1/deployments/"+dep+"/update", owner, api.UpdateDraft{VersionId: form.VersionId, Inputs: in}, 202, &req,
			"Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
		var d api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+dep, owner, nil, 200, &d)
		if d.Inputs["datastore"] != "vsan-gva-dev-cl01" || d.Inputs["size"] != "M" {
			t.Fatalf("inputs after the update = %v", d.Inputs)
		}
	}, model)
}
