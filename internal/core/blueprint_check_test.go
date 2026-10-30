package core

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
)

func TestCheckVMBlueprint(t *testing.T) {
	check := func(raw string, publish bool) []string {
		t.Helper()
		b, err := blueprint.Parse(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range checkVMBlueprint(b, "/blueprint", publish) {
			out = append(out, e.Field+" "+*e.Code)
		}
		slices.Sort(out)
		return out
	}
	good := `{"kind": "vm", "naming": "{project}-{component}{seq:02}", "deployment_name": "{project}-{inputs.app}",
		"template": "rhel9", "guest": {"os_family": "linux", "domain": "corp.example"},
		"components": [
		  {"key": "web", "count": "inputs.web_nodes", "anti_affinity": true, "disks": [{"size_gb": 50}],
		   "guest": {"cloud_init": {"user_data": "#cloud-config\nhostname: {{ .vm.name }}"}}},
		  {"key": "db", "count": 1, "template": "win2022", "nics": [{"key": "backup", "network": "\"dvpg-b\""}],
		   "guest": {"os_family": "windows", "domain_join": {"domain": "corp.example", "credential": "ad-join"}, "admin_password": "win-admin"},
		   "tags": ["backup/daily"]}]}`
	if errs := check(good, true); len(errs) > 0 {
		t.Fatalf("a good blueprint: %v", errs)
	}

	bad := `{"kind": "vm", "naming": "{project}", "deployment_name": "{project}{seq:02}",
		"guest": {"os_family": "linux"}, "tags": ["daily"],
		"components": [
		  {"key": "Web", "count": 1, "anti_affinity": true, "disks": [{"size_gb": 0}, {"datastore": "vsan-01 x"}]},
		  {"key": "db", "count": 30, "template": "w", "nics": [{"network": ""}, {"key": "nic2", "network": "inputs.n", "ipam": "static"}],
		   "guest": {"cloud_init": {"user_data": "{{ .vm.name"}, "domain_join": {"domain": ""}, "admin_password": "x"}},
		  {"key": "db", "count": "inputs.("}]}`
	want := []string{
		"/blueprint/components/0/anti_affinity invalid",
		"/blueprint/components/0/disks/0/size_gb out_of_range",
		"/blueprint/components/0/disks/1/datastore invalid",
		"/blueprint/components/0/disks/1/size_gb required",
		"/blueprint/components/0/key pattern",
		"/blueprint/components/0/template required",
		"/blueprint/components/1/count out_of_range",
		"/blueprint/components/1/guest/admin_password invalid",
		"/blueprint/components/1/guest/cloud_init/user_data invalid",
		"/blueprint/components/1/guest/domain_join invalid",
		"/blueprint/components/1/guest/domain_join/credential required",
		"/blueprint/components/1/guest/domain_join/domain required",
		"/blueprint/components/1/nics/0/network required",
		"/blueprint/components/1/nics/1/ipam enum",
		"/blueprint/components/1/nics/1/key duplicate",
		"/blueprint/components/2/count invalid",
		"/blueprint/components/2/key duplicate",
		"/blueprint/components/2/template required",
		"/blueprint/deployment_name invalid",
		"/blueprint/naming invalid",
		"/blueprint/tags/0 invalid",
	}
	if got := check(bad, true); !slices.Equal(got, want) {
		t.Fatalf("errors =\n%v\nwant\n%v", got, want)
	}

	// At most 20 VMs in all.
	if got := check(`{"kind": "vm", "template": "t", "guest": {"os_family": "linux"},
		"components": [{"key": "a", "count": 15}, {"key": "b", "count": 6}]}`, true); !slices.Equal(got, []string{"/blueprint/components out_of_range"}) {
		t.Fatalf("21 VMs = %v", got)
	}

	// NSX security groups (ADR-0028): made for the deployment, or existing.
	groups := `{"kind": "vm", "template": "t", "guest": {"os_family": "linux"}, "groups": ["web", "nsx:grp-web-servers"],
		"security_groups": [{"key": "web", "rules": [{"from": ["any"], "services": ["HTTPS"]},
		  {"from": ["nsx:grp-backup-clients", "web"], "services": ["SSH"], "action": "drop"}]}]}`
	if got := check(groups, true); len(got) > 0 {
		t.Fatalf("good groups: %v", got)
	}
	if got := check(`{"kind": "vm", "template": "t", "guest": {"os_family": "linux"}, "groups": ["db", "nsx:bad id"],
		"security_groups": [{"key": "Web", "rules": [{"from": [], "services": ["HTTPS x"], "action": "permit"}]},
		  {"key": "app", "rules": [{"from": ["web"], "services": []}]}, {"key": "app"}]}`, true); !slices.Equal(got, []string{
		"/blueprint/groups/0 unknown",
		"/blueprint/groups/1 pattern",
		"/blueprint/security_groups/0/key pattern",
		"/blueprint/security_groups/0/rules/0/action invalid",
		"/blueprint/security_groups/0/rules/0/from required",
		"/blueprint/security_groups/0/rules/0/services/0 pattern",
		"/blueprint/security_groups/1/rules/0/from/0 unknown",
		"/blueprint/security_groups/1/rules/0/services required",
		"/blueprint/security_groups/2/key duplicate",
	}) {
		t.Fatalf("bad groups = %v", got)
	}

	// A single VM doesn't name a deployment; a draft may lack its template.
	if got := check(`{"kind": "vm", "deployment_name": "x", "guest": {"os_family": "linux"}}`, false); !slices.Equal(got, []string{"/blueprint/deployment_name invalid"}) {
		t.Fatalf("single VM = %v", got)
	}
}
