package vraimport

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/vra"
)

// Policies are reported, not imported: they decide who may do what, so an
// administrator sets each in rvav from what the report says (ADR-0027).

// PolicyPlan is what a vRA policy becomes in rvav.
type PolicyPlan struct {
	VRAID      string `json:"vra_id"`
	Name       string `json:"name"`
	Type       string `json:"type"` // approval, lease, day2, quota, limit, other
	VRAProject string `json:"vra_project,omitempty"`
	Becomes    string `json:"becomes"`
	// Enforcement is the rvav policy's, hard or soft, where rvav's policy
	// has one (lease and day-2 deny or approval policies, ADR-0045).
	Enforcement string `json:"enforcement,omitempty"`
	Notes       []Note `json:"notes"`
}

// vRA's day-2 actions, by rvav's action keys.
var day2Keys = map[string]string{
	"Cloud.vSphere.Machine.PowerOn":         "power_on",
	"Cloud.vSphere.Machine.PowerOff":        "power_off",
	"Cloud.vSphere.Machine.Reboot":          "restart_guest",
	"Cloud.vSphere.Machine.Reset":           "reset",
	"Cloud.vSphere.Machine.Shutdown":        "shutdown_guest",
	"Cloud.vSphere.Machine.Suspend":         "suspend",
	"Cloud.vSphere.Machine.Resize":          "resize",
	"Cloud.vSphere.Machine.Snapshot.Create": "snapshot_create",
	"Cloud.vSphere.Machine.Snapshot.Delete": "snapshot_delete",
	"Cloud.vSphere.Machine.Snapshot.Revert": "snapshot_revert",
	"Cloud.vSphere.Machine.Remote.Console":  "console",
	"Cloud.vSphere.Machine.Add.Disk":        "disk_add",
	"Cloud.vSphere.Disk.Disk.Resize":        "disk_extend",
	"Cloud.vSphere.Disk.Disk.Remove":        "disk_remove",
	"Cloud.vSphere.Machine.Add.Nic":         "nic_add",
	"Cloud.vSphere.Machine.Remove.Nic":      "nic_remove",
	"Cloud.vSphere.Machine.Update.Tags":     "tags_edit",
	"Cloud.vSphere.Machine.Unregister":      "vm_unregister",
	"Deployment.Delete":                     "delete",
	"Deployment.ChangeOwner":                "change_owner",
	"Deployment.ChangeLease":                "extend_lease",
	"Deployment.ChangeProject":              "change_project",
	"Deployment.EditDeployment":             "edit",
	"Deployment.PowerOn":                    "power_on",
	"Deployment.PowerOff":                   "power_off",
	"Deployment.Update":                     "update",
}

// vRA's roles, as rvav's.
var roleKeys = map[string]string{
	"ROLE:project_member":        "member",
	"ROLE:project_administrator": "project-admin",
	"ROLE:project_viewer":        "viewer",
	"ROLE:project_supervisor":    "supervisor",
}

// PlanPolicies says what each of the estate's policies becomes.
func PlanPolicies(e *vra.Estate) []PolicyPlan {
	out := []PolicyPlan{}
	for _, p := range e.Policies {
		pl := PolicyPlan{VRAID: p.ID, Name: p.Name, Notes: []Note{}}
		if p.ProjectID != "" {
			pl.VRAProject = projectName(e, p.ProjectID)
		}
		note := func(level, where, text string) { pl.Notes = append(pl.Notes, Note{level, where, text}) }
		scope := "every project"
		if pl.VRAProject != "" {
			scope = "the project " + pl.VRAProject + " maps to"
		}
		var def map[string]any
		_ = json.Unmarshal(p.Definition, &def)
		soft := strings.EqualFold(p.EnforcementType, "SOFT")
		if pl.VRAProject != "" && slices.Contains([]string{vra.PolicyApproval, vra.PolicyLease, vra.PolicyDay2, vra.PolicyLimit}, p.TypeID) {
			note(Info, "projectId", "For one project: its project admins can write it in rvav (Administration › Policies), "+
				"unless an administrator does, and a global hard policy still wins over it.")
		}
		switch p.TypeID {
		case vra.PolicyApproval:
			pl.Type = "approval"
			planApproval(&pl, p, def, note, scope)
			if soft {
				note(Warning, "enforcementType", "It is soft in vRA: other policies may override it. rvav's approval policies "+
					"all apply; to only warn the requester, make it a soft day-2 policy with the effect approval instead.")
			}
		case vra.PolicyLease:
			pl.Type = "lease"
			planLease(&pl, p, def, note, scope, soft)
		case vra.PolicyDay2:
			pl.Type = "day2"
			planDay2(&pl, def, note, scope)
			if soft {
				note(Info, "enforcementType", "It is soft in vRA: a project's own policy may override it. In rvav an allow has no "+
					"enforcement: a project's policy tried before it decides.")
			}
		case vra.PolicyQuota:
			pl.Type = "quota"
			planQuota(&pl, def, note, scope, soft)
		case vra.PolicySharing:
			pl.Type = "sharing"
			pl.Becomes = "The sharing of the catalog items it names, in " + scope + ", made by the catalog's import"
			note(Info, "entitledUsers", "Imported with the items: users by email, groups by name, project roles as rvav's. What doesn't map is in each item's notes.")
		case vra.PolicyLimit:
			pl.Type = "limit"
			planLimit(&pl, p, def, note, scope, soft)
		default:
			pl.Type = "other"
			pl.Becomes = "Nothing"
			note(Unmapped, "typeId", p.TypeID+" has no rvav equivalent.")
		}
		if soft && slices.Contains([]string{"sharing", "other"}, pl.Type) {
			note(Warning, "enforcementType", "It is soft in vRA: rvav's "+pl.Type+" settings have no enforcement, they always apply.")
		}
		out = append(out, pl)
	}
	return out
}

