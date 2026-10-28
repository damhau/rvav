package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// Administration of projects, members, OIDC group mappings and
// environments (ADR-0020).

var (
	projectCode    = regexp.MustCompile(`^[a-z][a-z0-9]{1,7}$`)
	environmentKey = regexp.MustCompile(`^[a-z][a-z0-9]{1,9}$`)
	projectRoles   = []string{"project-admin", "member", "viewer"}
	globalRoles    = []string{"admin", "catalog-admin", "auditor"}
)

func (s *Service) audit(ctx context.Context, p *auth.Principal, action, targetType, targetID string, projectID *string, detail any) {
	if err := s.Store.Audit(ctx, s.Store.Pool, store.AuditEvent{ActorID: &p.User.ID, Action: action, TargetType: targetType,
		TargetID: targetID, ProjectID: projectID, Detail: detail}); err != nil {
		s.Log.Error("audit", "action", action, "err", err)
	}
}

func presentAdminProject(pr store.AdminProject, role string) api.AdminProject {
	base := presentProject(pr.Project, role)
	return api.AdminProject{Id: base.Id, Code: base.Code, Name: base.Name, Description: base.Description, CostCenter: base.CostCenter,
		Environments: base.Environments, MyRole: base.MyRole, Archived: pr.ArchivedAt != nil, ArchivedAt: pr.ArchivedAt,
		CreatedAt: pr.CreatedAt, Members: pr.Members, Deployments: pr.Deployments}
}

func presentProject(p store.Project, role string) api.Project {
	out := api.Project{Id: p.ID, Code: p.Code, Name: p.Name, Environments: append([]string{}, p.Environments...)}
	if p.Description != "" {
		out.Description = &p.Description
	}
	if p.CostCenter != "" {
		out.CostCenter = &p.CostCenter
	}
	if role != "" {
		r := api.ProjectRole(role)
		out.MyRole = &r
	}
	return out
}

// AdminProjects lists projects for administrators and auditors.
func (s *Service) AdminProjects(ctx context.Context, p *auth.Principal, search string, archived bool) ([]api.AdminProject, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	list, err := s.Store.AdminProjects(ctx, search, archived)
	if err != nil {
		return nil, err
	}
	out := make([]api.AdminProject, len(list))
	for i, pr := range list {
		out[i] = presentAdminProject(pr, p.ProjectRole(pr.ID))
	}
	return out, nil
}

