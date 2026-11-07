package blueprint

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func topologyOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	got := Topology(json.RawMessage(raw))
	if got == nil {
		t.Fatalf("no topology for %s", raw)
	}
	// As the API sends it.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTopologyOfAVMItem(t *testing.T) {
	got := topologyOf(t, `{
	  "kind": "vm",
	  "naming": "{project}-{environment}-tt{component}{seq:02}",
	  "deployment_name": "{project}-{environment}-threetier",
	  "template": "rhel9-template",
	  "guest": {"os_family": "linux", "domain": "corp.example", "admin_password": "local-admin",
	            "domain_join": {"domain": "corp.example", "credential": "ad-join"},
	            "cloud_init": {"user_data": "#cloud-config\npassword: hunter2"}},
	  "sizes": {"S": {"label": "Small", "vcpu": 2, "memory_gb": 4, "disk_gb": 60}},
	  "bindings": {"vcpu": "4", "cluster": "inputs.cluster", "datastore": "inputs.datastore",
	               "network": "segments.app", "post_config": "[\"linux-baseline\"]"},
	  "awx_templates": {"linux-baseline": {"title": "Corporate baseline", "job_template": "baseline",
	                    "inventory": "corp", "credentials": ["vault-ssh"]}},
	  "tags": ["backup/daily"],
	  "segments": [{"key": "app", "title": "Application"}],
	  "components": [
	    {"key": "web", "title": "Web", "count": "inputs.web_nodes", "anti_affinity": true, "groups": ["web"],
	     "naming": "x", "tags": ["t"], "bindings": {"cluster": "inputs.web_cluster", "network": "segments.app"}},
	    {"key": "db", "count": 1, "groups": ["db", "nsx:grp-backup-clients"],
	     "guest": {"os_family": "windows", "admin_password": "db-admin"},
	     "disks": [{"size_gb": 200, "datastore": "\"vsan-db\"", "storage_policy": "gold"}],
	     "nics": [{"key": "backup", "network": "\"dvpg-backup\"", "ipam": "dhcp"}]}
	  ],
	  "security_groups": [
	    {"key": "web", "title": "Web", "rules": [{"from": ["any"], "services": ["HTTPS"]}]},
	    {"key": "db", "rules": [{"from": ["web"], "services": ["PostgreSQL"]}, {"from": ["any"], "action": "drop"}]}
	  ]
	}`)
	want := map[string]any{
		"kind":          "vm",
		"template":      "rhel9-template",
		"guest":         map[string]any{"os_family": "linux"},
		"bindings":      map[string]any{"network": "segments.app", "post_config": `["linux-baseline"]`},
		"awx_templates": map[string]any{"linux-baseline": map[string]any{"title": "Corporate baseline"}},
		"segments":      []any{map[string]any{"key": "app", "title": "Application"}},
		"components": []any{
			map[string]any{"key": "web", "title": "Web", "count": "inputs.web_nodes", "anti_affinity": true,
				"groups": []any{"web"}, "bindings": map[string]any{"network": "segments.app"}},
			map[string]any{"key": "db", "count": float64(1), "groups": []any{"db", "nsx:grp-backup-clients"},
				"guest": map[string]any{"os_family": "windows"},
				"disks": []any{map[string]any{"size_gb": float64(200), "storage_policy": "gold"}},
				"nics":  []any{map[string]any{"key": "backup", "network": `"dvpg-backup"`, "ipam": "dhcp"}}},
		},
		"security_groups": []any{
			map[string]any{"key": "web", "title": "Web", "rules": []any{
				map[string]any{"from": []any{"any"}, "services": []any{"HTTPS"}}}},
			map[string]any{"key": "db", "rules": []any{
				map[string]any{"from": []any{"web"}, "services": []any{"PostgreSQL"}},
				map[string]any{"from": []any{"any"}, "action": "drop"}}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("topology:\n%s", g)
	}
}

func TestTopologyOfAComposite(t *testing.T) {
	got := topologyOf(t, `{
	  "kind": "composite",
	  "rollback_on_failure": true,
	  "steps": [
	    {"key": "vm", "item": "rhel9-server", "with": {"role": "inputs.role", "admin": "\"s3cret\""}},
	    {"parallel": [
	      {"key": "db_user", "awx": {"job_template": "pg-create-role", "title": "Database user"},
	       "when": "inputs.database != ''",
	       "with": {"host": "steps.vm.fqdn", "password": "'p4ss' + steps.vm.name", "role": "inputs.role"},
	       "undo": {"job_template": "pg-drop-role", "with": {"role": "inputs.role", "host": "steps.vm.fqdn"}}},
	      {"key": "monitoring", "workflow": "register-monitoring", "with": {"fqdn": "steps.vm.fqdn"}}
	    ]}
	  ]
	}`)
	want := map[string]any{
		"kind":                "composite",
		"rollback_on_failure": true,
		"steps": []any{
			map[string]any{"key": "vm", "item": "rhel9-server", "with": map[string]any{}},
			map[string]any{"parallel": []any{
				map[string]any{"key": "db_user", "awx": map[string]any{"job_template": "pg-create-role", "title": "Database user"},
					"when": "inputs.database != ''",
					"with": map[string]any{"host": "steps.vm", "password": "steps.vm"},
					"undo": map[string]any{"job_template": "pg-drop-role", "with": map[string]any{"host": "steps.vm"}}},
				map[string]any{"key": "monitoring", "workflow": "register-monitoring", "with": map[string]any{"fqdn": "steps.vm"}},
			}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("topology:\n%s", g)
	}
	if b, _ := json.Marshal(got); strings.Contains(string(b), "p4ss") || strings.Contains(string(b), "s3cret") {
		t.Fatalf("a binding's literal is in the topology: %s", b)
	}
}

func TestTopologyOfOtherKinds(t *testing.T) {
	for _, raw := range []string{
		`{"kind": "awx", "job_template": "patch", "vars": {"token": "x"}}`,
		`{"kind": "workflow", "workflow": "decommission"}`,
		`not json`,
		`[]`,
	} {
		if got := Topology(json.RawMessage(raw)); got != nil {
			t.Errorf("%s: topology %v", raw, got)
		}
	}
}

// Lists keep their positions: an item that isn't an object stays as null,
// so a pointer into the blueprint means the same in its topology.
func TestTopologyKeepsPositions(t *testing.T) {
	got := topologyOf(t, `{"kind": "vm", "components": ["odd", {"key": "b", "naming": "x"}]}`)
	want := []any{nil, map[string]any{"key": "b"}}
	if !reflect.DeepEqual(got["components"], want) {
		t.Fatalf("components = %#v", got["components"])
	}
}

// The CEL a requester sees reads what it reads, not the values it compares
// or maps to (review of D1): its string literals are masked, but for a
// binding that is only a literal, the network or keys the canvas draws.
func TestTopologyMasksCELLiterals(t *testing.T) {
	vm := topologyOf(t, `{"kind": "vm",
	  "bindings": {"network": "{'prod':'vlan-pci-secret'}[inputs.env]", "template": "\"rhel9-gold\"",
	               "groups": "inputs.env == 'prod' ? ['grp-pci'] : []", "post_config": "['linux-baseline', 'idm-join']"},
	  "components": [{"key": "web", "count": "inputs.tier == 'gold-secret' ? 3 : 1",
	    "disks": [{"size_gb": "inputs.env == 'prod' ? 500 : 50"}],
	    "nics": [{"key": "pci", "network": "\"dvpg-pci\""}, {"key": "b", "network": "project.code in ['fin', 'hr'] ? 'vl1' : 'vl2'"}]}]}`)
	composite := topologyOf(t, `{"kind": "composite", "steps": [
	  {"key": "a", "item": "rhel9-server", "when": "inputs.token == 'tok_live_SECRET'"},
	  {"key": "b", "workflow": "x", "when": "inputs.token == 'tok_live_SECRET"}]}`)
	b, _ := json.Marshal(map[string]any{"vm": vm, "composite": composite})
	for _, secret := range []string{"tok_live", "vlan-pci-secret", "gold-secret", "'fin'", "grp-pci", "'prod'", "vl1"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("%s is in the topology: %s", secret, b)
		}
	}
	bindings := vm["bindings"].(map[string]any)
	web := vm["components"].([]any)[0].(map[string]any)
	steps := composite["steps"].([]any)
	for got, want := range map[any]string{
		bindings["network"]:     "{'…':'…'}[inputs.env]",
		bindings["template"]:    `"rhel9-gold"`,
		bindings["groups"]:      "inputs.env == '…' ? ['…'] : []",
		bindings["post_config"]: "['linux-baseline', 'idm-join']",
		web["count"]:            "inputs.tier == '…' ? 3 : 1",
		web["disks"].([]any)[0].(map[string]any)["size_gb"]: "inputs.env == '…' ? 500 : 50",
		web["nics"].([]any)[0].(map[string]any)["network"]:  `"dvpg-pci"`,
		web["nics"].([]any)[1].(map[string]any)["network"]:  "project.code in ['…', '…'] ? '…' : '…'",
		steps[0].(map[string]any)["when"]:                   "inputs.token == '…'",
		steps[1].(map[string]any)["when"]:                   "…", // not CEL: nothing of it
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// A REST call's URL is masked as CEL's literals are: its text goes, each
// expression stays with its own literals masked; one left open says nothing.
func TestTopologyMasksRESTCalls(t *testing.T) {
	raw := json.RawMessage(`{"kind": "composite", "steps": [
	  {"key": "a", "rest": {"method": "POST", "url": "https://h.example/ci/${steps.vm.name}/x?e=${inputs.e == 'p' ? 'q' : ''}#f",
	    "body": "{'k': inputs.v}", "secret": "s", "accept": [201], "keep": "$.a"},
	   "undo": {"rest": {"method": "DELETE", "url": "${steps.a.json.id}", "secret": "s"}}},
	  {"key": "b", "rest": {"method": "GET", "url": "https://h.example/${inputs.a"}}]}`)
	steps := Topology(raw)["steps"].([]any)
	got := steps[0].(map[string]any)
	rest := got["rest"].(map[string]any)
	if rest["url"] != "…${steps.vm.name}…${inputs.e == '…' ? '…' : ''}…" || rest["body"] != "{'…': inputs.v}" || rest["method"] != "POST" || len(rest) != 3 {
		t.Fatalf("rest = %v", rest)
	}
	if undo := got["undo"].(map[string]any)["rest"].(map[string]any); undo["url"] != "${steps.a.json.id}" || undo["method"] != "DELETE" || len(undo) != 2 {
		t.Fatalf("undo = %v", undo)
	}
	if rest := steps[1].(map[string]any)["rest"].(map[string]any); rest["url"] != "…" {
		t.Fatalf("a URL that doesn't parse = %v", rest)
	}
}
