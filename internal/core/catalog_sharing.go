package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// Sharing: who may request an item, in which projects (ADR-0019).

// offered checks that a project's catalog has an item, published, for the
// principal: ErrNotFound when it doesn't, ErrForbidden when its sharing
// there names others only.
func (s *Service) offered(ctx context.Context, p *auth.Principal, it store.CatalogItem, proj store.Project) error {
	inProject, withYou, err := s.Store.SharedWith(ctx, it.ID, proj.ID, p.Requester(proj.ID))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return err
	case !inProject || it.PublishedVersionID == nil:
		return ErrNotFound
	case !withYou:
		return fmt.Errorf("%w: %s is shared in %s with chosen people, and you aren't one of them", ErrForbidden, it.Name, proj.Name)
	}
	return nil
}

// SharedWithRequester says whether the principal finds an item in a
// project's catalog: shared with the project, and with them there.
func (s *Service) SharedWithRequester(ctx context.Context, p *auth.Principal, itemID, projectID string) (bool, error) {
	inProject, withYou, err := s.Store.SharedWith(ctx, itemID, projectID, p.Requester(projectID))
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return inProject && withYou, err
}

// SharedAnywhere says whether the principal finds an item in the catalog
// of any project they see.
func (s *Service) SharedAnywhere(ctx context.Context, p *auth.Principal, itemID string) (bool, error) {
	page, err := s.Store.ListProjects(ctx, p.Projects(auth.ProjectSee), 500, nil)
	if err != nil {
		return false, err
	}
	for _, pr := range page.Items {
		if pr.ArchivedAt != nil {
			continue
		}
		if shared, err := s.SharedWithRequester(ctx, p, itemID, pr.ID); err != nil || shared {
			return shared, err
		}
	}
	return false, nil
}

// CatalogItemEntitlements is who may request an item, in which projects.
func (s *Service) CatalogItemEntitlements(ctx context.Context, p *auth.Principal, idOrSlug string) (api.CatalogEntitlements, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.CatalogEntitlements{}, err
	}
	return s.entitlements(ctx, it)
}

func presentAudience(a store.Audience) api.CatalogAudience {
	return api.CatalogAudience{UserIds: nonNilList(a.UserIDs), Groups: nonNilList(a.Groups), Roles: nonNilList(a.Roles)}
}

func nonNilList(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func (s *Service) entitlements(ctx context.Context, it store.AdminItem) (api.CatalogEntitlements, error) {
	sh, err := s.Store.ItemSharing(ctx, it.ID)
	if err != nil {
		return api.CatalogEntitlements{}, err
	}
	everywhere := presentAudience(sh.Everywhere)
	out := api.CatalogEntitlements{AllProjects: sh.AllProjects, ProjectIds: []string{}, Everywhere: &everywhere}
	audiences := []api.CatalogProjectAudience{}
	named := slices.Clone(sh.Everywhere.UserIDs)
	for _, ps := range sh.Projects {
		out.ProjectIds = append(out.ProjectIds, ps.ProjectID)
		if !ps.Everyone() {
			a := presentAudience(ps.Audience)
			audiences = append(audiences, api.CatalogProjectAudience{ProjectId: ps.ProjectID, UserIds: a.UserIds, Groups: a.Groups, Roles: a.Roles})
			named = append(named, ps.UserIDs...)
		}
	}
	out.Audiences = &audiences
	users := []api.UserRef{}
	if len(named) > 0 {
		found, err := s.Store.FindUsers(ctx, slices.Compact(slices.Sorted(slices.Values(named))))
		if err != nil {
			return out, err
		}
		for _, u := range found {
			users = append(users, userRef(u))
		}
		slices.SortFunc(users, func(a, b api.UserRef) int { return strings.Compare(a.Name+a.Id, b.Name+b.Id) })
	}
	out.Users = &users
	roles, err := s.projectRoles(ctx)
	if err != nil {
		return out, err
	}
	out.Roles = &roles
	all, err := s.Store.ListProjects(ctx, nil, 500, nil)
	if err != nil {
		return out, err
	}
	projects := make([]struct {
		Code string `json:"code"`
		Id   string `json:"id"`
		Name string `json:"name"`
	}, len(all.Items))
	for i, p := range all.Items {
		projects[i].Code, projects[i].Id, projects[i].Name = p.Code, p.ID, p.Name
	}
	out.Projects = &projects
	return out, nil
}

// SetCatalogItemEntitlements shares an item with every project or chosen
// projects, and in each with all its members or the people an audience
// names.
func (s *Service) SetCatalogItemEntitlements(ctx context.Context, p *auth.Principal, idOrSlug string, e api.CatalogEntitlements) (api.CatalogEntitlements, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.CatalogEntitlements{}, err
	}
	old, err := s.Store.ItemSharing(ctx, it.ID)
	if err != nil {
		return api.CatalogEntitlements{}, err
	}
	sh, err := s.sharingOf(ctx, e, old)
	if err != nil {
		return api.CatalogEntitlements{}, err
	}
	if err := s.Store.SetSharing(ctx, it.ID, sh); err != nil {
		return api.CatalogEntitlements{}, err
	}
	s.auditCatalog(ctx, p, "catalog.item.entitlements", it.ID, sharingDetail(sh))
	return s.CatalogItemEntitlements(ctx, p, it.ID)
}

