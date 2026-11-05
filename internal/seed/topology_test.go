package seed

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
)

var update = flag.Bool("update", false, "rewrite testdata/topology.json from blueprint.Topology")

// probes are blueprints written to show what a requester must not read in a
// topology (ADR-0032): guest settings, sizes, naming, a step's binding
// literals, and the string literals of the CEL it keeps.
var probes = []struct {
	name      string
	blueprint string
}{
	{"probe-vm-literals", `{"kind": "vm", "naming": "{project}-x{seq:02}",
	  "guest": {"os_family": "linux", "admin_password": "hunter2"},
	  "bindings": {"vcpu": "4", "network": "{'prod':'vlan-pci-secret'}[inputs.env]", "template": "\"rhel9-gold\"",
	               "groups": "inputs.env == 'prod' ? ['grp-pci'] : []", "post_config": "['linux-baseline', 'idm-join']"},
	  "components": [{"key": "web", "count": "inputs.tier == 'gold-secret' ? 3 : 1",
	    "disks": [{"size_gb": "inputs.env == 'prod' ? 500 : 50", "datastore": "\"vsan-secret\""}],
	    "nics": [{"key": "pci", "network": "\"dvpg-pci\""},
	             {"key": "b", "network": "project.code in ['fin', 'hr'] ? 'vl1' : 'vl2' // the PCI VLANs"}]}]}`},
	{"probe-composite-literals", `{"kind": "composite", "steps": [
	  {"key": "a", "item": "rhel9-server", "when": "inputs.token == 'tok_live_SECRET'",
	   "with": {"password": "'hunter2'", "host": "steps.x.fqdn + '.secret'"}},
	  {"parallel": [{"key": "b", "workflow": "x", "when": "inputs.token == 'open"},
	                {"key": "c", "awx": {"job_template": "t", "title": "T", "template_id": 4, "inventory": "inv"},
	                 "when": "r\"é\\d\" in inputs.tags", "undo": {"job_template": "u", "with": {"k": "'v'"}}}]}]}`},
}

type fixtureCase struct {
	Name      string `json:"name"`
	Blueprint any    `json:"blueprint"`
	Topology  any    `json:"topology"`
}

// TestTopologyFixture keeps testdata/topology.json current: the seeded VM
// and composite items' blueprints and the probes, each with what
// blueprint.Topology gives a requester of it. The mocks' topologyOf is
// checked against the same file (web/src/mocks/topology.test.ts), so the
// two projections can't drift. A change to the projection or the seed
// shows here: rewrite the file with -update and read what changed.
func TestTopologyFixture(t *testing.T) {
	var cases []fixtureCase
	add := func(name string, raw json.RawMessage) {
		var bp any
		if err := json.Unmarshal(raw, &bp); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cases = append(cases, fixtureCase{Name: name, Blueprint: bp, Topology: blueprint.Topology(raw)})
	}
	for _, it := range catalogItems() {
		if k := it.blueprint["kind"]; k != blueprint.KindVM && k != blueprint.KindComposite {
			continue
		}
		raw, err := json.Marshal(it.blueprint)
		if err != nil {
			t.Fatal(err)
		}
		add(it.slug, raw)
	}
	for _, p := range probes {
		add(p.name, json.RawMessage(p.blueprint))
	}
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cases); err != nil {
		t.Fatal(err)
	}
	const path = "testdata/topology.json"
	if *update {
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("%s isn't what blueprint.Topology gives: run go test ./internal/seed -run TestTopologyFixture -update and read the change", path)
	}
	// The probes give nothing of what they hide.
	for _, secret := range []string{"hunter2", "vlan-pci-secret", "grp-pci", "gold-secret", "vsan-secret", "'fin'",
		"PCI VLANs", "tok_live", ".secret", "{seq", "inv", "'v'", "é"} {
		for _, c := range cases[len(cases)-len(probes):] {
			if b, _ := json.Marshal(c.Topology); bytes.Contains(b, []byte(secret)) {
				t.Errorf("%s's topology has %q: %s", c.Name, secret, b)
			}
		}
	}
}
