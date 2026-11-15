package auth

import (
	"regexp"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/day2"
)

// legacy is today's check for each permission, from the code ADR-0034's
// appendix lists: the built-in roles must grant exactly what they allow.
var legacy = map[Permission]func(p *Principal, projectID string) bool{
	ProjectSee:           (*Principal).CanSeeProject,
	RequestSubmit:        (*Principal).CanRequest,
	RequestOnBehalf:      isProjectAdmin,
	RequestManage:        isProjectAdmin,
	ProjectMembersManage: isProjectAdmin,
	ApprovalProject:      func(p *Principal, id string) bool { return p.ProjectRoles[id] == "project-admin" },
	ProjectSettingsRead:  seesOrProjectAdmin,
	AuditRead:            seesOrProjectAdmin,
	ProjectUpdate:        admin,
	ProjectQuotasManage:  admin,
	PlacementManage:      admin,

	ApprovalReadAll:      sees,
	EnvironmentsRead:     sees,
	AccessRead:           sees,
	InfrastructureRead:   sees,
	PoliciesRead:         sees,
	ExtensibilityRead:    sees,
	InventoryRead:        sees,
	ProjectCreate:        admin,
	EnvironmentsManage:   admin,
	AccessManage:         admin,
	InfrastructureManage: admin,
	IntegrationsManage:   admin,
	PoliciesManage:       admin,
	ExtensibilityManage:  admin,
	ImportManage:         admin,
	SystemManage:         admin,
	TokensManage:         admin,
	CatalogManage:        func(p *Principal, _ string) bool { return p.IsAdmin() || p.HasGlobal("catalog-admin") },
}

func isProjectAdmin(p *Principal, id string) bool { return p.ProjectRole(id) == "project-admin" }
func seesOrProjectAdmin(p *Principal, id string) bool {
	return p.SeesEverything() || isProjectAdmin(p, id)
}
func sees(p *Principal, _ string) bool  { return p.SeesEverything() }
func admin(p *Principal, _ string) bool { return p.IsAdmin() }

// subsets lists every subset of the roles.
func subsets(roles []string) [][]string {
	out := [][]string{{}}
	for _, r := range roles {
		for _, s := range out {
			out = append(out, append(slices.Clone(s), r))
		}
	}
	return out
}

// TestBuiltinRolesGrantTodaysChecks holds every built-in role in every
// combination: rvav-wide roles, project roles held directly and through
// groups at once in one project (the strongest counts today), a viewer in
// another, and a project the principal isn't in.
func TestBuiltinRolesGrantTodaysChecks(t *testing.T) {
	for _, c := range Catalogue {
		if legacy[c.ID] == nil {
			t.Fatalf("%s has no check of today to compare with", c.ID)
		}
	}
	const a, b, none = "prj_a", "prj_b", "prj_none"
	for _, global := range subsets([]string{"admin", "catalog-admin", "auditor", "user"}) {
		for _, inA := range subsets([]string{"viewer", "member", "project-admin"}) {
			for _, viewsB := range []bool{false, true} {
				p := &Principal{GlobalRoles: global, ProjectRoles: map[string]string{}}
				var as []Assignment
				for _, r := range global {
					as = append(as, Assignment{Role: r})
				}
				for _, r := range inA {
					as = append(as, Assignment{Role: r, ProjectID: a})
					if roleRank[r] > roleRank[p.ProjectRoles[a]] {
						p.ProjectRoles[a] = r
					}
				}
				if viewsB {
					as = append(as, Assignment{Role: "viewer", ProjectID: b})
					p.ProjectRoles[b] = "viewer"
				}
				g := Compute(as, BuiltinRole)
				for _, c := range Catalogue {
					check := legacy[c.ID]
					for _, id := range []string{a, b, none} {
						if got, want := g.In(c.ID, id), check(p, id); got != want {
							t.Errorf("%v, %v in A, viewer in B %v: %s in %s is %v, today %v", global, inA, viewsB, c.ID, id, got, want)
						}
					}
					if c.Project {
						var want []string
						if !check(p, none) {
							want = []string{}
							for _, id := range []string{a, b} {
								if check(p, id) {
									want = append(want, id)
								}
							}
						}
						if got := g.Projects(c.ID); !slices.Equal(got, want) || (got == nil) != (want == nil) {
							t.Errorf("%v, %v in A, viewer in B %v: %s held in %v, today %v", global, inA, viewsB, c.ID, got, want)
						}
					} else if got, want := g.Everywhere(c.ID), check(p, ""); got != want {
						t.Errorf("%v, %v in A: %s for all of rvav is %v, today %v", global, inA, c.ID, got, want)
					}
				}
				for _, group := range Day2Groups {
					if g.RunsDay2(group, "power_on", a) {
						t.Errorf("%v, %v in A: a built-in role grants day-2 %s, which only policies do today", global, inA, group)
					}
				}
			}
		}
	}
}

func TestCatalogue(t *testing.T) {
	id := regexp.MustCompile(`^[a-z][a-z_]*(\.[a-z][a-z_]*)+$`)
	seen := map[Permission]bool{}
	for _, c := range Catalogue {
		if !id.MatchString(string(c.ID)) || seen[c.ID] || c.Area == "" || c.Summary == "" {
			t.Errorf("%+v: a unique dotted identifier with an area and a summary", c)
		}
		seen[c.ID] = true
		if i, ok := Info(c.ID); !ok || i != c {
			t.Errorf("Info(%s) = %+v, %v", c.ID, i, ok)
		}
	}
	for _, r := range BuiltinRoles {
		for _, p := range r.Permissions {
			if !seen[p] {
				t.Errorf("%s grants %s, which isn't in the catalogue", r.Key, p)
			}
		}
	}
}

