// Package seed loads the example organisation used for development, demos
// and tests: users, projects, placement, policies and the catalog. It is
// idempotent; running it again refreshes configuration and catalog forms
// without touching requests or deployments.
package seed

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/store"
)

func j(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type user struct {
	name, display, email, title string
	roles                       []string
}

var users = []user{
	{"lmartin", "Léa Martin", "lea.martin@corp.example", "Platform engineer", []string{"user"}},
	{"mkeller", "Marc Keller", "marc.keller@corp.example", "Infrastructure lead", []string{"user", "admin"}},
	{"tnguyen", "Thomas Nguyen", "thomas.nguyen@corp.example", "Developer", []string{"user"}},
	{"sbrunner", "Sophie Brunner", "sophie.brunner@corp.example", "DBA", []string{"user"}},
	{"afavre", "Alain Favre", "alain.favre@corp.example", "Network engineer", []string{"user"}},
	{"cdubois", "Claire Dubois", "claire.dubois@corp.example", "Head of infrastructure", []string{"user"}},
	// The account AWX's inventory plugin signs in with (an API token).
	{"svc-awx", "AWX inventory", "awx@corp.example", "Service account", []string{"auditor"}},
}

// userGroups are the OIDC groups seeded users are in, as in the Keycloak
// realm (deploy/keycloak), so dev authentication sees the same approvers.
var userGroups = []struct{ user, group string }{
	{"lmartin", "rvav-web-admins"},
	{"lmartin", "rvav-catalog-admins"},
	{"mkeller", "rvav-admins"},
	{"mkeller", "rvav-infra-leads"},
	{"tnguyen", "rvav-web"},
	{"sbrunner", "rvav-data"},
	{"afavre", "rvav-core"},
	{"afavre", "rvav-network"},
	{"cdubois", "rvav-infra-heads"},
}

// UserID returns the ID of a seeded user, for example UserID("lmartin").
func UserID(name string) string { return "usr_" + name }

// ProjectID returns the ID of a seeded project by code.
func ProjectID(code string) string { return "prj_" + code }

type project struct {
	code, name, description, costCenter string
	quota                               [4]int // vcpu, memory_gb, storage_gb, vm_count
}

var projects = []project{
	{"web", "Web Platform", "Public websites, APIs and the checkout service.", "CC-4410", [4]int{160, 640, 12288, 60}},
	{"data", "Data Engineering", "Data platform, warehouses and pipelines.", "CC-4520", [4]int{256, 2048, 61440, 40}},
	{"fin", "Finance Apps", "Ledger, payments and reporting.", "CC-3100", [4]int{96, 384, 8192, 30}},
	{"core", "Infra Core", "Shared infrastructure services.", "CC-1000", [4]int{128, 512, 20480, 80}},
}

var members = []struct{ project, user, role string }{
	{"web", "lmartin", "project-admin"},
	{"web", "tnguyen", "member"},
	{"data", "sbrunner", "project-admin"},
	{"fin", "sbrunner", "member"},
	{"core", "afavre", "project-admin"},
}

var groups = []struct{ group, project, role string }{
	{"rvav-admins", "", "admin"},
	{"rvav-auditors", "", "auditor"},
	{"rvav-catalog-admins", "", "catalog-admin"},
	{"rvav-web-admins", "web", "project-admin"},
	{"rvav-web", "web", "member"},
	{"rvav-data", "data", "member"},
	{"rvav-fin", "fin", "member"},
	{"rvav-core", "core", "member"},
}

var subnets = []store.NetworkSubnet{
	{Network: "dvpg-app-dev-vl310", Subnet: "10.31.10.0/24", Gateway: "10.31.10.1"},
	{Network: "dvpg-db-dev-vl320", Subnet: "10.32.20.0/24", Gateway: "10.32.20.1"},
	{Network: "dvpg-app-tst-vl410", Subnet: "10.41.10.0/24", Gateway: "10.41.10.1"},
	{Network: "dvpg-web-prd-vl210", Subnet: "10.21.10.0/24", Gateway: "10.21.10.1"},
	{Network: "dvpg-app-prd-vl220", Subnet: "10.22.20.0/24", Gateway: "10.22.20.1"},
	{Network: "dvpg-db-prd-vl230", Subnet: "10.22.30.0/25", Gateway: "10.22.30.1"},
}

func placements(code string) []store.PlacementRule {
	prdNetworks := []string{"dvpg-web-prd-*", "dvpg-app-prd-*"}
	if code == "data" || code == "fin" {
		prdNetworks = []string{"dvpg-db-prd-*", "dvpg-app-prd-*"}
	}
	return []store.PlacementRule{
		{Environment: "dev", ClusterPatterns: []string{"GVA-DEV-*"}, DatastorePatterns: []string{"vsan-gva-dev-*", "nfs-gva-bronze-*"},
			NetworkPatterns: []string{"dvpg-*-dev-*"}, Folder: "/GVA/vm/rvav/" + code + "/dev"},
		{Environment: "tst", ClusterPatterns: []string{"GVA-TST-*"}, DatastorePatterns: []string{"vsan-gva-tst-*"},
			NetworkPatterns: []string{"dvpg-*-tst-*"}, Folder: "/GVA/vm/rvav/" + code + "/tst"},
		{Environment: "prd", ClusterPatterns: []string{"GVA-PRD-*"}, DatastorePatterns: []string{"vsan-gva-prd-*", "nfs-gva-silver-*"},
			NetworkPatterns: prdNetworks, Folder: "/GVA/vm/rvav/" + code + "/prd"},
	}
}

// Run loads the seed data in one transaction.
func Run(ctx context.Context, s *store.Store) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		exec := func(sql string, args ...any) error {
			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				return fmt.Errorf("%w\n%s", err, sql)
			}
			return nil
		}
		for i, e := range []struct {
			key, name          string
			prod               bool
			snapshots, maxDays int
		}{{"dev", "Development", false, 3, 7}, {"tst", "Test", false, 3, 7}, {"prd", "Production", true, 2, 3}} {
			if err := exec(`insert into environments (key, name, production, sort_order, snapshot_max_count, snapshot_max_age_days)
				values ($1,$2,$3,$4,$5,$6)
				on conflict (key) do update set name = excluded.name, production = excluded.production, sort_order = excluded.sort_order,
				snapshot_max_count = excluded.snapshot_max_count, snapshot_max_age_days = excluded.snapshot_max_age_days`,
				e.key, e.name, e.prod, i, e.snapshots, e.maxDays); err != nil {
				return err
			}
		}
		for _, u := range users {
			if err := exec(`insert into users (id, subject, name, email, title, global_roles) values ($1,$2,$3,$4,$5,$6)
				on conflict (id) do update set global_roles = excluded.global_roles, title = excluded.title`,
				UserID(u.name), "seed:"+u.name, u.display, u.email, u.title, u.roles); err != nil {
				return err
			}
		}
		for _, p := range projects {
			if err := exec(`insert into projects (id, code, name, description, cost_center, environments) values ($1,$2,$3,$4,$5,$6)
				on conflict (id) do update set name = excluded.name, description = excluded.description,
				cost_center = excluded.cost_center, environments = excluded.environments`,
				ProjectID(p.code), p.code, p.name, p.description, p.costCenter, []string{"dev", "tst", "prd"}); err != nil {
				return err
			}
			for i, res := range []string{"vcpu", "memory_gb", "storage_gb", "vm_count"} {
				if err := exec(`insert into quotas (project_id, environment, resource, limit_value) values ($1,'',$2,$3)
					on conflict (project_id, environment, resource) do update set limit_value = excluded.limit_value`,
					ProjectID(p.code), res, p.quota[i]); err != nil {
					return err
				}
			}
			for _, r := range placements(p.code) {
				if err := exec(`insert into placement_rules (id, project_id, environment, priority, cluster_patterns,
					datastore_patterns, network_patterns, folder) values ($1,$2,$3,100,$4,$5,$6,$7)
					on conflict (id) do update set cluster_patterns = excluded.cluster_patterns,
					datastore_patterns = excluded.datastore_patterns, network_patterns = excluded.network_patterns, folder = excluded.folder`,
					"plc_"+p.code+"_"+r.Environment, ProjectID(p.code), r.Environment, r.ClusterPatterns,
					r.DatastorePatterns, r.NetworkPatterns, r.Folder); err != nil {
					return err
				}
			}
		}
		for _, m := range members {
			if err := exec(`insert into project_members (project_id, user_id, role) values ($1,$2,$3)
				on conflict (project_id, user_id) do update set role = excluded.role`,
				ProjectID(m.project), UserID(m.user), m.role); err != nil {
				return err
			}
		}
		for _, g := range groups {
			var project *string
			if g.project != "" {
				id := ProjectID(g.project)
				project = &id
			}
			if err := exec(`insert into group_mappings (oidc_group, project_id, role) values ($1,$2,$3) on conflict do nothing`,
				g.group, project, g.role); err != nil {
				return err
			}
		}
		for _, g := range userGroups {
			if err := exec(`insert into user_groups (user_id, group_name) values ($1,$2) on conflict do nothing`,
				UserID(g.user), g.group); err != nil {
				return err
			}
		}
		for _, n := range subnets {
			if err := exec(`insert into network_subnets (network, subnet, gateway, dns_zone, dns_servers) values ($1,$2,$3,'corp.example',$4)
				on conflict (network) do update set subnet = excluded.subnet, gateway = excluded.gateway`,
				n.Network, n.Subnet, n.Gateway, []string{"10.10.0.53", "10.10.1.53"}); err != nil {
				return err
			}
		}
		for _, l := range []struct {
			env        string
			def, max   *int
			extensions int
			action     string
		}{{"dev", ptr(30), ptr(90), 2, "power_off_then_delete"}, {"tst", ptr(30), ptr(90), 2, "power_off_then_delete"}, {"prd", nil, nil, 0, "notify_only"}} {
			if err := exec(`insert into lease_policies (environment, default_days, max_days, max_extensions, expiry_action, grace_days)
				values ($1,$2,$3,$4,$5,7) on conflict (environment, (coalesce(project_id, ''))) do update set default_days = excluded.default_days,
				max_days = excluded.max_days, max_extensions = excluded.max_extensions, expiry_action = excluded.expiry_action,
				grace_days = excluded.grace_days`,
				l.env, l.def, l.max, l.extensions, l.action); err != nil {
				return err
			}
		}
		if err := seedPolicies(exec); err != nil {
			return err
		}
		return seedCatalog(exec)
	})
}

