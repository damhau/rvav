package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Administration of projects, members, OIDC group mappings and
// environments (ADR-0020).

var (
	projectCode    = regexp.MustCompile(`^[a-z][a-z0-9]{1,7}$`)
	environmentKey = regexp.MustCompile(`^[a-z][a-z0-9]{1,9}$`)
)

func (s *Service) audit(ctx context.Context, p *auth.Principal, action, targetType, targetID string, projectID *string, detail any) {
	if err := s.Store.Audit(ctx, s.Store.Pool, store.AuditEvent{ActorID: &p.User.ID, Action: action, TargetType: targetType,
		TargetID: targetID, ProjectID: projectID, Detail: detail}); err != nil {
		s.Log.Error("audit", "action", action, "err", err)
	}
}

func presentAdminProject(pr store.AdminProject, p *auth.Principal) api.AdminProject {
	base := present.Project(pr.Project, p.PermissionsIn(pr.ID))
	return api.AdminProject{Id: base.Id, Code: base.Code, Name: base.Name, Description: base.Description, CostCenter: base.CostCenter,
		Environments: base.Environments, MyPermissions: base.MyPermissions, DeploymentSharing: base.DeploymentSharing, Archived: pr.ArchivedAt != nil,
		ArchivedAt: pr.ArchivedAt, CreatedAt: pr.CreatedAt, Members: pr.Members, Deployments: pr.Deployments,
		Properties: api.CustomProperties(nonNilProps(pr.Properties))}
}

