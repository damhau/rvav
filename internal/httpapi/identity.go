package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	idList := make([]string, 0, len(p.ProjectRoles))
	for id := range p.ProjectRoles {
		idList = append(idList, id)
	}
	projects, err := s.Store.ProjectsByID(r.Context(), idList)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := api.Me{
		User:        present.UserRef(p.User.ID, p.User.Name, p.User.Email),
		GlobalRoles: []api.MeGlobalRoles{},
		Projects: []struct {
			Project api.ProjectRef  `json:"project"`
			Role    api.ProjectRole `json:"role"`
		}{},
	}
	if p.User.Title != "" {
		out.Title = &p.User.Title
	}
	if p.Session != "" {
		out.Local, out.PasswordChangeRequired = present.Ptr(true), present.Ptr(p.MustChangePassword)
	}
	for _, g := range p.GlobalRoles {
		out.GlobalRoles = append(out.GlobalRoles, api.MeGlobalRoles(g))
	}
	for id, role := range p.ProjectRoles {
		pr, ok := projects[id]
		if !ok {
			continue
		}
		out.Projects = append(out.Projects, struct {
			Project api.ProjectRef  `json:"project"`
			Role    api.ProjectRole `json:"role"`
		}{Project: present.ProjectRef(pr.ID, pr.Code, pr.Name), Role: api.ProjectRole(role)})
	}
	// Administrators and auditors work in every project, not only their own.
	if p.SeesEverything() {
		all, err := s.Store.ListProjects(r.Context(), nil, 200, nil)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, pr := range all.Items {
			if _, member := p.ProjectRoles[pr.ID]; member {
				continue
			}
			role := api.Viewer
			if p.IsAdmin() {
				role = api.ProjectAdmin
			}
			out.Projects = append(out.Projects, struct {
				Project api.ProjectRef  `json:"project"`
				Role    api.ProjectRole `json:"role"`
			}{Project: present.ProjectRef(pr.ID, pr.Code, pr.Name), Role: role})
		}
	}
	sort.Slice(out.Projects, func(i, j int) bool { return out.Projects[i].Project.Name < out.Projects[j].Project.Name })
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
		out.Items[i] = present.Project(pr, p.ProjectRole(pr.ID))
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
	writeJSON(w, http.StatusOK, present.Project(pr, principal(r).ProjectRole(pr.ID)))
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
