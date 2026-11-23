package blueprint

import (
	"encoding/json"
	"errors"
	"maps"
	"testing"
)

// TestBindingsConstants: a number or a boolean where a binding's CEL is
// expected is the constant it means; a list or an object is refused (#208).
func TestBindingsConstants(t *testing.T) {
	b, err := Parse(json.RawMessage(`{"kind": "vm", "bindings": {"vcpu": "2", "memory_gb": 4, "disk_gb": 1.5,
		"linked": true, "network": null}, "components": [{"key": "web", "bindings": {"memory_gb": 8}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Bindings{"vcpu": "2", "memory_gb": "4", "disk_gb": "1.5", "linked": "true"}); !maps.Equal(b.Bindings, want) {
		t.Fatalf("bindings = %v, want %v", b.Bindings, want)
	}
	if got := b.Components[0].Bindings["memory_gb"]; got != "8" {
		t.Fatalf("the component's memory_gb = %q", got)
	}

	var te *json.UnmarshalTypeError
	_, err = Parse(json.RawMessage(`{"kind": "vm", "components": [{"key": "web"}, {"key": "db", "bindings": {"vcpu": [4]}}]}`))
	if !errors.As(err, &te) || te.Value != "array" || te.Field != "bindings.vcpu" {
		t.Fatalf("a list = %v (%+v)", err, te)
	}
}
