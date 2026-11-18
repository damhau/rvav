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

// A component's provisioning is the blueprint's unless it has its own, and
// an added disk's is its own, else its component's (ADR-0036).
func TestDiskProvisioningInherits(t *testing.T) {
	b, err := Parse(json.RawMessage(`{"kind": "vm", "template": "tpl", "guest": {"os_family": "linux", "domain": "x"},
		"provisioning": "thin", "disks": [{"size_gb": 10, "provisioning": "thick_eager_zeroed"}, {"size_gb": 5}],
		"components": [{"key": "web"}, {"key": "db", "provisioning": "thick_lazy_zeroed"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cs := b.ComponentList()
	for i, want := range []struct{ vm, disk0, disk1 string }{
		{ProvisionThin, ProvisionThickEager, ProvisionThin},
		{ProvisionThickLazy, ProvisionThickEager, ProvisionThickLazy},
	} {
		plan, _, _ := b.EvaluateComponent(cs[i], nil)
		if plan.Provisioning != want.vm || plan.Disks[0].Provisioning != want.disk0 || plan.Disks[1].Provisioning != want.disk1 {
			t.Fatalf("%s: plan %q, disks %+v", cs[i].Key, plan.Provisioning, plan.Disks)
		}
	}
	single, err := Parse(json.RawMessage(`{"kind": "vm", "template": "tpl", "disks": [{"size_gb": 10}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if plan, _ := single.EvaluateVM(nil); plan.Provisioning != "" || plan.Disks[0].Provisioning != "" {
		t.Fatalf("without provisioning: %+v", plan)
	}
}

// A component's linked_clone is the blueprint's unless it has its own;
// without either, the catalogue template's applies (ADR-0037).
func TestLinkedCloneInherits(t *testing.T) {
	b, err := Parse(json.RawMessage(`{"kind": "vm", "template": "tpl", "linked_clone": true,
		"components": [{"key": "web"}, {"key": "db", "linked_clone": false}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cs := b.ComponentList()
	if cs[0].LinkedClone == nil || !*cs[0].LinkedClone || cs[1].LinkedClone == nil || *cs[1].LinkedClone {
		t.Fatalf("components = %v, %v", cs[0].LinkedClone, cs[1].LinkedClone)
	}
	single, err := Parse(json.RawMessage(`{"kind": "vm", "template": "tpl"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c := single.ComponentList()[0]; c.LinkedClone != nil {
		t.Fatalf("without linked_clone: %v", *c.LinkedClone)
	}
}

// A component's first adapter is on DHCP as the blueprint says, unless it
// says otherwise (ADR-0039).
func TestIPAMInherits(t *testing.T) {
	b, err := Parse(json.RawMessage(`{"kind": "vm", "template": "tpl", "ipam": "dhcp",
		"components": [{"key": "web"}, {"key": "db", "ipam": "efficientip"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cs := b.ComponentList()
	web, _, _ := b.EvaluateComponent(cs[0], nil)
	db, _, _ := b.EvaluateComponent(cs[1], nil)
	if !web.DHCP || db.DHCP {
		t.Fatalf("web on DHCP: %v, db: %v", web.DHCP, db.DHCP)
	}
	single, _ := Parse(json.RawMessage(`{"kind": "vm", "template": "tpl", "ipam": "dhcp"}`))
	if vm, _, _ := single.EvaluateComponent(single.ComponentList()[0], nil); !vm.DHCP {
		t.Fatal("a single VM isn't on DHCP")
	}

	b, _ = Parse(json.RawMessage(`{"kind": "vm", "template": "tpl", "ipv6": true,
		"components": [{"key": "web"}, {"key": "db", "ipv6": false}]}`))
	cs = b.ComponentList()
	web, _, _ = b.EvaluateComponent(cs[0], nil)
	db, _, _ = b.EvaluateComponent(cs[1], nil)
	if !web.IPv6 || db.IPv6 {
		t.Fatalf("web on IPv6: %v, db: %v", web.IPv6, db.IPv6)
	}
}

func TestCheckUserData(t *testing.T) {
	for text, ok := range map[string]bool{
		"#cloud-config\nhostname: {{ .vm.name }}":              true,
		"motd: {{ .inputs.motd | json }}":                      true,
		"{{ if .inputs.debug }}debug: true{{ end }}":           true,
		"{{ $m := .inputs.motd }}motd: {{ $m | json }}":        true,
		"motd: {{ .inputs.motd }}":                             false,
		"motd: {{ printf \"%s\" .inputs.motd }}":               false,
		"{{ $m := .inputs.motd }}motd: {{ $m }}":               false,
		"{{ range .inputs.users }}- {{ . }}\n{{ end }}":        false,
		"{{ range .inputs.users }}- {{ . | json }}\n{{ end }}": true,
		"{{ with .inputs.motd }}motd: {{ . }}{{ end }}":        false,
		"fqdn: {{ $.vm.fqdn }}":                                true,
	} {
		if err := CheckUserData(text); (err == nil) != ok {
			t.Errorf("%q: %v", text, err)
		}
	}
}
