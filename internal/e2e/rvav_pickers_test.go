package e2e

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// TestRvavPickers: the project, user, deployment and vsphere_vm widgets
// offer what the requester may see, and the server refuses a crafted
// request naming anything else, such as another project's deployment (F1.1).
func TestRvavPickers(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		for _, d := range []struct{ id, name, status, project, item string }{
			{"dep_web_app", "web-dev-app001", "ready", "prj_web", "rhel9-server"},
			{"dep_web_db", "web-dev-db001", "ready", "prj_web", "ubuntu-server"},
			{"dep_web_gone", "web-dev-app000", "deleted", "prj_web", "rhel9-server"},
			{"dep_data_app", "data-dev-app001", "ready", "prj_data", "rhel9-server"},
		} {
			if err := h.store.InsertDeployment(ctx, h.store.Pool, store.Deployment{ID: d.id, Name: d.name, Status: d.status,
				ProjectID: d.project, Environment: "dev", ItemID: "itm_" + d.item, VersionID: "ver_" + d.item + "_1",
				OwnerID: "usr_tnguyen", ExpiryAction: "power_off_then_delete"}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.store.UpsertResource(ctx, h.store.Pool, store.Resource{ID: "res_" + d.id, DeploymentID: d.id,
				Type: "vsphere.vm", Name: d.name, ExternalID: new("vm-" + d.id), Properties: []byte(`{}`)}); err != nil {
				t.Fatal(err)
			}
		}

		builtin := func(name string, params map[string]any) *api.ValueSource {
			src := &api.ValueSource{Kind: api.Builtin, Name: &name}
			if params != nil {
				src.Params = &params
			}
			return src
		}
		fields := map[string]api.UiField{
			"project": {Widget: api.UiWidgetProject, Label: "Charge to"},
			"owner":   {Widget: api.UiWidgetUser, Label: "Technical owner", ValueSource: builtin("rvav.users", nil)},
			"deployment": {Widget: api.UiWidgetDeployment, Label: "Web server",
				ValueSource: builtin("rvav.deployments", map[string]any{"item": "rhel9-server"})},
			"vm": {Widget: api.UiWidgetVsphereVm, Label: "VM", DependsOn: &[]string{"deployment"},
				ValueSource: builtin("rvav.vms", map[string]any{"deployment": "${inputs.deployment}"})},
		}
		props := map[string]any{}
		for name := range fields {
			props[name] = map[string]any{"type": "string"}
		}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "properties": props},
			UiSchema:   api.UiSchema{Fields: fields},
			Blueprint:  map[string]any{"kind": "awx", "awx": map[string]any{"title": "Check a server", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Server", Fields: slices.Sorted(maps.Keys(fields))}}

		// These widgets take their choices from rvav only.
		bad := def
		bad.UiSchema.Fields = maps.Clone(fields)
		dep := fields["deployment"]
		dep.ValueSource = &api.ValueSource{Kind: api.Rest, Params: &map[string]any{"url": "https://cmdb.corp.example/servers"}}
		bad.UiSchema.Fields["deployment"] = dep
		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-picker", Name: "Bad picker",
			Summary: "A deployment from elsewhere.", Category: "compute", Type: api.AwxJobTemplate, Definition: &bad}, 422, &refused)
		if !slices.ContainsFunc(refused.Errors, func(e api.FieldError) bool {
			return e.Field == "/definition/ui_schema/fields/deployment/value_source" && deref(e.Code) == "widget_mismatch"
		}) {
			t.Errorf("a deployment field with a REST source: %+v", refused.Errors)
		}

		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pick-rvav", Name: "Check a server",
			Summary: "Pickers of rvav's own records.", Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/pick-rvav/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/pick-rvav/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		offered := func(field, search string, inputs map[string]any) []string {
			t.Helper()
			q := api.FieldValuesQuery{VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: new(api.EnvironmentKey("dev")),
				Field: field, Inputs: inputs}
			if search != "" {
				q.Search = &search
			}
			var values api.FieldValues
			h.json("POST", "/api/v1/forms/values", requester, q, 200, &values)
			var out []string
			for _, o := range *values.Options {
				out = append(out, o.Value)
			}
			return out
		}
		if got := offered("project", "", map[string]any{}); !slices.Contains(got, "prj_web") || slices.Contains(got, "prj_data") {
			t.Errorf("projects offered = %v", got)
		}
		if got := offered("owner", "nguyen", map[string]any{}); !slices.Contains(got, "usr_tnguyen") || slices.Contains(got, "usr_lmartin") {
			t.Errorf("people found for nguyen = %v", got)
		}
		if got := offered("owner", "usr_lmartin", map[string]any{}); !slices.Equal(got, []string{"usr_lmartin"}) {
			t.Errorf("people found by ID = %v", got)
		}
		if got := offered("deployment", "", map[string]any{}); !slices.Equal(got, []string{"dep_web_app"}) {
			t.Errorf("deployments of rhel9-server offered = %v", got)
		}
		if got := offered("vm", "", map[string]any{}); !slices.Equal(got, []string{"web-dev-app001", "web-dev-db001"}) {
			t.Errorf("the project's VMs offered = %v", got)
		}
		if got := offered("vm", "", map[string]any{"deployment": "dep_web_app"}); !slices.Equal(got, []string{"web-dev-app001"}) {
			t.Errorf("the deployment's VMs offered = %v", got)
		}

		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: "dev", Inputs: inputs}
		}
		valid := map[string]any{"project": "prj_web", "owner": "usr_lmartin", "deployment": "dep_web_app", "vm": "web-dev-app001"}
		for _, c := range []struct{ field, value, why string }{
			{"deployment", "dep_data_app", "another project's deployment"},
			{"deployment", "dep_web_db", "another item's deployment"},
			{"deployment", "dep_web_gone", "a deleted deployment"},
			{"project", "prj_data", "a project the requester can't request in"},
			{"owner", "usr_nobody", "nobody rvav knows"},
			{"vm", "data-dev-app001", "another project's VM"},
			{"vm", "web-dev-db001", "another deployment's VM"},
		} {
			inputs := maps.Clone(valid)
			inputs[c.field] = c.value
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/requests", requester, draft(inputs), 422, &problem, "Idempotency-Key", uuid.NewString())
			if !slices.ContainsFunc(problem.Errors, func(e api.FieldError) bool {
				return e.Field == "/inputs/"+c.field && deref(e.Code) == "not_allowed"
			}) {
				t.Errorf("%s (%s = %s): %+v, want not_allowed", c.why, c.field, c.value, problem.Errors)
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
			if !reflect.DeepEqual(vars[k], want) {
				t.Errorf("AWX got %s = %#v, want %#v", k, vars[k], want)
			}
		}
	})
}
