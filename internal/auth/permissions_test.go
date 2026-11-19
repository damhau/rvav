package auth

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/store"
)

var update = flag.Bool("update", false, "rewrite testdata/roles.json from the catalogue and the built-in roles")

// was is a principal as the checks before ADR-0034 saw it: the built-in
// roles held for all of rvav, and the strongest one held in each project.
type was struct {
	global   []string
	projects map[string]string
}

var rank = map[string]int{"viewer": 1, "member": 2, "project-admin": 3}

func (w was) has(role string) bool { return slices.Contains(w.global, role) }
func (w was) isAdmin() bool        { return w.has("admin") }
func (w was) seesEverything() bool { return w.isAdmin() || w.has("auditor") }

// projectRole: administrators acted as project admins everywhere.
func (w was) projectRole(id string) string {
	if w.isAdmin() {
		return "project-admin"
	}
	return w.projects[id]
}

// legacy is the check before ADR-0034 for each permission, from the code
// its appendix lists (the predicates, since removed): the built-in roles
// must grant exactly what they allowed.
var legacy = map[Permission]func(w was, projectID string) bool{
	ProjectSee: func(w was, id string) bool { return w.seesEverything() || w.projects[id] != "" },
	// Every member saw every deployment then: whoever sees all of them now
	// under owner_only (ADR-0045).
	DeploymentsReadAll: seesOrProjectAdmin,
	RequestSubmit: func(w was, id string) bool {
		r := w.projectRole(id)
		return r == "member" || r == "project-admin"
	},
	RequestOnBehalf:      isProjectAdmin,
	RequestManage:        isProjectAdmin,
	ProjectMembersManage: isProjectAdmin,
	ApprovalProject:      func(w was, id string) bool { return w.projects[id] == "project-admin" },
	ProjectSettingsRead:  seesOrProjectAdmin,
	AuditRead:            seesOrProjectAdmin,
	// Project admins write their projects' policies (ADR-0045).
	ProjectPolicies:     isProjectAdmin,
	ProjectUpdate:       admin,
	ProjectQuotasManage: admin,
	PlacementManage:     admin,

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
	CatalogManage:        func(w was, _ string) bool { return w.isAdmin() || w.has("catalog-admin") },
}

