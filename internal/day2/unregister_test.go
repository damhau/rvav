package day2

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/api"
)

// TestUnregister: a VM is unregistered with its addresses, their DNS
// records and its NSX memberships, but not an address another VM has; the
// deployment's only VM isn't offered it; a deployment is, whole, and is then
// deleted from rvav (X1.3).
func TestUnregister(t *testing.T) {
	props := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}
	nic := func(ips ...string) []api.VmNic { return []api.VmNic{{IpAddresses: &ips}} }
	fqdn := "app01.corp.example"
	app01 := Resource{ID: "res_vm1", Type: vmType, Name: "app01", VM: api.VmProperties{Fqdn: &fqdn, Nics: nic("10.0.0.1", "10.0.0.9")}}
	app02 := Resource{ID: "res_vm2", Type: vmType, Name: "app02", VM: api.VmProperties{Nics: nic("10.0.0.2", "10.0.0.9")}}
	address := func(id, ip, host, fqdn string) Resource {
		return Resource{ID: id, Type: "ipam.address", Name: ip, Properties: props(map[string]any{"address": ip, "hostname": host, "fqdn": fqdn})}
	}
	record := func(id, typ, name, value string) Resource {
		return Resource{ID: id, Type: "dns.record", Name: typ + " " + name,
			Properties: props(map[string]any{"fqdn": name, "record_type": typ, "value": value})}
	}
	all := []Resource{app01, app02,
		address("res_a1", "10.0.0.1", "app01", fqdn),
		address("res_a2", "10.0.0.2", "app02", "app02.corp.example"),
		address("res_vip", "10.0.0.9", "app01", "vip.corp.example"),
		record("res_r1", "A", fqdn, "10.0.0.1"),
		record("res_r2", "PTR", "1.0.0.10.in-addr.arpa", fqdn+"."),
		record("res_r3", "A", "app02.corp.example", "10.0.0.2"),
		record("res_r4", "A", "vip.corp.example", "10.0.0.9"),
		{ID: "res_m1", Type: "nsx.membership", Name: "app01"},
		{ID: "res_m2", Type: "nsx.membership", Name: "app02"},
		{ID: "res_drs", Type: "vsphere.drs_rule", Name: "app-apart"},
	}
	var ids []string
	for _, r := range OwnResources(app01, all) {
		ids = append(ids, r.ID)
	}
	if want := []string{"res_vm1", "res_a1", "res_r1", "res_r2", "res_m1"}; !slices.Equal(ids, want) {
		t.Fatalf("app01's own = %v, want %v", ids, want)
	}

	vm := Get("vm_unregister")
	if !vm.Admin || vm.After != "" || vm.Confirm == "" {
		t.Fatalf("vm_unregister = %+v", vm)
	}
	target := Target{Type: vmType, ResourceID: app01.ID, ResourceName: "app01", Resources: all}
	if !vm.Applies(target) {
		t.Error("with another VM: not offered")
	}
	if vm.Applies(Target{Type: vmType, ResourceID: app01.ID, ResourceName: "app01", Resources: []Resource{app01}}) {
		t.Error("the only VM: offered")
	}
	ops := vm.Ops(target, nil)
	if len(ops) != 1 || ops[0].Op != OpForget || ops[0].Args["vm"] != "res_vm1" || ops[0].System != "rvav" ||
		ops[0].Detail != "Stays in vCenter, with 10.0.0.1, A app01.corp.example, PTR 1.0.0.10.in-addr.arpa, its NSX group memberships" {
		t.Fatalf("vm ops = %+v", ops)
	}

	dep := Get("unregister")
	if !dep.Admin || dep.After != "deleted" || !dep.OnFailed || dep.Destructive {
		t.Fatalf("unregister = %+v", dep)
	}
	ops = dep.Ops(Target{Type: DeploymentType, ResourceName: "erp-dev", Resources: all}, nil)
	if len(ops) != 1 || ops[0].Op != OpForget || len(ops[0].Args) != 0 || ops[0].Title != "Release erp-dev from rvav" ||
		ops[0].Detail != "Stay in vCenter: app01, app02" {
		t.Fatalf("deployment ops = %+v", ops)
	}
}
