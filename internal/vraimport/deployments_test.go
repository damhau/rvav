package vraimport

import (
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// TestPlanDeploymentEdges: a lease that has ended, a deployment with one
// VM gone, one whose inputs name no environment, an owner rvav doesn't
// know.
func TestPlanDeploymentEdges(t *testing.T) {
	now := time.Now()
	e := vra.SampleEstate(now)
	r := Rvav{Now: now, Users: map[string]string{"thomas.nguyen@corp.example": "usr_t", "lea.martin@corp.example": "usr_l"},
		Projects: []RvavProject{{ID: "prj_web", Name: "web platform", Environments: []string{"dev", "prd"}, Members: []string{"usr_t", "usr_l"}}},
		VMs: map[string]VMRef{"vra-web-001": {VCenter: "vc1", Moref: "vm-1", Name: "vra-web-001"},
			"vra-api-001": {VCenter: "vc1", Moref: "vm-2", Name: "vra-api-001"}},
		Items: map[string]string{"rhel-9-server": "itm_rhel"}}
	ended := now.AddDate(0, 0, -3)
	e.Deployments[0].LeaseExpireAt = &ended
	delete(e.Deployments[3].Inputs, "environment")
	e.Deployments[3].Resources[0].Properties["resourceName"] = "vra-web-001"
	e.Deployments[0].OwnedBy = "nobody@corp.example"
	plans := PlanDeployments(&e, r)

	if p := plans[0]; p.Status != Skipped || !hasNote(p.Notes, "owner", "never signed in") {
		t.Fatalf("an owner rvav doesn't know: %+v", p)
	}
	e.Deployments[0].OwnedBy = "thomas.nguyen@corp.example"
	plans = PlanDeployments(&e, r)
	if p := plans[0]; p.Status != Adopt || p.LeaseDays == nil || *p.LeaseDays != 1 || !hasNote(p.Notes, "lease", "ended in vRA") ||
		p.ProjectID != "prj_web" || !hasNote(p.Notes, "project", "the same name") {
		t.Fatalf("an ended lease: %+v", p)
	}
	if p := plans[1]; p.Status != Adopt || len(p.VMs) != 1 || !hasNote(p.Notes, "vms", "vra-api-002") {
		t.Fatalf("one VM of two gone: %+v", p)
	}
	if p := plans[3]; p.Status != Skipped || !hasNote(p.Notes, "inputs", "no environment") {
		t.Fatalf("no environment: %+v", p)
	}
	r.VMs["vra-web-001"] = VMRef{Name: "vra-web-001", Ambiguous: true}
	if p := PlanDeployments(&e, r)[0]; p.Status != Skipped || !hasNote(p.Notes, "vms", "More than one vCenter") {
		t.Fatalf("a name two vCenters have: %+v", p)
	}
}

func hasNote(notes []Note, where, text string) bool {
	for _, n := range notes {
		if n.Where == where && strings.Contains(n.Text, text) {
			return true
		}
	}
	return false
}
