package core

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/api"
)

// fieldProblems validates one input against one property's schema and
// returns the problems' codes and messages, joined.
func fieldProblems(t *testing.T, prop map[string]any, value any) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"f": prop}})
	s, err := compileSchemaUncached(b)
	if err != nil {
		t.Fatalf("compile %v: %v", prop, err)
	}
	in, _ := json.Marshal(map[string]any{"f": value})
	errs, err := validateWith(s, in)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range errs {
		out = append(out, e.Field+" "+*e.Code+": "+e.Message)
	}
	return strings.Join(out, "; ")
}

func TestDateAndTimeInputs(t *testing.T) {
	when := map[string]any{"type": "string", "format": "date-time",
		"formatMinimum": "2026-12-01T00:00:00+01:00", "formatMaximum": "2026-12-31T23:59:59+01:00"}
	day := map[string]any{"type": "string", "format": "date", "formatMinimum": "2026-12-01"}
	for _, c := range []struct {
		prop  map[string]any
		value any
		want  string
	}{
		{when, "2026-12-10T14:30:00+01:00", ""},
		{when, "2026-12-10T13:30:00Z", ""},
		{when, "2026-12-10T14:30:00", "/inputs/f format: Enter a date and time with its time zone"},
		{when, "10.12.2026 14:30", "/inputs/f format:"},
		// The same instant as the bound, written in another zone.
		{when, "2026-11-30T23:00:00Z", ""},
		{when, "2026-11-30T22:59:59Z", "/inputs/f out_of_range: Pick a time on or after 2026-12-01T00:00:00+01:00."},
		{when, "2027-01-01T00:00:00+01:00", "/inputs/f out_of_range: Pick a time on or before 2026-12-31T23:59:59+01:00."},
		{day, "2026-12-01", ""},
		{day, "2026-11-30", "/inputs/f out_of_range: Pick a date on or after 2026-12-01."},
		{day, "2026-02-30", "/inputs/f format: Enter a date as YYYY-MM-DD"},
	} {
		if got := fieldProblems(t, c.prop, c.value); c.want == "" && got != "" || !strings.HasPrefix(got, c.want) {
			t.Errorf("%v against %v: %q, want %q", c.value, c.prop, got, c.want)
		}
	}
	for _, bad := range []map[string]any{
		{"type": "string", "format": "date-time", "formatMinimum": "tomorrow"},
		{"type": "string", "formatMinimum": "2026-12-01"},
		{"type": "string", "format": "date", "formatMaximum": 3},
	} {
		b, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"f": bad}})
		if _, err := compileSchemaUncached(b); err == nil {
			t.Errorf("%v compiled", bad)
		}
	}
}

func TestDecimalInputs(t *testing.T) {
	ratio := map[string]any{"type": "number", "minimum": 0.5, "maximum": 4, "multipleOf": 0.25}
	for value, want := range map[any]string{
		1.75: "",
		0.5:  "",
		0.25: "/inputs/f minimum: Enter 0.5 or more.",
		4.5:  "/inputs/f maximum: Enter 4 or less.",
		1.3:  "/inputs/f multiple_of: Enter a multiple of 0.25.",
		"2":  "/inputs/f type: Enter a number.",
	} {
		if got := fieldProblems(t, ratio, value); want == "" && got != "" || !strings.HasPrefix(got, want) {
			t.Errorf("%v: %q, want %q", value, got, want)
		}
	}
}