func nonNilProps(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// checkProperties checks custom properties (ADR-0038): their keys become
// vSphere custom attributes' names and CEL fields.
func checkProperties(props map[string]string, ptr string) []api.FieldError {
	var errs []api.FieldError
	if len(props) > blueprint.MaxProperties {
		errs = append(errs, fieldErr(ptr, "too_many", fmt.Sprintf("At most %d custom properties.", blueprint.MaxProperties)))
	}
	for _, k := range slices.Sorted(maps.Keys(props)) {
		if msg := blueprint.CheckProperty(k, props[k]); msg != "" {
			errs = append(errs, fieldErr(ptr+"/"+k, "invalid", msg))
		}
	}
	return errs
}

// AdminProjects lists projects for those who read every project's settings.
func (s *Service) AdminProjects(ctx context.Context, p *auth.Principal, search string, archived bool) ([]api.AdminProject, error) {
	if !p.Everywhere(auth.ProjectSettingsRead) {
		return nil, ErrForbidden
	}
	list, err := s.Store.AdminProjects(ctx, search, archived)
	if err != nil {
		return nil, err
	}
	out := make([]api.AdminProject, len(list))
	for i, pr := range list {
		out[i] = presentAdminProject(pr, p)
	}
	return out, nil
}

// projectPickers are the permissions whose Administration pages pick or
// name projects without reading their settings.
var projectPickers = []auth.Permission{auth.ProjectSettingsRead, auth.AccessRead, auth.AuditRead,
	auth.InfrastructureRead, auth.InfrastructureManage, auth.PoliciesRead, auth.PoliciesManage,
	auth.ExtensibilityRead, auth.ExtensibilityManage, auth.ImportManage}

// ProjectNames lists every project's reference, and whether it is
// archived, for those holding one of projectPickers for all of rvav: the
// IDs their pages show anyway, by name.
func (s *Service) ProjectNames(ctx context.Context, p *auth.Principal, archived bool) ([]api.ProjectName, error) {
	if !slices.ContainsFunc(projectPickers, p.Everywhere) {
		return nil, ErrForbidden
	}
	list, err := s.Store.AdminProjects(ctx, "", archived)
	if err != nil {
		return nil, err
	}
	out := make([]api.ProjectName, len(list))
	for i, pr := range list {
		out[i] = api.ProjectName{Id: pr.ID, Code: pr.Code, Name: pr.Name, Archived: pr.ArchivedAt != nil}
	}
	return out, nil
}

// AdminProject is one project for those who see its administration
// (project.settings.read).
func (s *Service) AdminProject(ctx context.Context, p *auth.Principal, id string) (api.AdminProject, error) {
	pr, err := s.Store.GetAdminProject(ctx, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && !p.In(auth.ProjectSee, pr.ID) {
		return api.AdminProject{}, ErrNotFound
	}
	if err != nil {
		return api.AdminProject{}, err
	}
	if !p.In(auth.ProjectSettingsRead, pr.ID) {
		return api.AdminProject{}, ErrForbidden
	}
	return presentAdminProject(pr, p), nil
}

// writtenProject is the answer to a change of a project: the change's own
// permission allowed it, whether or not its holder reads the settings.
func (s *Service) writtenProject(ctx context.Context, p *auth.Principal, id string) (api.AdminProject, error) {
	pr, err := s.Store.GetAdminProject(ctx, id)
	if err != nil {
		return api.AdminProject{}, err
	}
	return presentAdminProject(pr, p), nil
}

// checkEnvironments says which of these keys are not environments.
func (s *Service) checkEnvironments(ctx context.Context, keys []string, ptr string) ([]api.FieldError, error) {
	envs, err := s.Store.ListEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	var errs []api.FieldError
	for i, k := range keys {
		if !slices.ContainsFunc(envs, func(e store.Environment) bool { return e.Key == k }) {
			errs = append(errs, fieldErr(fmt.Sprintf("%s/%d", ptr, i), "unknown", fmt.Sprintf("%q is not an environment.", k)))
		}
	}
	return errs, nil
}

func (s *Service) CreateProject(ctx context.Context, p *auth.Principal, c api.AdminProjectCreate) (api.AdminProject, error) {
	if !p.Everywhere(auth.ProjectCreate) {
		return api.AdminProject{}, ErrForbidden
	}
	var errs []api.FieldError
	if !projectCode.MatchString(c.Code) {
		errs = append(errs, fieldErr("/code", "pattern", "2 to 8 lower-case letters and digits, starting with a letter."))
	}
	if strings.TrimSpace(c.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the project a name."))
	}
	envErrs, err := s.checkEnvironments(ctx, c.Environments, "/environments")
	if err != nil {
		return api.AdminProject{}, err
	}
	errs = append(errs, envErrs...)
	if c.Properties != nil {
		errs = append(errs, checkProperties(*c.Properties, "/properties")...)
	}
	var admins []string
	if c.ProjectAdmins != nil {
		admins = *c.ProjectAdmins
	}
	if len(admins) > 0 {
		found, err := s.Store.FindUsers(ctx, admins)
		if err != nil {
			return api.AdminProject{}, err
		}
		for i, u := range admins {
			if _, ok := found[u]; !ok {
				errs = append(errs, fieldErr(fmt.Sprintf("/project_admins/%d", i), "unknown", "There is no such user."))
			}
		}
	}
	if len(errs) > 0 {
		return api.AdminProject{}, &ValidationError{Errors: errs}
	}
	pr := store.Project{ID: ids.New(ids.Project), Code: c.Code, Name: strings.TrimSpace(c.Name), Description: deref(c.Description),
		CostCenter: deref(c.CostCenter), Environments: c.Environments}
	if c.Properties != nil {
		pr.Properties = *c.Properties
	}
	if err := s.Store.CreateProject(ctx, pr, admins); errors.Is(err, store.ErrInUse) {
		return api.AdminProject{}, &ConflictError{Msg: "A project has the code " + c.Code + "."}
	} else if err != nil {
		return api.AdminProject{}, err
	}
	s.audit(ctx, p, "project.create", "project", pr.ID, &pr.ID, map[string]any{"code": pr.Code, "name": pr.Name, "admins": admins})
	if len(admins) > 0 {
		s.accessChanged(ctx)
	}
	// Its creator named among its admins holds what they hold there now.
	if slices.Contains(admins, p.User.ID) {
		if p, err = auth.Again(ctx, s.Store, p); err != nil {
			return api.AdminProject{}, err
		}
	}
	return s.writtenProject(ctx, p, pr.ID)
}

func (s *Service) UpdateProject(ctx context.Context, p *auth.Principal, id string, u api.AdminProjectUpdate) (api.AdminProject, error) {
	pr, err := s.projectFor(ctx, p, auth.ProjectUpdate, id)
	if err != nil {
		return api.AdminProject{}, err
	}
	var errs []api.FieldError
	if u.Name != nil && strings.TrimSpace(*u.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the project a name."))
	}
	if u.Environments != nil {
		envErrs, err := s.checkEnvironments(ctx, *u.Environments, "/environments")
		if err != nil {
			return api.AdminProject{}, err
		}
		errs = append(errs, envErrs...)
	}
	if u.Properties != nil {
		errs = append(errs, checkProperties(*u.Properties, "/properties")...)
	}
	if len(errs) > 0 {
		return api.AdminProject{}, &ValidationError{Errors: errs}
	}
	if err := s.Store.UpdateProject(ctx, pr.ID, store.ProjectUpdate{Name: u.Name, Description: u.Description, CostCenter: u.CostCenter,
		Environments: u.Environments, Archived: u.Archived, Properties: (*map[string]string)(u.Properties)}); err != nil {
		return api.AdminProject{}, err
	}
	action := "project.update"
	if u.Archived != nil && *u.Archived != (pr.ArchivedAt != nil) {
		action = map[bool]string{true: "project.archive", false: "project.unarchive"}[*u.Archived]
	}
	s.audit(ctx, p, action, "project", pr.ID, &pr.ID, u)
	return s.writtenProject(ctx, p, pr.ID)
}

var roleRank = map[string]int{"viewer": 1, "member": 2, "project-admin": 3}

// strongest keeps the strongest built-in project role of the two.
func strongest(current *api.ProjectRole, key string) *api.ProjectRole {
	if roleRank[key] == 0 || current != nil && roleRank[key] <= roleRank[string(*current)] {
		return current
	}
	r := api.ProjectRole(key)
	return &r
}

// ProjectMembers lists a project's members: every role they hold there and
// where it comes from, themselves or groups.
func (s *Service) ProjectMembers(ctx context.Context, p *auth.Principal, projectID string) ([]api.ProjectMember, error) {
	proj, err := s.visibleProject(ctx, p, projectID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.ProjectMemberRoles(ctx, proj.ID)
	if err != nil {
		return nil, err
	}
	out := []api.ProjectMember{}
	index := map[string]int{}
	for _, r := range rows {
		i, ok := index[r.ID]
		if !ok {
			i = len(out)
			index[r.ID] = i
			out = append(out, api.ProjectMember{User: userRef(r.User), Groups: []string{}, Roles: []api.ProjectMemberRole{}, DirectRoles: []string{}})
		}
		m := &out[i]
		j := slices.IndexFunc(m.Roles, func(x api.ProjectMemberRole) bool { return x.Key == r.Role })
		if j < 0 {
			j = len(m.Roles)
			m.Roles = append(m.Roles, api.ProjectMemberRole{Key: r.Role, Name: r.RoleName, Groups: []string{}})
		}
		if r.Group == nil {
			m.Direct, m.Roles[j].Direct = true, true
			m.DirectRoles = append(m.DirectRoles, r.Role)
			m.DirectRole = strongest(m.DirectRole, r.Role)
		} else {
			if !slices.Contains(m.Roles[j].Groups, *r.Group) {
				m.Roles[j].Groups = append(m.Roles[j].Groups, *r.Group)
			}
			if !slices.Contains(m.Groups, *r.Group) {
				m.Groups = append(m.Groups, *r.Group)
			}
		}
		m.Role = strongest(m.Role, r.Role)
	}
	for i := range out {
		slices.SortFunc(out[i].Roles, func(a, b api.ProjectMemberRole) int { return strings.Compare(a.Name, b.Name) })
		slices.Sort(out[i].DirectRoles)
	}
	return out, nil
}

func userRef(u store.User) api.UserRef {
	ref := api.UserRef{Id: u.ID, Name: u.Name}
	if u.Email != "" {
		e := openapi_types.Email(u.Email)
		ref.Email = &e
	}
	return ref
}

// canManageMembers: project.members.manage in the project.
func (s *Service) canManageMembers(ctx context.Context, p *auth.Principal, projectID string) (store.Project, error) {
	proj, err := s.visibleProject(ctx, p, projectID)
	if err != nil {
		return proj, err
	}
	if !p.In(auth.ProjectMembersManage, proj.ID) {
		return proj, ErrForbidden
	}
	return proj, nil
}

// memberRoles finds the roles to give in a project, and says what is wrong
// with them: unknown, or given for all of rvav only.
func (s *Service) memberRoles(ctx context.Context, proj store.Project, w api.ProjectMemberWrite) ([]auth.Role, error) {
	var keys []string
	field := "/roles"
	switch {
	case w.Roles != nil && w.Role == nil:
		keys = *w.Roles
	case w.Role != nil && w.Roles == nil:
		keys, field = []string{*w.Role}, "/role"
	default:
		return nil, &ValidationError{Errors: []api.FieldError{fieldErr("/roles", "required", "Give the roles, or one role.")}}
	}
	var out []auth.Role
	var errs []api.FieldError
	for i, k := range keys {
		ptr := field
		if field == "/roles" {
			ptr = fmt.Sprintf("/roles/%d", i)
		}
		r, err := s.role(ctx, k)
		switch {
		case errors.Is(err, store.ErrNotFound):
			errs = append(errs, fieldErr(ptr, "unknown", fmt.Sprintf("%q is no role.", k)))
		case err != nil:
			return nil, err
		case !r.Assignable(proj.ID):
			errs = append(errs, fieldErr(ptr, "scope", r.Name+" is given for all of rvav, not in a project."))
		case !slices.ContainsFunc(out, func(o auth.Role) bool { return o.Key == r.Key }):
			out = append(out, r)
		}
	}
	if len(errs) > 0 {
		return nil, &ValidationError{Errors: errs}
	}
	return out, nil
}

// changeMembership gives a user exactly these roles in a project
// themselves (none: removes them). The caller may give or take away each
// role changed, and the project keeps someone managing its members.
func (s *Service) changeMembership(ctx context.Context, p *auth.Principal, proj store.Project, userID string, roles []auth.Role) (added, removed []string, err error) {
	err = s.Store.WithLock(ctx, store.LockAdmins, func() error {
		current, err := s.Store.DirectRoles(ctx, proj.ID, userID)
		if err != nil {
			return err
		}
		keys := make([]string, len(roles))
		manages := false
		for i, r := range roles {
			keys[i] = r.Key
			manages = manages || slices.Contains(r.Permissions, auth.ProjectMembersManage)
			if !slices.Contains(current, r.Key) {
				if !mayGive(p, r, proj.ID) {
					return fmt.Errorf("%w: you may not give %s: it allows what you may not do in %s", ErrForbidden, r.Name, proj.Name)
				}
				added = append(added, r.Key)
			}
		}
		managed := false
		for _, k := range current {
			if slices.Contains(keys, k) {
				continue
			}
			r, err := s.role(ctx, k)
			if err != nil {
				return err
			}
			if !mayGive(p, r, proj.ID) {
				return fmt.Errorf("%w: you may not take %s away: it allows what you may not do in %s", ErrForbidden, r.Name, proj.Name)
			}
			managed = managed || slices.Contains(r.Permissions, auth.ProjectMembersManage)
			removed = append(removed, k)
		}
		if managed && !manages {
			if err := s.keepsMembersManager(ctx, proj, "", userID, ""); err != nil {
				return err
			}
		}
		if len(roles) == 0 {
			return s.Store.RemoveMember(ctx, proj.ID, userID)
		}
		return s.Store.SetMemberRoles(ctx, proj.ID, userID, keys)
	})
	return added, removed, err
}

func (s *Service) SetProjectMember(ctx context.Context, p *auth.Principal, projectID, userID string, w api.ProjectMemberWrite) (api.ProjectMember, error) {
	proj, err := s.canManageMembers(ctx, p, projectID)
	if err != nil {
		return api.ProjectMember{}, err
	}
	roles, err := s.memberRoles(ctx, proj, w)
	if err != nil {
		return api.ProjectMember{}, err
	}
	if _, err := s.Store.GetUser(ctx, userID); errors.Is(err, store.ErrNotFound) {
		return api.ProjectMember{}, ErrNotFound
	} else if err != nil {
		return api.ProjectMember{}, err
	}
	added, removed, err := s.changeMembership(ctx, p, proj, userID, roles)
	if err != nil {
		return api.ProjectMember{}, err
	}
	detail := map[string]any{"user_id": userID, "added": nonNilStrings(added), "removed": nonNilStrings(removed)}
	if len(roles) == 1 {
		detail["role"] = roles[0].Key
	}
	s.audit(ctx, p, "member.set", "project", proj.ID, &proj.ID, detail)
	s.accessChanged(ctx)
	members, err := s.ProjectMembers(ctx, p, proj.ID)
	if err != nil {
		return api.ProjectMember{}, err
	}
	for _, m := range members {
		if m.User.Id == userID {
			return m, nil
		}
	}
	return api.ProjectMember{}, ErrNotFound
}

func (s *Service) RemoveProjectMember(ctx context.Context, p *auth.Principal, projectID, userID string) error {
	proj, err := s.canManageMembers(ctx, p, projectID)
	if err != nil {
		return err
	}
	_, removed, err := s.changeMembership(ctx, p, proj, userID, nil)
	if errors.Is(err, store.ErrNotFound) {
		return &ConflictError{Msg: "They are a member through a group only: change the group mapping."}
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "member.remove", "project", proj.ID, &proj.ID, map[string]any{"user_id": userID, "removed": nonNilStrings(removed)})
	s.accessChanged(ctx)
	return nil
}

func presentGroupMapping(m store.GroupMapping) api.GroupMapping {
	out := api.GroupMapping{Id: m.ID, OidcGroup: m.Group, Role: m.Role, RoleName: &m.RoleName, Users: &m.Users}
	if m.ProjectID != nil {
		out.Project = &api.ProjectRef{Id: *m.ProjectID, Code: deref(m.ProjectCode), Name: deref(m.ProjectName)}
	}
	return out
}

func (s *Service) GroupMappings(ctx context.Context, p *auth.Principal) ([]api.GroupMapping, error) {
	if !p.Everywhere(auth.AccessRead) {
		return nil, ErrForbidden
	}
	list, err := s.Store.GroupMappings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.GroupMapping, len(list))
	for i, m := range list {
		out[i] = presentGroupMapping(m)
	}
	return out, nil
}

func (s *Service) CreateGroupMapping(ctx context.Context, p *auth.Principal, w api.GroupMappingWrite) (api.GroupMapping, error) {
	if !p.Everywhere(auth.AccessManage) {
		return api.GroupMapping{}, ErrForbidden
	}
	group := strings.TrimSpace(w.OidcGroup)
	var errs []api.FieldError
	if group == "" {
		errs = append(errs, fieldErr("/oidc_group", "required", "Name the OIDC group."))
	}
	projectID := deref(w.ProjectId)
	r, err := s.role(ctx, w.Role)
	switch {
	case errors.Is(err, store.ErrNotFound):
		errs = append(errs, fieldErr("/role", "unknown", "There is no such role."))
	case err != nil:
		return api.GroupMapping{}, err
	case r.Key == "user":
		errs = append(errs, fieldErr("/role", "scope", "Everyone signed in holds User: map the group to another role."))
	case projectID != "" && !r.Assignable(projectID):
		errs = append(errs, fieldErr("/role", "scope", "In a project: "+r.Name+" is given for all of rvav only."))
	case projectID == "" && !r.Assignable(""):
		errs = append(errs, fieldErr("/role", "scope", "Without a project: "+r.Name+" is given in a project only."))
	}
	var project *string
	if projectID != "" {
		proj, err := s.Store.GetProject(ctx, projectID)
		if errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fieldErr("/project_id", "unknown", "There is no such project."))
		} else if err != nil {
			return api.GroupMapping{}, err
		} else {
			project = &proj.ID
		}
	}
	if len(errs) > 0 {
		return api.GroupMapping{}, &ValidationError{Errors: errs}
	}
	id := ids.New("gmp")
	if err := s.Store.CreateGroupMapping(ctx, id, group, project, w.Role); errors.Is(err, store.ErrInUse) {
		return api.GroupMapping{}, &ConflictError{Msg: group + " already has this role here."}
	} else if err != nil {
		return api.GroupMapping{}, err
	}
	s.audit(ctx, p, "group_mapping.create", "group_mapping", id, project, map[string]any{"group": group, "role": w.Role})
	s.accessChanged(ctx)
	m, err := s.Store.GetGroupMapping(ctx, id)
	return presentGroupMapping(m), err
}

