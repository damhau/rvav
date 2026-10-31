// Package inventory keeps the vCenter inventory cache and the state of VM
// resources up to date. It follows each vCenter's changes with the
// property collector (WaitForUpdatesEx): a VM that changes is read again at
// once, and when anything else rvav caches changes its vCenter is read
// again in full, a moment later. A full read every interval brings in what
// changes all the time and isn't followed: quick stats, free space, tags,
// content library items and storage policies.
package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vmprops"

	"github.com/damhau/rvav/internal/observe"
)

type Syncer struct {
	Store *store.Store
	// VCenters are the vCenters to sync, each in turn (ADR-0024); without
	// it, VSphere alone.
	VCenters VCenterSet
	VSphere  vsphere.Driver
	Events   *events.Publisher
	Log      *slog.Logger
	// Interval is the time between full reads; IntervalNow, when set, gives
	// it as saved now (ADR-0030).
	Interval    time.Duration
	IntervalNow func(context.Context) time.Duration
	// Settle is how long a change other than a VM's waits before its
	// vCenter is read in full, as such changes come in bursts (5s when 0).
	Settle time.Duration

	mu sync.Mutex
	// locks keep one change at a time per vCenter, so that an older read
	// doesn't overwrite a newer one.
	locks    map[string]*sync.Mutex
	watchers map[string]watcher
	// names are the vCenters rvav manages, as last listed.
	names []string
	// running counts the watchers and their settle loops, which Run waits
	// for: they release what they hold in vCenter as they stop.
	running sync.WaitGroup
}

// target is a vCenter to sync: its connection and the VM resources it
// owns, by the vCenter their properties name.
type target struct {
	name string
	d    vsphere.Driver
	owns func(vcenter string) bool
}

type watcher struct {
	d    vsphere.Driver
	stop context.CancelFunc
}

// VCenterSet is the vCenters rvav manages.
type VCenterSet interface {
	Names(ctx context.Context) ([]string, error)
	Get(ctx context.Context, name string) (vsphere.Driver, error)
}

// standbyPoll is how often a standby syncer checks whether it can take over.
var standbyPoll = 5 * time.Second

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
		// A standby: the lock is free as soon as the active process's
		// connection is gone, so it takes over within seconds (S14.1).
		s.Log.Info("another sync process is active, waiting")
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(standbyPoll):
		}
	}
	defer conn.Exec(context.Background(), `select pg_advisory_unlock($1)`, lockKey) //nolint:errcheck
	// The lock lives with its connection: should the connection be lost (a
	// database failover, a dropped idle connection), so is the lock, and a
	// standby takes over. This process then stops syncing, and exits to be
	// started again as a standby (S14 review).
	lost := make(chan error, 1)
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(standbyPoll):
			}
			if _, err := conn.Exec(ctx, `select 1`); err != nil && ctx.Err() == nil {
				lost <- fmt.Errorf("inventory sync: lost the connection that holds the sync lock: %w", err)
				stop()
				return
			}
		}
	}()
	defer s.running.Wait() // the watchers stop with ctx
	s.Log.Info("inventory sync started", "interval", every(ctx, s.Interval, s.IntervalNow).String())
	for {
		start := time.Now()
		targets, names, err := s.syncAll(ctx)
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("inventory sync failed", "err", err)
		} else if ctx.Err() == nil {
			s.Log.Debug("inventory synced", "duration_ms", time.Since(start).Milliseconds())
		}
		if names != nil { // else they couldn't be listed: the watchers carry on
			s.follow(ctx, targets, names)
		}
		select {
		case <-ctx.Done():
			select {
			case err := <-lost:
				return err
			default:
				return nil
			}
		case <-time.After(max(every(ctx, s.Interval, s.IntervalNow)-time.Since(start), 0)):
		}
	}
}

// every is an interval as saved now, when it is read on next use.
func every(ctx context.Context, fixed time.Duration, now func(context.Context) time.Duration) time.Duration {
	if now != nil {
		return now(ctx)
	}
	return fixed
}

// SyncOnce reads every vCenter in full; one that fails doesn't stop the
// others.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	_, _, err := s.syncAll(ctx)
	return err
}