// planLease: a lease policy with criteria becomes a lease rule whose
// condition is their CEL, and vRA's total lease term the lifetime (ADR-0048).
func planLease(pl *PolicyPlan, p vra.Policy, def map[string]any, note func(string, string, string), scope string, soft bool) {
	pl.Becomes = "The lease policy of " + scope + " (Administration › Policies › Leases), for each of its environments"
	rule, kind := len(p.Criteria) > 0 && string(p.Criteria) != "null", "policy"
	if rule {
		kind = "rule"
		pl.Becomes = "A lease rule of " + scope + " (Administration › Policies › Leases), tried before the lease policies"
		if cel, ok := criteriaCEL(p.Criteria); ok {
			note(Info, "criteria", "Condition: "+cel)
		} else {
			note(Unmapped, "criteria", "Its criteria don't translate: write the rule's condition in CEL. They were "+string(p.Criteria)+".")
		}
	}
	pl.Enforcement = "hard"
	if soft {
		pl.Enforcement = "soft"
		note(Info, "enforcementType", "Soft: set the lease "+kind+"'s enforcement to soft. Going beyond it warns the requester, who may go on.")
	}
	switch {
	case soft && pl.VRAProject != "" && rule:
		note(Warning, "enforcementType", "A project admin's lease rule can't be soft where the environment's lease is hard: "+
			"an administrator writes it.")
	case soft && pl.VRAProject != "":
		note(Warning, "enforcementType", "A project's override of a hard environment lease can't be soft: "+
			"only an administrator can make the environment's lease soft.")
	}
	if v, ok := num(def["leaseTermMax"]); ok {
		note(Info, "leaseTermMax", fmt.Sprintf("Maximum days: %d.", v))
	}
	if total, ok := num(def["leaseTotalTermMax"]); ok {
		past := "needs approval"
		if soft {
			past = "warns"
		}
		note(Info, "leaseTotalTermMax", fmt.Sprintf("Lifetime: %d days from the deployment's creation; an extension past it %s.", total, past))
		if term, ok := num(def["leaseTermMax"]); ok && term > 0 {
			note(Info, "leaseTotalTermMax", fmt.Sprintf("Extensions: vRA counts days, not extensions. Allow as many as the lifetime holds: %d of %d days.",
				max(total/term-1, 0), term))
		}
	}
	if v, ok := num(def["leaseGrace"]); ok {
		note(Info, "leaseGrace", fmt.Sprintf("Grace days: %d, between the end of the lease and the deletion.", v))
	}
}

var quotaNames = map[string]string{"instances": "VMs", "cpu": "vCPU", "memory": "Memory (GB)", "storage": "Storage (GB)"}

