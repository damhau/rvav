package provision

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/store"
)

// A deletion going on past failures keeps what the VMs it couldn't delete
// still use, a shared address too, and deletes the rest.
func TestStillUsed(t *testing.T) {
	props := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}
	vm := func(id, name, fqdn string, ips ...string) store.Resource {
		return store.Resource{ID: id, Type: "vsphere.vm", Name: name, Properties: props(map[string]any{"vm": map[string]any{
			"fqdn": fqdn, "nics": []map[string]any{{"ip_addresses": ips}}}})}
	}
	address := func(id, ip, host string) store.Resource {
		return store.Resource{ID: id, Type: "ipam.address", Name: ip, Properties: props(map[string]any{"address": ip, "hostname": host})}
	}
	record := func(id, typ, name, value string) store.Resource {
		return store.Resource{ID: id, Type: "dns.record", Name: typ + " " + name,
			Properties: props(map[string]any{"fqdn": name, "record_type": typ, "value": value})}
	}
	list := []store.Resource{
		vm("res_vm1", "app01", "app01.corp.example", "10.0.0.1", "10.0.0.9"),
		vm("res_vm3", "app03", "", "10.0.0.3", "10.0.0.9"),
		address("res_a1", "10.0.0.1", "app01"),
		address("res_a2", "10.0.0.2", "app02"),
		address("res_vip", "10.0.0.9", "app01"),
		record("res_r1", "A", "app01.corp.example", "10.0.0.1"),
		record("res_r2", "PTR", "1.0.0.10.in-addr.arpa", "app01.corp.example."),
		record("res_r3", "A", "app02.corp.example", "10.0.0.2"),
		{ID: "res_drs", Type: "vsphere.drs_rule", Name: "app-apart"},
	}
	got := stillUsed(list)
	if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, []string{"res_a1", "res_r1", "res_r2", "res_vip"}) {
		t.Fatalf("kept %v", got)
	}
	if got["res_r1"] != "app01" {
		t.Fatalf("res_r1 is kept for %q", got["res_r1"])
	}
	if len(stillUsed(list[2:])) != 0 {
		t.Fatal("kept records of VMs deleted")
	}
}
