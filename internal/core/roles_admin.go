package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// Custom roles and the roles given for all of rvav (ADR-0034).

// Permissions is the catalogue the roles are made of, with the day-2
// actions offered now, for any signed-in user.
func (s *Service) Permissions(ctx context.Context) (api.PermissionCatalogue, error) {
	out := api.PermissionCatalogue{Items: make([]api.PermissionInfo, len(auth.Catalogue)), Day2: []api.Day2PermissionGroup{}}
	for i, c := range auth.Catalogue {
		out.Items[i] = api.PermissionInfo{Id: string(c.ID), Scope: permissionScope(c), Area: c.Area, Summary: c.Summary}
	}
	actions, err := s.day2Actions(ctx)
	if err != nil {
		return out, err
	}
	for _, g := range auth.Day2Groups {
		group := api.Day2PermissionGroup{Group: g, Permission: string(auth.Day2(g))}
		for _, a := range actions {
			if a.Group == g && !a.Hidden {
				group.Actions = append(group.Actions, struct {
					Key        string         `json:"key"`
					Name       string         `json:"name"`
					Permission api.Permission `json:"permission"`
				}{a.Key, a.Name, string(auth.Day2Action(g, a.Key))})
			}
		}
		if group.Actions == nil {
			group.Actions = []struct {
				Key        string         `json:"key"`
				Name       string         `json:"name"`
				Permission api.Permission `json:"permission"`
			}{}
		}
		out.Day2 = append(out.Day2, group)
	}
	return out, nil
}

func permissionScope(c auth.PermissionInfo) api.PermissionInfoScope {
	if c.Project {
		return api.PermissionInfoScopeProject
	}
	return api.PermissionInfoScopeRvav
}

// role finds a role by its key: built-in, or made here.
func (s *Service) role(ctx context.Context, key string) (auth.Role, error) {
	if r, ok := auth.BuiltinRole(key); ok {
		return r, nil
	}
	stored, err := s.Store.GetRole(ctx, key)
	if err != nil || stored.BuiltIn {
		return auth.Role{}, store.ErrNotFound
	}
	return customRole(stored), nil
}

// accessChanged ends every live stream, so that each reconnects with the
// permissions held now.
func (s *Service) accessChanged(ctx context.Context) {
	if s.Events != nil {
		s.Events.AccessChanged(ctx)
	}
}

func customRole(r store.StoredRole) auth.Role {
	out := auth.Role{Key: r.Key, Name: r.Name}
	for _, p := range r.Permissions {
		out.Permissions = append(out.Permissions, auth.Permission(p))
	}
	return out
}