// planQuota reads a resource quota policy's levels: one level and its
// limits, or vRA's organization, project and user levels each with their
// own. Each becomes the rvav quota level that counts the same (ADR-0047).
func planQuota(pl *PolicyPlan, def map[string]any, note func(string, string, string), scope string, soft bool) {
	type level struct {
		where, name string
		limits      map[string]any
	}
	var levels []level
	if limits, ok := def["limits"].(map[string]any); ok {
		name, _ := def["level"].(string)
		levels = append(levels, level{"limits", cmp.Or(name, "project"), limits})
	}
	for _, k := range []string{"orgLevel", "projectLevel", "userLevel"} {
		if m, ok := def[k].(map[string]any); ok {
			limits, _ := m["limits"].(map[string]any)
			levels = append(levels, level{k + ".limits", strings.TrimSuffix(k, "Level"), limits})
		}
	}
	project := pl.VRAProject
	var becomes []string
	for _, l := range levels {
		switch {
		case l.name == "project" && project != "":
			becomes = append(becomes, "The quota of "+scope+" (Administration › Projects › Quotas)")
		case l.name == "project":
			becomes = append(becomes, "The quota of each project (Administration › Projects › Quotas)")
			note(Warning, l.where, "It limits every project the same in vRA: rvav sets each project's quota on its own.")
		case l.name == "user" && project != "":
			becomes = append(becomes, "Each member's quota in "+scope+" (Administration › Projects › Quotas)")
		case l.name == "user":
			becomes = append(becomes, "Each user across rvav (Administration › Policies › Quotas)")
			note(Warning, l.where, "It limits each user in each project in vRA; rvav's limit for each user across rvav counts "+
				"all their projects together. To keep vRA's, set each member's quota of every project instead.")
		case l.name == "org" || l.name == "organization":
			becomes = append(becomes, "All of rvav (Administration › Policies › Quotas)")
			if project != "" {
				note(Warning, l.where, "It is for one project in vRA; all of rvav's quota counts every project.")
			}
		default:
			note(Unmapped, l.where, "rvav has no quota level like vRA's "+l.name+".")
			continue
		}
		for _, k := range []string{"instances", "cpu", "memory", "storage"} {
			if v, ok := amount(l.limits[k]); ok {
				note(Info, l.where+"."+k, fmt.Sprintf("%s: %d.", quotaNames[k], v))
			}
		}
		for _, k := range slices.Sorted(maps.Keys(l.limits)) {
			if _, known := quotaNames[k]; !known {
				note(Unmapped, l.where+"."+k, "rvav's quotas limit vCPU, memory, storage and VMs, not "+k+".")
			}
		}
	}
	pl.Becomes = cmp.Or(strings.Join(becomes, "; "), "The quota of "+scope+" (Administration › Projects › Quotas)")
	pl.Enforcement = "hard"
	if soft {
		pl.Enforcement = "soft"
		note(Info, "enforcementType", "Soft: set the quota's enforcement to soft. Going over it then asks for approval instead of refusing.")
	}
}

// planLimit reads a deployment limit policy: its limits on each
// deployment's vCPU and memory, and on the number of deployments, become
// an rvav deployment limit policy (ADR-0047).
func planLimit(pl *PolicyPlan, p vra.Policy, def map[string]any, note func(string, string, string), scope string, soft bool) {
	where, limits := "limits", map[string]any{}
	if m, ok := def["deploymentLimits"].(map[string]any); ok {
		where, limits = "deploymentLimits", m
	} else if m, ok := def["limits"].(map[string]any); ok {
		limits = m
	}
	level, _ := def["level"].(string)
	mapped := false
	for _, k := range slices.Sorted(maps.Keys(limits)) {
		v, ok := amount(limits[k])
		switch {
		case !ok:
			note(Unmapped, where+"."+k, "Not a number rvav reads.")
		case k == "cpu":
			mapped = true
			note(Info, where+"."+k, fmt.Sprintf("vCPU per deployment: %d.", v))
		case k == "memory":
			mapped = true
			note(Info, where+"."+k, fmt.Sprintf("Memory per deployment (GB): %d.", v))
		case (k == "deployments" || k == "deploymentCount") && level == "user":
			mapped = true
			note(Info, where+"."+k, fmt.Sprintf("Deployments per user: %d.", v))
		case k == "deployments" || k == "deploymentCount":
			mapped = true
			note(Info, where+"."+k, fmt.Sprintf("Deployments per project: %d.", v))
		case k == "storage":
			note(Unmapped, where+"."+k, "rvav limits no storage per deployment: the project's storage quota bounds what it holds in all.")
		default:
			note(Unmapped, where+"."+k, "rvav's deployment limits are on the number of deployments, and on each one's vCPU and memory.")
		}
	}
	if _, ok := def["resourceLimits"]; ok {
		note(Unmapped, "resourceLimits", "rvav limits each deployment as a whole, not each of its resources: the sizes an item offers bound each VM.")
	}
	if c := strings.TrimSpace(string(p.Criteria)); c != "" && c != "null" && c != "{}" {
		note(Unmapped, "criteria", "rvav's deployment limits have no condition: they apply to every deployment of their project, in one environment or all.")
	}
	pl.Becomes = "A deployment limit policy for " + scope + " (Administration › Policies › Limits)"
	if !mapped {
		pl.Becomes = "Nothing"
		return
	}
	pl.Enforcement = "hard"
	if soft {
		pl.Enforcement = "soft"
		note(Info, "enforcementType", "Soft: set the policy's enforcement to soft. Going beyond it warns the requester, who may go on.")
	}
}