func (s *Service) DeleteGroupMapping(ctx context.Context, p *auth.Principal, id string) error {
	if !p.Everywhere(auth.AccessManage) {
		return ErrForbidden
	}
	m, err := s.Store.GetGroupMapping(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := s.Store.WithLock(ctx, store.LockAdmins, func() error {
		r, err := s.role(ctx, m.Role)
		if err != nil {
			return err
		}
		if m.ProjectID == nil && slices.Contains(r.Permissions, auth.AccessManage) {
			if err := s.keepsAccessManager(ctx, "", "", m.ID, "map another group to admin first."); err != nil {
				return err
			}
		}
		if m.ProjectID != nil && slices.Contains(r.Permissions, auth.ProjectMembersManage) {
			proj := store.Project{ID: *m.ProjectID, Name: deref(m.ProjectName)}
			if err := s.keepsMembersManager(ctx, proj, "", "", m.ID); err != nil {
				return err
			}
		}
		return s.Store.DeleteGroupMapping(ctx, id)
	}); err != nil {
		return err
	}
	s.audit(ctx, p, "group_mapping.delete", "group_mapping", id, m.ProjectID, map[string]any{"group": m.Group, "role": m.Role})
	s.accessChanged(ctx)
	return nil
}

func presentAdminEnvironment(e store.AdminEnvironment) api.AdminEnvironment {
	return api.AdminEnvironment{Key: e.Key, Name: e.Name, Production: e.Production, Order: e.SortOrder,
		SnapshotMaxCount: e.SnapshotMaxCount, SnapshotMaxAgeDays: e.SnapshotMaxAgeDays, Projects: &e.Projects, Deployments: &e.Deployments,
		TagCategories: present.Ptr(nonNilStrings(e.TagCategories))}
}

// tagCategoryList is an environment's tag categories as sent: trimmed,
// each once, sorted.
func tagCategoryList(in *[]string) []string {
	if in == nil {
		return nil
	}
	out := []string{}
	for _, c := range *in {
		if c = strings.TrimSpace(c); c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

func (s *Service) AdminEnvironments(ctx context.Context, p *auth.Principal) ([]api.AdminEnvironment, error) {
	if !p.Everywhere(auth.EnvironmentsRead) {
		return nil, ErrForbidden
	}
	list, err := s.Store.AdminEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.AdminEnvironment, len(list))
	for i, e := range list {
		out[i] = presentAdminEnvironment(e)
	}
	return out, nil
}

func (s *Service) adminEnvironment(ctx context.Context, key string) (api.AdminEnvironment, error) {
	list, err := s.Store.AdminEnvironments(ctx)
	if err != nil {
		return api.AdminEnvironment{}, err
	}
	for _, e := range list {
		if e.Key == key {
			return presentAdminEnvironment(e), nil
		}
	}
	return api.AdminEnvironment{}, ErrNotFound
}

func (s *Service) CreateEnvironment(ctx context.Context, p *auth.Principal, e api.AdminEnvironment) (api.AdminEnvironment, error) {
	if !p.Everywhere(auth.EnvironmentsManage) {
		return api.AdminEnvironment{}, ErrForbidden
	}
	var errs []api.FieldError
	if !environmentKey.MatchString(e.Key) {
		errs = append(errs, fieldErr("/key", "pattern", "2 to 10 lower-case letters and digits, starting with a letter."))
	}
	if strings.TrimSpace(e.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the environment a name."))
	}
	if e.SnapshotMaxCount < 0 {
		errs = append(errs, fieldErr("/snapshot_max_count", "minimum", "Zero or more."))
	}
	if e.SnapshotMaxAgeDays != nil && *e.SnapshotMaxAgeDays < 1 {
		errs = append(errs, fieldErr("/snapshot_max_age_days", "minimum", "At least a day, or none."))
	}
	if len(errs) > 0 {
		return api.AdminEnvironment{}, &ValidationError{Errors: errs}
	}
	if err := s.Store.CreateEnvironment(ctx, store.Environment{Key: e.Key, Name: strings.TrimSpace(e.Name), Production: e.Production,
		SortOrder: e.Order, SnapshotMaxCount: e.SnapshotMaxCount, SnapshotMaxAgeDays: e.SnapshotMaxAgeDays,
		TagCategories: tagCategoryList(e.TagCategories)}); errors.Is(err, store.ErrInUse) {
		return api.AdminEnvironment{}, &ConflictError{Msg: "An environment has the key " + e.Key + "."}
	} else if err != nil {
		return api.AdminEnvironment{}, err
	}
	s.audit(ctx, p, "environment.create", "environment", e.Key, nil, e)
	return s.adminEnvironment(ctx, e.Key)
}

func (s *Service) UpdateEnvironment(ctx context.Context, p *auth.Principal, key string, u api.AdminEnvironmentUpdate) (api.AdminEnvironment, error) {
	if !p.Everywhere(auth.EnvironmentsManage) {
		return api.AdminEnvironment{}, ErrForbidden
	}
	if u.Name != nil && strings.TrimSpace(*u.Name) == "" {
		return api.AdminEnvironment{}, &ValidationError{Errors: []api.FieldError{fieldErr("/name", "required", "Give the environment a name.")}}
	}
	upd := store.EnvironmentUpdate{Name: u.Name, Production: u.Production, SortOrder: u.Order, SnapshotMaxCount: u.SnapshotMaxCount,
		TagCategories: tagCategoryList(u.TagCategories)}
	if u.SnapshotMaxAgeDays != nil {
		if *u.SnapshotMaxAgeDays == 0 {
			upd.ClearSnapshotMaxAge = true
		} else {
			upd.SnapshotMaxAgeDays = u.SnapshotMaxAgeDays
		}
	}
	if err := s.Store.UpdateEnvironment(ctx, key, upd); errors.Is(err, store.ErrNotFound) {
		return api.AdminEnvironment{}, ErrNotFound
	} else if err != nil {
		return api.AdminEnvironment{}, err
	}
	s.audit(ctx, p, "environment.update", "environment", key, nil, u)
	return s.adminEnvironment(ctx, key)
}

func (s *Service) DeleteEnvironment(ctx context.Context, p *auth.Principal, key string) error {
	if !p.Everywhere(auth.EnvironmentsManage) {
		return ErrForbidden
	}
	switch err := s.Store.DeleteEnvironment(ctx, key); {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrInUse):
		return &ConflictError{Msg: "Projects, placement rules, quotas, requests or deployments use " + key + "."}
	case err != nil:
		return err
	}
	s.audit(ctx, p, "environment.delete", "environment", key, nil, nil)
	return nil
}

// SearchUsers finds people by name or email, for any signed-in user.
func (s *Service) SearchUsers(ctx context.Context, q string, limit int) ([]api.UserRef, error) {
	list, err := s.Store.SearchUsers(ctx, strings.TrimSpace(q), limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.UserRef, len(list))
	for i, u := range list {
		out[i] = userRef(u)
	}
	return out, nil
}