func ptr[T any](v T) *T { return &v }

func seedPolicies(exec func(string, ...any) error) error {
	// Infrastructure leads have two days; then the head of infrastructure is
	// asked too, for two more days, before the request is rejected.
	leads := j([]map[string]any{{"name": "Infrastructure leads", "group": "rvav-infra-leads", "approvers": []string{UserID("mkeller")},
		"mode": "any", "timeout": "48h", "on_timeout": "escalate",
		"escalate_to": map[string]any{"group": "rvav-infra-heads", "approvers": []string{UserID("cdubois")}}}})
	network := j([]map[string]any{
		{"name": "Network engineers", "group": "rvav-network", "approvers": []string{UserID("afavre")}, "mode": "any"},
		{"name": "Network security", "approvers": []string{UserID("mkeller")}, "mode": "any"},
	})
	policies := []struct {
		id, name, reason, condition, itemType string
		levels                                []byte
		priority                              int
		autoApprove                           *string
	}{
		{"pol_prd_vm", "Production VMs", "Production VMs need sign-off", `environment == "prd"`, "vm_blueprint", leads, 100,
			ptr(`"rvav-infra-leads" in user.groups`)},
		{"pol_prd_big_vm", "Large production VMs", "Production VM above 32 GB RAM", `environment == "prd" && vm.memory_gb > 32`, "vm_blueprint", leads, 50, nil},
		{"pol_prd_db", "Production databases", "Production database clusters always need approval", `item.category == "databases" && environment == "prd"`, "composite", leads, 100, nil},
		{"pol_fw", "Firewall flows", "Firewall changes are reviewed by network security", `true`, "awx_workflow", network, 100, nil},
		{"pol_soft_quota", "Over a soft quota", "Over the project's soft quota", `quota.over_soft`, "vm_blueprint", leads, 60, nil},
	}
	for _, p := range policies {
		if err := exec(`insert into approval_policies (id, name, reason, condition, item_type, levels, priority, auto_approve) values ($1,$2,$3,$4,$5,$6,$7,$8)
			on conflict (id) do update set name = excluded.name, reason = excluded.reason, condition = excluded.condition,
			item_type = excluded.item_type, levels = excluded.levels, priority = excluded.priority, auto_approve = excluded.auto_approve`,
			p.id, p.name, p.reason, p.condition, p.itemType, p.levels, p.priority, p.autoApprove); err != nil {
			return err
		}
	}
	return seedDay2Policies(exec, leads)
}

