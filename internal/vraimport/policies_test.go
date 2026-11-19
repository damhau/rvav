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
		!note(p, Info, "Lifetime: 90 days from the deployment's creation; an extension past it needs approval.") ||
		!note(p, Info, "Allow as many as the lifetime holds: 2 of 30 days.") || !note(p, Info, "Grace days: 7") ||
		!strings.Contains(p.Becomes, "The lease policy of") || !strings.Contains(p.Becomes, "(Administration › Policies › Lease)") {
		t.Fatalf("lease = %+v", p)
	}
	if p := by["pol-day2"]; p.Type != "day2" || !note(p, Info, "Allow power_off, power_on, snapshot_create to member.") ||
		!note(p, Info, "Allow change_owner, delete to project-admin.") {
		t.Fatalf("day2 = %+v", p)
	}
	if p := by["pol-quota-web"]; p.Type != "quota" || !note(p, Info, "VMs: 40.") || !note(p, Info, "vCPU: 64.") || !note(p, Info, "Memory (GB): 256.") ||
		!strings.HasPrefix(p.Becomes, "The quota of the project Web Platform maps to") || p.Enforcement != "hard" {
		t.Fatalf("quota = %+v", p)
	}
	if p := by["pol-limit"]; p.Type != "limit" || !strings.HasPrefix(p.Becomes, "A deployment limit policy for every project") ||
		!note(p, Info, "vCPU per deployment: 16.") || !note(p, Info, "Memory per deployment (GB): 64.") || p.Enforcement != "hard" {
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

// TestPlanLeaseRules: a lease policy with criteria becomes a lease rule,
// its condition their CEL, or reported when they don't translate (ADR-0048).
func TestPlanLeaseRules(t *testing.T) {
	lease := func(criteria, enforcement string) PolicyPlan {
		e := vra.Estate{
			Projects: []vra.Project{{ID: "vp", Name: "Web Platform"}},
			Policies: []vra.Policy{{ID: "p", Name: "p", TypeID: vra.PolicyLease, EnforcementType: enforcement, ProjectID: "vp",
				Criteria: json.RawMessage(criteria), Definition: json.RawMessage(`{"leaseTermMax":7,"leaseTotalTermMax":30}`)}},
		}
		return PlanPolicies(&e)[0]
	}
	has := func(p PolicyPlan, level, text string) bool {
		return slices.ContainsFunc(p.Notes, func(n Note) bool { return n.Level == level && n.Text == text })
	}
	p := lease(`{"matchExpression":[{"key":"input.size","operator":"eq","value":"small"}]}`, "SOFT")
	if !strings.HasPrefix(p.Becomes, "A lease rule of the project Web Platform maps to") || p.Enforcement != "soft" ||
		!has(p, Info, `Condition: inputs.size == "small"`) ||
		!has(p, Info, "Lifetime: 30 days from the deployment's creation; an extension past it warns.") ||
		!has(p, Info, "Extensions: vRA counts days, not extensions. Allow as many as the lifetime holds: 3 of 7 days.") ||
		!slices.ContainsFunc(p.Notes, func(n Note) bool { return n.Level == Warning && strings.Contains(n.Text, "lease rule can't be soft") }) {
		t.Fatalf("rule = %+v", p)
	}
	p = lease(`{"matchExpression":[{"key":"requestedBy","operator":"eq","value":"a@b"}]}`, "HARD")
	if !strings.HasPrefix(p.Becomes, "A lease rule of") ||
		!slices.ContainsFunc(p.Notes, func(n Note) bool {
			return n.Level == Unmapped && strings.Contains(n.Text, "write the rule's condition in CEL")
		}) {
		t.Fatalf("untranslated = %+v", p)
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
	if p := by["quota"]; p.Enforcement != "soft" || !note(p, Info, "set the quota's enforcement to soft") || note(p, Warning, "no enforcement") {
		t.Fatalf("soft quota = %+v", p)
	}
}

// TestPlanQuotaLevels: vRA's organization, project and user levels become
// the rvav quota levels that count the same, with or without a project
// (C2.3, ADR-0047); what rvav doesn't limit is said.
func TestPlanQuotaLevels(t *testing.T) {
	levels := `{"orgLevel":{"limits":{"cpu":500}},"projectLevel":{"limits":{"instances":40}},` +
		`"userLevel":{"limits":{"memory":{"value":32768,"unit":"MB"},"gpu":2}}}`
	e := vra.Estate{Projects: []vra.Project{{ID: "vp", Name: "Web Platform"}}, Policies: []vra.Policy{
		{ID: "web", Name: "web", TypeID: vra.PolicyQuota, ProjectID: "vp", Definition: json.RawMessage(levels)},
		{ID: "all", Name: "all", TypeID: vra.PolicyQuota, Definition: json.RawMessage(levels)},
		{ID: "user", Name: "user", TypeID: vra.PolicyQuota, Definition: json.RawMessage(`{"level":"user","limits":{"cpu":8}}`)},
		{ID: "odd", Name: "odd", TypeID: vra.PolicyQuota, Definition: json.RawMessage(`{"level":"tenant","limits":{"cpu":8}}`)},
	}}
	by := map[string]PolicyPlan{}
	for _, p := range PlanPolicies(&e) {
		by[p.VRAID] = p
	}
	has := func(p PolicyPlan, level, where, text string) bool {
		return slices.ContainsFunc(p.Notes, func(n Note) bool {
			return n.Level == level && n.Where == where && strings.Contains(n.Text, text)
		})
	}
	web := by["web"]
	if web.Becomes != "All of rvav (Administration › Policies › Quotas); "+
		"The quota of the project Web Platform maps to (Administration › Projects › Quotas); "+
		"Each member's quota in the project Web Platform maps to (Administration › Projects › Quotas)" ||
		!has(web, Info, "orgLevel.limits.cpu", "vCPU: 500.") || !has(web, Warning, "orgLevel.limits", "all of rvav's quota counts every project") ||
		!has(web, Info, "projectLevel.limits.instances", "VMs: 40.") || !has(web, Info, "userLevel.limits.memory", "Memory (GB): 32.") ||
		!has(web, Unmapped, "userLevel.limits.gpu", "not gpu") {
		t.Fatalf("a project's levels = %+v", web)
	}
	all := by["all"]
	if all.Becomes != "All of rvav (Administration › Policies › Quotas); The quota of each project (Administration › Projects › Quotas); "+
		"Each user across rvav (Administration › Policies › Quotas)" ||
		!has(all, Warning, "projectLevel.limits", "sets each project's quota on its own") ||
		!has(all, Warning, "userLevel.limits", "set each member's quota of every project instead") ||
		has(all, Warning, "orgLevel.limits", "") {
		t.Fatalf("every project's levels = %+v", all)
	}
	if p := by["user"]; p.Becomes != "Each user across rvav (Administration › Policies › Quotas)" || !has(p, Info, "limits.cpu", "vCPU: 8.") {
		t.Fatalf("one level = %+v", p)
	}
	if p := by["odd"]; !has(p, Unmapped, "limits", "no quota level like vRA's tenant") || has(p, Info, "limits.cpu", "") {
		t.Fatalf("an unknown level = %+v", p)
	}
}

// TestPlanDeploymentLimits: a deployment limit policy's vCPU, memory and
// count become an rvav deployment limit policy, soft as vRA's; storage,
// resource limits and criteria are said not to map (C2.3, ADR-0047).
func TestPlanDeploymentLimits(t *testing.T) {
	e := vra.Estate{Projects: []vra.Project{{ID: "vp", Name: "Web Platform"}}, Policies: []vra.Policy{
		{ID: "web", Name: "web", TypeID: vra.PolicyLimit, EnforcementType: "SOFT", ProjectID: "vp",
			Criteria: json.RawMessage(`{"matchExpression":[{"key":"input.size","operator":"eq","value":"large"}]}`),
			Definition: json.RawMessage(`{"level":"user","deploymentLimits":{"cpu":8,"memory":{"value":16,"unit":"GB"},` +
				`"deploymentCount":3,"storage":100},"resourceLimits":{"cpu":4}}`)},
		{ID: "count", Name: "count", TypeID: vra.PolicyLimit, Definition: json.RawMessage(`{"limits":{"deployments":50}}`)},
		{ID: "storage", Name: "storage", TypeID: vra.PolicyLimit, Definition: json.RawMessage(`{"limits":{"storage":100}}`)},
	}}
	by := map[string]PolicyPlan{}
	for _, p := range PlanPolicies(&e) {
		by[p.VRAID] = p
	}
	has := func(p PolicyPlan, level, where, text string) bool {
		return slices.ContainsFunc(p.Notes, func(n Note) bool {
			return n.Level == level && n.Where == where && strings.Contains(n.Text, text)
		})
	}
	web := by["web"]
	if web.Becomes != "A deployment limit policy for the project Web Platform maps to (Administration › Policies › Limits)" ||
		web.Enforcement != "soft" || !has(web, Info, "enforcementType", "Going beyond it warns the requester") ||
		!has(web, Info, "projectId", "its project admins can write it") ||
		!has(web, Info, "deploymentLimits.cpu", "vCPU per deployment: 8.") ||
		!has(web, Info, "deploymentLimits.memory", "Memory per deployment (GB): 16.") ||
		!has(web, Info, "deploymentLimits.deploymentCount", "Deployments per user: 3.") ||
		!has(web, Unmapped, "deploymentLimits.storage", "no storage per deployment") ||
		!has(web, Unmapped, "resourceLimits", "not each of its resources") || !has(web, Unmapped, "criteria", "no condition") ||
		has(web, Warning, "enforcementType", "") {
		t.Fatalf("a project's limits = %+v", web)
	}
	if p := by["count"]; !has(p, Info, "limits.deployments", "Deployments per project: 50.") || p.Enforcement != "hard" {
		t.Fatalf("a count = %+v", p)
	}
	if p := by["storage"]; p.Becomes != "Nothing" || p.Enforcement != "" || !has(p, Unmapped, "limits.storage", "") {
		t.Fatalf("only storage = %+v", p)
	}
}

// TestPlanAmountsRoundUp: an amount in MB that isn't a whole number of GB
// is rounded up and said, and one rvav can't read is said too.
func TestPlanAmountsRoundUp(t *testing.T) {
	e := vra.Estate{Policies: []vra.Policy{
		{ID: "quota", Name: "quota", TypeID: vra.PolicyQuota,
			Definition: json.RawMessage(`{"limits":{"memory":{"value":1536,"unit":"MB"},"storage":{"value":1,"unit":"PB"}}}`)},
		{ID: "limit", Name: "limit", TypeID: vra.PolicyLimit,
			Definition: json.RawMessage(`{"deploymentLimits":{"memory":{"value":512,"unit":"MB"}}}`)},
	}}
	by := map[string]PolicyPlan{}
	for _, p := range PlanPolicies(&e) {
		by[p.VRAID] = p
	}
	has := func(p PolicyPlan, level, where, text string) bool {
		return slices.ContainsFunc(p.Notes, func(n Note) bool {
			return n.Level == level && n.Where == where && n.Text == text
		})
	}
	if p := by["quota"]; !has(p, Info, "limits.memory", "Memory (GB): 2, rounded up from 1536 MB.") ||
		!has(p, Unmapped, "limits.storage", `Storage (GB): rvav doesn't read {"unit":"PB","value":1}. Set it by hand.`) {
		t.Fatalf("quota = %+v", p.Notes)
	}
	if p := by["limit"]; !has(p, Info, "deploymentLimits.memory", "Memory per deployment (GB): 1, rounded up from 512 MB.") {
		t.Fatalf("limit = %+v", p.Notes)
	}
}
