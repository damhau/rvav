package core

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
)

// TestParseProblem: a blueprint that doesn't parse is refused at the value
// that doesn't fit, in words (#208).
func TestParseProblem(t *testing.T) {
	for _, c := range []struct{ raw, field, message string }{
		{`{"kind": "vm", "components": [{"key": "web"}, {"key": "db", "bindings": {"vcpu": [4]}}]}`,
			"/blueprint/components/1/bindings/vcpu", `This is a list, where the blueprint takes CEL in quotes ("inputs.network") or a number.`},
		{`{"kind": "vm", "bindings": ["vcpu"]}`,
			"/blueprint/bindings", `This is a list, where the blueprint takes bindings by name, as {memory_gb: "4"}.`},
		{`{"kind": "vm", "components": [{"key": "web"}, {"key": "db", "nics": [{"network": "a"}, {"network": 4}]}]}`,
			"/blueprint/components/1/nics/1/network", "This is a number, where the blueprint takes text."},
		{`{"kind": "vm", "template": ["t"]}`, "/blueprint/template", "This is a list, where the blueprint takes text."},
	} {
		var bp map[string]any
		if err := json.Unmarshal([]byte(c.raw), &bp); err != nil {
			t.Fatal(err)
		}
		errs := checkBlueprint("vm_blueprint", bp, nil, nil, "/blueprint", false)
		if len(errs) != 1 || errs[0].Field != c.field || *errs[0].Code != "invalid" || errs[0].Message != c.message {
			t.Errorf("%s: %+v", c.raw, errs)
		}
	}
}

// TestCheckInputReads: CEL in a VM blueprint reads only the form's inputs,
// its property groups' among them (#208).
func TestCheckInputReads(t *testing.T) {
	inputs := map[string]bool{"size": true, "network": true, "nodes": true, "backup": true}
	b, err := blueprint.Parse(json.RawMessage(`{"kind": "vm",
		"bindings": {"vcpu": "sizes[inputs.size].vcpu", "network": "inputs.zone", "memory_gb": 4},
		"disks": [{"size_gb": "inputs.disk_gb", "datastore": "inputs.datastore"}, {"size_gb": 20}],
		"nics": [{"network": "inputs.backup"}, {"network": "has(inputs.lan) ? inputs.lan : 'x'"}],
		"components": [{"key": "web", "count": "inputs.nodes"}, {"key": "db", "count": "inputs.replicas",
			"bindings": {"folder": "'db/' + inputs.team"}}],
		"resources": [{"key": "lb", "type": "lb", "with": {"port": 443, "pool": "inputs.network", "name": "inputs.lb"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range checkInputReads(b, inputs, "/blueprint") {
		got = append(got, e.Field+" "+*e.Code)
	}
	want := []string{
		"/blueprint/bindings/network unknown_input",
		"/blueprint/disks/0/size_gb unknown_input",
		"/blueprint/disks/0/datastore unknown_input",
		"/blueprint/nics/1/network unknown_input",
		"/blueprint/components/1/count unknown_input",
		"/blueprint/components/1/bindings/folder unknown_input",
		"/blueprint/resources/0/with/name unknown_input",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if e := checkInputReads(b, inputs, "/blueprint")[0]; e.Message != `This reads inputs.zone, and "zone" is not an input of the form's schema.` {
		t.Fatalf("message = %q", e.Message)
	}
}