func TestListAndTableInputs(t *testing.T) {
	labels := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
		"propertyNames": map[string]any{"minLength": 1}}
	tags := map[string]any{"type": "array", "uniqueItems": true, "maxItems": 3,
		"items": map[string]any{"type": "string", "pattern": "^[^/]+/[^/]+$"}}
	disks := map[string]any{"type": "array", "minItems": 1, "maxItems": 2, "items": map[string]any{
		"type": "object", "required": []any{"size_gb"}, "additionalProperties": false,
		"properties": map[string]any{
			"size_gb": map[string]any{"type": "integer", "minimum": 10},
			"tier":    map[string]any{"type": "string", "enum": []any{"gold", "silver"}},
			"thin":    map[string]any{"type": "boolean"},
		}}}
	owners := map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []any{"lmartin", "tnguyen"}}}
	disk := map[string]any{"type": "object", "required": []any{"size_gb"}, "properties": map[string]any{
		"size_gb": map[string]any{"type": "integer", "minimum": 10}, "tier": map[string]any{"type": "string"}}}
	for _, c := range []struct {
		prop  map[string]any
		value any
		want  string
	}{
		{labels, map[string]any{"app": "billing", "owner": "ops"}, ""},
		{labels, map[string]any{"app": 3}, "/inputs/f/app type: Enter text."},
		{labels, map[string]any{"": "x"}, `/inputs/f key: Give every pair a key.`},
		{tags, []any{"backup/daily", "tier/gold"}, ""},
		{tags, []any{"backup/daily", "backup/daily"}, "/inputs/f unique:"},
		{tags, []any{"a/1", "b/2", "c/3", "d/4"}, "/inputs/f max_items: Keep it to 3 at most."},
		{tags, []any{"daily"}, "/inputs/f/0 pattern:"},
		{disks, []any{map[string]any{"size_gb": 50, "tier": "gold", "thin": true}}, ""},
		{disks, []any{}, "/inputs/f min_items: Add at least 1."},
		{disks, []any{map[string]any{"size_gb": 50}, map[string]any{"size_gb": 5}}, "/inputs/f/1/size_gb minimum: Enter 10 or more."},
		{disks, []any{map[string]any{"tier": "bronze"}}, "/inputs/f/0/size_gb required"},
		{disks, []any{map[string]any{"size_gb": 50, "extra": 1}}, "/inputs/f/0/extra unknown_field"},
		{owners, []any{"lmartin", "tnguyen"}, ""},
		{owners, []any{"lmartin", "lmartin"}, "/inputs/f unique:"},
		{owners, []any{"nobody"}, "/inputs/f/0 enum:"},
		{disk, map[string]any{"size_gb": 50, "tier": "gold"}, ""},
		{disk, map[string]any{"tier": "gold"}, "/inputs/f/size_gb required"},
		{disk, map[string]any{"size_gb": 5}, "/inputs/f/size_gb minimum: Enter 10 or more."},
		{disk, []any{}, "/inputs/f type: Enter keys and values."},
	} {
		if got := fieldProblems(t, c.prop, c.value); c.want == "" && got != "" || !strings.Contains(got, c.want) {
			t.Errorf("%v: %q, want %q", c.value, got, c.want)
		}
	}
}

func TestKeyProblemAmongOtherInputs(t *testing.T) {
	props := map[string]any{"labels": map[string]any{"type": "object", "propertyNames": map[string]any{"minLength": 1}}}
	in := map[string]any{"labels": map[string]any{"": "x"}}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		props[n] = map[string]any{"type": "string"}
		in[n] = "v"
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props})
	s, err := compileSchemaUncached(b)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		raw, _ := json.Marshal(in)
		errs, err := validateWith(s, raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(errs) != 1 || errs[0].Field != "/inputs/labels" {
			t.Fatalf("errors = %+v, want one under /inputs/labels", errs)
		}
	}
}

