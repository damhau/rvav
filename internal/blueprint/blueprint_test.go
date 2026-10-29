package blueprint

import (
	"encoding/json"
	"reflect"
	"testing"
)

const app = `{
  "kind": "vm",
  "naming": "{project}-{environment}-{component}{seq:02}",
  "template": "rhel9-template",
  "guest": {"os_family": "linux", "domain": "corp.example", "time_zone": "Europe/Zurich"},
  "sizes": {"S": {"label": "Small", "vcpu": 2, "memory_gb": 4, "disk_gb": 60}},
  "bindings": {"vcpu": "sizes[inputs.size].vcpu", "memory_gb": "sizes[inputs.size].memory_gb",
               "disk_gb": "sizes[inputs.size].disk_gb", "datastore": "inputs.datastore", "network": "inputs.network"},
  "tags": ["backup/daily"],
  "components": [
    {"key": "web", "count": "inputs.web_nodes", "anti_affinity": true,
     "bindings": {"cluster": "inputs.web_cluster", "tags": "[\"app/\" + inputs.app]"},
     "disks": [{"size_gb": "inputs.data_gb"}, {"size_gb": 20, "datastore": "\"vsan-logs\"", "storage_policy": "Gold"}]},
    {"key": "db", "template": "win2022-template",
     "guest": {"os_family": "windows", "domain_join": {"domain": "corp.example", "credential": "ad-join"}},
     "bindings": {"cluster": "inputs.db_cluster", "vcpu": "8"},
     "nics": [{"network": "inputs.network"}, {"key": "backup", "network": "\"dvpg-backup-vl900\"", "ipam": "dhcp"}],
     "tags": []}
  ]
}`

func TestComponentsInherit(t *testing.T) {
	b, err := Parse(json.RawMessage(app))
	if err != nil {
		t.Fatal(err)
	}
	cs := b.ComponentList()
	if len(cs) != 2 || !b.Multi() {
		t.Fatalf("components = %d", len(cs))
	}
	web, db := cs[0], cs[1]
	if web.Naming != b.Naming || web.Template != "rhel9-template" || web.Guest.OSFamily != "linux" ||
		web.Bindings["datastore"] != "inputs.datastore" || web.Bindings["cluster"] != "inputs.web_cluster" ||
		!reflect.DeepEqual(web.Tags, []string{"backup/daily"}) {
		t.Fatalf("web = %+v", web)
	}
	// The db's own template, guest fields and bindings win; the rest is inherited.
	if db.Template != "win2022-template" || db.Guest.OSFamily != "windows" || db.Guest.Domain != "corp.example" ||
		db.Guest.TimeZone != "Europe/Zurich" || db.Guest.DomainJoin == nil || db.Bindings["vcpu"] != "8" ||
		db.Bindings["memory_gb"] == "" || len(db.Tags) != 0 {
		t.Fatalf("db = %+v, guest %+v", db, db.Guest)
	}
	// The blueprint's own guest is unchanged.
	if b.Guest.OSFamily != "linux" || b.Guest.DomainJoin != nil {
		t.Fatalf("blueprint guest changed: %+v", b.Guest)
	}

	inputs := map[string]any{"size": "S", "datastore": "vsan-01", "network": "dvpg-web", "web_nodes": 3.0,
		"web_cluster": "CL01", "db_cluster": "CL02", "app": "shop", "data_gb": 100.0}
	plan, count, missing := b.EvaluateComponent(web, inputs)
	if len(missing) > 0 || count != 3 {
		t.Fatalf("web count %d, missing %v", count, missing)
	}
	want := VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 60, Cluster: "CL01", Datastore: "vsan-01", Network: "dvpg-web",
		Template: "rhel9-template", Tags: []string{"backup/daily", "app/shop"},
		Disks: []DiskPlan{{SizeGB: 100}, {SizeGB: 20, Datastore: "vsan-logs", StoragePolicy: "Gold"}}}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("web plan =\n%+v, want\n%+v", plan, want)
	}
	if plan.StorageGB() != 180 {
		t.Fatalf("storage = %d", plan.StorageGB())
	}
	plan, count, missing = b.EvaluateComponent(db, inputs)
	if len(missing) > 0 || count != 1 || plan.VCPU != 8 || plan.Cluster != "CL02" ||
		!reflect.DeepEqual(plan.NICs, []NICPlan{{Key: "nic2", Network: "dvpg-web", IPAM: "efficientip"}, {Key: "backup", Network: "dvpg-backup-vl900", IPAM: "dhcp"}}) {
		t.Fatalf("db plan = %+v count %d missing %v", plan, count, missing)
	}

	// Inputs not given yet are missing, not errors.
	_, _, missing = b.EvaluateComponent(web, map[string]any{"size": "S"})
	if !reflect.DeepEqual(missing, []string{"count", "disks/0/size_gb", "cluster", "datastore", "network", "tags"}) {
		t.Fatalf("missing = %v", missing)
	}
}

func TestSingleVMBlueprint(t *testing.T) {
	b, err := Parse(json.RawMessage(`{"kind": "vm", "template": "tpl", "guest": {"os_family": "linux", "domain": "x"},
		"bindings": {"vcpu": "2", "cluster": "inputs.cluster"}, "disks": [{"size_gb": 10}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cs := b.ComponentList()
	if b.Multi() || len(cs) != 1 || cs[0].Key != SingleKey || cs[0].Template != "tpl" || len(cs[0].Disks) != 1 {
		t.Fatalf("components = %+v", cs)
	}
	plan, missing := b.EvaluateVM(map[string]any{"cluster": "CL01"})
	if len(missing) > 0 || plan.VCPU != 2 || plan.Cluster != "CL01" || len(plan.Disks) != 1 || plan.Disks[0].SizeGB != 10 {
		t.Fatalf("plan = %+v, missing %v", plan, missing)
	}
}
