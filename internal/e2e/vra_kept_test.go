package e2e

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/vra"
)

const vraKeptTemplate = `formatVersion: 1
inputs:
  environment:
    type: string
    enum: [dev, prd]
  app:
    type: string
    title: Application
resources:
  Cloud_vSphere_Machine_1:
    type: Cloud.vSphere.Machine
    preventDelete: true
    ignoreChanges: true
    properties:
      image: rhel9
      flavor: medium
      folderName: apps/${input.app}
      owner: ${input.app}-team
      backupPolicies: [daily]
      cloudConfig: |
        #cloud-config
        runcmd:
          - echo ${secret.Root_Password} | passwd --stdin root
`

// TestVRAKeptTemplate: the importer carries over what T1.5 added (#138):
// preventDelete, folderName and custom properties of a machine, a secret
// its cloud-init opens (the item waits until rvav stores it), a vRA
// project's custom properties and its custom naming's counter.
func TestVRAKeptTemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		e := vra.SampleEstate(time.Now())
		e.Projects[0].Properties = map[string]string{"costCenter": "CC-1001", "tier": "gold", "bad key": "x"}
		e.Blueprints = append(e.Blueprints, vra.Blueprint{ID: "bp-kept", Name: "Kept server", ProjectID: "vra-prj-web",
			Status: "RELEASED", Content: vraKeptTemplate, Versions: []vra.BlueprintVersion{
				{ID: "bp-kept-v1", BlueprintID: "bp-kept", Version: "1", Status: "RELEASED", Content: vraKeptTemplate}}})
		h.json("PATCH", "/api/v1/admin/projects/prj_web", admin,
			api.AdminProjectUpdate{Properties: &api.CustomProperties{"costCenter": "CC-9999"}}, 200, nil)
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, e, 201, nil)

		plan := func() api.VraPlan {
			t.Helper()
			var p api.VraPlan
			h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
			return p
		}
		item := func(p api.VraPlan, id string) api.VraItemPlan {
			t.Helper()
			i := slices.IndexFunc(p.Items, func(x api.VraItemPlan) bool { return x.BlueprintId == id })
			if i < 0 {
				t.Fatalf("no plan for %s", id)
			}
			return p.Items[i]
		}
		note := func(it api.VraItemPlan, level, where, text string) bool {
			return slices.ContainsFunc(it.Notes, func(n api.VraNote) bool {
				return string(n.Level) == level && strings.HasPrefix(n.Where, where) && strings.Contains(n.Text, text)
			})
		}

		// Its secret isn't in rvav: the item waits for it.
		p := plan()
		kept := item(p, "bp-kept")
		if kept.Action != "conflict" || !strings.Contains(deref(kept.Reason), "root-password") || kept.Status != "partial" ||
			!note(kept, "unmapped", "resources.Cloud_vSphere_Machine_1.ignoreChanges", "no rvav equivalent") ||
			!note(kept, "unmapped", "resources.Cloud_vSphere_Machine_1.backupPolicies", "a list or a map") {
			t.Fatalf("kept = %+v", kept)
		}
		if db := item(p, "bp-db"); !note(db, "unmapped", "naming", "counts from 100 by 10") {
			t.Fatalf("db notes = %+v", db.Notes)
		}
		web := p.Projects[slices.IndexFunc(p.Projects, func(x api.VraProjectPlan) bool { return x.VraId == "vra-prj-web" })]
		if web.PropertiesAdd == nil || !maps.Equal(*web.PropertiesAdd, map[string]string{"tier": "gold"}) ||
			web.PropertiesUnmapped == nil || !slices.Equal(*web.PropertiesUnmapped, []string{"bad key"}) {
			t.Fatalf("web project = %+v", web)
		}

		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "root-password", Kind: api.SecretWriteKindToken,
			Value: new("s3cret")}, 201, nil)
		if kept := item(plan(), "bp-kept"); kept.Action != "create" {
			t.Fatalf("kept with its secret = %+v", kept)
		}

		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if !slices.Contains(res.Created, "Kept server") || !slices.Equal(res.ProjectsUpdated, []string{"Web Platform"}) || len(res.Failed) != 0 {
			t.Fatalf("import = %+v", res)
		}
		var proj api.AdminProject
		h.json("GET", "/api/v1/admin/projects/prj_web", admin, nil, 200, &proj)
		if !maps.Equal(map[string]string(proj.Properties), map[string]string{"costCenter": "CC-9999", "tier": "gold"}) {
			t.Fatalf("web properties = %v", proj.Properties)
		}

		id := deref(item(res.Plan, "bp-kept").RvavItemId)
		var it api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/"+id, admin, nil, 200, &it)
		var v api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/"+id+"/versions/"+it.Versions[0].Id, admin, nil, 200, &v)
		bp := v.Definition.Blueprint
		props, _ := bp["properties"].(map[string]any)
		ud := bp["guest"].(map[string]any)["cloud_init"].(map[string]any)["user_data"]
		if bp["prevent_delete"] != true || bp["folder"] != "apps/${inputs.app}" || len(props) != 1 || props["owner"] != "${inputs.app}-team" ||
			!strings.Contains(ud.(string), `echo {{ secret "root-password" }} | passwd`) {
			t.Fatalf("kept blueprint = %+v", bp)
		}

		// Imported again: nothing to add to the project.
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if len(res.ProjectsUpdated) != 0 || len(res.Created) != 0 {
			t.Fatalf("again = %+v", res)
		}
	}, model)
}
