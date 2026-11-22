package auth

import (
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/day2"
)

// Permission is something a role allows (ADR-0034). Its identifier is
// stable: roles, the API and policies' CEL store it.
type Permission string

const (
	ProjectSee           Permission = "project.see"
	DeploymentsReadAll   Permission = "deployments.read_all"
	RequestSubmit        Permission = "request.submit"
	RequestOnBehalf      Permission = "request.on_behalf"
	RequestManage        Permission = "request.manage"
	ApprovalProject      Permission = "approval.project"
	ProjectMembersManage Permission = "project.members.manage"
	ProjectSettingsRead  Permission = "project.settings.read"
	ProjectUpdate        Permission = "project.update"
	ProjectQuotasManage  Permission = "project.quotas.manage"
	PlacementManage      Permission = "placement.manage"
	AuditRead            Permission = "audit.read"

	ApprovalReadAll      Permission = "approval.read_all"
	ProjectCreate        Permission = "project.create"
	EnvironmentsRead     Permission = "environments.read"
	EnvironmentsManage   Permission = "environments.manage"
	AccessRead           Permission = "access.read"
	AccessManage         Permission = "access.manage"
	CatalogManage        Permission = "catalog.manage"
	InfrastructureRead   Permission = "infrastructure.read"
	InfrastructureManage Permission = "infrastructure.manage"
	IntegrationsManage   Permission = "integrations.manage"
	PoliciesRead         Permission = "policies.read"
	PoliciesManage       Permission = "policies.manage"
	ProjectPolicies      Permission = "project.policies.manage"
	ExtensibilityRead    Permission = "extensibility.read"
	ExtensibilityManage  Permission = "extensibility.manage"
	ImportManage         Permission = "import.manage"
	SystemManage         Permission = "system.manage"
	TokensManage         Permission = "tokens.manage"
	InventoryRead        Permission = "inventory.read"
)

// PermissionInfo is a permission of the catalogue.
type PermissionInfo struct {
	ID Permission
	// Project: it can be granted in one project. Otherwise it is granted
	// for all of rvav only.
	Project bool
	// Area groups permissions in the role editor.
	Area    string
	Summary string
}

// Catalogue lists every permission but the day-2 ones (Day2, Day2Action).
var Catalogue = []PermissionInfo{
	{ProjectSee, true, "Projects", "See the project: its catalog, deployments, requests, members, quotas and reports, as they change."},
	{DeploymentsReadAll, true, "Projects", "See every deployment and request of the project, when its deployments show to their owners only."},
	{RequestSubmit, true, "Requests", "Request from the project's catalog and save drafts; own deployments there, and move one into it."},
	{RequestOnBehalf, true, "Requests", "Request for someone else."},
	{RequestManage, true, "Requests", "Cancel, retry and roll back anyone's request in the project."},
	{ApprovalProject, true, "Approvals", "Be one of the project's approvers, asked when a lease is extended beyond its policy; held only from a role given in the project."},
	{ProjectMembersManage, true, "Projects", "Add and remove the project's members, and change their roles."},
	{ProjectSettingsRead, true, "Projects", "See the project's administration: its placement, quotas and the policies that apply there; for all of rvav, every project's."},
	{ProjectUpdate, true, "Projects", "Change the project's details and environments, or archive it."},
	{ProjectQuotasManage, true, "Projects", "Set the project's quotas."},
	{PlacementManage, true, "Projects", "Change the project's placement rules and Active Directory OUs."},
	{AuditRead, true, "Audit", "Read the audit log: the project's events, or every event for all of rvav."},
	{ProjectPolicies, true, "Policies", "Write the project's own approval, day-2 and lease policies; a global hard policy still wins."},

	{ApprovalReadAll, false, "Approvals", "See every approval, and the questions and answers of composites' waits for a person."},
	{ProjectCreate, false, "Projects", "Create projects, and name their first project admins, themselves included."},
	{EnvironmentsRead, false, "Projects", "See the environments' settings."},
	{EnvironmentsManage, false, "Projects", "Add, change and remove environments."},
	{AccessRead, false, "Access", "See the roles and who holds them, groups of the identity provider included."},
	{AccessManage, false, "Access", "Make roles, and give them to users and groups: whoever holds it can give themselves any permission."},
	{CatalogManage, false, "Catalog", "Build, publish and open catalog items, with their categories, secrets, shared variables, Git repository and AWX templates; read what their steps were given and gave."},
	{InfrastructureRead, false, "Infrastructure", "See templates, sizes, network mappings, IP ranges and the vCenters rvav reaches."},
	{InfrastructureManage, false, "Infrastructure", "Change templates, sizes, network mappings and IP ranges, and reconcile IPAM."},
	{IntegrationsManage, false, "Integrations", "See, set and test the identity provider and every connection, and make the inventory token."},
	{PoliciesRead, false, "Policies", "See the approval, day-2, lease and naming policies, every project's included."},
	{PoliciesManage, false, "Policies", "Change the approval, day-2, lease and naming policies."},
	{ExtensibilityRead, false, "Extensibility", "See event subscriptions, their runs, the custom workflows and the custom resource types; read the shared variables and what composite steps were given and gave."},
	{ExtensibilityManage, false, "Extensibility", "Change event subscriptions, custom workflows, custom resource types, AWX day-2 actions and day-2 forms, retry and test runs."},
	{ImportManage, false, "Import", "Onboard existing VMs and import from vRA."},
	{SystemManage, false, "System", "Use the setup checklist and the System page; set the branding and the notification templates."},
	{TokensManage, false, "Access", "See and revoke everyone's API tokens."},
	{InventoryRead, false, "Integrations", "Read the Ansible inventory."},
}

