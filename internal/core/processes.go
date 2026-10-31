package core

import (
	"context"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
)

// rvav's processes (ADR-0030): each records itself and the settings
// revision it runs with; one that sees newer connection settings drains,
// exits and is started again by its supervisor.

// ProcessesSeen is how recently a process must have recorded itself to be
// running: it does every few seconds.
const ProcessesSeen = 30 * time.Second

// Processes are those running now, and the latest connection settings'
// revision.
func (s *Service) Processes(ctx context.Context, p *auth.Principal) (api.ProcessList, error) {
	out := api.ProcessList{Items: []api.Process{}}
	if !p.IsAdmin() {
		return out, ErrForbidden
	}
	rows, err := s.Store.Processes(ctx, time.Now().Add(-ProcessesSeen))
	if err != nil {
		return out, err
	}
	if out.Revision, err = s.Store.ConnectionRevision(ctx, LiveKinds); err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Items = append(out.Items, api.Process{Id: r.ID, Role: api.ProcessRole(r.Role), Host: r.Host, Version: r.Version,
			Revision: r.Revision, State: api.ProcessState(r.State), StartedAt: r.StartedAt, SeenAt: r.SeenAt})
	}
	return out, nil
}
