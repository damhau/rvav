// Package snowsync keeps ServiceNow up to date with rvav (ADR-0065): the
// CI of each VM in the CMDB, through the IRE, retired when the VM goes; a
// periodic reconcile that catches what an event couldn't and reports the
// CIs rvav no longer has; and the progress of requests made from
// ServiceNow, in their requested items' work notes and state.
package snowsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/store"
)

// Syncer makes a pass every Every: in one sync process at a time, under
// an advisory lock.
type Syncer struct {
	Store *store.Store
	// Config is the integration's settings now: they are read on next use.
	Config func(ctx context.Context) config.ServiceNowConfig
	// PublicURL is rvav's, for the links in work notes.
	PublicURL string
	Log       *slog.Logger
	Every     time.Duration
	Now       func() time.Time
}

const (
	batch       = 200
	firstRetry  = 10 * time.Second
	latestRetry = 15 * time.Minute
	orphansKept = 500
)

func (s *Syncer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Syncer) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

// Run makes passes until ctx is done.
func (s *Syncer) Run(ctx context.Context) error {
	every := s.Every
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Pass(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("servicenow", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// Pass does what is due: the deployments queued, the reconcile when its
// interval has gone by, and the work notes owed to requested items.
func (s *Syncer) Pass(ctx context.Context) error {
	c := s.Config(ctx)
	if err := s.Store.SetServiceNowCMDB(ctx, c.Configured() && c.CMDB); err != nil {
		return err
	}
	if !c.Configured() {
		return nil
	}
	// Checked first on the pool: the lock takes a connection of its own.
	if due, err := s.Store.ServiceNowWorkDue(ctx, s.now(), c.CMDB, c.SyncInterval); err != nil || !due {
		return err
	}
	cl, err := servicenow.New(servicenow.Config{URL: c.URL, Auth: c.Auth, Username: c.Username, ClientID: c.ClientID, Secret: c.Secret,
		TLS: c.TLS()})
	if err != nil {
		return err
	}
	return s.Store.WithLock(ctx, store.LockServiceNowSync, func() error {
		var errs []error
		if c.CMDB {
			errs = append(errs, s.cmdb(ctx, cl, c))
		}
		return errors.Join(append(errs, s.notes(ctx, cl))...)
	})
}

// backoff is how long a call ServiceNow refused waits, from 10 seconds to
// 15 minutes.
func backoff(attempts int) time.Duration { return min(firstRetry<<min(attempts, 10), latestRetry) }

func (s *Syncer) cmdb(ctx context.Context, cl *servicenow.Client, c config.ServiceNowConfig) error {
	st, err := s.Store.ServiceNowState(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	reconcile := st.ReconciledAt == nil || now.Sub(*st.ReconciledAt) >= c.SyncInterval
	if reconcile {
		if err := s.Store.QueueAllForServiceNow(ctx, c.Projects); err != nil {
			return err
		}
	}
	due, err := s.Store.DueForServiceNow(ctx, now, batch)
	if err != nil {
		return err
	}
	owners := map[string]string{}
	for _, q := range due {
		if err := s.deployment(ctx, cl, c, q.DeploymentID, owners); err != nil {
			if err := s.Store.RetryForServiceNow(ctx, q.DeploymentID, s.now().Add(backoff(q.Attempts)), err.Error()); err != nil {
				return err
			}
			continue
		}
		if err := s.Store.DoneForServiceNow(ctx, q); err != nil {
			return err
		}
	}
	if reconcile {
		return s.report(ctx, cl, c, now)
	}
	return nil
}

// deployment brings its VMs' CIs to what they should hold, and retires
// those whose VM is gone.
func (s *Syncer) deployment(ctx context.Context, cl *servicenow.Client, c config.ServiceNowConfig, id string, owners map[string]string) error {
	vms, err := s.Store.ServiceNowVMs(ctx, id)
	if err != nil {
		return err
	}
	cis, err := s.Store.ServiceNowCIs(ctx, id)
	if err != nil {
		return err
	}
	kept := map[string]store.ServiceNowCI{}
	for _, ci := range cis {
		kept[ci.InstanceUUID] = ci
	}
	var errs []error
	alive := map[string]bool{}
	for _, vm := range vms {
		alive[vm.InstanceUUID] = true
		if !InScope(c.Projects, vm.ProjectID, vm.ProjectCode) {
			continue
		}
		values := Values(vm, s.owner(ctx, cl, vm.OwnerEmail, owners))
		if ci, ok := kept[vm.InstanceUUID]; ok && ci.RetiredAt == nil && ci.Class == c.Class && maps.Equal(ci.Sent, values) {
			continue
		}
		got, err := cl.Identify(ctx, c.DataSource, servicenow.CI{Class: c.Class, Values: values, NativeKey: vm.InstanceUUID})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", vm.Name, err))
			continue
		}
		if err := s.Store.SaveServiceNowCI(ctx, store.ServiceNowCI{InstanceUUID: vm.InstanceUUID, ResourceID: vm.ResourceID,
			DeploymentID: id, SysID: got.SysID, Class: c.Class, Sent: values}); err != nil {
			return err
		}
	}
	for _, ci := range cis {
		if ci.RetiredAt != nil || alive[ci.InstanceUUID] {
			continue
		}
		// Moved to another deployment (onboarded again): that one's look
		// keeps it.
		if moved, err := s.Store.VMAlive(ctx, ci.InstanceUUID); err != nil || moved {
			if err != nil {
				return err
			}
			continue
		}
		err := cl.Update(ctx, ci.Class, ci.SysID, map[string]string{"install_status": servicenow.InstallRetired,
			"operational_status": servicenow.OperationalRetired})
		if err != nil && !servicenow.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("retiring %s: %w", ci.Sent["name"], err))
			continue
		}
		if err := s.Store.RetireServiceNowCI(ctx, ci.InstanceUUID, s.now()); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

// owner is the sys_id of the ServiceNow user with this email: "" when
// there isn't exactly one, or rvav may not read users.
func (s *Syncer) owner(ctx context.Context, cl *servicenow.Client, email string, cache map[string]string) string {
	email = strings.ToLower(email)
	if email == "" {
		return ""
	}
	if id, ok := cache[email]; ok {
		return id
	}
	id, err := cl.UserSysID(ctx, email)
	if err != nil {
		s.log().Debug("servicenow user", "email", email, "err", err)
	}
	cache[email] = id
	return id
}

// InScope says whether a project's VMs are kept: every project's when none
// is named.
func InScope(projects []string, id, code string) bool {
	return len(projects) == 0 || slices.Contains(projects, id) || slices.Contains(projects, code)
}

// Values are what a VM's CI holds; those rvav doesn't know are left out,
// so that they don't clear what discovery found.
func Values(vm store.ServiceNowVM, ownerSysID string) map[string]string {
	var p api.VmProperties
	_ = json.Unmarshal(vm.Properties, &p)
	size := 0
	for _, d := range p.Disks {
		size += d.CapacityGb
	}
	operational := servicenow.OperationalOn
	if vm.SyncState == "missing" {
		operational = servicenow.OperationalOff
	}
	owner := vm.OwnerName
	if owner == "" {
		owner = "no one"
	}
	v := map[string]string{
		"name":               vm.Name,
		"vm_instance_uuid":   vm.InstanceUUID,
		"object_id":          p.Moref,
		"vcenter_uuid":       vm.VCenterUUID,
		"ip_address":         address(p.Nics),
		"fqdn":               deref(p.Fqdn),
		"cpus":               strconv.Itoa(p.CpuCount),
		"memory":             strconv.Itoa(p.MemoryMb),
		"disks":              strconv.Itoa(len(p.Disks)),
		"disks_size":         strconv.Itoa(size),
		"guest_os_fullname":  deref(p.GuestOs),
		"state":              state(p.PowerState),
		"install_status":     servicenow.InstallInstalled,
		"operational_status": operational,
		"correlation_id":     vm.ResourceID,
		"owned_by":           ownerSysID,
		"short_description": fmt.Sprintf("rvav: deployment %s (%s) in %s, owned by %s", vm.DeploymentName, vm.DeploymentID,
			vm.ProjectName, owner),
	}
	maps.DeleteFunc(v, func(_, x string) bool { return x == "" })
	return v
}

// address is the first IPv4 address of the first adapter that has one,
// else its first address.
func address(nics []api.VmNic) string {
	first := ""
	for _, n := range nics {
		for _, a := range deref(n.IpAddresses) {
			if !strings.Contains(a, ":") {
				return a
			}
			if first == "" {
				first = a
			}
		}
	}
	return first
}

func state(p api.VmPowerState) string {
	switch p {
	case api.On:
		return "on"
	case api.Off:
		return "off"
	case api.Suspended:
		return "paused"
	}
	return ""
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// Report is what the last reconcile found.
type Report struct {
	CheckedAt time.Time `json:"checked_at"`
	// ServiceNow is how many CIs of rvav's data source, not retired,
	// ServiceNow has.
	ServiceNow int `json:"servicenow"`
	// Orphans are the first of those rvav doesn't keep; Orphaned counts
	// them all.
	Orphans  []Orphan `json:"orphans"`
	Orphaned int      `json:"orphaned"`
	Error    string   `json:"error,omitempty"`
}

// Orphan is a CI of rvav's data source that rvav doesn't keep.
type Orphan struct {
	SysID        string `json:"sys_id"`
	Name         string `json:"name"`
	InstanceUUID string `json:"instance_uuid,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

func (s *Syncer) report(ctx context.Context, cl *servicenow.Client, c config.ServiceNowConfig, at time.Time) error {
	r := Report{CheckedAt: at, Orphans: []Orphan{}}
	// Pages of a read with no order may repeat or skip rows.
	cis, err := cl.All(ctx, c.Class, servicenow.Query{Encoded: "discovery_source=" + c.DataSource + "^install_status!=" + servicenow.InstallRetired + "^ORDERBYsys_id",
		Fields: []string{"sys_id", "name", "vm_instance_uuid", "sys_updated_on"}}, 500)
	if err != nil {
		r.Error = err.Error()
	} else {
		kept, err := s.Store.ServiceNowKept(ctx)
		if err != nil {
			return err
		}
		r.ServiceNow = len(cis)
		for _, ci := range cis {
			if kept[ci["sys_id"]] {
				continue
			}
			if r.Orphaned++; len(r.Orphans) < orphansKept {
				r.Orphans = append(r.Orphans, Orphan{SysID: ci["sys_id"], Name: ci["name"], InstanceUUID: ci["vm_instance_uuid"],
					UpdatedAt: ci["sys_updated_on"]})
			}
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.Store.SaveServiceNowReconcile(ctx, at, b)
}