func isProjectAdmin(w was, id string) bool { return w.projectRole(id) == "project-admin" }
func seesOrProjectAdmin(w was, id string) bool {
	return w.seesEverything() || isProjectAdmin(w, id)
}
func sees(w was, _ string) bool  { return w.seesEverything() }
func admin(w was, _ string) bool { return w.isAdmin() }

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
				p := was{global: global, projects: map[string]string{}}
				var as []Assignment
				for _, r := range global {
					as = append(as, Assignment{Role: r})
				}
				for _, r := range inA {
					as = append(as, Assignment{Role: r, ProjectID: a})
					if rank[r] > rank[p.projects[a]] {
						p.projects[a] = r
					}
				}
				if viewsB {
					as = append(as, Assignment{Role: "viewer", ProjectID: b})
					p.projects[b] = "viewer"
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

// TestSupervisor: a supervisor sees the project and every deployment of it,
// and is one of its approvers, and nothing else (ADR-0041, ADR-0045).
func TestSupervisor(t *testing.T) {
	g := Compute([]Assignment{{Role: "supervisor", ProjectID: "prj_a"}}, BuiltinRole)
	for _, c := range Catalogue {
		want := c.ID == ProjectSee || c.ID == DeploymentsReadAll || c.ID == ApprovalProject
		if got := g.In(c.ID, "prj_a"); got != want {
			t.Errorf("a supervisor holds %s: %v", c.ID, got)
		}
		if g.In(c.ID, "prj_b") {
			t.Errorf("a supervisor holds %s outside their project", c.ID)
		}
	}
	for _, group := range Day2Groups {
		if g.RunsDay2(group, "power_on", "prj_a") {
			t.Errorf("a supervisor runs day-2 %s", group)
		}
	}
	if r := mustBuiltin(t, "supervisor"); r.Global || !r.Assignable("prj_a") || r.Assignable("") || ValidRoleKey("supervisor") {
		t.Errorf("the supervisor is given in a project only, and no custom role takes its key: %+v", r)
	}
}

func TestValidRoleKey(t *testing.T) {
	for key, want := range map[string]bool{"vm-operator": true, "read-only": true, "admin": false, "member": false,
		"user": false, "owner": false, "new": false, "Operator": false, "o": false, "vm operator": false} {
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

// TestRolesFixture keeps testdata/roles.json current: the catalogue and the
// built-in roles' permissions. The mocks hold the same and are checked
// against it (web/src/mocks/roles.test.ts), so the two can't drift. A change
// shows here: rewrite the file with -update and read what changed.
func TestRolesFixture(t *testing.T) {
	type permission struct {
		ID      Permission `json:"id"`
		Scope   string     `json:"scope"`
		Area    string     `json:"area"`
		Summary string     `json:"summary"`
	}
	type role struct {
		Key         string       `json:"key"`
		Name        string       `json:"name"`
		Scope       string       `json:"scope"`
		Permissions []Permission `json:"permissions"`
	}
	scope := func(rvav bool) string { return map[bool]string{true: "rvav", false: "project"}[rvav] }
	var fixture struct {
		Catalogue  []permission `json:"catalogue"`
		Day2Groups []string     `json:"day2_groups"`
		Roles      []role       `json:"roles"`
	}
	for _, c := range Catalogue {
		fixture.Catalogue = append(fixture.Catalogue, permission{c.ID, scope(!c.Project), c.Area, c.Summary})
	}
	fixture.Day2Groups = Day2Groups
	for _, r := range BuiltinRoles {
		fixture.Roles = append(fixture.Roles, role{r.Key, r.Name, scope(r.Global), append([]Permission{}, r.Permissions...)})
	}
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(fixture); err != nil {
		t.Fatal(err)
	}
	const path = "testdata/roles.json"
	if *update {
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("%s isn't the catalogue and the built-in roles: run go test ./internal/auth -run TestRolesFixture -update and read the change", path)
	}
}

// TestPrincipalRoles: the role keys a principal holds, for CEL and /me, and
// those that grant nothing where they are given.
func TestPrincipalRoles(t *testing.T) {
	operator := Role{Key: "vm-operator", Name: "VM operator", Permissions: []Permission{ProjectSee, Day2Action(day2.GroupPower, "power_on")}}
	p := newPrincipal(store.User{ID: "usr_a"}, nil, []Assignment{
		{Role: "user"}, {Role: "auditor"}, {Role: "member", ProjectID: "prj_a"}, {Role: "viewer", ProjectID: "prj_a"},
		{Role: "vm-operator", ProjectID: "prj_a"}, {Role: "vm-operator"}, {Role: "auditor"},
		{Role: "admin", ProjectID: "prj_b"}, {Role: "member"}, {Role: "gone", ProjectID: "prj_c"},
	}, map[string]Role{"vm-operator": operator})
	if !slices.Equal(p.Roles, []string{"user", "auditor", "vm-operator"}) {
		t.Errorf("roles for all of rvav: %v", p.Roles)
	}
	if got := p.RolesIn("prj_a"); !slices.Equal(got, []string{"member", "viewer", "vm-operator"}) {
		t.Errorf("roles in A: %v", got)
	}
	if p.RolesIn("prj_b") != nil || p.RolesIn("prj_c") != nil {
		t.Errorf("roles in B %v, C %v", p.RolesIn("prj_b"), p.RolesIn("prj_c"))
	}
	if r, ok := p.Role("vm-operator"); !ok || r.Name != "VM operator" {
		t.Errorf("the operator's role: %+v", r)
	}
	if !p.RunsDay2(day2.GroupPower, "power_on", "prj_z") || p.In(RequestSubmit, "prj_z") || !p.In(RequestSubmit, "prj_a") {
		t.Error("the grants aren't the roles'")
	}
	if s := System(store.User{ID: "usr_rvav"}, "admin"); !s.Everywhere(AccessManage) || !slices.Equal(s.Roles, []string{"admin"}) {
		t.Errorf("rvav's own principal: %v", s.Roles)
	}
}
