package e2e

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestFormDefaults: a default rule fills a field from the others while it
// has no input, and stays editable; a request without the input gets the
// default, and one that emptied it (null) none. A CEL source gives a
// number field its default and a computed field its value, which the
// server works out whatever the client sends (F1.4).
func TestFormDefaults(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		text := map[string]any{"type": "string"}
		cores := map[string]any{"value": `environment == "prd" ? 4 : 2`}
		tag := map[string]any{"value": `"tag-" + inputs.app`}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []any{"app"}, "properties": map[string]any{
				"app": text, "host": text, "fqdn": text, "ticket": text, "tag": text, "cores": map[string]any{"type": "integer"}}},
			UiSchema: api.UiSchema{Fields: map[string]api.UiField{
				"app":    {Widget: api.UiWidgetText, Label: "Application"},
				"host":   {Widget: api.UiWidgetText, Label: "Host", Rules: &api.UiRules{Default: new(`inputs.app + "-" + environment`)}},
				"fqdn":   {Widget: api.UiWidgetText, Label: "FQDN", Rules: &api.UiRules{Default: new(`inputs.host + ".corp.example"`)}},
				"ticket": {Widget: api.UiWidgetText, Label: "Ticket", Rules: &api.UiRules{ReadOnly: new("true"), Default: new(`"CHG-" + inputs.app`)}},
				"cores": {Widget: api.UiWidgetNumber, Label: "Cores", DependsOn: &[]string{"$environment"},
					ValueSource: &api.ValueSource{Kind: api.Cel, Params: &cores}},
				"tag": {Widget: api.UiWidgetComputed, Label: "Tag", DependsOn: &[]string{"app"},
					ValueSource: &api.ValueSource{Kind: api.Cel, Params: &tag}},
			}},
			InitialValues: map[string]any{"host": "fixed"},
			Blueprint:     map[string]any{"kind": "awx", "awx": map[string]any{"title": "Register", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Host", Fields: []string{"app", "host", "fqdn", "ticket", "cores", "tag"}}}
		create := api.CatalogItemCreate{Slug: "host-register", Name: "Register a host", Summary: "Defaults from the application.",
			Category: "compute", Type: api.AwxJobTemplate, Definition: &def}

		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 422, &refused)
		if len(refused.Errors) != 1 || refused.Errors[0].Field != "/definition/ui_schema/fields/host/rules/default" || deref(refused.Errors[0].Code) != "conflict" {
			t.Fatalf("a default rule and an initial value: %+v", refused.Errors)
		}
		def.InitialValues = map[string]any{}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/host-register/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/host-register/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev", Inputs: inputs}
		}
		preview := func(inputs map[string]any) api.FieldStates {
			t.Helper()
			var out api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", requester, draft(inputs), 200, &out)
			if !out.Valid || out.Fields == nil {
				t.Fatalf("preview of %v = %+v", inputs, out)
			}
			return *out.Fields
		}
		st := preview(map[string]any{"app": "web"})
		if st["host"].Default != "web-dev" || st["fqdn"].Default != "web-dev.corp.example" || st["ticket"].Default != "CHG-web" ||
			st["cores"].Default != float64(2) || st["tag"].Value != "tag-web" || st["host"].ReadOnly {
			t.Fatalf("defaults = %+v", st)
		}
		// An edited field keeps its value; the defaults that read it follow it.
		if st := preview(map[string]any{"app": "web", "host": "mine"}); st["fqdn"].Default != "mine.corp.example" || st["host"].Default != "web-dev" {
			t.Fatalf("with host edited = %+v", st)
		}

		submit := func(inputs map[string]any) map[string]any {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", requester, draft(inputs), 202, &req, "Idempotency-Key", uuid.NewString())
			return *req.Inputs
		}
		got := submit(map[string]any{"app": "web", "ticket": "CHG-other", "tag": "forged"})
		if got["host"] != "web-dev" || got["fqdn"] != "web-dev.corp.example" || got["ticket"] != "CHG-web" || got["cores"] != float64(2) || got["tag"] != "tag-web" {
			t.Fatalf("a request without the inputs = %v", got)
		}
		got = submit(map[string]any{"app": "web", "host": nil, "cores": 8})
		if _, ok := got["host"]; ok || got["cores"] != float64(8) {
			t.Fatalf("a request that emptied host and changed cores = %v", got)
		}
		if _, ok := got["fqdn"]; ok {
			t.Fatalf("fqdn's default reads host, emptied: %v", got)
		}

		// The designer's preview works the defaults out the same way.
		var designed api.DesignerPreview
		env := api.EnvironmentKey("dev")
		h.json("POST", "/api/v1/admin/catalog/items/host-register/versions/"+item.DraftVersion.Id+"/preview", admin,
			api.DesignerPreviewQuery{ProjectId: "prj_web", Environment: &env, Inputs: map[string]any{"app": "db"}}, 200, &designed)
		if designed.Fields["host"].Default != "db-dev" || designed.Fields["tag"].Value != "tag-db" {
			t.Fatalf("designer preview = %+v", designed.Fields)
		}
	})
}