func sharingDetail(sh store.Sharing) map[string]any {
	projects := make([]string, len(sh.Projects))
	audiences := []api.CatalogProjectAudience{}
	for i, ps := range sh.Projects {
		projects[i] = ps.ProjectID
		if !ps.Everyone() {
			a := presentAudience(ps.Audience)
			audiences = append(audiences, api.CatalogProjectAudience{ProjectId: ps.ProjectID, UserIds: a.UserIds, Groups: a.Groups, Roles: a.Roles})
		}
	}
	d := map[string]any{"all_projects": sh.AllProjects, "projects": projects}
	if !sh.Everywhere.Everyone() {
		d["everywhere"] = presentAudience(sh.Everywhere)
	}
	if len(audiences) > 0 {
		d["audiences"] = audiences
	}
	return d
}

// sharingOf checks the sharing asked for: an audience a request leaves
// out is the one each project had (all members for one added), the
// everywhere audience the one the item had while it stays shared with
// every project.
func (s *Service) sharingOf(ctx context.Context, e api.CatalogEntitlements, old store.Sharing) (store.Sharing, error) {
	unknown, err := s.Store.UnknownProjects(ctx, e.ProjectIds)
	if err != nil {
		return store.Sharing{}, err
	}
	if len(unknown) > 0 {
		return store.Sharing{}, &ValidationError{Errors: []api.FieldError{fieldErr("/project_ids", "unknown",
			"No such project: "+strings.Join(unknown, ", ")+".")}}
	}
	projects := slices.Compact(slices.Sorted(slices.Values(e.ProjectIds)))
	sh := store.Sharing{AllProjects: e.AllProjects}
	var errs []api.FieldError
	audiences := map[string]store.Audience{}
	if e.Audiences == nil {
		for _, ps := range old.Projects {
			audiences[ps.ProjectID] = ps.Audience
		}
	} else {
		for i, a := range *e.Audiences {
			ptr := fmt.Sprintf("/audiences/%d", i)
			switch _, dup := audiences[a.ProjectId]; {
			case !slices.Contains(projects, a.ProjectId):
				errs = append(errs, fieldErr(ptr+"/project_id", "not_shared",
					"The item isn't shared with "+a.ProjectId+": add it to project_ids."))
				continue
			case dup:
				errs = append(errs, fieldErr(ptr+"/project_id", "duplicate", "Give one audience per project."))
				continue
			}
			aud, aerrs, err := s.audienceOf(ctx, ptr, api.CatalogAudience{UserIds: a.UserIds, Groups: a.Groups, Roles: a.Roles})
			if err != nil {
				return sh, err
			}
			errs = append(errs, aerrs...)
			audiences[a.ProjectId] = aud
		}
	}
	switch {
	case e.Everywhere != nil:
		aud, aerrs, err := s.audienceOf(ctx, "/everywhere", *e.Everywhere)
		if err != nil {
			return sh, err
		}
		errs = append(errs, aerrs...)
		if !e.AllProjects && !aud.Everyone() {
			errs = append(errs, fieldErr("/everywhere", "not_all_projects",
				"Name who may request in every project only when the item is shared with every project."))
		}
		if e.AllProjects {
			sh.Everywhere = aud
		}
	case e.AllProjects && old.AllProjects:
		sh.Everywhere = old.Everywhere
	}
	if len(errs) > 0 {
		return sh, &ValidationError{Errors: errs}
	}
	for _, id := range projects {
		sh.Projects = append(sh.Projects, store.ProjectShare{ProjectID: id, Audience: audiences[id]})
	}
	return sh, nil
}

