package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/ad"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// Directory is a client of the directory the settings name, nil when
// they name none.
func Directory(c config.ADConfig) *ad.Client {
	if !c.Configured() {
		return nil
	}
	return ad.New(DirectoryConfig(c, nil))
}

// directory is a client of the directory as saved now, and its base DN;
// false when there is none.
func (s *Service) directory(ctx context.Context) (*ad.Client, string, bool) {
	c := s.liveConfig(ctx).AD
	return Directory(c), c.BaseDN, c.Configured()
}

// evaluateDirectory finds the OU row a request's VMs follow, once rvav has
// a directory: without one, no row applies (ADR-0064).
func (s *Service) evaluateDirectory(ctx context.Context, ev *Evaluation) error {
	ev.adRow, ev.adDir, ev.adBase = nil, nil, ""
	dir, base, ok := s.directory(ctx)
	if !ok {
		return nil
	}
	row, err := s.Store.ADPlacementFor(ctx, ev.Project.ID, ev.Env.Key)
	if err != nil || row == nil {
		return err
	}
	ev.adRow, ev.adDir, ev.adBase = row, dir, base
	return nil
}

// computerOf is the computer object a component's VMs get, nil for none:
// in the row's OU, or the one the blueprint chooses where the row lets it.
func computerOf(ev *Evaluation, g blueprint.Guest, add func(field, code, msg string)) *provision.ADUnit {
	row := ev.adRow
	if row == nil {
		return nil
	}
	ou := row.OU
	if b := g.ActiveDirectory; b != nil && row.AllowBlueprintOU {
		if b.Skip {
			return nil
		}
		ou = cmp.Or(b.OU, ou)
	}
	u := &provision.ADUnit{OU: ad.Resolve(ou, ev.adBase)}
	for _, dn := range append([]string{u.OU}, row.Groups...) {
		if msg := ad.CheckDN(ad.Resolve(dn, ev.adBase), ev.adBase); msg != "" {
			add("", "invalid", "Its VMs' computer objects can't be made: "+msg+" Ask an administrator to check the OUs in Active Directory.")
			return nil
		}
	}
	for _, g := range row.Groups {
		u.Groups = append(u.Groups, ad.Resolve(g, ev.adBase))
	}
	if j := g.DomainJoin; j != nil && !strings.EqualFold(strings.TrimSuffix(j.Domain, "."), ad.Domain(ev.adBase)) {
		add("", "invalid", fmt.Sprintf("Its VMs join %s, and rvav makes their computer objects in %s. Ask a catalog admin to check this item.",
			j.Domain, ad.Domain(ev.adBase)))
	}
	return u
}

// dir is the directory its VMs' names are checked in: the one they get
// computer objects in.
func (p vmPart) dir() *ad.Client {
	if p.ad == nil {
		return nil
	}
	return p.adDir
}

// ADPlacements are the rows that may apply to a project, or every row.
func (s *Service) ADPlacements(ctx context.Context, p *auth.Principal, projectID string) ([]api.AdPlacement, error) {
	if projectID != "" {
		proj, err := s.visibleProject(ctx, p, projectID)
		if err != nil {
			return nil, err
		}
		projectID = proj.ID
	}
	if !p.In(auth.ProjectSettingsRead, projectID) {
		return nil, ErrForbidden
	}
	rows, err := s.Store.ADPlacements(ctx, projectID)
	if err != nil {
		return nil, err
	}
	base := s.liveConfig(ctx).AD.BaseDN
	out := make([]api.AdPlacement, len(rows))
	for i, r := range rows {
		out[i] = presentADPlacement(r, base)
	}
	return out, nil
}

func presentADPlacement(r store.ADPlacement, base string) api.AdPlacement {
	out := api.AdPlacement{Id: r.ID, ProjectId: r.ProjectID, ProjectName: r.ProjectName, Ou: r.OU, OuDn: ad.Resolve(r.OU, base),
		Groups: r.Groups, AllowBlueprintOu: r.AllowBlueprintOU, UpdatedAt: r.UpdatedAt}
	if out.Groups == nil {
		out.Groups = []string{}
	}
	if r.Environment != nil {
		out.Environment = new(api.EnvironmentKey(*r.Environment))
	}
	return out
}

// mayPlaceIn says whether p manages a project's rows, or those of every
// project (nil).
func mayPlaceIn(p *auth.Principal, projectID *string) bool {
	return p.In(auth.PlacementManage, deref(projectID))
}

