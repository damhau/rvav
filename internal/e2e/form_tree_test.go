package e2e

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestTreeFields: a tree field offers a hierarchy, each option naming its
// parent: vSphere's VM folders, a CEL source's or its static options. The
// server refuses on submit a node its source doesn't offer (F1.2).
func TestTreeFields(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		fields := map[string]api.UiField{
			"folder": {Widget: api.UiWidgetTree, Label: "Folder", ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("vsphere.folders")}},
			"ou": {Widget: api.UiWidgetTree, Label: "Unit", ValueSource: &api.ValueSource{Kind: api.Cel, Params: &map[string]any{
				"options": `[{"value": "OU=apps,DC=corp", "label": "Applications"}, {"value": "OU=web,OU=apps,DC=corp", "label": "Web", "parent": "OU=apps,DC=corp"}]`}}},
			"zone": {Widget: api.UiWidgetTree, Label: "Zone", Options: &[]api.Option{{Value: "dmz", Label: "DMZ"},
				{Value: "dmz-web", Label: "Web", Parent: new("dmz")}}},
		}
		props := map[string]any{}
		for name := range fields {
			props[name] = map[string]any{"type": "string"}
		}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "properties": props},
			UiSchema:   api.UiSchema{Fields: fields},
			Blueprint:  map[string]any{"kind": "awx", "awx": map[string]any{"title": "Place", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Where", Fields: []string{"folder", "ou", "zone"}}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "tree", Name: "Tree fields",
			Summary: "Nodes of hierarchies.", Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/tree/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/tree/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		offered := func(field string) map[string]api.Option {
			t.Helper()
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", requester, api.FieldValuesQuery{VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: new(api.EnvironmentKey("dev")), Field: field, Inputs: map[string]any{}}, 200, &out)
			byValue := map[string]api.Option{}
			for _, o := range *out.Options {
				byValue[o.Value] = o
			}
			return byValue
		}
		folders := offered("folder")
		if tst := folders["/GVA/vm/rvav/web/tst"]; tst.Label != "tst" || deref(tst.Parent) != "/GVA/vm/rvav/web" {
			t.Errorf("a folder in the tree: %+v", tst)
		}
		if top := folders["/GVA/vm"]; top.Label != "/GVA/vm" || top.Parent != nil {
			t.Errorf("the top of the tree: %+v", top)
		}
		if web := offered("ou")["OU=web,OU=apps,DC=corp"]; web.Label != "Web" || deref(web.Parent) != "OU=apps,DC=corp" {
			t.Errorf("a unit from CEL: %+v", web)
		}

		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: "dev", Inputs: inputs}
		}
		valid := map[string]any{"folder": "/GVA/vm/rvav/web/tst", "ou": "OU=web,OU=apps,DC=corp", "zone": "dmz-web"}
		for field, value := range map[string]string{"folder": "/DC0/vm", "ou": "OU=db,DC=corp", "zone": "lan"} {
			inputs := maps.Clone(valid)
			inputs[field] = value
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/requests", requester, draft(inputs), 422, &problem, "Idempotency-Key", uuid.NewString())
			if len(problem.Errors) != 1 || problem.Errors[0].Field != "/inputs/"+field || deref(problem.Errors[0].Code) != "not_allowed" {
				t.Errorf("%s = %s, which its tree doesn't offer: %+v", field, value, problem.Errors)
			}
		}

		var req api.Request
		h.json("POST", "/api/v1/requests", requester, draft(valid), 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, requester, nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("request = %s %s", req.Status, errorOf(req))
		}
		var timeline struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", requester, nil, 200, &timeline)
		i := slices.IndexFunc(timeline.Items, func(s api.Step) bool { return strings.HasPrefix(s.Key, "awx.job:") })
		if i < 0 || timeline.Items[i].ExternalRef == nil {
			t.Fatalf("no AWX job in %+v", timeline.Items)
		}
		job, _ := strconv.Atoi(timeline.Items[i].ExternalRef.Id)
		vars := h.awx.Launched(job).ExtraVars
		for k, want := range valid {
			if vars[k] != want {
				t.Errorf("AWX got %s = %v, want %v", k, vars[k], want)
			}
		}
	})
}
