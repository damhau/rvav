package core

import (
	"context"
	"slices"
	"sort"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/buildinfo"
	"github.com/damhau/rvav/internal/config"
)

// System is the System page (U1.5, ADR-0030), read only: the version, the
// schema, each process and the settings revision it runs, the settings
// saved in Administration and how far each has applied, and the install
// settings as this process reads them, secrets left out.
func (s *Service) System(ctx context.Context, p *auth.Principal) (api.System, error) {
	if !p.IsAdmin() {
		return api.System{}, ErrForbidden
	}
	procs, err := s.Processes(ctx, p)
	if err != nil {
		return api.System{}, err
	}
	out := api.System{Version: buildinfo.Version(), Revision: procs.Revision, Processes: procs.Items,
		Settings: []api.SettingsRevision{}, Install: []api.InstallSetting{}}
	if out.Schema, err = s.Store.SchemaVersion(ctx); err != nil {
		return api.System{}, err
	}
	stored, err := s.Store.StoredIntegrations(ctx)
	if err != nil {
		return api.System{}, err
	}
	for _, kind := range integrationKinds {
		row, ok := stored[kind]
		if !ok {
			continue
		}
		sr := api.SettingsRevision{Kind: api.IntegrationKind(kind), Revision: row.Revision, UpdatedAt: row.UpdatedAt,
			Applies: api.Applied}
		switch {
		case slices.Contains(LiveKinds, kind):
			sr.Applies = api.NextUse
		case slices.ContainsFunc(procs.Items, func(p api.Process) bool { return p.Revision < row.Revision }):
			sr.Applies = api.Applying
		}
		if row.UpdatedBy != nil {
			sr.UpdatedBy = &api.UserRef{Id: *row.UpdatedBy, Name: deref(row.UpdatedByName)}
		}
		out.Settings = append(out.Settings, sr)
	}
	sort.SliceStable(out.Settings, func(i, j int) bool { return out.Settings[i].Revision > out.Settings[j].Revision })
	for _, is := range config.Install {
		v, set := is.Shown(s.Config)
		out.Install = append(out.Install, api.InstallSetting{Name: is.Env, Value: v, Secret: is.Secret, Set: set})
	}
	return out, nil
}