// audienceOf checks an audience's users, groups and roles, without
// repeats, in order.
func (s *Service) audienceOf(ctx context.Context, ptr string, a api.CatalogAudience) (store.Audience, []api.FieldError, error) {
	var errs []api.FieldError
	out := store.Audience{}
	for i, g := range a.Groups {
		if g = strings.TrimSpace(g); g == "" {
			errs = append(errs, fieldErr(fmt.Sprintf("%s/groups/%d", ptr, i), "required", "Give the group's name."))
			continue
		}
		out.Groups = append(out.Groups, g)
	}
	for i, k := range a.Roles {
		r, err := s.role(ctx, k)
		if errors.Is(err, store.ErrNotFound) || err == nil && !r.InProjects() {
			errs = append(errs, fieldErr(fmt.Sprintf("%s/roles/%d", ptr, i), "unknown", "There is no role "+k+" given in projects."))
			continue
		} else if err != nil {
			return out, nil, err
		}
		out.Roles = append(out.Roles, k)
	}
	if len(a.UserIds) > 0 {
		found, err := s.Store.FindUsers(ctx, a.UserIds)
		if err != nil {
			return out, nil, err
		}
		for i, id := range a.UserIds {
			if _, ok := found[id]; !ok {
				errs = append(errs, fieldErr(fmt.Sprintf("%s/user_ids/%d", ptr, i), "unknown", "There is no user "+id+"."))
				continue
			}
			out.UserIDs = append(out.UserIDs, id)
		}
	}
	tidy := func(l []string) []string { return slices.Compact(slices.Sorted(slices.Values(l))) }
	out.UserIDs, out.Groups, out.Roles = tidy(out.UserIDs), tidy(out.Groups), tidy(out.Roles)
	return out, errs, nil
}

// CatalogRequesters is who an audience lets request an item in a project,
// before it is saved: the project's members who may request there, and
// the users it names.
func (s *Service) CatalogRequesters(ctx context.Context, p *auth.Principal, idOrSlug string, a api.CatalogProjectAudience) (api.CatalogRequesters, error) {
	out := api.CatalogRequesters{People: []api.CatalogRequester{}, LeftOut: []api.UserRef{}, NotRequesters: []api.UserRef{},
		Groups: []api.CatalogRequesterGroup{}}
	if _, err := s.adminItem(ctx, p, idOrSlug); err != nil {
		return out, err
	}
	proj, err := s.Store.GetProject(ctx, a.ProjectId)
	if errors.Is(err, store.ErrNotFound) {
		return out, &ValidationError{Errors: []api.FieldError{fieldErr("/project_id", "unknown", "No such project: "+a.ProjectId+".")}}
	}
	if err != nil {
		return out, err
	}
	out.Project = api.ProjectRef{Id: proj.ID, Code: proj.Code, Name: proj.Name}
	aud, errs, err := s.audienceOf(ctx, "", api.CatalogAudience{UserIds: a.UserIds, Groups: a.Groups, Roles: a.Roles})
	if err != nil {
		return out, err
	}
	if len(errs) > 0 {
		return out, &ValidationError{Errors: errs}
	}
	roles, err := s.projectRoles(ctx)
	if err != nil {
		return out, err
	}
	roleName := map[string]string{}
	for _, r := range roles {
		roleName[r.Key] = r.Name
	}
	members, err := s.Store.ProjectMemberRoles(ctx, proj.ID)
	if err != nil {
		return out, err
	}
	var ids []string
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	ids = slices.Compact(slices.Sorted(slices.Values(append(ids, aud.UserIDs...))))
	type person struct {
		user store.User
		who  *auth.Principal
	}
	var people []person
	for _, id := range ids {
		who, err := auth.ForUser(ctx, s.Store, id)
		if err != nil {
			return out, err
		}
		people = append(people, person{who.User, who})
	}
	slices.SortFunc(people, func(a, b person) int { return strings.Compare(a.user.Name+a.user.ID, b.user.Name+b.user.ID) })
	for _, pe := range people {
		named := slices.Contains(aud.UserIDs, pe.user.ID)
		if !pe.who.In(auth.RequestSubmit, proj.ID) {
			if named {
				out.NotRequesters = append(out.NotRequesters, userRef(pe.user))
			}
			continue
		}
		r := api.CatalogRequester{User: userRef(pe.user), Named: named, Groups: []string{}, Roles: []api.RoleRef{}}
		for _, g := range aud.Groups {
			if slices.Contains(pe.who.Groups, g) {
				r.Groups = append(r.Groups, g)
			}
		}
		for _, k := range aud.Roles {
			if slices.Contains(pe.who.RolesIn(proj.ID), k) {
				r.Roles = append(r.Roles, api.RoleRef{Key: k, Name: roleName[k]})
			}
		}
		if aud.Everyone() || named || len(r.Groups) > 0 || len(r.Roles) > 0 {
			out.People = append(out.People, r)
		} else {
			out.LeftOut = append(out.LeftOut, userRef(pe.user))
		}
	}
	sizes, err := s.Store.GroupSizes(ctx, aud.Groups)
	if err != nil {
		return out, err
	}
	for _, g := range aud.Groups {
		out.Groups = append(out.Groups, api.CatalogRequesterGroup{Name: g, Seen: sizes[g]})
	}
	return out, nil
}

