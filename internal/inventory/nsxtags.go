package inventory

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/store"
)

// NSXTagSync flags the VMs whose NSX tags were changed outside rvav
// (ADR-0028): a tag rvav set that the VM no longer has in NSX takes it out
// of a group, and its firewall rules no longer apply to it. Every interval,
// each VM on the NSX Manager its groups are on (ADR-0062).
type NSXTagSync struct {
	Store *store.Store
	// NSX is the NSX integration's Manager, nil without one; NSXManagers
	// those added besides it.
	NSX         *nsx.Client
	NSXManagers interface {
		Get(ctx context.Context, name string) (*nsx.Client, error)
	}
	Events   *events.Publisher
	Log      *slog.Logger
	Interval time.Duration
	// IntervalNow, when set, gives it as saved now (ADR-0030).
	IntervalNow func(context.Context) time.Duration
}

func (s *NSXTagSync) Run(ctx context.Context) error {
	for {
		if s.anyManager(ctx) {
			err := s.Store.WithLock(ctx, store.LockNSXSync, func() error { return s.SyncOnce(ctx) })
			if err != nil && ctx.Err() == nil {
				s.Log.Warn("NSX tag sync failed", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every(ctx, s.Interval, s.IntervalNow)):
		}
	}
}

// anyManager says whether an NSX Manager is set. Without one the sync
// takes no lock: each of the sync process's jobs holds its lock's
// connection while its queries take another, and one job more used up a
// pool of four (pgx's default up to four CPUs), the inventory sync waiting
// for good (I1.1).
func (s *NSXTagSync) anyManager(ctx context.Context) bool {
	if s.NSX != nil {
		return true
	}
	added, err := s.Store.NSXManagers(ctx)
	if err != nil && ctx.Err() == nil {
		s.Log.Warn("NSX tag sync can't list the NSX Managers", "err", err)
	}
	return len(added) > 0
}

// membership is what a VM was given to join its groups, as the worker
// records it (provision.Membership), and the tags it has lost since.
type membership struct {
	InstanceUUID string    `json:"instance_uuid"`
	Tags         []nsx.Tag `json:"tags"`
	Missing      []nsx.Tag `json:"missing,omitempty"`
	NSX          string    `json:"nsx_manager,omitempty"`
}

// vms are the VMs an NSX Manager sees, by instance UUID; nil when rvav
// can't ask it.
func (s *NSXTagSync) vms(ctx context.Context, manager string) (map[string]nsx.VM, error) {
	client := s.NSX
	if manager != "" {
		if s.NSXManagers == nil {
			return nil, nil
		}
		c, err := s.NSXManagers.Get(ctx, manager)
		if err != nil {
			s.Log.Warn("NSX tag sync can't reach an NSX Manager", "nsx_manager", manager, "err", err)
			return nil, nil
		}
		client = c
	}
	if client == nil {
		return nil, nil
	}
	vms, err := client.VMs(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]nsx.VM{}
	for _, v := range vms {
		out[v.ExternalID] = v
	}
	return out, nil
}

// SyncOnce compares each VM's tags in NSX with those rvav set. A VM NSX
// doesn't list is left as it is: vCenter's sync says when one is gone. An
// NSX Manager that doesn't answer keeps its VMs as they were, the others'
// are compared, and the error names it.
func (s *NSXTagSync) SyncOnce(ctx context.Context) error {
	list, err := s.Store.ResourcesOfType(ctx, "nsx.membership")
	if err != nil || len(list) == 0 {
		return err
	}
	seen := map[string]map[string]nsx.VM{}
	var down []error
	for _, r := range list {
		var props map[string]any
		var m membership
		if json.Unmarshal(r.Properties, &props) != nil || json.Unmarshal(r.Properties, &m) != nil {
			continue
		}
		byUUID, asked := seen[m.NSX]
		if !asked {
			if byUUID, err = s.vms(ctx, m.NSX); err != nil {
				down = append(down, fmt.Errorf("NSX Manager %s: %w", cmp.Or(m.NSX, "(the NSX integration's)"), err))
			}
			seen[m.NSX] = byUUID
		}
		vm, ok := byUUID[m.InstanceUUID]
		if !ok {
			continue
		}
		dep, err := s.Store.GetDeployment(ctx, r.DeploymentID)
		if err != nil || dep.Status != "ready" && dep.Status != "failed" {
			continue // being changed by rvav: what it sets isn't drift
		}
		missing := slices.DeleteFunc(slices.Clone(m.Tags), func(t nsx.Tag) bool { return slices.Contains(vm.Tags, t) })
		state := "in_sync"
		if len(missing) > 0 {
			state, props["missing"] = "drifted", missing
		} else {
			delete(props, "missing")
		}
		next, _ := json.Marshal(props)
		if state == r.SyncState && bytes.Equal(compact(next), compact(r.Properties)) {
			continue
		}
		ok, err = s.Store.UpdateResourceStateIf(ctx, r.ID, r.Properties, state, next)
		if err != nil {
			return err
		}
		if !ok {
			continue // changed meanwhile: compared again next time
		}
		_ = s.Store.RefreshDeploymentSyncState(ctx, s.Store.Pool, r.DeploymentID)
		r.Properties, r.SyncState = next, state
		s.Events.Resource(ctx, dep.ProjectID, r)
		s.Events.Deployment(ctx, r.DeploymentID)
	}
	return errors.Join(down...)
}

func compact(raw []byte) []byte {
	var m any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	out, _ := json.Marshal(m)
	return out
}
