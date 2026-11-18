package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestFormLayouts: a form's layout is presentation only. The same request is
// refused, or taken, the same way whether its form is one page, a wizard or
// tabs, and the fields of a page its rules hide aren't checked. A layout
// rvav doesn't know is refused on save, and the kind goes through the
// YAML export and import (F1.3).
func TestFormLayouts(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		definition := func(kind api.UiSchemaLayoutKind) *api.CatalogVersionDefinition {
			def := api.CatalogVersionDefinition{
				FormSchema: map[string]any{"type": "object", "required": []any{"role", "database"}, "properties": map[string]any{
					"role": map[string]any{"type": "string", "pattern": "^[a-z]{2,12}$"}, "with_db": map[string]any{"type": "boolean"},
					"database": map[string]any{"type": "string", "pattern": "^[a-z_]{3,}$"}}},
				UiSchema: api.UiSchema{Fields: map[string]api.UiField{
					"role":     {Widget: api.UiWidgetText, Label: "Role"},
					"with_db":  {Widget: api.UiWidgetToggle, Label: "With a database"},
					"database": {Widget: api.UiWidgetText, Label: "Database", Rules: &api.UiRules{Visible: new("inputs.with_db == true")}},
				}},
				InitialValues: map[string]any{"with_db": false},
				Blueprint:     map[string]any{"kind": "awx", "awx": map[string]any{"title": "Create role", "job_template": "pg-create-role"}},
			}
			def.UiSchema.Layout.Kind = kind
			def.UiSchema.Layout.Sections = []api.UiSection{{Key: "basics", Title: "Basics", Fields: []string{"role", "with_db"}},
				{Key: "db", Title: "Database", Fields: []string{"database"}}}
			return &def
		}
		create := func(slug string, kind api.UiSchemaLayoutKind) api.CatalogItemCreate {
			return api.CatalogItemCreate{Slug: slug, Name: "Layout " + string(kind), Summary: "A role, and a database if asked.",
				Category: "compute", Type: api.AwxJobTemplate, Definition: definition(kind)}
		}

		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, create("layout-carousel", "carousel"), 422, &refused)
		if len(refused.Errors) != 1 || refused.Errors[0].Field != "/definition/ui_schema/layout/kind" || *refused.Errors[0].Code != "invalid" {
			t.Fatalf("a carousel layout: %+v", refused.Errors)
		}

		problems := func(errs []api.FieldError) []string {
			var out []string
			for _, e := range errs {
				out = append(out, e.Field+" "+*e.Code)
			}
			slices.Sort(out)
			return out
		}
		cases := []struct {
			inputs map[string]any
			want   []string
		}{
			{map[string]any{"role": "app", "with_db": true}, []string{"/inputs/database required"}},
			{map[string]any{"role": "App!", "with_db": true, "database": "ab"}, []string{"/inputs/database pattern", "/inputs/role pattern"}},
			// The Database page is hidden: its field isn't checked, nor sent.
			{map[string]any{"role": "app", "with_db": false, "database": "ab"}, nil},
		}
		for _, kind := range []api.UiSchemaLayoutKind{api.Page, api.Wizard, api.Tabs} {
			slug := "layout-" + string(kind)
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, create(slug, kind), 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			var form api.RequestForm
			h.json("GET", "/api/v1/catalog/items/"+slug+"/form?project_id=prj_web", requester, nil, 200, &form)
			if form.UiSchema.Layout.Kind != kind {
				t.Errorf("%s: the request form's layout is %q", kind, form.UiSchema.Layout.Kind)
			}

			draft := func(inputs map[string]any) api.RequestDraft {
				return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
					Environment: "dev", Inputs: inputs}
			}
			for i, c := range cases {
				var preview api.RequestPreview
				h.json("POST", "/api/v1/requests/preview", requester, draft(c.inputs), 200, &preview)
				if got := problems(preview.Errors); !slices.Equal(got, c.want) {
					t.Errorf("%s, case %d: the preview finds %v, want %v", kind, i, got, c.want)
				}
				if c.want == nil {
					continue
				}
				var problem struct{ Errors []api.FieldError }
				h.json("POST", "/api/v1/requests", requester, draft(c.inputs), 422, &problem, "Idempotency-Key", uuid.NewString())
				if got := problems(problem.Errors); !slices.Equal(got, c.want) {
					t.Errorf("%s, case %d: submit is refused for %v, want %v", kind, i, got, c.want)
				}
			}
			var req api.Request
			h.json("POST", "/api/v1/requests", requester, draft(cases[2].inputs), 202, &req, "Idempotency-Key", uuid.NewString())
			if _, sent := (*req.Inputs)["database"]; sent || (*req.Inputs)["role"] != "app" {
				t.Errorf("%s: the request keeps %v", kind, *req.Inputs)
			}
		}

		// YAML keeps the layout: exported, imported as another item.
		status, body, _ := h.do("GET", "/api/v1/admin/catalog/items/layout-tabs/export", admin, nil)
		if status != 200 || !strings.Contains(string(body), "kind: tabs") {
			t.Fatalf("export = %d %s", status, body)
		}
		copied := strings.Replace(string(body), "slug: layout-tabs", "slug: layout-tabs-copy", 1)
		var imported api.CatalogImportResult
		h.json("POST", "/api/v1/admin/catalog/import", admin, rawYAML(copied), 200, &imported, "Content-Type", "application/yaml")
		var version api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/layout-tabs-copy/versions/"+imported.Versions[0].Id, admin, nil, 200, &version)
		if version.Definition.UiSchema.Layout.Kind != api.Tabs {
			t.Errorf("imported layout = %q", version.Definition.UiSchema.Layout.Kind)
		}
		bad := strings.Replace(copied, "kind: tabs", "kind: carousel", 1)
		bad = strings.Replace(bad, "slug: layout-tabs-copy", "slug: layout-tabs-bad", 1)
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/import", admin, rawYAML(bad), 422, &problem, "Content-Type", "application/yaml")
		if got := problems(problem.Errors); !slices.ContainsFunc(got, func(p string) bool {
			return strings.HasSuffix(p, "/ui_schema/layout/kind invalid")
		}) {
			t.Errorf("importing a carousel layout: %v", got)
		}
	})
}