// AdminProject is one project for its administrators: administrators,
// auditors and its project admins.
func (s *Service) AdminProject(ctx context.Context, p *auth.Principal, id string) (api.AdminProject, error) {
	pr, err := s.Store.GetAdminProject(ctx, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && !p.CanSeeProject(pr.ID) {
		return api.AdminProject{}, ErrNotFound
	}
	if err != nil {
		return api.AdminProject{}, err
	}
	if !p.SeesEverything() && p.ProjectRole(pr.ID) != "project-admin" {
		return api.AdminProject{}, ErrForbidden
	}
	return presentAdminProject(pr, p.ProjectRole(pr.ID)), nil
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
	if !p.IsAdmin() {
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
	if err := s.Store.CreateProject(ctx, pr, admins); errors.Is(err, store.ErrInUse) {
		return api.AdminProject{}, &ConflictError{Msg: "A project has the code " + c.Code + "."}
	} else if err != nil {
		return api.AdminProject{}, err
	}
	s.audit(ctx, p, "project.create", "project", pr.ID, &pr.ID, map[string]any{"code": pr.Code, "name": pr.Name, "admins": admins})
	return s.AdminProject(ctx, p, pr.ID)
}

func (s *Service) UpdateProject(ctx context.Context, p *auth.Principal, id string, u api.AdminProjectUpdate) (api.AdminProject, error) {
	if !p.IsAdmin() {
		return api.AdminProject{}, ErrForbidden
	}
	pr, err := s.Store.GetProject(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.AdminProject{}, ErrNotFound
	}
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
	if len(errs) > 0 {
		return api.AdminProject{}, &ValidationError{Errors: errs}
	}
	if err := s.Store.UpdateProject(ctx, pr.ID, store.ProjectUpdate{Name: u.Name, Description: u.Description, CostCenter: u.CostCenter,
		Environments: u.Environments, Archived: u.Archived}); err != nil {
		return api.AdminProject{}, err
	}
	action := "project.update"
	if u.Archived != nil && *u.Archived != (pr.ArchivedAt != nil) {
		action = map[bool]string{true: "project.archive", false: "project.unarchive"}[*u.Archived]
	}
	s.audit(ctx, p, action, "project", pr.ID, &pr.ID, u)
	return s.AdminProject(ctx, p, pr.ID)
}

var roleRank = map[string]int{"viewer": 1, "member": 2, "project-admin": 3}

// ProjectMembers lists a project's members: their role (the highest they
// have), their own role in it and the groups that give them one.
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
			out = append(out, api.ProjectMember{User: userRef(r.User), Groups: []string{}})
		}
		m := &out[i]
		if r.Group == nil {
			role := api.ProjectRole(r.Role)
			m.Direct, m.DirectRole = true, &role
		} else if !slices.Contains(m.Groups, *r.Group) {
			m.Groups = append(m.Groups, *r.Group)
		}
		if roleRank[r.Role] > roleRank[string(m.Role)] {
			m.Role = api.ProjectRole(r.Role)
		}
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

// canManageMembers: project admins of the project, and administrators.
func (s *Service) canManageMembers(ctx context.Context, p *auth.Principal, projectID string) (store.Project, error) {
	proj, err := s.visibleProject(ctx, p, projectID)
	if err != nil {
		return proj, err
	}
	if p.ProjectRole(proj.ID) != "project-admin" {
		return proj, ErrForbidden
	}
	return proj, nil
}

// keepsAdmin refuses to take away a project's last project admin.
func (s *Service) keepsAdmin(ctx context.Context, proj store.Project, userID string) error {
	current, err := s.Store.DirectRole(ctx, proj.ID, userID)
	if err != nil || current != "project-admin" {
		return err
	}
	others, err := s.Store.OtherProjectAdmins(ctx, proj.ID, userID)
	if err != nil {
		return err
	}
	if others == 0 {
		return &ConflictError{Msg: proj.Name + " would have no project admin: make someone else one first."}
	}
	return nil
}

func (s *Service) SetProjectMember(ctx context.Context, p *auth.Principal, projectID, userID string, role string) (api.ProjectMember, error) {
	proj, err := s.canManageMembers(ctx, p, projectID)
	if err != nil {
		return api.ProjectMember{}, err
	}
	if !slices.Contains(projectRoles, role) {
		return api.ProjectMember{}, &ValidationError{Errors: []api.FieldError{fieldErr("/role", "enum", "project-admin, member or viewer.")}}
	}
	if _, err := s.Store.GetUser(ctx, userID); errors.Is(err, store.ErrNotFound) {
		return api.ProjectMember{}, ErrNotFound
	} else if err != nil {
		return api.ProjectMember{}, err
	}
	if role != "project-admin" {
		if err := s.keepsAdmin(ctx, proj, userID); err != nil {
			return api.ProjectMember{}, err
		}
	}
	if err := s.Store.SetMember(ctx, proj.ID, userID, role); err != nil {
		return api.ProjectMember{}, err
	}
	s.audit(ctx, p, "member.set", "project", proj.ID, &proj.ID, map[string]any{"user_id": userID, "role": role})
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
	if err := s.keepsAdmin(ctx, proj, userID); err != nil {
		return err
	}
	if err := s.Store.RemoveMember(ctx, proj.ID, userID); errors.Is(err, store.ErrNotFound) {
		return &ConflictError{Msg: "They are a member through a group only: change the group mapping."}
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "member.remove", "project", proj.ID, &proj.ID, map[string]any{"user_id": userID})
	return nil
}

func presentGroupMapping(m store.GroupMapping) api.GroupMapping {
	out := api.GroupMapping{Id: m.ID, OidcGroup: m.Group, Role: m.Role, Users: &m.Users}
	if m.ProjectID != nil {
		out.Project = &api.ProjectRef{Id: *m.ProjectID, Code: deref(m.ProjectCode), Name: deref(m.ProjectName)}
	}
	return out
}

func (s *Service) GroupMappings(ctx context.Context, p *auth.Principal) ([]api.GroupMapping, error) {
	if !p.SeesEverything() {
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
	if !p.IsAdmin() {
		return api.GroupMapping{}, ErrForbidden
	}
	group := strings.TrimSpace(w.OidcGroup)
	var errs []api.FieldError
	if group == "" {
		errs = append(errs, fieldErr("/oidc_group", "required", "Name the OIDC group."))
	}
	projectID := deref(w.ProjectId)
	switch {
	case projectID != "" && !slices.Contains(projectRoles, w.Role):
		errs = append(errs, fieldErr("/role", "enum", "In a project: project-admin, member or viewer."))
	case projectID == "" && !slices.Contains(globalRoles, w.Role):
		errs = append(errs, fieldErr("/role", "enum", "Without a project: admin, catalog-admin or auditor."))
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
	m, err := s.Store.GetGroupMapping(ctx, id)
	return presentGroupMapping(m), err
}

func (s *Service) DeleteGroupMapping(ctx context.Context, p *auth.Principal, id string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	m, err := s.Store.GetGroupMapping(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if m.Role == "admin" && m.ProjectID == nil {
		if n, err := s.Store.AdminSources(ctx, m.ID); err != nil {
			return err
		} else if n == 0 {
			return &ConflictError{Msg: "Nobody would be an administrator any more: map another group to admin first."}
		}
	}
	if m.Role == "project-admin" && m.ProjectID != nil {
		if n, err := s.Store.OtherProjectAdmins(ctx, *m.ProjectID, ""); err != nil {
			return err
		} else if n <= 1 {
			return &ConflictError{Msg: deref(m.ProjectName) + " would have no project admin: make someone one first."}
		}
	}
	if err := s.Store.DeleteGroupMapping(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, p, "group_mapping.delete", "group_mapping", id, m.ProjectID, map[string]any{"group": m.Group, "role": m.Role})
	return nil
}

func presentAdminEnvironment(e store.AdminEnvironment) api.AdminEnvironment {
	return api.AdminEnvironment{Key: e.Key, Name: e.Name, Production: e.Production, Order: e.SortOrder,
		SnapshotMaxCount: e.SnapshotMaxCount, SnapshotMaxAgeDays: e.SnapshotMaxAgeDays, Projects: &e.Projects, Deployments: &e.Deployments}
}

func (s *Service) AdminEnvironments(ctx context.Context, p *auth.Principal) ([]api.AdminEnvironment, error) {
	if !p.SeesEverything() {
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
	if !p.IsAdmin() {
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
		SortOrder: e.Order, SnapshotMaxCount: e.SnapshotMaxCount, SnapshotMaxAgeDays: e.SnapshotMaxAgeDays}); errors.Is(err, store.ErrInUse) {
		return api.AdminEnvironment{}, &ConflictError{Msg: "An environment has the key " + e.Key + "."}
	} else if err != nil {
		return api.AdminEnvironment{}, err
	}
	s.audit(ctx, p, "environment.create", "environment", e.Key, nil, e)
	return s.adminEnvironment(ctx, e.Key)
}

func (s *Service) UpdateEnvironment(ctx context.Context, p *auth.Principal, key string, u api.AdminEnvironmentUpdate) (api.AdminEnvironment, error) {
	if !p.IsAdmin() {
		return api.AdminEnvironment{}, ErrForbidden
	}
	if u.Name != nil && strings.TrimSpace(*u.Name) == "" {
		return api.AdminEnvironment{}, &ValidationError{Errors: []api.FieldError{fieldErr("/name", "required", "Give the environment a name.")}}
	}
	upd := store.EnvironmentUpdate{Name: u.Name, Production: u.Production, SortOrder: u.Order, SnapshotMaxCount: u.SnapshotMaxCount}
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
	if !p.IsAdmin() {
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
