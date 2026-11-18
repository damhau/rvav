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
)

// TestFormFieldTypes: each field type the designer offers reaches the AWX
// job as the value typed, and the server refuses a value its schema
// doesn't take, whatever the browser let through (F1.1).
func TestFormFieldTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		props := map[string]any{
			"window": map[string]any{"type": "string", "format": "date-time",
				"formatMinimum": "2026-12-01T00:00:00+01:00", "formatMaximum": "2026-12-31T23:59:59+01:00"},
			"cutover": map[string]any{"type": "string", "format": "date", "formatMinimum": "2026-12-01"},
			"ratio":   map[string]any{"type": "number", "minimum": 0.5, "maximum": 4, "multipleOf": 0.25},
		}
		fields := map[string]api.UiField{
			"window":  {Widget: api.UiWidgetDatetime, Label: "Maintenance window"},
			"cutover": {Widget: api.UiWidgetDate, Label: "Cut-over day"},
			"ratio":   {Widget: api.UiWidgetNumber, Label: "CPU ratio"},
		}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []any{"window"}, "properties": props},
			UiSchema:   api.UiSchema{Fields: fields},
			Blueprint:  map[string]any{"kind": "awx", "awx": map[string]any{"title": "Create role", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Change", Fields: slices.Sorted(maps.Keys(fields))}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "every-field", Name: "Every field type",
			Summary: "Each type the designer offers.", Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/every-field/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/every-field/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: "dev", Inputs: inputs}
		}
		valid := map[string]any{"window": "2026-12-10T14:30:00+01:00", "cutover": "2026-12-12", "ratio": 1.75}

		// Refused, each with what is wrong under its field.
		for _, c := range []struct {
			field string
			value any
			code  string
		}{
			{"window", "2026-12-10T14:30:00", "format"},
			{"window", "2026-11-30T22:00:00Z", "out_of_range"},
			{"cutover", "2026-11-30", "out_of_range"},
			{"cutover", "12/12/2026", "format"},
			{"ratio", 1.3, "multiple_of"},
			{"ratio", 4.25, "maximum"},
		} {
			inputs := maps.Clone(valid)
			inputs[c.field] = c.value
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/requests", requester, draft(inputs), 422, &problem, "Idempotency-Key", uuid.NewString())
			if !slices.ContainsFunc(problem.Errors, func(e api.FieldError) bool {
				return strings.HasPrefix(e.Field, "/inputs/"+c.field) && e.Code != nil && *e.Code == c.code
			}) {
				t.Errorf("%s = %v: %+v, want a %s problem", c.field, c.value, problem.Errors, c.code)
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