// amount reads a number, or vRA's {value, unit} with the unit MB, GB or TB, in GB.
func amount(v any) (int, bool) {
	if n, ok := num(v); ok {
		return n, true
	}
	m, _ := v.(map[string]any)
	n, ok := num(m["value"])
	if !ok {
		return 0, false
	}
	switch unit, _ := m["unit"].(string); strings.ToUpper(unit) {
	case "MB":
		return n / 1024, true
	case "TB":
		return n * 1024, true
	case "", "GB":
		return n, true
	}
	return 0, false
}

func planApproval(pl *PolicyPlan, p vra.Policy, def map[string]any, note func(string, string, string), scope string) {
	actions := strs(def["actions"])
	day2 := len(actions) > 0 && !contains(actions, "Deployment.Create")
	if day2 {
		pl.Becomes = "A day-2 policy with the effect approval, in " + scope + " (Administration › Policies › Day-2)"
		pl.Enforcement = "hard"
		var keys, unknown []string
		for _, a := range actions {
			if k, ok := day2Keys[a]; ok {
				keys = append(keys, k)
			} else {
				unknown = append(unknown, a)
			}
		}
		if len(keys) > 0 {
			note(Info, "actions", "Actions: "+strings.Join(keys, ", ")+".")
		}
		if len(unknown) > 0 {
			note(Unmapped, "actions", "No rvav action for "+strings.Join(unknown, ", ")+".")
		}
	} else {
		pl.Becomes = "An approval policy for catalog requests, in " + scope + " (Administration › Policies › Approvals)"
		var others []string
		for _, a := range actions {
			if a != "Deployment.Create" && a != "Deployment.Update" {
				others = append(others, a)
			}
		}
		if len(others) > 0 {
			note(Warning, "actions", "It also covers "+strings.Join(others, ", ")+": those need a day-2 policy with the effect approval.")
		}
		// rvav's approval policies apply to updates as well (ADR-0042).
		if !contains(actions, "Deployment.Update") {
			note(Info, "actions", `It applies to updates in rvav too: to keep it to new requests, add request.kind == "provision" to its condition.`)
		}
	}
	if cel, ok := criteriaCEL(p.Criteria); ok {
		note(Info, "criteria", "Condition: "+cel)
	} else if len(p.Criteria) > 0 && string(p.Criteria) != "null" {
		note(Unmapped, "criteria", "Its criteria don't translate: write the condition in CEL. They were "+string(p.Criteria)+".")
	} else {
		note(Info, "criteria", "Condition: true (every request).")
	}
	level, _ := num(def["level"])
	mode := "any of them"
	if m, _ := def["approvalMode"].(string); m == "ALL_OF" {
		mode = "all of them"
	}
	var groups, users, roles, unknownRoles []string
	approvalType, _ := def["approvalType"].(string)
	manager := strings.EqualFold(approvalType, "manager")
	for _, a := range strs(def["approvers"]) {
		kind, name, ok := strings.Cut(a, ":")
		switch {
		case isManager(a):
			manager = true
		case ok && strings.EqualFold(kind, "group"):
			groups = append(groups, name)
		case ok && strings.EqualFold(kind, "user"):
			users = append(users, name)
		case ok && strings.EqualFold(kind, "role"):
			if k, known := roleKeys["ROLE:"+strings.ToLower(name)]; known {
				roles = append(roles, k)
			} else {
				unknownRoles = append(unknownRoles, a)
			}
		default:
			users = append(users, a)
		}
	}
	who := append(groups, users...)
	if len(roles) > 0 {
		who = append(who, "whoever holds "+strings.Join(roles, " or ")+" in the project")
	}
	if manager {
		who = append(who, "the requester's manager")
	}
	note(Info, "approvers", fmt.Sprintf("Level %d: %s, %s.", max(level, 1), strings.Join(who, ", "), mode))
	if len(groups) > 0 {
		note(Warning, "approvers", "vRA names groups by email ("+strings.Join(groups, ", ")+"): give rvav the group's name in the identity provider.")
	}
	if len(roles) > 0 {
		note(Info, "approvers", "Project roles: "+strings.Join(roles, ", ")+", in the level's roles.")
	}
	if manager {
		note(Info, "approvers", "The requester's manager: set the level's manager, and the identity provider's manager claim.")
	}
	if len(unknownRoles) > 0 {
		note(Unmapped, "approvers", "No rvav project role for "+strings.Join(unknownRoles, ", ")+": name a role or the users.")
	}
	if days, ok := num(def["autoApprovalExpiry"]); ok && days > 0 {
		decision := "reject"
		if d, _ := def["autoApprovalDecision"].(string); d == "APPROVE" {
			decision = "approve"
		}
		note(Info, "autoApprovalExpiry", fmt.Sprintf("Unanswered after %dh: %s.", days*24, decision))
	}
}

