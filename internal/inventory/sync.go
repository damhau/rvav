// Package inventory keeps the vCenter inventory cache and the state of VM
// resources up to date. It polls vCenter with the property collector every
// interval; WaitForUpdatesEx-based change tracking can replace the polling
// later without changing what is stored.
package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vmprops"
)

type Syncer struct {
	Store *store.Store
	// VCenters are the vCenters to sync, each in turn (ADR-0024); without
	// it, VSphere alone.
	VCenters VCenterSet
	VSphere  vsphere.Driver
	Events   *events.Publisher
	Log      *slog.Logger
	Interval time.Duration
}

// VCenterSet is the vCenters rvav manages.
type VCenterSet interface {
	Names(ctx context.Context) ([]string, error)
	Get(ctx context.Context, name string) (vsphere.Driver, error)
}

// lockKey is the Postgres advisory lock that keeps one active syncer.
var lockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("rvav-inventory-sync"))
	return int64(h.Sum64() >> 1)
}()

// Run syncs until ctx is done. Only the process holding the advisory lock
// syncs; others wait and take over if it stops.
func (s *Syncer) Run(ctx context.Context) error {
	conn, err := s.Store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	for {
		var got bool
		if err := conn.QueryRow(ctx, `select pg_try_advisory_lock($1)`, lockKey).Scan(&got); err != nil {
			return err
		}
		if got {
			break
		}
		s.Log.Info("another sync process is active, waiting")
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(30 * time.Second):
		}
	}
	defer conn.Exec(context.Background(), `select pg_advisory_unlock($1)`, lockKey) //nolint:errcheck
	s.Log.Info("inventory sync started", "interval", s.Interval.String())
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		start := time.Now()
		if err := s.SyncOnce(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("inventory sync failed", "err", err)
		} else if ctx.Err() == nil {
			s.Log.Debug("inventory synced", "duration_ms", time.Since(start).Milliseconds())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// SyncOnce syncs every vCenter; one that fails doesn't stop the others.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	if s.VCenters == nil {
		return s.syncVCenter(ctx, s.VSphere)
	}
	names, err := s.VCenters.Names(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		d, err := s.VCenters.Get(ctx, name)
		if err == nil {
			err = s.syncVCenter(ctx, d)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// syncVCenter reads a vCenter's inventory, replaces its cache and refreshes
// its VM resources.
func (s *Syncer) syncVCenter(ctx context.Context, vc vsphere.Driver) error {
	inv, err := vc.Inventory(ctx)
	if err != nil {
		return err
	}
	// Tags that couldn't be read keep their last known values: a hiccup of
	// the tagging service mustn't empty placement by tag.
	clusterTags, datastoreTags := map[string][]string{}, map[string][]string{}
	if inv.TagsError != nil {
		s.Log.Warn("inventory: vSphere tags not read; keeping the last ones", "err", inv.TagsError)
		if cs, err := s.Store.InventoryClusters(ctx); err == nil {
			for _, c := range cs {
				clusterTags[c.Moref] = c.Tags
			}
		}
		if ds, err := s.Store.InventoryDatastores(ctx); err == nil {
			for _, d := range ds {
				datastoreTags[d.Moref] = d.Tags
			}
		}
	}
	snap := store.Inventory{VCenter: vc.Name()}
	for _, c := range inv.Clusters {
		tags := c.Tags
		if inv.TagsError != nil {
			tags = clusterTags[c.Moref]
		}
		snap.Clusters = append(snap.Clusters, store.InvCluster{Moref: c.Moref, Name: c.Name, Datacenter: c.Datacenter,
			HostCount: c.HostCount, CPUTotalMHz: c.CPUTotalMHz, CPUUsedMHz: c.CPUUsedMHz,
			MemoryTotalMB: c.MemoryTotalMB, MemoryUsedMB: c.MemoryUsedMB, Tags: tags})
	}
	for _, d := range inv.Datastores {
		tags := d.Tags
		if inv.TagsError != nil {
			tags = datastoreTags[d.Moref]
		}
		snap.Datastores = append(snap.Datastores, store.InvDatastore{Moref: d.Moref, Name: d.Name,
			CapacityGB: d.CapacityGB, FreeGB: d.FreeGB, Clusters: d.Clusters, Tags: tags})
	}
	for _, n := range inv.Networks {
		snap.Networks = append(snap.Networks, store.InvNetwork{Moref: n.Moref, Name: n.Name, Kind: n.Kind, Clusters: n.Clusters})
	}
	for _, t := range inv.Templates {
		snap.Templates = append(snap.Templates, store.InvTemplate{Moref: t.Moref, Name: t.Name, GuestOS: t.GuestOS, Library: t.Library})
	}
	for _, p := range inv.StoragePolicies {
		snap.StoragePolicies = append(snap.StoragePolicies, store.InvStoragePolicy{ID: p.ID, Name: p.Name})
	}
	// Content library items and storage policies that couldn't be read keep
	// their last known values too (ADR-0024).
	if inv.LibrariesError != nil {
		s.Log.Warn("inventory: content libraries not read; keeping the last items", "err", inv.LibrariesError)
		if ts, err := s.Store.InventoryTemplatesOf(ctx, snap.VCenter); err == nil {
			for _, t := range ts {
				if t.Library != "" {
					snap.Templates = append(snap.Templates, t)
				}
			}
		}
	}
	if inv.PoliciesError != nil {
		s.Log.Warn("inventory: storage policies not read; keeping the last ones", "err", inv.PoliciesError)
		snap.StoragePolicies, _ = s.Store.InventoryStoragePoliciesOf(ctx, snap.VCenter)
	}
	byUUID := map[string]vsphere.InventoryVM{}
	for _, v := range inv.VMs {
		snap.VMs = append(snap.VMs, store.InvVM{Moref: v.Moref, InstanceUUID: v.InstanceUUID, Name: v.Name,
			PowerState: v.PowerState, GuestState: v.GuestState, IPAddress: v.IP, Hostname: v.Hostname, GuestOS: v.GuestOS,
			CPUCount: v.CPUCount, MemoryMB: v.MemoryMB, Cluster: v.Cluster, Host: v.Host, CPUPct: v.CPUPct,
			MemoryPct: v.MemoryPct, SnapshotCount: v.SnapshotCount})
		byUUID[v.InstanceUUID] = v
	}
	if err := s.Store.ReplaceInventory(ctx, snap); err != nil {
		return err
	}
	return s.refreshResources(ctx, vc.Name(), byUUID)
}

type storedVM struct {
	VM struct {
		Fqdn     string `json:"fqdn"`
		Template string `json:"template"`
		Folder   string `json:"folder"`
	} `json:"vm"`
}

func (s *Syncer) refreshResources(ctx context.Context, vcenter string, byUUID map[string]vsphere.InventoryVM) error {
	resources, err := s.Store.VMResources(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, r := range resources {
		// Another vCenter's VMs aren't missing from this one.
		if r.ExternalID == nil || vmprops.VCenterOf(r.Properties) != vcenter {
			continue
		}
		dep, err := s.Store.GetDeployment(ctx, r.DeploymentID)
		if err != nil {
			continue
		}
		vm, ok := byUUID[*r.ExternalID]
		if !ok {
			if dep.Status == "provisioning" || r.SyncState == "missing" {
				continue // not cloned yet, or already reported
			}
			if err := s.Store.UpdateResourceState(ctx, s.Store.Pool, r.ID, "missing", r.Properties); err != nil {
				return err
			}
			_ = s.Store.RefreshDeploymentSyncState(ctx, s.Store.Pool, r.DeploymentID)
			r.SyncState = "missing"
			s.Events.Resource(ctx, dep.ProjectID, r)
			s.Events.Deployment(ctx, r.DeploymentID)
			continue
		}
		var old storedVM
		_ = json.Unmarshal(r.Properties, &old)
		if vm.Folder == "" {
			vm.Folder = old.VM.Folder
		}
		props := vmprops.Build(vcenter, vm.VM, vmprops.OursOf(r.Properties),
			&vmprops.Usage{CPUPct: vm.CPUPct, MemoryPct: vm.MemoryPct, SampledAt: now}, vm.SnapshotCount)
		changed := !sameIgnoringUsage(r.Properties, props) || r.SyncState != "in_sync"
		if err := s.Store.UpdateResourceState(ctx, s.Store.Pool, r.ID, "in_sync", props); err != nil {
			return err
		}
		if changed {
			_ = s.Store.RefreshDeploymentSyncState(ctx, s.Store.Pool, r.DeploymentID)
			r.Properties, r.SyncState = props, "in_sync"
			s.Events.Resource(ctx, dep.ProjectID, r)
			s.Events.Deployment(ctx, r.DeploymentID)
		}
	}
	return nil
}

// sameIgnoringUsage compares two VM property documents without quick stats,
// which change on every poll and would flood the event stream.
func sameIgnoringUsage(a, b json.RawMessage) bool {
	strip := func(raw json.RawMessage) []byte {
		var m map[string]map[string]any
		if json.Unmarshal(raw, &m) != nil {
			return raw
		}
		delete(m["vm"], "usage")
		out, _ := json.Marshal(m)
		return out
	}
	return bytes.Equal(strip(a), strip(b))
}
