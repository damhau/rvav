package snowsync

import (
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/store"
)

func TestValues(t *testing.T) {
	vm := store.ServiceNowVM{ResourceID: "res_1", Name: "web-dev-app001", InstanceUUID: "4215-1", SyncState: "missing",
		DeploymentID: "dep_1", DeploymentName: "web-dev-app001", ProjectName: "Web Platform",
		Properties: []byte(`{"moref": "vm-42", "cpu_count": 2, "memory_mb": 4096, "power_state": "suspended",
			"disks": [{"capacity_gb": 40}, {"capacity_gb": 100}],
			"nics": [{"network": "a", "ip_addresses": ["fe80::1", "10.0.0.5"]}, {"network": "b", "ip_addresses": ["10.0.0.6"]}]}`)}
	v := Values(vm, "")
	want := map[string]string{"object_id": "vm-42", "cpus": "2", "memory": "4096", "disks": "2", "disks_size": "140",
		"ip_address": "10.0.0.5", "state": "paused", "operational_status": "2", "install_status": "1", "correlation_id": "res_1",
		"short_description": "rvav: deployment web-dev-app001 (dep_1) in Web Platform, owned by no one"}
	for k, w := range want {
		if v[k] != w {
			t.Errorf("%s = %q, want %q", k, v[k], w)
		}
	}
	// Unknown values are left out, so as not to clear what discovery found.
	for _, k := range []string{"owned_by", "fqdn", "guest_os_fullname", "vcenter_uuid"} {
		if _, ok := v[k]; ok {
			t.Errorf("%s is sent: %q", k, v[k])
		}
	}
}

func TestInScope(t *testing.T) {
	for _, c := range []struct {
		projects []string
		want     bool
	}{{nil, true}, {[]string{"web"}, true}, {[]string{"prj_web"}, true}, {[]string{"data"}, false}} {
		if got := InScope(c.projects, "prj_web", "web"); got != c.want {
			t.Errorf("InScope(%v) = %v", c.projects, got)
		}
	}
}

func TestNote(t *testing.T) {
	for _, c := range []struct {
		topic, data, state string
		says               []string
	}{
		{"request.submitted", `"request": {"number": "REQ-10491", "status": "queued"}, "item": {"name": "RHEL 9 server"}`,
			servicenow.ItemWorkInProgress, []string{"REQ-10491 (RHEL 9 server) submitted", "https://rvav.corp.example/requests/REQ-10491"}},
		{"approval.requested", `"request": {"number": "REQ-10491"}, "approval": {"name": "Production change", "level": 1,
			"approvers": [{"name": "Léa Martin"}], "groups": ["rvav-web-admins"]}`, "",
			[]string{"waits for approval: Production change (level 1), by Léa Martin, rvav-web-admins"}},
		{"approval.decided", `"request": {"number": "REQ-10491"}, "approval": {"name": "Production change", "level": 1,
			"outcome": "approved", "decided_by": {"name": "Léa Martin"}}`, "", []string{"REQ-10491 approved by Léa Martin at Production change"}},
		{"provisioning.post", `"request": {"number": "REQ-10491", "status": "succeeded",
			"outcome": "succeeded"}, "deployments": [{"id": "dep_1", "name": "web-dev-app001"}]`,
			servicenow.ItemClosedComplete, []string{"succeeded", "web-dev-app001 https://rvav.corp.example/deployments/dep_1"}},
		{"provisioning.post", `"request": {"number": "REQ-10491", "status": "failed", "outcome": "failed"}`,
			servicenow.ItemClosedIncomplete, []string{"REQ-10491 failed."}},
		{"provisioning.post", `"request": {"number": "REQ-10491", "status": "rejected", "outcome": "failed"}`,
			servicenow.ItemClosedIncomplete, []string{"was rejected"}},
		{"provisioning.post", `"request": {"number": "REQ-10491", "status": "cancelled", "outcome": "cancelled"}`,
			servicenow.ItemClosedIncomplete, []string{"was cancelled"}},
		{"deployment.changed", `"request": {"number": "REQ-10491"}`, "", nil},
	} {
		state, text := Note(store.ServiceNowNote{Topic: c.topic, Payload: []byte(`{"data": {` + c.data + `}}`)}, "https://rvav.corp.example/")
		if state != c.state || (c.says == nil) != (text == "") {
			t.Errorf("%s %s: state %q, note %q", c.topic, c.data, state, text)
		}
		for _, w := range c.says {
			if !strings.Contains(text, w) {
				t.Errorf("%s: %q doesn't say %q", c.topic, text, w)
			}
		}
	}
}
