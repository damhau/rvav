package vraimport

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// TestPlanPolicies: each of the sample's policies, with what to set in rvav.
func TestPlanPolicies(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	ps := PlanPolicies(&e)
	by := map[string]PolicyPlan{}
	for _, p := range ps {
		by[p.VRAID] = p
	}
	note := func(p PolicyPlan, level, text string) bool {
		return slices.ContainsFunc(p.Notes, func(n Note) bool { return n.Level == level && strings.Contains(n.Text, text) })
	}
	if p := by["pol-approval-prd"]; p.Type != "approval" || !strings.Contains(p.Becomes, "approval policy for catalog requests") ||
		!note(p, Info, `Condition: environment == "prd"`) || !note(p, Info, "Level 1: infra-leads@corp.example, any of them.") ||
		!note(p, Warning, "identity provider") || !note(p, Info, "Unanswered after 72h: reject.") {
		t.Fatalf("approval = %+v", p)
	}
	if p := by["pol-lease-dev"]; p.Type != "lease" || p.VRAProject != "Web Platform" || !note(p, Info, "Maximum days: 30.") ||
		!note(p, Info, "at most 2 extensions of 30 days") || !note(p, Info, "Grace days: 7") {
		t.Fatalf("lease = %+v", p)
	}
	if p := by["pol-day2"]; p.Type != "day2" || !note(p, Info, "Allow power_off, power_on, snapshot_create to member.") ||
		!note(p, Info, "Allow change_owner, delete to project-admin.") {
		t.Fatalf("day2 = %+v", p)
	}
	if p := by["pol-quota-web"]; p.Type != "quota" || !note(p, Info, "VMs: 40.") || !note(p, Info, "vCPU: 64.") || !note(p, Info, "Memory (GB): 256.") {
		t.Fatalf("quota = %+v", p)
	}
	if p := by["pol-limit"]; p.Type != "limit" || !note(p, Unmapped, "no limit per deployment") {
		t.Fatalf("limit = %+v", p)
	}
	for in, want := range map[string]string{
		`{"or":[{"key":"input.size","operator":"eq","value":"large"},{"key":"projectName","operator":"startsWith","value":"Fin"}]}`: `(inputs.size == "large" || project.name.startsWith("Fin"))`,
		`{"matchExpression":[{"key":"requestedBy","operator":"eq","value":"a@b"}]}`:                                                 "",
	} {
		got, ok := criteriaCEL([]byte(in))
		if got != want || ok != (want != "") {
			t.Fatalf("%s = %q %v, want %q", in, got, ok, want)
		}
	}
}
