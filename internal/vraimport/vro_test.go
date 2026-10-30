package vraimport

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// TestTriageVRO: the sample's workflows and actions, each in its group with
// what uses it and why; vRO's library left out (P2.4).
func TestTriageVRO(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	tr := TriageVRO(&e)
	items := map[string]TriageItem{}
	for _, it := range tr.Items {
		items[it.ID] = it
	}
	reason := func(it TriageItem, text string) bool {
		return slices.ContainsFunc(it.Reasons, func(r string) bool { return strings.Contains(r, text) })
	}
	used := func(it TriageItem, kind, name string) bool {
		return slices.ContainsFunc(it.UsedBy, func(u Use) bool { return u.Kind == kind && u.Name == name })
	}

	if tr.Library != 2 || len(tr.Items) != 11 || tr.Groups["A"] != 2 || tr.Groups["B"] != 5 || tr.Groups["C"] != 1 || tr.Groups["D"] != 3 {
		t.Fatalf("library %d, %d items, groups %v", tr.Library, len(tr.Items), tr.Groups)
	}
	if it := items["wf-tag-vm"]; it.Group != GroupNative || !slices.Equal(it.Systems, []string{"vCenter"}) ||
		!used(it, "subscription", "Cost centre tag") || !used(it, "workflow", "Decommission application") ||
		!reason(it, "only drives vCenter") || !reason(it, "provisioning.post in rvav, blocking") {
		t.Fatalf("tag = %+v", it)
	}
	if it := items["wf-ad-group"]; it.Group != GroupAWX || !reason(it, "microsoft.ad") || !used(it, "catalog_item", "Create AD group") ||
		it.UsedBy[0].Detail != "shared with Web Platform, Data Services" || it.Runs != 120 || it.LastRun == nil {
		t.Fatalf("ad = %+v", it)
	}
	// It reaches REST through its own script, and ciPayload through vRO's
	// module call.
	if it := items["wf-cmdb-register"]; it.Group != GroupAWX || !slices.Equal(it.Systems, []string{"REST"}) || !reason(it, "ansible.builtin.uri") {
		t.Fatalf("cmdb = %+v", it)
	}
	// Waits for people and days, and reaches vCenter through the workflow it
	// calls.
	if it := items["wf-decom"]; it.Group != GroupTemporal || !slices.Equal(it.Systems, []string{"REST", "vCenter"}) ||
		!reason(it, "rvav.RequestApproval") || !reason(it, "timer") || !used(it, "day2_action", "Decommission") {
		t.Fatalf("decom = %+v", it)
	}
	if it := items["wf-old-snap"]; it.Group != GroupDrop || !reason(it, "Only disabled") || !reason(it, "last ran on") {
		t.Fatalf("old = %+v", it)
	}
	if it := items["wf-tag-vm-copy"]; it.Group != GroupDrop || !reason(it, "the same as Tag VM with cost centre's") || !reason(it, "never run") {
		t.Fatalf("copy = %+v", it)
	}
	if it := items["act-db-versions"]; it.Group != GroupNative || it.Becomes != "A value source on the field" ||
		!reason(it, "a REST value source") || !used(it, "form_field", "PostgreSQL version") {
		t.Fatalf("getVersions = %+v", it)
	}
	if it := items["act-ci-payload"]; it.Group != GroupAWX || !reason(it, "goes with them") {
		t.Fatalf("ciPayload = %+v", it)
	}
	if it := items["act-legacy"]; it.Group != GroupDrop {
		t.Fatalf("legacy = %+v", it)
	}
	if it := items["abx-backup-reg"]; it.Kind != "abx_action" || it.Group != GroupAWX || !used(it, "subscription", "Backup registration") {
		t.Fatalf("abx = %+v", it)
	}

	md := tr.Markdown()
	if !strings.Contains(md, "| C. Custom Temporal workflow | 1 |") || !strings.Contains(md, "| Decommission application (Corp/Lifecycle) | workflow | deploymentId (string) | day2 action Decommission (on Deployment) |") ||
		!strings.Contains(md, "left out") || strings.Contains(md, "Clone virtual machine") {
		t.Fatalf("markdown:\n%s", md)
	}

	// A workflow in use stays out of D, however old; one nothing uses but
	// that ran last week isn't dropped either.
	e.Workflows[1].LastRun = ptr(time.Now().AddDate(-2, 0, 0))
	e.Subscriptions = slices.DeleteFunc(e.Subscriptions, func(s vra.Subscription) bool { return s.ID == "sub-tag" })
	e.Workflows[4].LastRun = ptr(time.Now().AddDate(0, 0, -7))
	tr = TriageVRO(&e)
	for _, it := range tr.Items {
		switch it.ID {
		case "wf-cmdb-register":
			if it.Group != GroupAWX || !reason(it, "last ran on") {
				t.Fatalf("an old workflow in use: %+v", it)
			}
		case "wf-old-snap":
			if it.Group != GroupNative {
				t.Fatalf("an unused workflow run last week: %+v", it)
			}
		}
	}
}

func ptr[T any](v T) *T { return &v }
