package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/store"
)

// NSXTagSync flags the VMs whose NSX tags were changed outside rvav
// (ADR-0028): a tag rvav set that the VM no longer has in NSX takes it out
// of a group, and its firewall rules no longer apply to it. Every interval.
type NSXTagSync struct {
	Store    *store.Store
	NSX      *nsx.Client
	Events   *events.Publisher
	Log      *slog.Logger
	Interval time.Duration
}

func (s *NSXTagSync) Run(ctx context.Context) error {
	for {
		err := s.Store.WithLock(ctx, store.LockNSXSync, func() error { return s.SyncOnce(ctx) })
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("NSX tag sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Interval):
		}
	}
}

// membership is what a VM was given to join its groups, as the worker
// records it (provision.Membership), and the tags it has lost since.
type membership struct {
	InstanceUUID string    `json:"instance_uuid"`
	Tags         []nsx.Tag `json:"tags"`
	Missing      []nsx.Tag `json:"missing,omitempty"`
}

// SyncOnce compares each VM's tags in NSX with those rvav set. A VM NSX
// doesn't list is left as it is: vCenter's sync says when one is gone.
func (s *NSXTagSync) SyncOnce(ctx context.Context) error {
	list, err := s.Store.ResourcesOfType(ctx, "nsx.membership")
	if err != nil || len(list) == 0 {
		return err
	}
	vms, err := s.NSX.VMs(ctx)
	if err != nil {
		return err
	}
	byUUID := map[string]nsx.VM{}
	for _, v := range vms {
		byUUID[v.ExternalID] = v
	}
	for _, r := range list {
		var props map[string]any
		var m membership
		if json.Unmarshal(r.Properties, &props) != nil || json.Unmarshal(r.Properties, &m) != nil {
			continue
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
	return nil
}

func compact(raw []byte) []byte {
	var m any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	out, _ := json.Marshal(m)
	return out
}
