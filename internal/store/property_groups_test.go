package store

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWithGroups(t *testing.T) {
	v := ItemVersion{
		FormSchema:    json.RawMessage(`{"type":"object","required":["role"],"properties":{"role":{"type":"string"},"backup":{"type":"boolean"}}}`),
		UISchema:      json.RawMessage(`{"fields":{"role":{"label":"Role","widget":"text"}},"layout":{"kind":"wizard","sections":[{"key":"main","title":"VM","fields":["role"]}]}}`),
		InitialValues: json.RawMessage(`{"role":"web"}`),
	}
	ops := PropertyGroup{Key: "ops", Name: "Operations", Constants: json.RawMessage(`{"domain":"corp.example","tier":2}`),
		Inputs: json.RawMessage(`[{"name":"costCenter","label":"Cost centre","type":"string","required":true,"options":["CC-1","CC-2"],"default":"CC-1"},
			{"name":"backup","label":"Back up","type":"boolean"},
			{"name":"token","label":"Token","type":"string","secret":true},
			{"name":"disks","label":"Disks","type":"integer","default":1}]`)}
	empty := PropertyGroup{Key: "tags", Name: "Tags", Constants: json.RawMessage(`{}`), Inputs: json.RawMessage(`[]`)}
	got := WithGroups(v, []PropertyGroup{ops, empty})

	var form, ui, initial map[string]any
	_ = json.Unmarshal(got.FormSchema, &form)
	_ = json.Unmarshal(got.UISchema, &ui)
	_ = json.Unmarshal(got.InitialValues, &initial)
	props := form["properties"].(map[string]any)
	if !reflect.DeepEqual(props["costCenter"], map[string]any{"type": "string", "enum": []any{"CC-1", "CC-2"}, "minLength": 1.0}) ||
		!reflect.DeepEqual(props["backup"], map[string]any{"type": "boolean"}) {
		t.Fatalf("properties = %v", props)
	}
	if !reflect.DeepEqual(form["required"], []any{"role", "costCenter"}) {
		t.Fatalf("required = %v", form["required"])
	}
	fields := ui["fields"].(map[string]any)
	if fields["costCenter"].(map[string]any)["widget"] != "select" || fields["token"].(map[string]any)["secret"] != true ||
		fields["disks"].(map[string]any)["widget"] != "number" || fields["backup"] != nil {
		t.Fatalf("fields = %v", fields)
	}
	layout := ui["layout"].(map[string]any)
	sections := layout["sections"].([]any)
	if layout["kind"] != "wizard" || len(sections) != 2 ||
		!reflect.DeepEqual(sections[1], map[string]any{"key": "propgroup-ops", "title": "Operations", "fields": []any{"costCenter", "token", "disks"}}) {
		t.Fatalf("layout = %v", layout)
	}
	if !reflect.DeepEqual(initial, map[string]any{"role": "web", "costCenter": "CC-1", "disks": 1.0}) {
		t.Fatalf("initial values = %v", initial)
	}
	want := map[string]any{"ops": map[string]any{"domain": "corp.example", "tier": 2.0}, "tags": map[string]any{}}
	if !reflect.DeepEqual(got.GroupConstants, want) {
		t.Fatalf("constants = %v", got.GroupConstants)
	}
}

func TestGroupRefs(t *testing.T) {
	refs := GroupRefs(json.RawMessage(`{"kind":"vm","property_groups":[{"key":"ops","version":2}]}`))
	if !reflect.DeepEqual(refs, []GroupRef{{Key: "ops", Version: 2}}) {
		t.Fatalf("refs = %v", refs)
	}
	if refs := GroupRefs(json.RawMessage(`{"property_groups":"ops"}`)); refs != nil {
		t.Fatalf("refs of a string = %v", refs)
	}
}