// sharingYAML is who an item is shared with, for its file: projects by
// code, users by email (by ID without one); nil when with nobody.
func (s *Service) sharingYAML(ctx context.Context, itemID string) (*sharingDoc, error) {
	sh, err := s.Store.ItemSharing(ctx, itemID)
	if err != nil || !sh.AllProjects && len(sh.Projects) == 0 {
		return nil, err
	}
	all, err := s.Store.ListProjects(ctx, nil, 500, nil)
	if err != nil {
		return nil, err
	}
	code := map[string]string{}
	for _, p := range all.Items {
		code[p.ID] = p.Code
	}
	ids := slices.Clone(sh.Everywhere.UserIDs)
	for _, ps := range sh.Projects {
		ids = append(ids, ps.UserIDs...)
	}
	found, err := s.Store.FindUsers(ctx, nonNilList(ids))
	if err != nil {
		return nil, err
	}
	audience := func(a store.Audience) audienceDoc {
		out := audienceDoc{Groups: a.Groups, Roles: a.Roles}
		for _, id := range a.UserIDs {
			if u, ok := found[id]; ok && u.Email != "" {
				id = u.Email
			}
			out.Users = append(out.Users, id)
		}
		slices.Sort(out.Users)
		return out
	}
	doc := &sharingDoc{AllProjects: sh.AllProjects}
	if !sh.Everywhere.Everyone() {
		e := audience(sh.Everywhere)
		doc.Everywhere = &e
	}
	for _, ps := range sh.Projects {
		c, ok := code[ps.ProjectID]
		if !ok {
			c = ps.ProjectID
		}
		doc.Projects = append(doc.Projects, projectShareDoc{Project: c, Audience: audience(ps.Audience)})
	}
	slices.SortFunc(doc.Projects, func(a, b projectShareDoc) int { return strings.Compare(a.Project, b.Project) })
	return doc, nil
}

// sharingFromYAML checks a file's sharing, with problems on its pointers.
func (s *Service) sharingFromYAML(ctx context.Context, d sharingDoc) (store.Sharing, []api.FieldError, error) {
	sh := store.Sharing{AllProjects: d.AllProjects}
	var logins []string
	if d.Everywhere != nil {
		logins = append(logins, d.Everywhere.Users...)
	}
	for _, ps := range d.Projects {
		logins = append(logins, ps.Audience.Users...)
	}
	known, err := s.Store.UserIDsByLogin(ctx, nonNilList(logins))
	if err != nil {
		return sh, nil, err
	}
	var errs []api.FieldError
	audience := func(ptr string, a audienceDoc) (store.Audience, error) {
		ids := []string{}
		for i, l := range a.Users {
			if id, ok := known[l]; ok {
				ids = append(ids, id)
			} else {
				errs = append(errs, fieldErr(fmt.Sprintf("%s/users/%d", ptr, i), "unknown", "There is no user "+l+"."))
			}
		}
		aud, aerrs, err := s.audienceOf(ctx, ptr, api.CatalogAudience{UserIds: ids, Groups: nonNilList(a.Groups), Roles: nonNilList(a.Roles)})
		errs = append(errs, aerrs...)
		return aud, err
	}
	if d.Everywhere != nil {
		if !d.AllProjects {
			errs = append(errs, fieldErr("/sharing/everywhere", "not_all_projects",
				"Name who may request in every project only with all_projects."))
		}
		if sh.Everywhere, err = audience("/sharing/everywhere", *d.Everywhere); err != nil {
			return sh, nil, err
		}
	}
	seen := map[string]bool{}
	for i, ps := range d.Projects {
		ptr := fmt.Sprintf("/sharing/projects/%d", i)
		proj, err := s.Store.GetProject(ctx, ps.Project)
		switch {
		case errors.Is(err, store.ErrNotFound):
			errs = append(errs, fieldErr(ptr+"/project", "unknown", fmt.Sprintf("There is no project %q.", ps.Project)))
			continue
		case err != nil:
			return sh, nil, err
		case seen[proj.ID]:
			errs = append(errs, fieldErr(ptr+"/project", "duplicate", fmt.Sprintf("The project %q is shared twice.", ps.Project)))
			continue
		}
		seen[proj.ID] = true
		aud, err := audience(ptr, ps.Audience)
		if err != nil {
			return sh, nil, err
		}
		sh.Projects = append(sh.Projects, store.ProjectShare{ProjectID: proj.ID, Audience: aud})
	}
	return sh, errs, nil
}
