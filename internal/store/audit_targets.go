package store

import (
	"context"
	"fmt"
	"strings"
)

// auditTargets reads the name of an audit event's target, by its type: a
// query taking the target's ID. Types left out (settings, integrations,
// assignments) have no name rvav looks up, and no record to link to.
var auditTargets = []struct{ typ, name string }{
	{"deployment", "select name from deployments where id = %s"},
	{"request", "select number from requests where id = %s"},
	{"approval", "select r.number || ' · ' || x.name from approvals x join requests r on r.id = x.request_id where x.id = %s"},
	{"project", "select name from projects where id = %s"},
	{"catalog_item", "select name from catalog_items where id = %s"},
	{"role", "select name from roles where key = %s"},
	{"user", "select name from users where id = %s"},
	{"api_token", "select name from api_tokens where id = %s"},
	{"environment", "select name from environments where key = %s"},
	// An environment's lease, audited by the environment's key.
	{"lease_policy", "select name from environments where key = %s"},
	{"subscription", "select name from subscriptions where id = %s"},
	{"schedule", "select name from schedules where id = %s and deleted_at is null"},
	{"onboarding_plan", "select name from onboarding_plans where id = %s"},
	{"workflow", "select title from workflow_registrations where key = %s"},
	{"resource_type", "select title from resource_types where key = %s"},
	{"form_library", "select name from form_library where key = %s order by version desc limit 1"},
	{"property_group", "select name from property_groups where key = %s order by version desc limit 1"},
	{"approval_policy", "select name from approval_policies where id = %s"},
	{"day2_policy", "select name from day2_policies where id = %s"},
	{"lease_rule", "select name from lease_rules where id = %s"},
	{"naming_policy", "select name from naming_policies where id = %s"},
	{"deployment_limit_policy", "select name from deployment_limit_policies where id = %s"},
	{"vm_template", "select label from vm_templates where key = %s"},
	{"size_preset", "select label from size_presets where key = %s"},
	{"ip_range", "select name from ip_ranges where id = %s"},
	{"audit_destination", "select name from audit_destinations where id = %s"},
}

// TargetName is what the audit log calls a target now, for a deletion to
// keep: "" for a type it doesn't name, a target gone, or a failed read,
// which leaves the event the name an earlier one recorded.
func (s *Store) TargetName(ctx context.Context, typ, id string) string {
	for _, t := range auditTargets {
		if t.typ != typ {
			continue
		}
		var name *string
		if err := s.Pool.QueryRow(ctx, fmt.Sprintf(t.name, "$1"), id).Scan(&name); err != nil || name == nil {
			return ""
		}
		return *name
	}
	return ""
}

// targetNameSQL is the SQL for the current name of the target of the
// given type and ID expressions, null for a type without one or a target
// gone.
func targetNameSQL(typ, id string) string {
	var b strings.Builder
	b.WriteString("case " + typ)
	for _, t := range auditTargets {
		fmt.Fprintf(&b, " when '%s' then (%s)", t.typ, fmt.Sprintf(t.name, id))
	}
	b.WriteString(" end")
	return b.String()
}

// auditTargetKept is the SQL that says whether rvav still keeps the target
// of an audit row aliased a, whose current name is in t.live: null for a
// type rvav doesn't keep as a record of its own, or no target at all, as a
// failed sign-in by nobody known has.
func auditTargetKept() string {
	return "case when a.target_type in (" + auditTargetTypes() + ") and a.target_id <> '' then t.live is not null end"
}

// auditTargetTypes lists the types in auditTargets, as SQL literals.
func auditTargetTypes() string {
	types := make([]string, len(auditTargets))
	for i, t := range auditTargets {
		types[i] = "'" + t.typ + "'"
	}
	return strings.Join(types, ", ")
}

// auditRowColumns are the columns of an AuditRow, from audit_events a with
// its actor u and project p.
var auditRowColumns = `a.id, a.at, a.actor_id, u.name as actor_name, a.action, a.target_type, a.target_id,
	coalesce(a.target_name, t.live) as target_name, ` + auditTargetKept() + ` as target_exists,
	a.project_id, p.code as project_code, p.name as project_name, a.detail`

// auditRowJoins joins an audit_events a to its actor, project and target.
// Each target's name is a query of its own: join them to a page of events,
// limited first, rather than to the whole log.
var auditRowJoins = `left join users u on u.id = a.actor_id left join projects p on p.id = a.project_id
	left join lateral (select ` + targetNameSQL("a.target_type", "a.target_id") + ` as live) t on true`

// auditNameAtWrite is the name a new event's target gets: the one the
// caller gives ($8), else the target's own, else the one the last event on
// it recorded, for a target deleted before its event is written. Only a
// record rvav names has such events: a failed sign-in by nobody known, a
// user with no ID, has none, and looking would read every one of them, as
// many as a flood of them leaves.
var auditNameAtWrite = `coalesce(nullif($8, ''), ` + targetNameSQL("$3", "$4") + `,
	case when $4 <> '' and $3 in (` + auditTargetTypes() + `) then
	(select o.target_name from audit_events o where o.target_type = $3 and o.target_id = $4 and o.target_name is not null
	 order by o.id desc limit 1) end)`
