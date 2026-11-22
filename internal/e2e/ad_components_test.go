package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/ad/adtest"
	"github.com/damhau/rvav/internal/provision"
)

// TestActiveDirectoryComponents: a multi-VM item's VMs get their computer
// objects as a single VM does, in the project's OU before they are cloned:
// when it is made and when an update adds one; a failed clone gives its
// object back; an update that takes a VM away, and the deployment's
// delete, remove theirs (ADR-0064).
func TestActiveDirectoryComponents(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, user = "mkeller", "tnguyen"
		web := "OU=Web," + adtest.BaseDN
		dir := adtest.Start(t, []string{web})
		h.svc.Config.AD = directoryConfig(dir)
		h.acts.Directory = func(context.Context) provision.Directory {
			return renaming{core.Directory(h.svc.Config.AD), dir}
		}
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{ProjectId: new("prj_web"), Ou: "OU=Web"}, 201, nil)
		def := shopApp()
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "ad-shop", Name: "AD shop", Summary: "x",
			Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/ad-shop/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/ad-shop/entitlements", admin, api.CatalogEntitlements{AllProjects: true,
			ProjectIds: []string{}}, 200, nil)
		inputs := func(app string, webNodes int) map[string]any {
			return map[string]any{"app": app, "web_nodes": webNodes, "size": "S", "web_cluster": "GVA-DEV-CL01",
				"web_datastore": "vsan-gva-dev-cl01", "db_cluster": "GVA-DEV-CL01", "db_datastore": "nfs-gva-bronze-01",
				"network": "dvpg-app-dev-vl310", "db_post": []string{}}
		}
		submit := func(app string, setup func(env *testsuite.TestWorkflowEnvironment)) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", user, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: inputs(app, 2)}, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, setup)
			return req
		}
		names := func(dns []string) []string {
			var out []string
			for _, dn := range dns {
				cn, _, _ := strings.Cut(strings.TrimPrefix(dn, "CN="), ",")
				out = append(out, cn)
			}
			return out
		}
		adSteps := func(id string) map[string]api.Step {
			t.Helper()
			out := map[string]api.Step{}
			for k, s := range timelineOf(h, user, id) {
				if strings.Contains(k, "/"+provision.KeyAD+"@") {
					out[k] = s
				}
			}
			return out
		}

		// A clone that fails gives the object it was made for back.
		failed := submit("f", func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(h.acts.CloneVM, mock.Anything, mock.Anything).Return("", temporal.NewNonRetryableApplicationError(
				"vCenter refused the clone", "test", nil))
		})
		h.waitStatus(failed.Id, user, api.RequestStatusFailed)
		steps := adSteps(failed.Id)
		if len(steps) != 3 || dir.Adds() != 3 || len(dir.Computers()) != 0 {
			t.Fatalf("after failed clones: steps %+v, %d adds, computers %v", steps, dir.Adds(), dir.Computers())
		}
		for k, s := range steps {
			if !strings.HasPrefix(s.Title, "Removed computer object ") {
				t.Errorf("%s = %q %q", k, s.Title, deref(s.Detail))
			}
		}

		// Made before each VM is cloned, one per VM of each component.
		req := submit("s", nil)
		req = h.waitStatus(req.Id, user, api.RequestStatusSucceeded)
		depID := (*req.DeploymentIds)[0]
		want := []string{"WEB-DEV-SDB01", "WEB-DEV-SWEB01", "WEB-DEV-SWEB02"}
		if got := names(dir.Computers()); !slices.Equal(got, want) {
			t.Fatalf("computers = %v, want %v", got, want)
		}
		for k, s := range adSteps(req.Id) {
			if s.Status != api.StepStatusDone || !strings.HasPrefix(s.Title, "Created computer object ") {
				t.Errorf("%s = %s %q", k, s.Status, s.Title)
			}
		}
		e := dir.Entry("CN=WEB-DEV-SWEB02," + web)
		if !slices.Equal(e["description"], []string{"rvav: request " + req.Id + ", deployment " + depID}) {
			t.Fatalf("an object = %v", e)
		}

		// An update adds a web server, with its object, then takes two away.
		update := func(webNodes int) {
			t.Helper()
			var r api.Request
			h.json("POST", "/api/v1/deployments/"+depID+"/update", user, api.UpdateDraft{VersionId: item.DraftVersion.Id,
				Inputs: inputs("s", webNodes)}, 202, &r, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(r.Id, nil)
			h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
		}
		update(3)
		if got := names(dir.Computers()); !slices.Equal(got, append(want, "WEB-DEV-SWEB03")) {
			t.Fatalf("after adding a VM: %v", got)
		}
		update(1)
		if got := names(dir.Computers()); !slices.Equal(got, want[:2]) {
			t.Fatalf("after removing two: %v", got)
		}

		// Deleted with the deployment.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", user, api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, user, api.RequestStatusSucceeded)
		if got := dir.Computers(); len(got) != 0 {
			t.Fatalf("after the delete: %v", got)
		}

		// A composite's step that is a multi-VM item makes them too.
		with := map[string]any{}
		for k := range inputs("", 0) {
			with[k] = "inputs." + k
		}
		comp := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "properties": def.FormSchema["properties"]},
			UiSchema:   def.UiSchema, InitialValues: map[string]any{},
			Blueprint: map[string]any{"kind": "composite", "steps": []any{
				map[string]any{"key": "shop", "title": "The shop", "item": "ad-shop", "with": with}}},
		}
		var ci api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "ad-shop-composite", Name: "AD shop composite",
			Summary: "x", Category: "compute", Type: api.Composite, Definition: &comp}, 201, &ci)
		h.json("POST", "/api/v1/admin/catalog/items/ad-shop-composite/versions/"+ci.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/ad-shop-composite/entitlements", admin, api.CatalogEntitlements{AllProjects: true,
			ProjectIds: []string{}}, 200, nil)
		var cr api.Request
		h.json("POST", "/api/v1/requests", user, api.RequestDraft{CatalogItemId: ci.Id, VersionId: ci.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: inputs("c", 1)}, 202, &cr, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(cr.Id, nil)
		h.waitStatus(cr.Id, user, api.RequestStatusSucceeded)
		if got := names(dir.Computers()); !slices.Equal(got, []string{"WEB-DEV-CDB01", "WEB-DEV-CWEB01"}) {
			t.Fatalf("a composite's computers = %v", got)
		}
		if s := timelineOf(h, user, cr.Id)["step:shop/ad.computer@web-dev-cweb01"]; s.Status != api.StepStatusDone {
			t.Fatalf("a composite's step = %+v", s)
		}
	})
}