func TestFieldShapes(t *testing.T) {
	cols := []api.UiColumn{{Key: "size_gb", Label: "Size (GB)", Widget: api.UiColumnWidgetNumber},
		{Key: "tier", Label: "Tier", Widget: api.UiColumnWidgetSelect, Options: &[]api.Option{{Value: "gold", Label: "Gold"}}}}
	rows := func(props map[string]any) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": props}}
	}
	good := rows(map[string]any{"size_gb": map[string]any{"type": "integer"}, "tier": map[string]any{"type": "string", "enum": []any{"gold"}}})
	for _, c := range []struct {
		f    api.UiField
		prop map[string]any
		want []string
	}{
		{api.UiField{Widget: api.UiWidgetDatagrid, Columns: &cols}, good, nil},
		{api.UiField{Widget: api.UiWidgetDatagrid}, good, []string{"/ui_schema/fields/f/columns required"}},
		{api.UiField{Widget: api.UiWidgetDatagrid, Columns: &cols}, rows(map[string]any{"size_gb": map[string]any{"type": "string"},
			"tier": map[string]any{"type": "string"}, "extra": map[string]any{"type": "string"}}),
			[]string{"/ui_schema/fields/f/columns/0/widget widget_mismatch", "/ui_schema/fields/f/columns/1/options required",
				"/ui_schema/fields/f/columns/1/options/0 enum", "/form_schema/properties/f/items/properties/extra no_ui_field"}},
		{api.UiField{Widget: api.UiWidgetDatagrid, Columns: &cols}, map[string]any{"type": "string"},
			[]string{"/form_schema/properties/f widget_mismatch"}},
		{api.UiField{Widget: api.UiWidgetDatetime}, map[string]any{"type": "string"}, []string{"/form_schema/properties/f widget_mismatch"}},
		{api.UiField{Widget: api.UiWidgetTags}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, nil},
		{api.UiField{Widget: api.UiWidgetKeyValue}, map[string]any{"type": "array"}, []string{"/form_schema/properties/f widget_mismatch"}},
		{api.UiField{Widget: api.UiWidgetText, Columns: &cols}, map[string]any{"type": "string"}, []string{"/ui_schema/fields/f/columns invalid"}},
		{api.UiField{Widget: api.UiWidgetDeployment}, map[string]any{"type": "string"}, nil},
		{api.UiField{Widget: api.UiWidgetUser, ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("rvav.users")}},
			map[string]any{"type": "string"}, nil},
		{api.UiField{Widget: api.UiWidgetVsphereVm, ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("vsphere.templates")}},
			map[string]any{"type": "string"}, []string{"/ui_schema/fields/f/value_source widget_mismatch"}},
		{api.UiField{Widget: api.UiWidgetProject, ValueSource: &api.ValueSource{Kind: api.Cel}},
			map[string]any{"type": "string"}, []string{"/ui_schema/fields/f/value_source widget_mismatch"}},
		{api.UiField{Widget: api.UiWidgetDualList}, map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string"}}, nil},
		{api.UiField{Widget: api.UiWidgetDualList}, map[string]any{"type": "string"}, []string{"/form_schema/properties/f widget_mismatch"}},
		{api.UiField{Widget: api.UiWidgetObject, Columns: &cols}, map[string]any{"type": "object", "properties": map[string]any{
			"size_gb": map[string]any{"type": "integer"}, "tier": map[string]any{"type": "string", "enum": []any{"gold"}}}}, nil},
		{api.UiField{Widget: api.UiWidgetObject}, map[string]any{"type": "object"}, []string{"/ui_schema/fields/f/columns required"}},
		{api.UiField{Widget: api.UiWidgetObject, Columns: &cols}, map[string]any{"type": "object", "properties": map[string]any{
			"tier": map[string]any{"type": "string", "enum": []any{"gold"}}, "port": map[string]any{"type": "integer"}}},
			[]string{"/ui_schema/fields/f/columns/0/key unknown_input", "/form_schema/properties/f/properties/port no_ui_field"}},
		{api.UiField{Widget: api.UiWidgetObject, Columns: &cols}, good, []string{"/form_schema/properties/f widget_mismatch"}},
	} {
		var got []string
		for _, e := range checkFieldShape("f", c.f, c.prop, "") {
			got = append(got, e.Field+" "+*e.Code)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s over %v: %v, want %v", c.f.Widget, c.prop, got, c.want)
		}
	}
}

