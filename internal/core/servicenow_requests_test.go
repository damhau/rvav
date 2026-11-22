package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestServiceNowValues(t *testing.T) {
	schema := json.RawMessage(`{"properties": {"count": {"type": "integer"}, "ratio": {"type": "number"},
		"backup": {"type": "boolean"}, "name": {"type": "string"}, "tags": {"type": "array"}}}`)
	got := ServiceNowValues(schema, map[string]any{"count": " 4 ", "ratio": "0.5", "backup": "true", "name": " web ",
		"tags": []any{"a"}, "empty": "", "blank": "  ", "bad": "x", "unknown": "7", "ok": false})
	want := map[string]any{"count": int64(4), "ratio": 0.5, "backup": true, "name": " web ", "tags": []any{"a"},
		"bad": "x", "unknown": "7", "ok": false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ServiceNowValues = %#v\nwant %#v", got, want)
	}
	if got := ServiceNowValues(schema, map[string]any{"count": "four"}); got["count"] != "four" {
		t.Errorf("a value that isn't an integer = %#v, kept for the form to refuse", got["count"])
	}
}
