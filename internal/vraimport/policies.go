package vraimport

import (
	"encoding/json"
	"fmt"
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
	Notes      []Note `json:"notes"`
}

// vRA's day-2 actions, by rvav's action keys.
var day2Keys = map[string]string{
	"Cloud.vSphere.Machine.PowerOn":         "power_on",
	"Cloud.vSphere.Machine.PowerOff":        "power_off",
	"Cloud.vSphere.Machine.Reboot":          "restart_guest",
	"Cloud.vSphere.Machine.Reset":           "reset",
	"Cloud.vSphere.Machine.Shutdown":        "shutdown_guest",
	"Cloud.vSphere.Machine.Resize":          "resize",
	"Cloud.vSphere.Machine.Snapshot.Create": "snapshot_create",
	"Cloud.vSphere.Machine.Snapshot.Delete": "snapshot_delete",
	"Cloud.vSphere.Machine.Snapshot.Revert": "snapshot_revert",
	"Cloud.vSphere.Machine.Remote.Console":  "console",
	"Cloud.vSphere.Machine.Add.Disk":        "disk_add",
	"Cloud.vSphere.Disk.Disk.Resize":        "disk_extend",
	"Cloud.vSphere.Machine.Add.Nic":         "nic_add",
	"Cloud.vSphere.Machine.Remove.Nic":      "nic_remove",
	"Deployment.Delete":                     "delete",
	"Deployment.ChangeOwner":                "change_owner",
	"Deployment.ChangeLease":                "extend_lease",
	"Deployment.ChangeProject":              "change_project",
	"Deployment.EditDeployment":             "rename",
	"Deployment.PowerOn":                    "power_on",
	"Deployment.PowerOff":                   "power_off",
}

// vRA's roles, as rvav's.
var roleKeys = map[string]string{
	"ROLE:project_member":        "member",
	"ROLE:project_administrator": "project-admin",
	"ROLE:project_viewer":        "viewer",
	"ROLE:project_supervisor":    "project-admin",
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
		if p.EnforcementType == "SOFT" {
			note(Warning, "enforcementType", "It is soft in vRA: other policies may override it. rvav's policies apply by priority.")
		}
		switch p.TypeID {
		case vra.PolicyApproval:
			pl.Type = "approval"
			planApproval(&pl, p, def, note, scope)
		case vra.PolicyLease:
			pl.Type = "lease"
			pl.Becomes = "The lease policy of " + scope + " (Administration › Policies › Leases), for each of its environments"
			if v, ok := num(def["leaseTermMax"]); ok {
				note(Info, "leaseTermMax", fmt.Sprintf("Maximum days: %d.", v))
			}
			if total, ok := num(def["leaseTotalTermMax"]); ok {
				if term, ok := num(def["leaseTermMax"]); ok && term > 0 {
					note(Info, "leaseTotalTermMax", fmt.Sprintf("%d days in all: at most %d extensions of %d days.", total, max(total/term-1, 0), term))
				} else {
					note(Warning, "leaseTotalTermMax", fmt.Sprintf("%d days in all: rvav counts extensions, not days in all.", total))
				}
			}
			if v, ok := num(def["leaseGrace"]); ok {
				note(Info, "leaseGrace", fmt.Sprintf("Grace days: %d, between the end of the lease and the deletion.", v))
			}
		case vra.PolicyDay2:
			pl.Type = "day2"
			planDay2(&pl, def, note, scope)
		case vra.PolicyQuota:
			pl.Type = "quota"
			pl.Becomes = "The quotas of " + scope + " (Administration › Projects › Quotas)"
			limits, _ := def["limits"].(map[string]any)
			level, _ := def["level"].(string)
			if level != "" && level != "project" {
				note(Warning, "level", "It limits each "+level+" in vRA; rvav's quotas are per project, and per environment.")
			}
			keys := []string{"instances", "cpu", "memory", "storage"}
			names := map[string]string{"instances": "VMs", "cpu": "vCPU", "memory": "Memory (GB)", "storage": "Storage (GB)"}
			for _, k := range keys {
				if v, ok := num(limits[k]); ok {
					note(Info, "limits."+k, fmt.Sprintf("%s: %d.", names[k], v))
				}
			}
		case vra.PolicyLimit:
			pl.Type = "limit"
			pl.Becomes = "The sizes of the items requested in " + scope
			note(Unmapped, "limits", "rvav has no limit per deployment: the sizes an item offers bound each request, and the project's quota what it holds in all.")
		default:
			pl.Type = "other"
			pl.Becomes = "Nothing"
			note(Unmapped, "typeId", p.TypeID+" has no rvav equivalent.")
		}
		out = append(out, pl)
	}
	return out
}

func planApproval(pl *PolicyPlan, p vra.Policy, def map[string]any, note func(string, string, string), scope string) {
	actions := strs(def["actions"])
	day2 := len(actions) > 0 && !contains(actions, "Deployment.Create")
	if day2 {
		pl.Becomes = "A day-2 policy with the effect approval, in " + scope + " (Administration › Policies › Day-2)"
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
		if len(actions) > 1 {
			var others []string
			for _, a := range actions {
				if a != "Deployment.Create" {
					others = append(others, a)
				}
			}
			note(Warning, "actions", "It also covers "+strings.Join(others, ", ")+": those need a day-2 policy with the effect approval.")
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
	var groups, users []string
	for _, a := range strs(def["approvers"]) {
		kind, name, ok := strings.Cut(a, ":")
		switch {
		case ok && strings.EqualFold(kind, "group"):
			groups = append(groups, name)
		case ok && strings.EqualFold(kind, "user"):
			users = append(users, name)
		default:
			users = append(users, a)
		}
	}
	who := strings.Join(append(groups, users...), ", ")
	note(Info, "approvers", fmt.Sprintf("Level %d: %s, %s.", max(level, 1), who, mode))
	if len(groups) > 0 {
		note(Warning, "approvers", "vRA names groups by email ("+strings.Join(groups, ", ")+"): give rvav the group's name in the identity provider.")
	}
	if days, ok := num(def["autoApprovalExpiry"]); ok && days > 0 {
		decision := "reject"
		if d, _ := def["autoApprovalDecision"].(string); d == "APPROVE" {
			decision = "approve"
		}
		note(Info, "autoApprovalExpiry", fmt.Sprintf("Unanswered after %dh: %s.", days*24, decision))
	}
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