func TestShownFields(t *testing.T) {
	for _, c := range []struct {
		f       api.UiField
		input   bool
		publish bool
		want    []string
	}{
		{api.UiField{Widget: api.UiWidgetLink, Url: new("https://wiki.corp.example/sizes")}, false, true, nil},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("/requests")}, false, true, nil},
		{api.UiField{Widget: api.UiWidgetImage, Url: new("http://cdn.corp.example/a.png"), Alt: new("The network")}, false, true, nil},
		{api.UiField{Widget: api.UiWidgetLink}, false, false, nil},
		{api.UiField{Widget: api.UiWidgetLink}, false, true, []string{"/ui_schema/fields/f/url required"}},
		{api.UiField{Widget: api.UiWidgetImage, Url: new("https://cdn.corp.example/a.png")}, false, true, []string{"/ui_schema/fields/f/alt required"}},
		{api.UiField{Widget: api.UiWidgetImage, Url: new("https://cdn.corp.example/a.png")}, false, false, nil},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("javascript:alert(1)")}, false, false, []string{"/ui_schema/fields/f/url invalid"}},
		{api.UiField{Widget: api.UiWidgetLink, Url: new(" JavaScript:alert(1)")}, false, false, []string{"/ui_schema/fields/f/url invalid"}},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("java\tscript:alert(1)")}, false, false, []string{"/ui_schema/fields/f/url invalid"}},
		{api.UiField{Widget: api.UiWidgetImage, Url: new("data:image/svg+xml,<svg/>"), Alt: new("x")}, false, false, []string{"/ui_schema/fields/f/url invalid"}},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("//evil.example/")}, false, false, []string{"/ui_schema/fields/f/url invalid"}},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("/\\evil.example/")}, false, false, []string{"/ui_schema/fields/f/url invalid"}},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("ftp://files.corp.example/")}, false, false, []string{"/ui_schema/fields/f/url invalid"}},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("https:///no-host")}, false, false, []string{"/ui_schema/fields/f/url invalid"}},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("wiki.corp.example")}, false, false, []string{"/ui_schema/fields/f/url invalid"}},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("https://wiki.corp.example")}, true, false, []string{"/form_schema/properties/f widget_mismatch"}},
		{api.UiField{Widget: api.UiWidgetText, Url: new("https://x.example"), Alt: new("x")}, true, false,
			[]string{"/ui_schema/fields/f/url invalid", "/ui_schema/fields/f/alt invalid"}},
		{api.UiField{Widget: api.UiWidgetLink, Url: new("https://x.example"), Alt: new("x")}, false, false, []string{"/ui_schema/fields/f/alt invalid"}},
	} {
		var got []string
		for _, e := range checkShown("f", c.f, c.input, "", c.publish) {
			got = append(got, e.Field+" "+*e.Code)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s %q (publish %v): %v, want %v", c.f.Widget, deref(c.f.Url), c.publish, got, c.want)
		}
	}
}

func TestLocateNested(t *testing.T) {
	cols := []api.UiColumn{{Key: "size_gb", Label: "Size (GB)", Widget: api.UiColumnWidgetNumber}}
	ui := api.UiSchema{Fields: map[string]api.UiField{"disks": {Widget: api.UiWidgetDatagrid, Columns: &cols},
		"labels": {Widget: api.UiWidgetKeyValue}, "tags": {Widget: api.UiWidgetTags}, "disk": {Widget: api.UiWidgetObject, Columns: &cols},
		"owners": {Widget: api.UiWidgetDualList}}}
	errs := locateNested(ui, []api.FieldError{
		fieldErr("/inputs/disk/size_gb", "required", "This field is required."),
		fieldErr("/inputs/owners/1", "enum", "Choose one of the listed values."),
		fieldErr("/inputs/disks/1/size_gb", "minimum", "Enter 10 or more."),
		fieldErr("/inputs/disks/0", "invalid", "Wrong."),
		fieldErr("/inputs/labels/team~1app", "type", "Enter text."),
		fieldErr("/inputs/tags/2", "pattern", "Not category/tag."),
		fieldErr("/inputs/tags", "unique", "Twice."),
	})
	var got []string
	for _, e := range errs {
		got = append(got, e.Message)
	}
	want := []string{"Size (GB): This field is required.", "Item 2: Choose one of the listed values.", "Row 2, Size (GB): Enter 10 or more.", "Row 1: Wrong.", "team/app: Enter text.", "Item 3: Not category/tag.", "Twice."}
	if !slices.Equal(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
}

func TestSourceOf(t *testing.T) {
	if got := sourceOf(api.UiField{Widget: api.UiWidgetDeployment}); got == nil || got.Kind != api.Builtin || deref(got.Name) != "rvav.deployments" {
		t.Errorf("a deployment field without a source resolves %+v", got)
	}
	params := map[string]any{"item": "rhel9-server"}
	own := &api.ValueSource{Kind: api.Builtin, Name: new("rvav.deployments"), Params: &params}
	if got := sourceOf(api.UiField{Widget: api.UiWidgetDeployment, ValueSource: own}); got != own {
		t.Errorf("a deployment field naming its source resolves %+v", got)
	}
	if got := sourceOf(api.UiField{Widget: api.UiWidgetText}); got != nil {
		t.Errorf("a text field resolves %+v", got)
	}
}
