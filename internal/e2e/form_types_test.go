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
// doesn't take, whatever the browser let through (F1.1, F1.2).
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
			"tags": map[string]any{"type": "array", "uniqueItems": true, "maxItems": 3,
				"items": map[string]any{"type": "string", "pattern": "^[^/]+/[^/]+$"}},
			"labels": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
				"propertyNames": map[string]any{"minLength": 1}},
			"disks": map[string]any{"type": "array", "minItems": 1, "maxItems": 3, "items": map[string]any{
				"type": "object", "required": []any{"size_gb"}, "additionalProperties": false, "properties": map[string]any{
					"size_gb": map[string]any{"type": "integer", "minimum": 10},
					"tier":    map[string]any{"type": "string", "enum": []any{"gold", "silver"}},
					"thin":    map[string]any{"type": "boolean"},
					"mount":   map[string]any{"type": "string"},
				}}},
			"owners": map[string]any{"type": "array", "uniqueItems": true, "maxItems": 3,
				"items": map[string]any{"type": "string", "enum": []any{"lmartin", "tnguyen", "jroux"}}},
			"backup": map[string]any{"type": "object", "required": []any{"policy"}, "additionalProperties": false, "properties": map[string]any{
				"policy":    map[string]any{"type": "string", "enum": []any{"daily", "weekly"}},
				"keep_days": map[string]any{"type": "integer", "minimum": 1},
				"offsite":   map[string]any{"type": "boolean"},
			}},
		}
		fields := map[string]api.UiField{
			"window":  {Widget: api.UiWidgetDatetime, Label: "Maintenance window"},
			"cutover": {Widget: api.UiWidgetDate, Label: "Cut-over day"},
			"ratio":   {Widget: api.UiWidgetNumber, Label: "CPU ratio"},
			"tags":    {Widget: api.UiWidgetTags, Label: "vSphere tags"},
			"labels":  {Widget: api.UiWidgetKeyValue, Label: "Labels"},
			"disks": {Widget: api.UiWidgetDatagrid, Label: "Extra disks", Columns: &[]api.UiColumn{
				{Key: "size_gb", Label: "Size (GB)", Widget: api.UiColumnWidgetNumber},
				{Key: "tier", Label: "Tier", Widget: api.UiColumnWidgetSelect, Options: &[]api.Option{{Value: "gold", Label: "Gold"}, {Value: "silver", Label: "Silver"}}},
				{Key: "thin", Label: "Thin", Widget: api.UiColumnWidgetToggle},
				{Key: "mount", Label: "Mount point", Widget: api.UiColumnWidgetText},
			}},
			"owners": {Widget: api.UiWidgetDualList, Label: "Owners", Options: &[]api.Option{
				{Value: "lmartin", Label: "Léa Martin"}, {Value: "tnguyen", Label: "Thanh Nguyen"}, {Value: "jroux", Label: "Jean Roux"}}},
			"backup": {Widget: api.UiWidgetObject, Label: "Backup", Columns: &[]api.UiColumn{
				{Key: "policy", Label: "Policy", Widget: api.UiColumnWidgetSelect, Options: &[]api.Option{{Value: "daily", Label: "Daily"}, {Value: "weekly", Label: "Weekly"}}},
				{Key: "keep_days", Label: "Keep (days)", Widget: api.UiColumnWidgetNumber},
				{Key: "offsite", Label: "Copy off site", Widget: api.UiColumnWidgetToggle},
			}},
		}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []any{"window"}, "properties": props},
			UiSchema:   api.UiSchema{Fields: fields},
			Blueprint:  map[string]any{"kind": "awx", "awx": map[string]any{"title": "Create role", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Change", Fields: slices.Sorted(maps.Keys(fields))}}
		// A table needs columns that edit its rows' properties, of their types.
		bad := def
		bad.UiSchema.Fields = maps.Clone(fields)
		grid := fields["disks"]
		grid.Columns = &[]api.UiColumn{{Key: "size_gb", Label: "Size", Widget: api.UiColumnWidgetToggle}}
		bad.UiSchema.Fields["disks"] = grid
		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-grid", Name: "Bad grid",
			Summary: "A table whose columns don't fit.", Category: "compute", Type: api.AwxJobTemplate, Definition: &bad}, 422, &refused)
		var got []string
		for _, e := range refused.Errors {
			got = append(got, e.Field)
		}
		if want := []string{"/definition/ui_schema/fields/disks/columns/0/widget", "/definition/form_schema/properties/disks/items/properties/mount",
			"/definition/form_schema/properties/disks/items/properties/thin", "/definition/form_schema/properties/disks/items/properties/tier"}; !slices.Equal(got, want) {
			t.Errorf("a table with the wrong columns: %v, want %v", got, want)
		}
		// So does an object, with its own properties.
		bad.UiSchema.Fields = maps.Clone(fields)
		group := fields["backup"]
		group.Columns = &[]api.UiColumn{{Key: "policy", Label: "Policy", Widget: api.UiColumnWidgetText}, {Key: "retention", Label: "Retention", Widget: api.UiColumnWidgetText}}
		bad.UiSchema.Fields["backup"] = group
		refused.Errors = nil
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-group", Name: "Bad group",
			Summary: "An object whose sub-fields don't fit.", Category: "compute", Type: api.AwxJobTemplate, Definition: &bad}, 422, &refused)
		got = nil
		for _, e := range refused.Errors {
			got = append(got, e.Field)
		}
		if want := []string{"/definition/ui_schema/fields/backup/columns/1/key", "/definition/form_schema/properties/backup/properties/keep_days",
			"/definition/form_schema/properties/backup/properties/offsite"}; !slices.Equal(got, want) {
			t.Errorf("an object with the wrong sub-fields: %v, want %v", got, want)
		}

		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "every-field", Name: "Every field type",
			Summary: "Each type the designer offers.", Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/every-field/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/every-field/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: "dev", Inputs: inputs}
		}
		valid := map[string]any{"window": "2026-12-10T14:30:00+01:00", "cutover": "2026-12-12", "ratio": 1.75,
			"tags": []any{"backup/daily", "tier/gold"}, "labels": map[string]any{"app": "billing", "cost-centre": "4410"},
			"disks": []any{map[string]any{"size_gb": float64(50), "tier": "gold", "thin": true, "mount": "/data"},
				map[string]any{"size_gb": float64(20)}},
			"owners": []any{"tnguyen", "lmartin"}, "backup": map[string]any{"policy": "weekly", "keep_days": float64(14), "offsite": true}}

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
			{"tags", []any{"backup/daily", "backup/daily"}, "unique"},
			{"tags", []any{"daily"}, "pattern"},
			{"labels", map[string]any{"app": 3}, "type"},
			{"labels", map[string]any{"": "x"}, "key"},
			{"disks", []any{}, "min_items"},
			{"disks", []any{map[string]any{"size_gb": 50}, map[string]any{"size_gb": 5}}, "minimum"},
			{"disks", []any{map[string]any{"size_gb": 50, "tier": "bronze"}}, "enum"},
			{"disks", []any{map[string]any{"tier": "gold"}}, "required"},
			{"owners", []any{"lmartin", "lmartin"}, "unique"},
			{"owners", []any{"lmartin", "nobody"}, "enum"},
			{"backup", map[string]any{"keep_days": 3}, "required"},
			{"backup", map[string]any{"policy": "hourly"}, "enum"},
			{"backup", map[string]any{"policy": "daily", "extra": 1}, "unknown_field"},
			{"backup", []any{"daily"}, "type"},
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
		// A row's problem says which row and column.
		inputs := maps.Clone(valid)
		inputs["disks"] = []any{map[string]any{"size_gb": 50}, map[string]any{"size_gb": 5}}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/requests/preview", requester, draft(inputs), 200, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Message != "Row 2, Size (GB): Enter 10 or more." {
			t.Errorf("the preview of a row too small: %+v", problem.Errors)
		}
		// A sub-field's, which sub-field.
		inputs = maps.Clone(valid)
		inputs["backup"] = map[string]any{"policy": "daily", "keep_days": 0}
		problem.Errors = nil
		h.json("POST", "/api/v1/requests/preview", requester, draft(inputs), 200, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Message != "Keep (days): Enter 1 or more." {
			t.Errorf("the preview of a sub-field too small: %+v", problem.Errors)
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
