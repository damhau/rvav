package core

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// ownSources are the built-in sources the project, user, deployment and
// vsphere_vm widgets always take their choices from: rvav's own records,
// which the server checks against what the requester may see.
var ownSources = map[api.UiWidget]string{
	api.UiWidgetProject:    "rvav.projects",
	api.UiWidgetUser:       "rvav.users",
	api.UiWidgetDeployment: "rvav.deployments",
	api.UiWidgetVsphereVm:  "rvav.vms",
}

// sourceOf is the value source a field resolves: its own, or, for a widget
// that always has one, that one.
func sourceOf(f api.UiField) *api.ValueSource {
	if name, ok := ownSources[f.Widget]; ok && f.ValueSource == nil {
		return &api.ValueSource{Kind: api.Builtin, Name: &name}
	}
	return f.ValueSource
}

// peopleFound is how many people a search of rvav.users lists at most.
const peopleFound = 20

// rvavOptions are what an rvav.* source offers the principal requesting in
// a project.
func (s *Service) rvavOptions(ctx context.Context, p *auth.Principal, proj store.Project, name string,
	params map[string]string, search string) ([]api.Option, error) {
	out := []api.Option{}
	switch name {
	case "rvav.projects":
		projects, err := s.requestableProjects(ctx, p)
		for _, pr := range projects {
			out = append(out, option(pr.ID, pr.Name, pr.Code))
		}
		return out, err
	case "rvav.users":
		// A search for an ID finds that person, so a form can name the one
		// it was given (a draft, Request again).
		if u, err := s.person(ctx, strings.TrimSpace(search)); err == nil {
			return []api.Option{option(u.ID, u.Name, u.Email)}, nil
		}
		users, err := s.Store.SearchUsers(ctx, strings.TrimSpace(search), peopleFound)
		for _, u := range users {
			out = append(out, option(u.ID, u.Name, u.Email))
		}
		return out, err
	case "rvav.deployments":
		page, err := s.Store.ListDeployments(ctx, store.DeploymentFilter{ProjectID: proj.ID, Sort: "name", Limit: 500})
		for _, d := range page.Items {
			if item := params["item"]; item == "" || d.ItemSlug == item {
				out = append(out, option(d.ID, d.Name, d.ItemName+" · "+d.Environment))
			}
		}
		return out, err
	case "rvav.vms":
		vms, err := s.Store.ProjectVMs(ctx, proj.ID, params["deployment"])
		for _, vm := range vms {
			out = append(out, option(vm.Name, vm.Name, vm.DeploymentName))
		}
		return out, err
	}
	return nil, errors.New("not an rvav source: " + name)
}

// rvavAllowed are the values of an rvav.* source the principal requesting
// in a project may give, among those given: checked one by one, as a list
// offered may be cut short.
func (s *Service) rvavAllowed(ctx context.Context, p *auth.Principal, proj store.Project, name string,
	params map[string]string, values []string) ([]string, error) {
	var allowed []string
	switch name {
	case "rvav.projects":
		projects, err := s.requestableProjects(ctx, p)
		if err != nil {
			return nil, err
		}
		for _, pr := range projects {
			allowed = append(allowed, pr.ID)
		}
	case "rvav.users":
		for _, v := range values {
			switch _, err := s.person(ctx, v); {
			case err == nil:
				allowed = append(allowed, v)
			case !errors.Is(err, store.ErrNotFound):
				return nil, err
			}
		}
	case "rvav.deployments":
		for _, v := range values {
			d, err := s.Store.GetDeployment(ctx, v)
			switch {
			case errors.Is(err, store.ErrNotFound):
			case err != nil:
				return nil, err
			case d.ProjectID == proj.ID && d.Status != "deleted" && (params["item"] == "" || d.ItemSlug == params["item"]):
				allowed = append(allowed, v)
			}
		}
	case "rvav.vms":
		vms, err := s.Store.ProjectVMs(ctx, proj.ID, params["deployment"])
		if err != nil {
			return nil, err
		}
		for _, vm := range vms {
			allowed = append(allowed, vm.Name)
		}
	}
	return allowed, nil
}

// requestableProjects are the projects the principal may request in, not
// archived, by name.
func (s *Service) requestableProjects(ctx context.Context, p *auth.Principal) ([]store.Project, error) {
	page, err := s.Store.ListProjects(ctx, p.Projects(auth.RequestSubmit), 500, nil)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(page.Items, func(pr store.Project) bool { return pr.ArchivedAt != nil }), nil
}

// person finds someone by ID: a person, not a user rvav acts as itself.
func (s *Service) person(ctx context.Context, id string) (store.User, error) {
	if id == "" {
		return store.User{}, store.ErrNotFound
	}
	u, err := s.Store.GetUser(ctx, id)
	if err == nil && strings.HasPrefix(u.Subject, "system:") {
		return u, store.ErrNotFound
	}
	return u, err
}
