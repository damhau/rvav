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
	{"probe-composite-rest", `{"kind": "composite", "steps": [
	  {"key": "cmdb", "title": "CMDB", "rest": {"method": "POST", "url": "https://cmdb-secret.corp.example/api/ci/${steps.vm.name}?token=tok_live_q&env=${environment == 'prod' ? 'p-secret' : ''}",
	   "body": "{'owner': inputs.owner, 'api_key': 'tok_live_b'}", "secret": "cmdb-token-name", "accept": [201, 418], "keep": "$.result_secret"},
	   "undo": {"rest": {"method": "DELETE", "url": "https://cmdb-secret.corp.example/api/ci/${steps.cmdb.json.id}", "secret": "cmdb-token-name"}}},
	  {"parallel": [{"key": "bad", "rest": {"method": "GET", "url": "https://cmdb-secret.corp.example/${'tok_live_open"}},
	                {"key": "awx", "awx": {"job_template": "t"}, "undo": {"rest": {"method": "POST", "url": "https://cmdb-secret.corp.example/undo", "body": "'tok_live_c'"}}}]}]}`},
	{"probe-composite-ask", `{"kind": "composite", "steps": [
	  {"key": "manager", "title": "Manager's answer", "when": "inputs.size == 'huge-secret'", "ask": {"group": "grp-ask-secret",
	   "approvers": ["usr_ask_secret"], "message": "Why so big, secret-text?", "timeout": "48h", "on_timeout": "escalate",
	   "escalate_to": {"group": "grp-esc-secret", "approvers": ["usr_esc_secret"]},
	   "form": [{"key": "size", "label": "Label-secret", "type": "choice", "required": true, "options": ["opt-secret"], "default": "opt-secret"}]}},
	  {"parallel": [{"key": "owner_ok", "ask": {"owner": true}},
	                {"key": "mine", "ask": {"requester": true, "timeout": "1h", "on_timeout": "continue"}}]}]}`},
	{"probe-composite-wait", `{"kind": "composite", "steps": [
	  {"key": "keep", "title": "Keep it", "wait": {"for": "72h"}},
	  {"key": "window", "when": "inputs.size == 'huge-secret'", "wait": {"until": "timestamp(inputs.window + 'T22:00:00Z-secret')"}},
	  {"key": "change", "title": "The change", "poll": {"rest": {"method": "GET", "url": "https://snow-secret.corp.example/api/${steps.keep.until}?token=tok_live_q&env=${environment == 'prod' ? 'p-secret' : ''}",
	   "secret": "snow-token-name", "accept": [200, 418], "keep": "$.result_secret"},
	   "until": "steps.change.json == 'approved-secret'", "every": "10m", "timeout": "72h", "on_timeout": "continue"}},
	  {"parallel": [{"key": "soon", "wait": {"for": "1h"}},
	                {"key": "put", "poll": {"rest": {"method": "PUT", "url": "https://snow-secret.corp.example/x", "body": "{'token': 'tok_live_b'}"}, "until": "steps.put.status == 200", "every": "1m", "timeout": "1h"}}]}]}`},
	{"probe-composite-branch", `{"kind": "composite", "steps": [
	  {"key": "choose", "title": "Which way", "when": "inputs.size == 'huge-secret'", "branch": {"unknown": "dropped-secret", "cases": [
	    {"title": "Big", "when": "inputs.size == 'big-secret'", "steps": [
	      {"key": "ask_big", "title": "Ask", "ask": {"group": "grp-ask-secret", "message": "Why, secret-text?", "owner": true}},
	      {"key": "call_big", "rest": {"method": "POST", "url": "https://cmdb-secret.corp.example/${inputs.name}", "body": "'tok_live_b'", "secret": "cmdb-token-name"},
	       "when": "steps.ask_big.answered == 'lit-secret'"}]},
	    {"when": "inputs.size == 'open"}],
	   "else": [{"key": "call_else", "awx": {"job_template": "t"}, "with": {"password": "'hunter2'", "host": "steps.choose.case"}}]}},
	  {"key": "tell", "title": "Tell them", "when": "inputs.size == 'huge-secret'", "notify": {"requester": true, "owner": false, "groups": ["grp-told-secret"],
	   "users": ["usr_told_secret"], "message": "Secret-text for ${inputs.name + 'lit-secret'}"}},
	  {"key": "tell_owner", "notify": {"owner": true, "message": "Secret-text"}}]}`},
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
		"PCI VLANs", "tok_live", ".secret", "{seq", "inv", "'v'", "é", "cmdb-secret", "p-secret", "cmdb-token-name", "418", "result_secret", "api_key", "/api/",
		"huge-secret", "grp-ask-secret", "usr_ask_secret", "secret-text", "grp-esc-secret", "usr_esc_secret", "Label-secret", "opt-secret",
		"big-secret", "lit-secret", "dropped-secret", "grp-told-secret", "usr_told_secret", "Secret-text"} {
		for _, c := range cases[len(cases)-len(probes):] {
			if b, _ := json.Marshal(c.Topology); bytes.Contains(b, []byte(secret)) {
				t.Errorf("%s's topology has %q: %s", c.Name, secret, b)
			}
		}
	}
}