// requesters are those given a role holding request.submit in the project,
// who may own its deployments.
func (s *Service) requesters(ctx context.Context, projectID string) ([]string, error) {
	submit, err := s.rolesWith(ctx, auth.RequestSubmit)
	if err != nil {
		return nil, err
	}
	members, err := s.Store.ProjectMemberRoles(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range members {
		if slices.Contains(submit, m.Role) && !slices.Contains(out, m.ID) {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

// rolesWith are the keys of the roles holding a permission.
func (s *Service) rolesWith(ctx context.Context, perm auth.Permission) ([]string, error) {
	var out []string
	for _, r := range auth.BuiltinRoles {
		if slices.Contains(r.Permissions, perm) {
			out = append(out, r.Key)
		}
	}
	custom, err := s.Store.CustomRolesWith(ctx, string(perm))
	return append(out, custom...), err
}

// mayGive says whether the principal may give a role in a project ("" for
// all of rvav), or take it away: with access.manage, any; with
// project.members.manage there, one whose permissions they hold there. No
// one gives more than they hold.
func mayGive(p *auth.Principal, r auth.Role, projectID string) bool {
	if p.Everywhere(auth.AccessManage) {
		return true
	}
	if projectID == "" || !p.In(auth.ProjectMembersManage, projectID) {
		return false
	}
	for _, perm := range r.Permissions {
		if !holds(p, perm, projectID) {
			return false
		}
	}
	return true
}

// holds says whether a permission is held in a project: a day-2 action's
// by itself or by its group.
func holds(p *auth.Principal, perm auth.Permission, projectID string) bool {
	if p.In(perm, projectID) {
		return true
	}
	rest, day2 := strings.CutPrefix(string(perm), "day2.")
	group, _, one := strings.Cut(rest, ":")
	return day2 && one && p.In(auth.Day2(group), projectID)
}

func roleScope(r auth.Role) api.RoleScope {
	switch {
	case r.BuiltIn && r.Global, !r.BuiltIn && !r.Assignable("prj"):
		return api.RoleScopeRvav
	case r.BuiltIn:
		return api.RoleScopeProject
	}
	return api.RoleScopeAny
}

func presentRole(r auth.Role, stored store.StoredRole) api.Role {
	out := api.Role{Key: r.Key, Name: r.Name, Description: stored.Description, Permissions: []string{}, BuiltIn: r.BuiltIn,
		Scope: roleScope(r), Holders: stored.Holders, Policies: stored.Policies}
	for _, p := range r.Permissions {
		out.Permissions = append(out.Permissions, string(p))
	}
	if out.Policies == nil {
		out.Policies = []string{}
	}
	return out
}

func (s *Service) Roles(ctx context.Context, p *auth.Principal) ([]api.Role, error) {
	if !p.Everywhere(auth.AccessRead) {
		return nil, ErrForbidden
	}
	list, err := s.Store.Roles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Role, 0, len(list))
	for _, st := range list {
		r, ok := auth.BuiltinRole(st.Key)
		if !st.BuiltIn {
			r, ok = customRole(st), true
		}
		if ok {
			out = append(out, presentRole(r, st))
		}
	}
	return out, nil
}

func (s *Service) Role(ctx context.Context, p *auth.Principal, key string) (api.Role, error) {
	if !p.Everywhere(auth.AccessRead) {
		return api.Role{}, ErrForbidden
	}
	return s.apiRole(ctx, key)
}

func (s *Service) apiRole(ctx context.Context, key string) (api.Role, error) {
	st, err := s.Store.GetRole(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return api.Role{}, ErrNotFound
	}
	if err != nil {
		return api.Role{}, err
	}
	r, ok := auth.BuiltinRole(st.Key)
	if !st.BuiltIn {
		r, ok = customRole(st), true
	}
	if !ok {
		return api.Role{}, ErrNotFound
	}
	return presentRole(r, st), nil
}

// checkPermissions keeps a role's permissions known, once each, and says
// what is wrong with them.
func checkPermissions(perms []string) ([]string, []api.FieldError) {
	var out []string
	var errs []api.FieldError
	project := false
	for i, p := range perms {
		info, ok := auth.Info(auth.Permission(p))
		if !ok {
			errs = append(errs, fieldErr(fmt.Sprintf("/permissions/%d", i), "unknown", fmt.Sprintf("%q is no permission.", p)))
			continue
		}
		project = project || info.Project
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	switch {
	case len(perms) == 0:
		errs = append(errs, fieldErr("/permissions", "required", "Give the role at least one permission."))
	case len(errs) == 0 && project && !slices.Contains(out, string(auth.ProjectSee)):
		errs = append(errs, fieldErr("/permissions", "project_see",
			"Add project.see: it allows something in a project, and every check there starts by seeing it."))
	}
	return out, errs
}

func (s *Service) CreateRole(ctx context.Context, p *auth.Principal, c api.RoleCreate) (api.Role, error) {
	if !p.Everywhere(auth.AccessManage) {
		return api.Role{}, ErrForbidden
	}
	var errs []api.FieldError
	if !auth.ValidRoleKey(c.Key) {
		errs = append(errs, fieldErr("/key", "pattern",
			"2 to 41 lower-case letters, digits and hyphens, starting with a letter; not a built-in role's key, nor owner or new."))
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the role a name."))
	}
	perms, permErrs := checkPermissions(c.Permissions)
	if errs = append(errs, permErrs...); len(errs) > 0 {
		return api.Role{}, &ValidationError{Errors: errs}
	}
	r := store.StoredRole{Key: c.Key, Name: name, Description: strings.TrimSpace(deref(c.Description)), Permissions: perms}
	if err := s.Store.CreateRole(ctx, r); errors.Is(err, store.ErrInUse) {
		return api.Role{}, &ConflictError{Msg: "A role has the key " + c.Key + "."}
	} else if err != nil {
		return api.Role{}, err
	}
	s.audit(ctx, p, "role.create", "role", r.Key, nil, map[string]any{"name": r.Name, "permissions": perms})
	return s.apiRole(ctx, r.Key)
}

func (s *Service) UpdateRole(ctx context.Context, p *auth.Principal, key string, u api.RoleUpdate) (api.Role, error) {
	if !p.Everywhere(auth.AccessManage) {
		return api.Role{}, ErrForbidden
	}
	st, err := s.Store.GetRole(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return api.Role{}, ErrNotFound
	}
	if err != nil {
		return api.Role{}, err
	}
	if st.BuiltIn {
		return api.Role{}, &ConflictError{Msg: st.Name + " is built in: it can't be changed. Create a role of your own instead."}
	}
	var errs []api.FieldError
	if u.Name != nil {
		if *u.Name = strings.TrimSpace(*u.Name); *u.Name == "" {
			errs = append(errs, fieldErr("/name", "required", "Give the role a name."))
		}
	}
	var perms, added, removed []string
	if u.Permissions != nil {
		var permErrs []api.FieldError
		perms, permErrs = checkPermissions(*u.Permissions)
		errs = append(errs, permErrs...)
		for _, perm := range perms {
			if !slices.Contains(st.Permissions, perm) {
				added = append(added, perm)
			}
		}
		for _, perm := range st.Permissions {
			if !slices.Contains(perms, perm) {
				removed = append(removed, perm)
			}
		}
	}
	if len(errs) > 0 {
		return api.Role{}, &ValidationError{Errors: errs}
	}
	if err := s.Store.WithLock(ctx, store.LockAdmins, func() error {
		if u.Permissions != nil && !customRole(store.StoredRole{Key: key, Permissions: perms}).Assignable("prj") {
			where, err := s.Store.RoleProjects(ctx, key)
			if err != nil {
				return err
			}
			if len(where) > 0 {
				return &ValidationError{Errors: []api.FieldError{fieldErr("/permissions", "scope", fmt.Sprintf(
					"%s is given in %s: a permission for all of rvav only can't be added while it is. Take it away there first.",
					st.Name, projectNames(where)))}}
			}
		}
		if slices.Contains(removed, string(auth.AccessManage)) {
			if err := s.keepsAccessManager(ctx, key, "", "", "give access.manage to someone else first."); err != nil {
				return err
			}
		}
		if slices.Contains(removed, string(auth.ProjectMembersManage)) {
			where, err := s.Store.RoleProjects(ctx, key)
			if err != nil {
				return err
			}
			for _, pr := range where {
				if err := s.keepsMembersManager(ctx, pr, key, "", ""); err != nil {
					return err
				}
			}
		}
		var set []string
		if u.Permissions != nil {
			set = perms
		}
		return s.Store.UpdateRole(ctx, key, u.Name, trimmed(u.Description), set)
	}); err != nil {
		return api.Role{}, err
	}
	detail := map[string]any{}
	if u.Name != nil {
		detail["name"] = *u.Name
	}
	if u.Description != nil {
		detail["description"] = strings.TrimSpace(*u.Description)
	}
	if u.Permissions != nil {
		detail["added"], detail["removed"] = nonNilStrings(added), nonNilStrings(removed)
	}
	s.audit(ctx, p, "role.update", "role", key, nil, detail)
	if u.Permissions != nil {
		s.accessChanged(ctx)
	}
	return s.apiRole(ctx, key)
}

func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}

func projectNames(list []store.Project) string {
	names := make([]string, len(list))
	for i, pr := range list {
		names[i] = pr.Name
	}
	if len(names) > 3 {
		names = append(names[:3], fmt.Sprintf("%d more", len(list)-3))
	}
	return strings.Join(names, ", ")
}

func (s *Service) DeleteRole(ctx context.Context, p *auth.Principal, key string) error {
	if !p.Everywhere(auth.AccessManage) {
		return ErrForbidden
	}
	st, err := s.Store.GetRole(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	switch {
	case st.BuiltIn:
		return &ConflictError{Msg: st.Name + " is built in: it can't be deleted."}
	case len(st.Policies) > 0:
		return &ConflictError{Msg: "The policies " + strings.Join(st.Policies, ", ") + " name " + st.Name + ": change them first."}
	case st.Holders > 0:
		return &ConflictError{Msg: fmt.Sprintf("%s is given %d times, to users or groups: take it away first.", st.Name, st.Holders)}
	case len(st.Items) > 0:
		return &ConflictError{Msg: "The items " + strings.Join(st.Items, ", ") + " are shared with " + st.Name + ": change their sharing first."}
	case len(st.Categories) > 0:
		return &ConflictError{Msg: "The categories " + strings.Join(st.Categories, ", ") + " are shared with " + st.Name + ": change their sharing first."}
	case len(st.Sources) > 0:
		return &ConflictError{Msg: "The items made from " + strings.Join(st.Sources, ", ") + " are shared with " + st.Name + ": change their sharing first."}
	}
	if err := s.Store.DeleteRole(ctx, key); errors.Is(err, store.ErrInUse) {
		return &ConflictError{Msg: st.Name + " is given to users or groups: take it away first."}
	} else if err != nil {
		return err
	}
	s.auditGone(ctx, p, "role.delete", "role", key, st.Name, nil, map[string]any{"name": st.Name})
	return nil
}

// keepsAccessManager refuses to take away the last way anyone holds
// access.manage: leaving out a role's every assignment, one assignment or
// one group mapping.
func (s *Service) keepsAccessManager(ctx context.Context, exceptRole, exceptAssignment, exceptMapping, hint string) error {
	roles, err := s.rolesWith(ctx, auth.AccessManage)
	if err != nil {
		return err
	}
	roles = slices.DeleteFunc(roles, func(r string) bool { return r == exceptRole })
	n, err := s.Store.RvavHolders(ctx, roles, exceptAssignment, exceptMapping)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ConflictError{Msg: "Nobody would be an administrator any more: " + hint}
	}
	return nil
}

// keepsMembersManager refuses to take away the last way a project has
// someone holding project.members.manage given in it: leaving out a role's
// every assignment there, a user's own roles or one group mapping.
func (s *Service) keepsMembersManager(ctx context.Context, proj store.Project, exceptRole, exceptUser, exceptMapping string) error {
	roles, err := s.rolesWith(ctx, auth.ProjectMembersManage)
	if err != nil {
		return err
	}
	roles = slices.DeleteFunc(roles, func(r string) bool { return r == exceptRole })
	n, err := s.Store.ProjectHolders(ctx, proj.ID, roles, exceptUser, exceptMapping)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ConflictError{Msg: proj.Name + " would have no project admin: make someone else one first."}
	}
	return nil
}

func presentRoleAssignment(a store.RoleAssignment) api.RoleAssignment {
	return api.RoleAssignment{Id: a.ID, User: userRef(store.User{ID: a.UserID, Name: a.UserName, Email: a.UserEmail}),
		Role: api.RoleRef{Key: a.Role, Name: a.RoleName}, Fixed: a.Fixed, CreatedAt: a.CreatedAt}
}

func (s *Service) RoleAssignments(ctx context.Context, p *auth.Principal, role, userID string) ([]api.RoleAssignment, error) {
	if !p.Everywhere(auth.AccessRead) {
		return nil, ErrForbidden
	}
	list, err := s.Store.RoleAssignments(ctx, role, userID)
	if err != nil {
		return nil, err
	}
	out := make([]api.RoleAssignment, len(list))
	for i, a := range list {
		out[i] = presentRoleAssignment(a)
	}
	return out, nil
}

func (s *Service) CreateRoleAssignment(ctx context.Context, p *auth.Principal, w api.RoleAssignmentWrite) (api.RoleAssignment, error) {
	if !p.Everywhere(auth.AccessManage) {
		return api.RoleAssignment{}, ErrForbidden
	}
	var errs []api.FieldError
	u, err := s.Store.GetUser(ctx, w.UserId)
	if errors.Is(err, store.ErrNotFound) {
		errs = append(errs, fieldErr("/user_id", "unknown", "There is no such user."))
	} else if err != nil {
		return api.RoleAssignment{}, err
	}
	r, err := s.role(ctx, w.Role)
	switch {
	case errors.Is(err, store.ErrNotFound):
		errs = append(errs, fieldErr("/role", "unknown", "There is no such role."))
	case err != nil:
		return api.RoleAssignment{}, err
	case r.Key == "user" || !r.Assignable(""):
		errs = append(errs, fieldErr("/role", "scope", r.Name+" is given in a project, not for all of rvav."))
	}
	if len(errs) > 0 {
		return api.RoleAssignment{}, &ValidationError{Errors: errs}
	}
	id := ids.New("ras")
	if err := s.Store.CreateRoleAssignment(ctx, id, u.ID, r.Key); errors.Is(err, store.ErrInUse) {
		return api.RoleAssignment{}, &ConflictError{Msg: u.Name + " already holds " + r.Name + "."}
	} else if err != nil {
		return api.RoleAssignment{}, err
	}
	s.audit(ctx, p, "role_assignment.create", "role_assignment", id, nil, map[string]any{"user_id": u.ID, "role": r.Key})
	s.accessChanged(ctx)
	a, err := s.Store.GetRoleAssignment(ctx, id)
	return presentRoleAssignment(a), err
}

func (s *Service) DeleteRoleAssignment(ctx context.Context, p *auth.Principal, id string) error {
	if !p.Everywhere(auth.AccessManage) {
		return ErrForbidden
	}
	a, err := s.Store.GetRoleAssignment(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := s.Store.WithLock(ctx, store.LockAdmins, func() error {
		r, err := s.role(ctx, a.Role)
		if err != nil {
			return err
		}
		if slices.Contains(r.Permissions, auth.AccessManage) {
			if err := s.keepsAccessManager(ctx, "", id, "", "make someone else one first."); err != nil {
				return err
			}
		}
		return s.Store.DeleteRoleAssignment(ctx, id)
	}); err != nil {
		return err
	}
	s.audit(ctx, p, "role_assignment.delete", "role_assignment", id, nil, map[string]any{"user_id": a.UserID, "role": a.Role})
	s.accessChanged(ctx)
	return nil
}

// AssignableRoles are the roles the principal may give the project's
// members, as changeMembership checks them.
func (s *Service) AssignableRoles(ctx context.Context, p *auth.Principal, projectID string) ([]api.AssignableRole, error) {
	proj, err := s.canManageMembers(ctx, p, projectID)
	if err != nil {
		return nil, err
	}
	list, err := s.Store.Roles(ctx)
	if err != nil {
		return nil, err
	}
	out := []api.AssignableRole{}
	for _, st := range list {
		r, ok := auth.BuiltinRole(st.Key)
		if !st.BuiltIn {
			r, ok = customRole(st), true
		}
		if !ok || !r.Assignable(proj.ID) || !mayGive(p, r, proj.ID) {
			continue
		}
		a := api.AssignableRole{Key: r.Key, Name: r.Name, Description: st.Description, BuiltIn: r.BuiltIn, Permissions: []string{}}
		for _, perm := range r.Permissions {
			a.Permissions = append(a.Permissions, string(perm))
		}
		out = append(out, a)
	}
	return out, nil
}

// ExplainAccess says what a user may do in a project ("" for all of rvav),
// and which of their roles allows each permission, as Compute counts them:
// anyone about themselves, in the projects they see; access.read about
// anyone, anywhere.
func (s *Service) ExplainAccess(ctx context.Context, p *auth.Principal, userID, projectID string) (api.AccessExplanation, error) {
	out := api.AccessExplanation{Groups: []string{}, Roles: []api.AccessGrant{}, Permissions: []api.AccessPermission{}}
	anyone := p.Everywhere(auth.AccessRead)
	if !anyone && userID != p.User.ID {
		return out, ErrForbidden
	}
	u, err := s.Store.GetUser(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.User = userRef(u)
	if projectID != "" {
		var proj store.Project
		if anyone {
			if proj, err = s.Store.GetProject(ctx, projectID); errors.Is(err, store.ErrNotFound) {
				err = ErrNotFound
			}
		} else {
			proj, err = s.visibleProject(ctx, p, projectID)
		}
		if err != nil {
			return out, err
		}
		projectID = proj.ID
		out.Project = &api.ProjectRef{Id: proj.ID, Code: proj.Code, Name: proj.Name}
	}
	groups, err := s.Store.UserGroups(ctx, u.ID)
	if err != nil {
		return out, err
	}
	out.Groups = append(out.Groups, groups...)
	held, err := s.Store.RoleGrants(ctx, u.ID, u.GlobalRoles, groups)
	if err != nil {
		return out, err
	}
	granted := map[auth.Permission][]api.AccessGrant{}
	seen := map[string]bool{}
	for _, h := range held {
		r, ok := auth.RoleOf(h)
		if !ok || h.ProjectID != "" && h.ProjectID != projectID || !r.Assignable(h.ProjectID) {
			continue
		}
		id := strings.Join([]string{h.Role, h.ProjectID, h.Source, h.Group}, "\x00")
		if seen[id] {
			continue
		}
		seen[id] = true
		g := accessGrant(r, h)
		out.Roles = append(out.Roles, g)
		for _, perm := range r.Permissions {
			if perm != auth.ApprovalProject || h.ProjectID != "" {
				granted[perm] = append(granted[perm], g)
			}
		}
	}
	for _, c := range auth.Catalogue {
		out.Permissions = append(out.Permissions, accessPermission(c.ID, granted[c.ID]))
	}
	var day2 []auth.Permission
	for perm := range granted {
		if strings.HasPrefix(string(perm), "day2.") {
			day2 = append(day2, perm)
		}
	}
	slices.Sort(day2)
	for _, perm := range day2 {
		out.Permissions = append(out.Permissions, accessPermission(perm, granted[perm]))
	}
	return out, nil
}

func accessGrant(r auth.Role, h store.RoleGrant) api.AccessGrant {
	g := api.AccessGrant{Role: api.RoleRef{Key: r.Key, Name: r.Name}, Given: api.AccessGrantGivenRvav, Through: api.AccessGrantThrough(h.Source)}
	if h.ProjectID != "" {
		g.Given = api.AccessGrantGivenProject
	}
	if h.Source == store.GrantGroup {
		g.Group = &h.Group
	}
	return g
}

func accessPermission(perm auth.Permission, by []api.AccessGrant) api.AccessPermission {
	if by == nil {
		by = []api.AccessGrant{}
	}
	return api.AccessPermission{Id: string(perm), Held: len(by) > 0, GrantedBy: by}
}