func (s *Service) SaveADPlacement(ctx context.Context, p *auth.Principal, id string, w api.AdPlacementWrite) (api.AdPlacement, error) {
	projectID := w.ProjectId
	if projectID != nil && *projectID == "" {
		projectID = nil
	}
	if !mayPlaceIn(p, projectID) {
		return api.AdPlacement{}, ErrForbidden
	}
	action := "ad.placement.update"
	if id == "" {
		id, action = ids.New("adp"), "ad.placement.create"
	} else if was, err := s.Store.GetADPlacement(ctx, id); errors.Is(err, store.ErrNotFound) {
		return api.AdPlacement{}, ErrNotFound
	} else if err != nil {
		return api.AdPlacement{}, err
	} else if !mayPlaceIn(p, was.ProjectID) {
		return api.AdPlacement{}, ErrForbidden
	}
	var environment *string
	if w.Environment != nil && *w.Environment != "" {
		environment = new(string(*w.Environment))
	}
	var errs []api.FieldError
	where := "every project"
	if projectID != nil {
		proj, err := s.Store.GetProject(ctx, *projectID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			errs = append(errs, fieldErr("/project_id", "unknown", "There is no such project."))
		case err != nil:
			return api.AdPlacement{}, err
		case environment != nil && !slices.Contains(proj.Environments, *environment):
			errs = append(errs, fieldErr("/environment", "not_allowed", fmt.Sprintf("%s doesn't deploy to %s.", proj.Name, *environment)))
		default:
			projectID, where = &proj.ID, proj.Name
		}
	}
	if environment != nil {
		if _, err := s.Store.GetEnvironment(ctx, *environment); errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fieldErr("/environment", "unknown", "There is no environment "+*environment+"."))
		} else if err != nil {
			return api.AdPlacement{}, err
		}
		where += " in " + *environment
	} else if projectID == nil {
		where += " and environment"
	}
	dir, base, configured := s.directory(ctx)
	ou := strings.TrimSpace(w.Ou)
	groups := slices.Clone(derefSlice(w.Groups))
	dns := map[string]string{}
	if ou == "" {
		errs = append(errs, fieldErr("/ou", "required", "Name the OU, for example OU=Servers,OU=Web."))
	} else if msg := ad.CheckDN(ad.Resolve(ou, base), base); msg != "" {
		errs = append(errs, fieldErr("/ou", "pattern", msg))
	} else {
		dns["/ou"] = ad.Resolve(ou, base)
	}
	for i, g := range groups {
		groups[i] = strings.TrimSpace(g)
		ptr := fmt.Sprintf("/groups/%d", i)
		if msg := ad.CheckDN(ad.Resolve(groups[i], base), base); msg != "" {
			errs = append(errs, fieldErr(ptr, "pattern", msg))
		} else {
			dns[ptr] = ad.Resolve(groups[i], base)
		}
	}
	if configured && len(errs) == 0 {
		errs = missingEntries(ctx, dir, dns)
	}
	if len(errs) > 0 {
		return api.AdPlacement{}, &ValidationError{Errors: errs}
	}
	r := store.ADPlacement{ID: id, ProjectID: projectID, Environment: environment, OU: ou, Groups: groups,
		AllowBlueprintOU: w.AllowBlueprintOu != nil && *w.AllowBlueprintOu}
	if err := s.Store.SaveADPlacement(ctx, r); store.IsUniqueViolation(err) {
		return api.AdPlacement{}, &ConflictError{Msg: "There is already an OU for " + where + "."}
	} else if err != nil {
		return api.AdPlacement{}, err
	}
	s.audit(ctx, p, action, "ad_placement", id, projectID, w)
	row, err := s.Store.GetADPlacement(ctx, id)
	return presentADPlacement(row, base), err
}

// missingEntries says which of the OU and groups the directory doesn't
// have, by their field.
func missingEntries(ctx context.Context, dir *ad.Client, dns map[string]string) []api.FieldError {
	var errs []api.FieldError
	for _, ptr := range slices.Sorted(maps.Keys(dns)) {
		ok, err := dir.Exists(ctx, dns[ptr])
		switch {
		case err != nil:
			return []api.FieldError{fieldErr(ptr, "directory", "Active Directory can't be reached: "+err.Error())}
		case !ok:
			errs = append(errs, fieldErr(ptr, "unknown", "Active Directory has no "+dns[ptr]+"."))
		}
	}
	return errs
}

func (s *Service) DeleteADPlacement(ctx context.Context, p *auth.Principal, id string) error {
	if !p.Anywhere(auth.PlacementManage) {
		return ErrForbidden
	}
	r, err := s.Store.GetADPlacement(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !mayPlaceIn(p, r.ProjectID) {
		return ErrForbidden
	}
	if err := s.Store.DeleteADPlacement(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, p, "ad.placement.delete", "ad_placement", id, r.ProjectID, nil)
	return nil
}
