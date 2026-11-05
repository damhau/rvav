package core

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/api"
)

// D1.3: a step's inputs, as the composite designer offers them to bind.
func TestFormInputs(t *testing.T) {
	schema := json.RawMessage(`{"type": "object", "required": ["role", "privileges"], "properties": {
		"vm_name": {"type": "string", "readOnly": true},
		"role": {"type": "string", "title": "Role in the schema"},
		"size": {"type": ["null", "string"], "enum": ["S", "M"]},
		"privileges": {"type": "string", "enum": ["read", "owner"], "description": "What it may do"},
		"extra": {"type": "integer"},
		"aliases": {"type": "array", "items": {"type": "string", "enum": ["a", "b"]}}}}`)
	ui := json.RawMessage(`{"layout": {"sections": [{"fields": ["size", "vm_name"]}, {"fields": ["role"]}]},
		"fields": {"role": {"label": "Role", "help": "Lower case"}, "size": {"label": "Size"}}}`)
	got := formInputs(schema, ui)
	names := make([]string, len(got))
	for i, in := range got {
		names[i] = in.Name
	}
	// The sections' order, then the rest: required first, then by name; no read-only field.
	if want := []string{"size", "role", "privileges", "aliases", "extra"}; !slices.Equal(names, want) {
		t.Fatalf("inputs = %v, want %v", names, want)
	}
	byName := map[string]api.CompositeStepInput{}
	for _, in := range got {
		byName[in.Name] = in
	}
	if r := byName["role"]; r.Title != "Role" || !r.Required || r.Type != "string" || deref(r.Description) != "Lower case" {
		t.Errorf("role = %+v", r)
	}
	if s := byName["size"]; s.Type != "string" || s.Required || s.Enum == nil || len(*s.Enum) != 2 {
		t.Errorf("size = %+v", s)
	}
	if p := byName["privileges"]; p.Title != "privileges" || deref(p.Description) != "What it may do" {
		t.Errorf("privileges = %+v", p)
	}
	if a := byName["aliases"]; a.Type != "array" || a.Enum == nil || len(*a.Enum) != 2 {
		t.Errorf("aliases = %+v", a)
	}

	// A workflow's input schema has no UI schema: titles from the schema.
	wf := formInputs(json.RawMessage(`{"properties": {"team": {"type": "string"}, "fqdn": {"type": "string", "title": "FQDN"}},
		"required": ["fqdn"]}`), nil)
	if len(wf) != 2 || wf[0].Name != "fqdn" || wf[0].Title != "FQDN" || !wf[0].Required || wf[1].Title != "team" {
		t.Fatalf("a workflow's inputs = %+v", wf)
	}
	if out := formInputs(json.RawMessage(`not json`), nil); out == nil || len(out) != 0 {
		t.Fatalf("a schema that doesn't parse = %+v", out)
	}
}