// Day2Groups are the day-2 action groups a role may grant.
var Day2Groups = []string{day2.GroupPower, day2.GroupConfiguration, day2.GroupSnapshots, day2.GroupAccess,
	day2.GroupLifecycle, day2.GroupAutomation}

// Day2 grants every action of a day-2 group, those added later included.
func Day2(group string) Permission { return Permission("day2." + group) }

// Day2Action grants one day-2 action.
func Day2Action(group, key string) Permission { return Permission("day2." + group + ":" + key) }

var day2Key = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

// Info describes a permission; false for one rvav doesn't know. A day-2
// action that doesn't exist (yet, or any more) is known: its group is.
func Info(p Permission) (PermissionInfo, bool) {
	if i := slices.IndexFunc(Catalogue, func(c PermissionInfo) bool { return c.ID == p }); i >= 0 {
		return Catalogue[i], true
	}
	rest, ok := strings.CutPrefix(string(p), "day2.")
	if !ok {
		return PermissionInfo{}, false
	}
	group, key, one := strings.Cut(rest, ":")
	if !slices.Contains(Day2Groups, group) || one && !day2Key.MatchString(key) {
		return PermissionInfo{}, false
	}
	return PermissionInfo{ID: p, Project: true, Area: "Day-2 actions"}, true
}

// Role is a named set of permissions.
type Role struct {
	// Key is stable: assignments and policies name it.
	Key         string
	Name        string
	Permissions []Permission
	BuiltIn     bool
	// Global: a built-in role given for all of rvav only; the other
	// built-in roles are given in a project only.
	Global bool
}

// Assignable says whether the role may be given in a project ("" for all
// of rvav). A custom role holding a permission granted for all of rvav only
// is given that way.
func (r Role) Assignable(projectID string) bool {
	if r.BuiltIn {
		return r.Global == (projectID == "")
	}
	if projectID == "" {
		return true
	}
	return !slices.ContainsFunc(r.Permissions, func(p Permission) bool {
		i, ok := Info(p)
		return ok && !i.Project
	})
}

// InProjects says whether the role may be given in projects.
func (r Role) InProjects() bool { return r.Assignable("any project") }

// BuiltinRoles are the roles of ADR-0020, unchanged, and the supervisor.
// None grants a day-2 action: for them, the day-2 policies decide alone, as
// before.
var BuiltinRoles = []Role{
	{Key: "admin", Name: "Administrator", BuiltIn: true, Global: true, Permissions: allPermissions()},
	{Key: "catalog-admin", Name: "Catalog admin", BuiltIn: true, Global: true, Permissions: []Permission{CatalogManage}},
	{Key: "auditor", Name: "Auditor", BuiltIn: true, Global: true, Permissions: []Permission{
		ProjectSee, DeploymentsReadAll, ProjectSettingsRead, AuditRead, ApprovalReadAll, EnvironmentsRead, AccessRead,
		InfrastructureRead, PoliciesRead, ExtensibilityRead, InventoryRead}},
	// Every user signed in through the identity provider has it, as
	// users.global_roles' default: it grants nothing, and keeps
	// `"user" in user.roles` true in policies where it is today.
	{Key: "user", Name: "User", BuiltIn: true, Global: true},
	{Key: "project-admin", Name: "Project admin", BuiltIn: true, Permissions: []Permission{
		ProjectSee, DeploymentsReadAll, RequestSubmit, RequestOnBehalf, RequestManage, ApprovalProject, ProjectMembersManage,
		ProjectSettingsRead, AuditRead, ProjectPolicies}},
	{Key: "member", Name: "Member", BuiltIn: true, Permissions: []Permission{ProjectSee, RequestSubmit}},
	{Key: "viewer", Name: "Viewer", BuiltIn: true, Permissions: []Permission{ProjectSee}},
	// ADR-0041: approves and sees, but neither requests nor runs day-2
	// actions.
	{Key: "supervisor", Name: "Supervisor", BuiltIn: true, Permissions: []Permission{ProjectSee, DeploymentsReadAll, ApprovalProject}},
}