func TestDay2Permissions(t *testing.T) {
	for p, want := range map[Permission]bool{
		Day2(day2.GroupPower):                                       true,
		Day2Action(day2.GroupPower, "power_on"):                     true,
		Day2Action(day2.GroupAutomation, "awx.restart-web"):         true,
		Day2Action(day2.GroupAutomation, day2.WorkflowKey("patch")): true,
		"day2.power:":           false,
		"day2.power:Power On":   false,
		"day2.billing":          false,
		"day2.billing:power_on": false,
		"day2":                  false,
		"project.delete":        false,
	} {
		i, ok := Info(p)
		if ok != want || ok && !i.Project {
			t.Errorf("Info(%q) = %+v, %v; want known %v and given in a project", p, i, ok, want)
		}
	}
}

func TestAssignable(t *testing.T) {
	operator := Role{Key: "vm-operator", Permissions: []Permission{ProjectSee, Day2Action(day2.GroupPower, "power_on"),
		Day2Action(day2.GroupPower, "power_off")}}
	reader := Role{Key: "policy-reader", Permissions: []Permission{PoliciesRead}}
	for _, c := range []struct {
		role    Role
		project string
		want    bool
	}{
		{mustBuiltin(t, "admin"), "", true},
		{mustBuiltin(t, "admin"), "prj_a", false},
		{mustBuiltin(t, "auditor"), "prj_a", false},
		{mustBuiltin(t, "member"), "prj_a", true},
		{mustBuiltin(t, "member"), "", false},
		{operator, "prj_a", true},
		{operator, "", true},
		{reader, "", true},
		{reader, "prj_a", false},
	} {
		if got := c.role.Assignable(c.project); got != c.want {
			t.Errorf("%s given in %q: %v, want %v", c.role.Key, c.project, got, c.want)
		}
	}
}

// TestCustomRoles holds A1's acceptance roles (#109): an operator who may
// only power a project's VMs on and off, and an auditor made from the
// built-in auditor's permissions.
func TestCustomRoles(t *testing.T) {
	operator := Role{Key: "vm-operator", Permissions: []Permission{ProjectSee, Day2Action(day2.GroupPower, "power_on"),
		Day2Action(day2.GroupPower, "power_off")}}
	auditor := Role{Key: "read-only", Permissions: mustBuiltin(t, "auditor").Permissions}
	roles := func(key string) (Role, bool) {
		for _, r := range []Role{operator, auditor} {
			if r.Key == key {
				return r, true
			}
		}
		return BuiltinRole(key)
	}

	g := Compute([]Assignment{{Role: "vm-operator", ProjectID: "prj_a"}}, roles)
	if !g.RunsDay2(day2.GroupPower, "power_on", "prj_a") || !g.RunsDay2(day2.GroupPower, "power_off", "prj_a") {
		t.Error("the operator can't power on and off in their project")
	}
	if g.RunsDay2(day2.GroupPower, "reset", "prj_a") || g.RunsDay2(day2.GroupPower, "power_on", "prj_b") ||
		g.In(RequestSubmit, "prj_a") || !g.In(ProjectSee, "prj_a") || g.In(ProjectSee, "prj_b") {
		t.Error("the operator holds more than their role")
	}

	custom := Compute([]Assignment{{Role: "read-only"}}, roles)
	builtin := Compute([]Assignment{{Role: "auditor"}}, BuiltinRole)
	for _, c := range Catalogue {
		if custom.In(c.ID, "prj_a") != builtin.In(c.ID, "prj_a") || custom.Everywhere(c.ID) != builtin.Everywhere(c.ID) {
			t.Errorf("the custom auditor and the built-in one differ on %s", c.ID)
		}
	}

	everywhere := Compute([]Assignment{{Role: "project-admin-everywhere"}}, func(string) (Role, bool) {
		return Role{Key: "project-admin-everywhere", Permissions: mustBuiltin(t, "project-admin").Permissions}, true
	})
	if everywhere.In(ApprovalProject, "prj_a") || !everywhere.In(RequestManage, "prj_a") {
		t.Error("approval.project counts from a role given for all of rvav")
	}

	if g := Compute([]Assignment{{Role: "gone", ProjectID: "prj_a"}, {Role: "member"}}, BuiltinRole); g.In(ProjectSee, "prj_a") {
		t.Error("a role rvav doesn't know, or given where it can't be, grants something")
	}
}

func TestValidRoleKey(t *testing.T) {
	for key, want := range map[string]bool{"vm-operator": true, "read-only": true, "admin": false, "member": false,
		"user": false, "owner": false, "Operator": false, "o": false, "vm operator": false} {
		if ValidRoleKey(key) != want {
			t.Errorf("ValidRoleKey(%q) = %v", key, !want)
		}
	}
}

func mustBuiltin(t *testing.T, key string) Role {
	t.Helper()
	r, ok := BuiltinRole(key)
	if !ok {
		t.Fatalf("no built-in role %s", key)
	}
	return r
}
