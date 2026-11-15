package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

func roleRefs(p *auth.Principal, keys []string) []api.RoleRef {
	out := []api.RoleRef{}
	for _, r := range p.RoleRefs(keys) {
		out = append(out, api.RoleRef{Key: r.Key, Name: r.Name})
	}
	return out
}

// meRole is a project's deprecated role in /me: the strongest built-in role
// held there, project-admin for an administrator and viewer for an auditor
// elsewhere, as before (ADR-0034).
func meRole(p *auth.Principal, projectID string) *api.ProjectRole {
	role := p.ProjectRoles[projectID]
	switch {
	case role != "":
	case p.IsAdmin():
		role = string(api.ProjectAdmin)
	case p.SeesEverything():
		role = string(api.Viewer)
	default:
		return nil
	}
	r := api.ProjectRole(role)
	return &r
}

func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	// Held for all of rvav, project.see lists every project, up to 200, and
	// those where a role is given, as before.
	ids := p.Projects(auth.ProjectSee)
	var projects []store.Project
	if ids == nil {
		all, err := s.Store.ListProjects(r.Context(), nil, 200, nil)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		projects = all.Items
		ids = slices.DeleteFunc(p.ProjectsGiven(), func(id string) bool {
			return slices.ContainsFunc(projects, func(pr store.Project) bool { return pr.ID == id })
		})
	}
	byID, err := s.Store.ProjectsByID(r.Context(), ids)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, pr := range byID {
		projects = append(projects, pr)
	}
	out := api.Me{
		User:        present.UserRef(p.User.ID, p.User.Name, p.User.Email),
		GlobalRoles: []api.MeGlobalRoles{},
		Permissions: p.PermissionsIn(""),
		Roles:       roleRefs(p, p.Roles),
		Projects:    []api.MeProject{},
	}
	if p.User.Title != "" {
		out.Title = &p.User.Title
	}
	if p.Session != "" {
		out.Local, out.PasswordChangeRequired = present.Ptr(true), present.Ptr(p.MustChangePassword)
	}
	for _, g := range p.GlobalRoles {
		if b, ok := auth.BuiltinRole(g); ok && b.Global {
			out.GlobalRoles = append(out.GlobalRoles, api.MeGlobalRoles(g))
		}
	}
	for _, pr := range projects {
		out.Projects = append(out.Projects, api.MeProject{Project: present.ProjectRef(pr.ID, pr.Code, pr.Name),
			Role: meRole(p, pr.ID), Roles: roleRefs(p, p.RolesIn(pr.ID)), Permissions: p.PermissionsIn(pr.ID)})
	}
	sort.SliceStable(out.Projects, func(i, j int) bool { return out.Projects[i].Project.Name < out.Projects[j].Project.Name })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ListEnvironments(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListEnvironments(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]api.Environment, len(list))
	for i, e := range list {
		items[i] = present.Environment(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) ListProjects(w http.ResponseWriter, r *http.Request, params api.ListProjectsParams) {
	p := principal(r)
	after, err := cursorOf(params.Cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	only := p.VisibleProjects()
	if only != nil && len(only) == 0 {
		writeJSON(w, http.StatusOK, api.ProjectPage{Items: []api.Project{}})
		return
	}
	page, err := s.Store.ListProjects(r.Context(), only, limitOf(params.Limit), after)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := api.ProjectPage{Items: make([]api.Project, len(page.Items)), NextCursor: nextCursor(page.Next)}
	for i, pr := range page.Items {
		out.Items[i] = present.Project(pr, p.ProjectRole(pr.ID), p.PermissionsIn(pr.ID))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) visibleProject(w http.ResponseWriter, r *http.Request, idOrCode string) (store.Project, bool) {
	pr, err := s.Store.GetProject(r.Context(), idOrCode)
	if err == nil && !principal(r).CanSeeProject(pr.ID) {
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return pr, false
	}
	return pr, true
}

func (s *Server) GetProject(w http.ResponseWriter, r *http.Request, projectID api.ProjectIdPath) {
	pr, ok := s.visibleProject(w, r, projectID)
	if !ok {
		return
	}
	p := principal(r)
	writeJSON(w, http.StatusOK, present.Project(pr, p.ProjectRole(pr.ID), p.PermissionsIn(pr.ID)))
}

type quotaLine struct {
	Enforcement *string `json:"enforcement,omitempty"`
	Limit       *int    `json:"limit"`
	Reserved    int     `json:"reserved"`
	Resource    string  `json:"resource"`
	Used        int     `json:"used"`
}

func (s *Server) GetProjectQuota(w http.ResponseWriter, r *http.Request, projectID api.ProjectIdPath, params api.GetProjectQuotaParams) {
	pr, ok := s.visibleProject(w, r, projectID)
	if !ok {
		return
	}
	env := ""
	if params.Environment != nil {
		env = *params.Environment
	}
	ctx := r.Context()
	limits, err := s.Store.QuotaLimits(ctx, pr.ID, env)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	used, err := s.Store.QuotaUsed(ctx, pr.ID, env)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reserved, err := s.Store.QuotaReserved(ctx, pr.ID, env)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byRes := map[string]store.QuotaLimit{}
	for _, l := range limits {
		byRes[l.Resource] = l
	}
	// Written by hand so an unlimited resource is sent as "limit": null.
	lines := []quotaLine{}
	for _, res := range []string{"vcpu", "memory_gb", "storage_gb", "vm_count"} {
		line := quotaLine{Resource: res, Used: used[res], Reserved: reserved[res]}
		if l, ok := byRes[res]; ok {
			line.Limit, line.Enforcement = l.Limit, &l.Enforcement
		}
		lines = append(lines, line)
	}
	out := map[string]any{"project_id": pr.ID, "environment": nil, "lines": lines}
	if env != "" {
		out["environment"] = env
	}
	b, _ := json.Marshal(out)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(append(b, '\n'))
}