// isManager: vRA's approver for the requester's manager.
func isManager(a string) bool {
	return strings.EqualFold(a, "manager") || strings.EqualFold(a, "role:manager") || strings.EqualFold(a, "role:requester_manager")
}

func planDay2(pl *PolicyPlan, def map[string]any, note func(string, string, string), scope string) {
	pl.Becomes = "Day-2 policies with the effect allow, in " + scope + " (Administration › Policies › Day-2)"
	allowed, _ := def["allowedActions"].([]any)
	for i, a := range allowed {
		m, _ := a.(map[string]any)
		var roles, unknownRoles, keys, unknown []string
		for _, r := range strs(m["authorities"]) {
			if k, ok := roleKeys[r]; ok {
				roles = append(roles, k)
			} else {
				unknownRoles = append(unknownRoles, r)
			}
		}
		for _, x := range strs(m["actions"]) {
			if k, ok := day2Keys[x]; ok {
				keys = append(keys, k)
			} else if x == "*" {
				keys = append(keys, "*")
			} else {
				unknown = append(unknown, x)
			}
		}
		where := "allowedActions." + strconv.Itoa(i)
		sort.Strings(keys)
		if len(roles) > 0 && len(keys) > 0 {
			note(Info, where, "Allow "+strings.Join(keys, ", ")+" to "+strings.Join(roles, ", ")+".")
		}
		if len(unknownRoles) > 0 {
			note(Unmapped, where, "rvav's day-2 policies match roles, not "+strings.Join(unknownRoles, ", ")+": give them a project role.")
		}
		if len(unknown) > 0 {
			note(Unmapped, where, "No rvav action for "+strings.Join(unknown, ", ")+".")
		}
	}
	note(Warning, "allowedActions", "In rvav, nothing a day-2 policy allows is denied: check the actions vRA allowed without a policy.")
}

// criteriaCEL translates vRA's criteria when they are simple matches:
// {"matchExpression":[{"key","operator","value"}]}, with and/or.
func criteriaCEL(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var c any
	if json.Unmarshal(raw, &c) != nil {
		return "", false
	}
	return exprCEL(c)
}

func exprCEL(c any) (string, bool) {
	m, ok := c.(map[string]any)
	if !ok {
		return "", false
	}
	if list, ok := m["matchExpression"].([]any); ok {
		return joinCEL(list, " && ")
	}
	if list, ok := m["and"].([]any); ok {
		return joinCEL(list, " && ")
	}
	if list, ok := m["or"].([]any); ok {
		s, ok := joinCEL(list, " || ")
		return "(" + s + ")", ok
	}
	key, _ := m["key"].(string)
	op, _ := m["operator"].(string)
	field, ok := celField(key)
	if !ok {
		return "", false
	}
	val, _ := json.Marshal(m["value"])
	switch op {
	case "eq":
		return field + " == " + string(val), true
	case "notEq":
		return field + " != " + string(val), true
	case "hasAny", "in":
		return field + " in " + string(val), true
	case "matchesRegex":
		return field + ".matches(" + string(val) + ")", true
	case "startsWith":
		return field + ".startsWith(" + string(val) + ")", true
	case "endsWith":
		return field + ".endsWith(" + string(val) + ")", true
	case "contains":
		return field + ".contains(" + string(val) + ")", true
	}
	return "", false
}

func joinCEL(list []any, sep string) (string, bool) {
	var parts []string
	for _, x := range list {
		s, ok := exprCEL(x)
		if !ok {
			return "", false
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, sep), len(parts) > 0
}

// celField is the rvav CEL name of a vRA criteria key.
func celField(key string) (string, bool) {
	switch {
	case key == "input.environment" || key == "input.env":
		return "environment", true
	case strings.HasPrefix(key, "input."):
		return "inputs." + strings.TrimPrefix(key, "input."), true
	case key == "projectName":
		return "project.name", true
	}
	// rvav's conditions see the item's slug, not its name, and the user's
	// ID and groups, not their email.
	return "", false
}

func num(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

func strs(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