func allPermissions() []Permission {
	out := make([]Permission, len(Catalogue))
	for i, c := range Catalogue {
		out[i] = c.ID
	}
	return out
}

var roleKey = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)

// ValidRoleKey says whether a custom role may take this key: none of the
// built-in roles', nor owner, which day-2 policies name for a deployment's
// owner, nor new, the address of the web app's new role.
func ValidRoleKey(key string) bool {
	_, builtIn := BuiltinRole(key)
	return roleKey.MatchString(key) && !builtIn && key != "owner" && key != "new"
}

// BuiltinRole finds a built-in role by its key.
func BuiltinRole(key string) (Role, bool) {
	i := slices.IndexFunc(BuiltinRoles, func(r Role) bool { return r.Key == key })
	if i < 0 {
		return Role{}, false
	}
	return BuiltinRoles[i], true
}

// Assignment is a role held in a project, or for all of rvav (ProjectID "").
type Assignment struct {
	Role      string
	ProjectID string
}

// Grants are the permissions a principal holds.
type Grants struct {
	all      map[Permission]bool
	projects map[string]map[Permission]bool
}

// Compute gathers what the assignments grant; role finds a role by its key,
// and an assignment of a role it doesn't know grants nothing.
func Compute(assignments []Assignment, role func(key string) (Role, bool)) Grants {
	g := Grants{all: map[Permission]bool{}, projects: map[string]map[Permission]bool{}}
	for _, a := range assignments {
		r, ok := role(a.Role)
		if !ok || !r.Assignable(a.ProjectID) {
			continue
		}
		set := g.all
		if a.ProjectID != "" {
			if g.projects[a.ProjectID] == nil {
				g.projects[a.ProjectID] = map[Permission]bool{}
			}
			set = g.projects[a.ProjectID]
		}
		for _, p := range r.Permissions {
			// The project's admins asked to approve are its own, as
			// Store.ProjectAdmins finds them: never an administrator, nor
			// anyone by a role given for all of rvav.
			if p == ApprovalProject && a.ProjectID == "" {
				continue
			}
			set[p] = true
		}
	}
	return g
}

// In says whether the permission is held in the project, by a role given
// there or for all of rvav.
func (g Grants) In(p Permission, projectID string) bool {
	return g.all[p] || projectID != "" && g.projects[projectID][p]
}

// Everywhere says whether the permission is held for all of rvav.
func (g Grants) Everywhere(p Permission) bool { return g.all[p] }

// Anywhere says whether the permission is held in a project at least, or
// for all of rvav.
func (g Grants) Anywhere(p Permission) bool {
	if g.all[p] {
		return true
	}
	for _, set := range g.projects {
		if set[p] {
			return true
		}
	}
	return false
}

// Projects lists the projects where the permission is held, sorted; nil
// means all of them.
func (g Grants) Projects(p Permission) []string {
	if g.all[p] {
		return nil
	}
	out := []string{}
	for id, set := range g.projects {
		if set[p] {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// Held lists the permissions held in a project, those held for all of rvav
// included, sorted; "" lists those held for all of rvav.
func (g Grants) Held(projectID string) []Permission {
	out := []Permission{}
	for p := range g.all {
		out = append(out, p)
	}
	for p := range g.projects[projectID] {
		if projectID != "" && !g.all[p] {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// RunsDay2 says whether a role grants the day-2 action in the project, by
// its group or by itself.
func (g Grants) RunsDay2(group, key, projectID string) bool {
	return g.In(Day2(group), projectID) || g.In(Day2Action(group, key), projectID)
}