// syncAll reads every vCenter in full, and says which it could connect to
// among those rvav manages.
func (s *Syncer) syncAll(ctx context.Context) ([]target, []string, error) {
	if s.VCenters == nil {
		t := target{name: s.VSphere.Name(), d: s.VSphere, owns: func(string) bool { return true }}
		return []target{t}, []string{t.name}, s.syncTarget(ctx, t)
	}
	names, err := s.VCenters.Names(ctx)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	s.names = names
	s.mu.Unlock()
	var targets []target
	var errs []error
	for _, name := range names {
		// The first's VMs are those of a name rvav doesn't know too: the
		// integration's may have been renamed since they were built.
		owns := s.ownsFor(name)
		d, err := s.VCenters.Get(ctx, name)
		if err == nil {
			t := target{name: name, d: d, owns: owns}
			targets = append(targets, t)
			err = s.syncTarget(ctx, t)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	// The inventory of a vCenter rvav no longer manages (or renamed) goes.
	if len(errs) == 0 {
		if err := s.Store.PruneInventory(ctx, names); err != nil {
			errs = append(errs, err)
		}
	}
	return targets, names, errors.Join(errs...)
}

// ownsFor says which VM resources a vCenter's reads cover: its own, and,
// for the first, those of a vCenter name rvav doesn't know (the
// integration's may have been renamed since they were built). It reads the
// vCenters rvav manages now: a watcher outlives the read that started it.
func (s *Syncer) ownsFor(name string) func(vcenter string) bool {
	return func(vc string) bool {
		if vc == name {
			return true
		}
		s.mu.Lock()
		names := s.names
		s.mu.Unlock()
		return len(names) > 0 && names[0] == name && !slices.Contains(names, vc)
	}
}

// lock takes a vCenter's lock; the function it returns releases it.
func (s *Syncer) lock(name string) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	m := s.locks[name]
	if m == nil {
		m = new(sync.Mutex)
		s.locks[name] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func (s *Syncer) syncTarget(ctx context.Context, t target) error {
	defer s.lock(t.name)()
	err := s.syncVCenter(ctx, t.d, t.owns)
	if err == nil {
		observe.FullRead(t.name, time.Now())
	}
	return err
}

// follow keeps one watcher on each vCenter connected to, a new one when
// its connection changed, and stops those of vCenters no longer managed.
func (s *Syncer) follow(ctx context.Context, targets []target, names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchers == nil {
		s.watchers = map[string]watcher{}
	}
	for _, t := range targets {
		if w, ok := s.watchers[t.name]; ok {
			if w.d == t.d {
				continue
			}
			w.stop()
		}
		wctx, stop := context.WithCancel(ctx)
		s.watchers[t.name] = watcher{d: t.d, stop: stop}
		s.running.Add(1)
		go func() {
			defer s.running.Done()
			s.watch(wctx, t)
		}()
	}
	for name, w := range s.watchers {
		if !slices.Contains(names, name) {
			w.stop()
			delete(s.watchers, name)
		}
	}
}

// watch follows a vCenter until ctx is done, and again each time its
// session breaks, after a pause that grows while it keeps breaking.
func (s *Syncer) watch(ctx context.Context, t target) {
	kick := make(chan struct{}, 1)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		s.settle(ctx, t, kick)
	}()
	pause := time.Second
	for {
		start := time.Now()
		observe.Watching(t.name, true)
		err := t.d.Watch(ctx, func(cs []vsphere.Change) { s.apply(ctx, t, cs, kick) })
		observe.Watching(t.name, false)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			pause = time.Second
		}
		s.Log.Warn("inventory: vCenter changes not followed; following again", "vcenter", t.name, "err", err,
			"in", pause.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		pause = min(2*pause, time.Minute)
	}
}

// settle reads a vCenter in full a moment after something other than a
// VM changed, once for a burst of changes.
func (s *Syncer) settle(ctx context.Context, t target, kick chan struct{}) {
	wait := s.Settle
	if wait == 0 {
		wait = 5 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		select {
		case <-kick:
		default:
		}
		if err := s.syncTarget(ctx, t); err != nil && ctx.Err() == nil {
			s.Log.Warn("inventory sync failed", "vcenter", t.name, "err", err)
		}
	}
}

// apply brings in what a vCenter says changed: each VM read again at once,
// the rest of its inventory a moment later.
func (s *Syncer) apply(ctx context.Context, t target, changes []vsphere.Change, kick chan struct{}) {
	var morefs []string
	gone := map[string]bool{}
	for _, c := range changes {
		if c.Inventory {
			select {
			case kick <- struct{}{}:
			default:
			}
		}
		if c.VM == "" {
			continue
		}
		if _, ok := gone[c.VM]; !ok {
			morefs = append(morefs, c.VM)
		}
		gone[c.VM] = gone[c.VM] || c.Gone
	}
	for _, m := range morefs {
		if err := s.syncVM(ctx, t, m, gone[m]); err != nil && ctx.Err() == nil {
			s.Log.Warn("inventory: VM not read", "vcenter", t.name, "moref", m, "err", err)
		}
	}
}

// syncVM reads one VM again, or forgets it when vCenter no longer has it
// (or it became a template), and refreshes its resource.
func (s *Syncer) syncVM(ctx context.Context, t target, moref string, gone bool) error {
	defer s.lock(t.name)()
	vcenter := t.d.Name()
	var vm *vsphere.InventoryVM
	if !gone {
		var err error
		vm, err = t.d.InventoryVM(ctx, moref)
		switch {
		case errors.Is(err, vsphere.ErrNotFound):
			gone = true
		case err != nil:
			return err
		}
	}
	if gone || vm.Template {
		uuid, err := s.Store.DeleteInventoryVM(ctx, vcenter, moref)
		if err != nil || uuid == "" {
			return err
		}
		return s.refreshVM(ctx, t, uuid, nil)
	}
	v := vm.VM
	if err := s.Store.UpsertInventoryVM(ctx, vcenter, store.InvVM{Moref: v.Moref, InstanceUUID: v.InstanceUUID, Name: v.Name,
		PowerState: v.PowerState, GuestState: v.GuestState, IPAddress: v.IP, Hostname: v.Hostname, GuestOS: v.GuestOS,
		CPUCount: v.CPUCount, MemoryMB: v.MemoryMB, Cluster: v.Cluster, Host: v.Host, SnapshotCount: vm.SnapshotCount}); err != nil {
		return err
	}
	return s.refreshVM(ctx, t, v.InstanceUUID, vm)
}

// refreshVM refreshes the resources of one VM (vm nil when it's gone); its
// quick stats stay those of the last full read.
func (s *Syncer) refreshVM(ctx context.Context, t target, instanceUUID string, vm *vsphere.InventoryVM) error {
	resources, err := s.Store.VMResourcesOf(ctx, instanceUUID)
	if err != nil {
		return err
	}
	for _, r := range resources {
		if !t.owns(vmprops.VCenterOf(r.Properties)) {
			continue
		}
		if err := s.refreshResource(ctx, t.d.Name(), r, vm, vmprops.UsageOf(r.Properties)); err != nil {
			return err
		}
	}
	return nil
}

// syncVCenter reads a vCenter's inventory, replaces its cache and refreshes
// its VM resources.
func (s *Syncer) syncVCenter(ctx context.Context, vc vsphere.Driver, owns func(vcenter string) bool) error {
	inv, err := vc.Inventory(ctx)
	if err != nil {
		return err
	}
	// Tags that couldn't be read keep their last known values: a hiccup of
	// the tagging service mustn't empty placement by tag.
	clusterTags, datastoreTags := map[string][]string{}, map[string][]string{}
	if inv.TagsError != nil {
		s.Log.Warn("inventory: vSphere tags not read; keeping the last ones", "err", inv.TagsError)
		// This vCenter's: morefs repeat between vCenters.
		if cs, err := s.Store.InventoryClusters(ctx); err == nil {
			for _, c := range cs {
				if c.VCenter == vc.Name() {
					clusterTags[c.Moref] = c.Tags
				}
			}
		}
		if ds, err := s.Store.InventoryDatastores(ctx); err == nil {
			for _, d := range ds {
				if d.VCenter == vc.Name() {
					datastoreTags[d.Moref] = d.Tags
				}
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
	return s.refreshResources(ctx, vc.Name(), owns, byUUID)
}

type storedVM struct {
	VM struct {
		Fqdn     string `json:"fqdn"`
		Template string `json:"template"`
		Folder   string `json:"folder"`
	} `json:"vm"`
}

func (s *Syncer) refreshResources(ctx context.Context, vcenter string, owns func(string) bool, byUUID map[string]vsphere.InventoryVM) error {
	resources, err := s.Store.VMResources(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, r := range resources {
		// Another vCenter's VMs aren't missing from this one.
		if r.ExternalID == nil || !owns(vmprops.VCenterOf(r.Properties)) {
			continue
		}
		var vm *vsphere.InventoryVM
		var usage *vmprops.Usage
		if v, ok := byUUID[*r.ExternalID]; ok {
			vm, usage = &v, &vmprops.Usage{CPUPct: v.CPUPct, MemoryPct: v.MemoryPct, SampledAt: now}
		}
		if err := s.refreshResource(ctx, vcenter, r, vm, usage); err != nil {
			return err
		}
	}
	return nil
}

// refreshResource stores what a VM resource's VM is now, or that it is
// missing (vm nil), and tells those watching when that changed. The worker
// writes the same row as rvav changes the VM: what is stored is only
// replaced if it is still what was read, else read again.
func (s *Syncer) refreshResource(ctx context.Context, vcenter string, r store.Resource, vm *vsphere.InventoryVM, usage *vmprops.Usage) error {
	dep, err := s.Store.GetDeployment(ctx, r.DeploymentID)
	if err != nil {
		return nil
	}
	for attempt := 0; ; attempt++ {
		state, props, skip := s.refreshed(vcenter, dep.Status, r, vm, usage)
		if skip {
			return nil
		}
		changed := !sameIgnoringUsage(r.Properties, props) || r.SyncState != state
		ok, err := s.Store.UpdateResourceStateIf(ctx, r.ID, r.Properties, state, props)
		if err != nil {
			return err
		}
		if ok {
			if changed {
				_ = s.Store.RefreshDeploymentSyncState(ctx, s.Store.Pool, r.DeploymentID)
				r.Properties, r.SyncState = props, state
				s.Events.Resource(ctx, dep.ProjectID, r)
				s.Events.Deployment(ctx, r.DeploymentID)
			}
			return nil
		}
		if attempt == 2 {
			return nil // changing fast: the next read compares again
		}
		if r, err = s.Store.GetResource(ctx, r.ID); err != nil {
			return nil // gone meanwhile
		}
	}
}

// refreshed is a VM resource's new state and properties from vCenter's VM
// (nil: missing); skip when there is nothing to store.
func (s *Syncer) refreshed(vcenter, depStatus string, r store.Resource, vm *vsphere.InventoryVM, usage *vmprops.Usage) (string, json.RawMessage, bool) {
	if vm == nil {
		// Not cloned yet, being deleted, or already reported.
		if depStatus == "provisioning" || depStatus == "deleting" || r.SyncState == "missing" {
			return "", nil, true
		}
		return "missing", r.Properties, false
	}
	var old storedVM
	_ = json.Unmarshal(r.Properties, &old)
	v := vm.VM
	if v.Folder == "" {
		v.Folder = old.VM.Folder
	}
	props := vmprops.Build(vcenter, v, vmprops.OursOf(r.Properties), usage, vm.SnapshotCount)
	state := "in_sync"
	switch depStatus {
	case "provisioning", "updating", "deleting":
		// While rvav changes the VM, what changes isn't drift: the action
		// settles what it changed when it ends.
		props = vmprops.Carry(r.Properties, props)
		if r.SyncState == "drifted" {
			state = "drifted"
		}
	default:
		var drifted bool
		if props, drifted = vmprops.Compare(r.Properties, props); drifted {
			state = "drifted"
		}
	}
	return state, props, false
}

// sameIgnoringUsage compares two VM property documents without quick stats,
// which change on every poll and would flood the event stream.
func sameIgnoringUsage(a, b json.RawMessage) bool {
	strip := func(raw json.RawMessage) []byte {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			return raw
		}
		if vm, ok := m["vm"].(map[string]any); ok {
			delete(vm, "usage")
		}
		out, _ := json.Marshal(m)
		return out
	}
	return bytes.Equal(strip(a), strip(b))
}
