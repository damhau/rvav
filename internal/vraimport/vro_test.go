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
	// A subscription runs it, and a subscription doesn't run a composite item:
	// a playbook, though its REST calls would make REST call steps.
	if it := items["wf-cmdb-register"]; it.Group != GroupAWX || !slices.Equal(it.Systems, []string{"REST"}) || !reason(it, "ansible.builtin.uri") ||
		!reason(it, "REST call steps") || !slices.Contains(it.Steps, StepREST) || reason(it, "may add") {
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

// TestStandardVRO: the vRO workflows an enterprise vRA typically has, sorted
// and sized for the composite step kinds rvav may add (D2.1): which kinds,
// and how many workflows need each.
func TestStandardVRO(t *testing.T) {
	e := vra.StandardVRO(time.Now())
	tr := TriageVRO(&e)
	items := map[string]TriageItem{}
	for _, it := range tr.Items {
		items[it.ID] = it
	}
	for id, want := range map[string]struct {
		group string
		steps []string
	}{
		"std-decommission":  {GroupNative, []string{StepREST, StepRvav, StepPerson, StepTime}},
		"std-change-wait":   {GroupNative, []string{StepREST, StepTime, StepBranch}},
		"std-cmdb":          {GroupNative, []string{StepREST, StepRvav}},
		"std-backup":        {GroupNative, []string{StepREST, StepRvav}},
		"std-notify":        {GroupNative, []string{StepNotify}},
		"std-patch-window":  {GroupNative, []string{StepAWX, StepRvav, StepTime}},
		"std-big-disk":      {GroupNative, []string{StepRvav, StepPerson, StepBranch}},
		"std-ad-computer":   {GroupAWX, []string{StepAWX}},
		"std-guest-script":  {GroupAWX, []string{StepAWX, StepRvav}},
		"std-rename":        {GroupNative, []string{StepRvav}},
		"std-ask-requester": {GroupNative, []string{StepREST, StepPerson}},
	} {
		it := items[id]
		if it.Group != want.group || !slices.Equal(it.Steps, want.steps) {
			t.Errorf("%s: group %s, steps %v; want %s, %v", id, it.Group, it.Steps, want.group, want.steps)
		}
	}
	// REST calls and nothing else, that nothing runs alone: a composite item with REST call steps.
	if it := items["std-cmdb"]; it.Becomes != "A composite item with REST call steps" || !strings.Contains(strings.Join(it.Reasons, " "), "ADR-0033") {
		t.Errorf("cmdb = %+v", it)
	}
	// A user interaction with no timer: a composite item with a step that waits for a person.
	if it := items["std-ask-requester"]; it.Becomes != "A composite item with a step that waits for a person" || !strings.Contains(strings.Join(it.Reasons, " "), "ADR-0033") {
		t.Errorf("ask-requester = %+v", it)
	}
	// With a timer too: steps that wait for both.
	if it := items["std-decommission"]; it.Becomes != "A composite item with steps that wait for a person and for a time" {
		t.Errorf("decommission = %+v", it)
	}
	// A timer, around a REST call or not: a step that waits, or polls it.
	for _, id := range []string{"std-change-wait", "std-patch-window"} {
		if it := items[id]; it.Becomes != "A composite item with a step that waits for a time" || !strings.Contains(strings.Join(it.Reasons, " "), "polls") {
			t.Errorf("%s = %+v", id, it)
		}
	}
	// The form's datastore choice: a value source.
	if it := items["std-act-datastore"]; it.Group != GroupNative || it.Becomes != "A value source on the field" {
		t.Errorf("datastore action = %+v", it)
	}
	// Mail and nothing else: a composite item with a step that sends a message.
	if it := items["std-notify"]; it.Becomes != "A composite item with a step that sends a message" || !strings.Contains(strings.Join(it.Reasons, " "), "ADR-0033") {
		t.Errorf("notify = %+v", it)
	}
	// D2's sizing: no workflow waits for an event. A REST call, a wait for a
	// person or a time, a message and a branch are built: the workflows that
	// have one say so, and they are no longer to be added.
	if len(tr.Steps) != 0 {
		t.Fatalf("step kinds = %v, want none", tr.Steps)
	}
	md := tr.Markdown()
	_, toAdd, _ := strings.Cut(md, "| Step kind rvav may add |")
	toAdd, _, _ = strings.Cut(toAdd, "| Workflow | Group |")
	for _, built := range []string{"| REST call |", "| wait for a person |", "| wait for a time |", "| notification |", "| branch |"} {
		if strings.Contains(toAdd, built) {
			t.Fatalf("%s is built, so it isn't to be added:\n%s", built, md)
		}
	}
	if !strings.Contains(toAdd, "| wait for an event | 0 |") {
		t.Fatalf("report:\n%s", md)
	}
}
