package vraimport

import (
	"encoding/json"
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

// TestPlanApprovalRoles: vRA's role approvers become project roles, its
// manager the requester's manager; a role rvav has no match for is said.
func TestPlanApprovalRoles(t *testing.T) {
	approval := func(def string) PolicyPlan {
		e := vra.Estate{Policies: []vra.Policy{{ID: "p", Name: "p", TypeID: vra.PolicyApproval, EnforcementType: "HARD",
			Definition: json.RawMessage(def)}}}
		return PlanPolicies(&e)[0]
	}
	has := func(p PolicyPlan, level, text string) bool {
		return slices.ContainsFunc(p.Notes, func(n Note) bool { return n.Level == level && n.Text == text })
	}
	p := approval(`{"level":2,"approvalMode":"ALL_OF","approvalType":"ROLE","approvers":["ROLE:project_supervisor","role:Project_Administrator","USER:claire.dubois@corp.example"],"actions":["Deployment.Create"]}`)
	if !has(p, Info, "Level 2: claire.dubois@corp.example, whoever holds supervisor or project-admin in the project, all of them.") ||
		!has(p, Info, "Project roles: supervisor, project-admin, in the level's roles.") ||
		slices.ContainsFunc(p.Notes, func(n Note) bool { return n.Level == Unmapped || strings.Contains(n.Text, "manager") }) {
		t.Fatalf("roles = %+v", p.Notes)
	}
	for _, def := range []string{
		`{"level":1,"approvalMode":"ANY_OF","approvalType":"MANAGER","approvers":[],"actions":["Deployment.Create"]}`,
		`{"level":1,"approvalMode":"ANY_OF","approvalType":"USER","approvers":["MANAGER"],"actions":["Deployment.Create"]}`,
	} {
		if p := approval(def); !has(p, Info, "Level 1: the requester's manager, any of them.") ||
			!has(p, Info, "The requester's manager: set the level's manager, and the identity provider's manager claim.") {
			t.Fatalf("%s = %+v", def, p.Notes)
		}
	}
	p = approval(`{"level":1,"approvalType":"ROLE","approvers":["ROLE:org_owner","GROUP:leads@corp.example"],"actions":["Cloud.vSphere.Machine.PowerOff"]}`)
	if !has(p, Unmapped, "No rvav project role for ROLE:org_owner: name a role or the users.") ||
		!has(p, Info, "Level 1: leads@corp.example, any of them.") || !strings.Contains(p.Becomes, "day-2 policy") {
		t.Fatalf("unknown role = %+v", p)
	}
}

// TestPlanApprovalUpdates: vRA's Deployment.Update is rvav's update; an
// approval policy for new requests only says how to keep it to them, as
// rvav's apply to updates too.
func TestPlanApprovalUpdates(t *testing.T) {
	approval := func(actions string) PolicyPlan {
		e := vra.Estate{Policies: []vra.Policy{{ID: "p", Name: "p", TypeID: vra.PolicyApproval, EnforcementType: "HARD",
			Definition: json.RawMessage(`{"level":1,"approvers":["USER:a@corp.example"],"actions":` + actions + `}`)}}}
		return PlanPolicies(&e)[0]
	}
	says := func(p PolicyPlan, f func(Note) bool) bool { return slices.ContainsFunc(p.Notes, f) }
	keepsToNew := func(n Note) bool { return strings.Contains(n.Text, `add request.kind == "provision"`) }
	if p := approval(`["Deployment.Create"]`); !says(p, keepsToNew) {
		t.Fatalf("create only = %+v", p.Notes)
	}
	if p := approval(`["Deployment.Create","Deployment.Update"]`); says(p, keepsToNew) ||
		says(p, func(n Note) bool { return n.Level == Warning && n.Where == "actions" }) {
		t.Fatalf("create and update = %+v", p.Notes)
	}
	if p := approval(`["Deployment.Update"]`); !strings.Contains(p.Becomes, "day-2 policy") ||
		!says(p, func(n Note) bool { return n.Text == "Actions: update." }) {
		t.Fatalf("update only = %+v", p)
	}
}

// TestPlanEnforcement: vRA's soft lease policy is a soft rvav one, a soft
// approval of catalog requests stays hard, and a project's policy says
// its project admins can write it (G1.3, ADR-0045).
func TestPlanEnforcement(t *testing.T) {
	e := vra.Estate{Projects: []vra.Project{{ID: "vp", Name: "Web Platform"}}, Policies: []vra.Policy{
		{ID: "lease", Name: "lease", TypeID: vra.PolicyLease, EnforcementType: "SOFT", Definition: json.RawMessage(`{"leaseTermMax":30}`)},
		{ID: "project-lease", Name: "project lease", TypeID: vra.PolicyLease, EnforcementType: "SOFT", ProjectID: "vp",
			Definition: json.RawMessage(`{"leaseTermMax":30}`)},
		{ID: "hard-lease", Name: "hard lease", TypeID: vra.PolicyLease, EnforcementType: "HARD", Definition: json.RawMessage(`{}`)},
		{ID: "approval", Name: "approval", TypeID: vra.PolicyApproval, EnforcementType: "SOFT",
			Definition: json.RawMessage(`{"level":1,"approvers":["USER:a@corp.example"],"actions":["Deployment.Create"]}`)},
		{ID: "day2-approval", Name: "day-2 approval", TypeID: vra.PolicyApproval, EnforcementType: "HARD",
			Definition: json.RawMessage(`{"level":1,"approvers":["USER:a@corp.example"],"actions":["Deployment.Delete"]}`)},
		{ID: "quota", Name: "quota", TypeID: vra.PolicyQuota, EnforcementType: "SOFT", Definition: json.RawMessage(`{}`)},
	}}
	by := map[string]PolicyPlan{}
	for _, p := range PlanPolicies(&e) {
		by[p.VRAID] = p
	}
	note := func(p PolicyPlan, level, text string) bool {
		return slices.ContainsFunc(p.Notes, func(n Note) bool { return n.Level == level && strings.Contains(n.Text, text) })
	}
	if p := by["lease"]; p.Enforcement != "soft" || !note(p, Info, "set the lease policy's enforcement to soft") || note(p, Info, "project admins") {
		t.Fatalf("soft lease = %+v", p)
	}
	if p := by["project-lease"]; p.Enforcement != "soft" || !note(p, Info, "its project admins can write it") || !note(p, Warning, "can't be soft") {
		t.Fatalf("a project's soft lease = %+v", p)
	}
	if p := by["hard-lease"]; p.Enforcement != "hard" || note(p, Info, "enforcement") {
		t.Fatalf("hard lease = %+v", p)
	}
	if p := by["approval"]; p.Enforcement != "" || !note(p, Warning, "soft day-2 policy with the effect approval") {
		t.Fatalf("soft approval = %+v", p)
	}
	if p := by["day2-approval"]; p.Enforcement != "hard" {
		t.Fatalf("day-2 approval = %+v", p)
	}
	if p := by["quota"]; p.Enforcement != "" || !note(p, Warning, "no enforcement") {
		t.Fatalf("soft quota = %+v", p)
	}
}
