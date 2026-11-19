package core

import (
	"reflect"
	"testing"

	"github.com/damhau/rvav/internal/api"
)

func TestStartValues(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{
		"size":  map[string]any{"type": "string", "default": "S"},
		"port":  map[string]any{"type": "integer", "default": 9100},
		"notes": map[string]any{"type": "string"},
	}}
	got := StartValues(schema, map[string]any{"size": "M", "role": "app"})
	if want := map[string]any{"size": "M", "port": 9100, "role": "app"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("StartValues = %v, want %v", got, want)
	}
	if got := StartValues(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("StartValues of nothing = %#v", got)
	}
}

func TestWithStart(t *testing.T) {
	ui := api.UiSchema{Fields: map[string]api.UiField{
		"size":  {Widget: api.UiWidgetSelect},
		"agent": {Widget: api.UiWidgetText, Rules: &api.UiRules{Default: new(`"node_exporter"`)}},
	}}
	tags := []any{"web"}
	start := map[string]any{"size": "M", "role": "app", "cpu": 2, "tags": tags, "agent": "telegraf", "port": 9100}
	inputs := map[string]any{"role": "db", "size": nil, "agent": nil, "port": 80}
	withStart(ui, start, inputs)
	want := map[string]any{"role": "db", "agent": nil, "port": 80, "cpu": float64(2), "tags": []any{"web"}}
	if !reflect.DeepEqual(inputs, want) {
		t.Fatalf("withStart = %#v, want %#v", inputs, want)
	}
	inputs["tags"].([]any)[0] = "db"
	if tags[0] != "web" {
		t.Fatal("an input filled in shares its list with the form")
	}
}

func TestStartForm(t *testing.T) {
	f := &api.ActionForm{Schema: map[string]any{"properties": map[string]any{"port": map[string]any{"default": 9100}}},
		InitialValues: map[string]any{"agent": "telegraf"}}
	got := startForm(f)
	if !reflect.DeepEqual(got.InitialValues, map[string]any{"agent": "telegraf", "port": 9100}) || len(f.InitialValues) != 1 {
		t.Fatalf("startForm = %v, the form's own = %v", got.InitialValues, f.InitialValues)
	}
	if startForm(nil) != nil {
		t.Fatal("no form, no start form")
	}
}