// seedDay2Policies: members operate their VMs; only owners and project
// admins delete or hand a deployment over, and deleting in production needs
// the Infrastructure leads. Viewers match nothing, so they are refused.
func seedDay2Policies(exec func(string, ...any) error, leads []byte) error {
	handover := []string{"delete", "change_owner", "change_project"}
	for _, p := range []struct {
		id, name, reason string
		priority         int
		actions, roles   []string
		condition        string
		effect           string
		levels           []byte
	}{
		{"d2p_admins", "Administrators", "", 10, []string{"*"}, []string{"admin"}, "true", "allow", nil},
		{"d2p_prd_delete", "Deleting in production", "Deleting a production deployment needs sign-off", 20,
			[]string{"delete"}, []string{"owner", "project-admin"}, "environment.production", "approval", leads},
		{"d2p_soft_quota_resize", "Over a soft quota", "Over the project's soft quota", 25,
			[]string{"resize"}, []string{"owner", "member", "project-admin"}, "quota.over_soft", "approval", leads},
		{"d2p_soft_quota_move", "Over a soft quota", "Over the new project's soft quota", 26,
			[]string{"change_project"}, []string{"owner", "project-admin"}, "quota.over_soft", "approval", leads},
		{"d2p_handover", "Owners and project admins", "", 30, handover, []string{"owner", "project-admin"}, "true", "allow", nil},
		{"d2p_handover_others", "Not the owner", "Only the owner and the project admins can delete this deployment or hand it over.", 40,
			handover, nil, "true", "deny", nil},
		{"d2p_members", "Members operate their VMs", "", 50, []string{"*"}, []string{"owner", "member", "project-admin"}, "true", "allow", nil},
	} {
		levels := p.levels
		if levels == nil {
			levels = []byte("[]")
		}
		if err := exec(`insert into day2_policies (id, name, reason, priority, actions, roles, condition, effect, levels)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			on conflict (id) do update set name = excluded.name, reason = excluded.reason, priority = excluded.priority,
			actions = excluded.actions, roles = excluded.roles, condition = excluded.condition, effect = excluded.effect,
			levels = excluded.levels`,
			p.id, p.name, p.reason, p.priority, p.actions, nonNil(p.roles), p.condition, p.effect, levels); err != nil {
			return err
		}
	}
	return nil
}
