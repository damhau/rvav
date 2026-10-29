package provision

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/observe"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vcenters"
	"github.com/damhau/rvav/internal/vmprops"
)

// Activities are the steps of a request workflow. Each one records its own
// step (running, then done or failed) so the timeline shows retries too.
type Activities struct {
	Store              *store.Store
	VSphere            vsphere.Driver
	IPAM               efficientip.Client
	AWX                awx.Client
	AWXInventorySource int
	// AWXInventoryRequired: a real AWX, where jobs on rvav's VMs need the
	// inventory source (the fake has none).
	AWXInventoryRequired bool
	Events               *events.Publisher
	Log                  *slog.Logger
	// PollInterval is how often AWX jobs and inventory updates are polled.
	PollInterval time.Duration
	// IPTimeout bounds the wait for VMware Tools to report the guest IP.
	IPTimeout time.Duration
	// ShutdownTimeout bounds the wait for a guest OS to shut down.
	ShutdownTimeout time.Duration
	// Housekeeper applies retention and lease policies (the core service).
	Housekeeper Housekeeper
	// Subscriptions delivers to blocking subscriptions (ADR-0021).
	Subscriptions *dispatch.Dispatcher
	// TaskQueue is rvav's: custom workflows call the contract's activities
	// on it (ADR-0022).
	TaskQueue string
	// Now is the housekeeping clock (time.Now when nil); tests move it on.
	Now func() time.Time
	// Secrets opens the secrets a guest names: a domain join's account, a
	// local administrator's password (ADR-0024).
	Secrets *secrets.Box
	// VCenters connects to the vCenter a VM is in (ADR-0024); without it,
	// VSphere is the only one.
	VCenters VCenterSet
}

// VCenterSet is the vCenters rvav manages, by name ("" for the first).
type VCenterSet interface {
	Get(ctx context.Context, name string) (vsphere.Driver, error)
}

// vc connects to a vCenter by name ("" for the first).
func (a *Activities) vc(ctx context.Context, name string) (vsphere.Driver, error) {
	if a.VCenters == nil {
		return a.VSphere, nil
	}
	d, err := a.VCenters.Get(ctx, name)
	if errors.Is(err, vcenters.ErrUnknown) {
		return nil, permanent("vCenter %s: %v", name, err)
	}
	return d, err
}

// Info is what the workflow loads first.
type Info struct {
	RequestID   string     `json:"request_id"`
	Number      string     `json:"number"`
	ProjectID   string     `json:"project_id"`
	Status      string     `json:"status"`
	ScheduledAt *time.Time `json:"scheduled_at"`
	Plan        Plan       `json:"plan"`
	// Approvals are the request's approval levels, in the order they run.
	Approvals []string `json:"approvals,omitempty"`
	// Done are the keys of steps already done, and Built what they built
	// per deployment: a retried request resumes after them (ADR-0013).
	Done  []string         `json:"done,omitempty"`
	Built map[string]Built `json:"built,omitempty"`
	// BuiltVMs is the same by VM name: a multi-VM deployment has several
	// (ADR-0024); Rules are the DRS rules still recorded.
	BuiltVMs map[string]Built `json:"built_vms,omitempty"`
	Rules    map[string]bool  `json:"rules,omitempty"`
	// Outputs are what composite steps already done gave, by step key.
	Outputs map[string]map[string]any `json:"outputs,omitempty"`
}

// Built is what earlier steps made for one VM.
type Built struct {
	IP    string `json:"ip,omitempty"`
	Moref string `json:"moref,omitempty"`
}

func (info Info) done(key string) bool { return slices.Contains(info.Done, key) }

// LevelState is an approval level after it was opened or timed out.
type LevelState struct {
	// Status is pending, approved or rejected.
	Status string     `json:"status"`
	DueAt  *time.Time `json:"due_at"`
}

type VMInput struct {
	RequestID string `json:"request_id"`
	ProjectID string `json:"project_id"`
	Key       string `json:"key"`
	VM        VMUnit `json:"vm"`
	// Guest is the VM's: its secrets by name only, read when customizing.
	Guest blueprint.Guest `json:"guest"`
	IP    string          `json:"ip,omitempty"`
	Moref string          `json:"moref,omitempty"`
	// HostIndex spreads a component's VMs kept apart over the hosts.
	HostIndex int `json:"host_index,omitempty"`
}

// RuleInput is a component's DRS rule: its VMs by name and moref.
type RuleInput struct {
	VCenter      string   `json:"vcenter,omitempty"`
	RequestID    string   `json:"request_id"`
	ProjectID    string   `json:"project_id"`
	Key          string   `json:"key"`
	DeploymentID string   `json:"deployment_id"`
	Component    string   `json:"component"`
	Name         string   `json:"name"`
	Cluster      string   `json:"cluster"`
	VMs          []string `json:"vms"`
	Morefs       []string `json:"morefs"`
}

type JobInput struct {
	RequestID string         `json:"request_id"`
	ProjectID string         `json:"project_id"`
	StepKey   string         `json:"step_key"`
	Job       Job            `json:"job"`
	Limit     string         `json:"limit"`
	ExtraVars map[string]any `json:"extra_vars"`
	// Action is the AWX day-2 action the job runs, checked to be still
	// offered before launching (ADR-0018).
	Action string `json:"action,omitempty"`
}

type FinishInput struct {
	RequestID string `json:"request_id"`
	ProjectID string `json:"project_id"`
	Outcome   string `json:"outcome"` // succeeded, failed, cancelled
	StepKey   string `json:"step_key,omitempty"`
	Message   string `json:"message,omitempty"`
	// Untouched: the request stopped before changing anything (a blocking
	// subscription said no), so a day-2 deployment keeps its status.
	Untouched bool `json:"untouched,omitempty"`
}

// permanent marks errors that retrying cannot fix.
func permanent(format string, args ...any) error {
	return temporal.NewNonRetryableApplicationError(fmt.Sprintf(format, args...), "Permanent", nil)
}

func (a *Activities) update(ctx context.Context, requestID, projectID, key string, u store.StepUpdate) {
	st, err := a.Store.UpdateStep(ctx, a.Store.Pool, requestID, key, u)
	if err != nil {
		a.Log.Warn("update step", "request", requestID, "step", key, "err", err)
		return
	}
	a.Events.Step(ctx, projectID, requestID, st)
	a.Events.Request(ctx, requestID)
}

// run marks a step running, calls fn and records the outcome. fn returns the
// title and detail to show when done ("" keeps the current text).
func (a *Activities) run(ctx context.Context, requestID, projectID, key string, fn func() (title, detail string, err error)) error {
	a.update(ctx, requestID, projectID, key, store.StepUpdate{Status: "running"})
	start := time.Now()
	title, detail, err := fn()
	observe.Step(key, err != nil, time.Since(start))
	if errors.Is(err, store.ErrTaken) {
		err = temporal.NewNonRetryableApplicationError(err.Error(), "Permanent", err)
	}
	if err != nil {
		msg := errMessage(err)
		a.update(ctx, requestID, projectID, key, store.StepUpdate{Status: "failed", Error: &msg})
		return err
	}
	u := store.StepUpdate{Status: "done"}
	if title != "" {
		// One of several VMs' steps keeps saying which VM it was for.
		if _, vm, ok := strings.Cut(key, "@"); ok && !strings.Contains(title, vm) {
			title += " · " + vm
		}
		u.Title = &title
	}
	if detail != "" {
		u.Detail = &detail
	}
	a.update(ctx, requestID, projectID, key, u)
	return nil
}

func errMessage(err error) string {
	var app *temporal.ApplicationError
	if errors.As(err, &app) {
		return app.Message()
	}
	return err.Error()
}

// heartbeatWhile heartbeats every 10 s while fn runs.
func heartbeatWhile(ctx context.Context, fn func() error) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx)
			}
		}
	}()
	return fn()
}

func (a *Activities) LoadPlan(ctx context.Context, requestID string) (Info, error) {
	r, err := a.Store.GetRequest(ctx, requestID)
	if err != nil {
		return Info{}, err
	}
	info := Info{RequestID: r.ID, Number: r.Number, ProjectID: r.ProjectID, Status: r.Status, ScheduledAt: r.ScheduledAt}
	if err := json.Unmarshal(r.Plan, &info.Plan); err != nil {
		return info, permanent("request %s has no valid plan: %v", r.Number, err)
	}
	levels, err := a.Store.ApprovalsForRequest(ctx, r.ID)
	if err != nil {
		return info, err
	}
	for _, l := range levels {
		info.Approvals = append(info.Approvals, l.ID)
	}
	steps, err := a.Store.ListSteps(ctx, r.ID)
	if err != nil {
		return info, err
	}
	for _, s := range steps {
		// A composite step's own steps count too (step:<key>/…), and a
		// component's (component:<key>/…).
		top := s.ParentID == nil || strings.HasPrefix(s.Key, KeyStep) || strings.HasPrefix(s.Key, KeyComponent)
		if top && s.Status == "done" && s.Key != KeySubmit && s.Key != KeyPolicy {
			info.Done = append(info.Done, s.Key)
		}
		if len(s.Outputs) > 0 && strings.HasPrefix(s.Key, KeyStep) {
			var out map[string]any
			if json.Unmarshal(s.Outputs, &out) == nil {
				if info.Outputs == nil {
					info.Outputs = map[string]map[string]any{}
				}
				info.Outputs[s.Key] = out
			}
		}
	}
	if len(info.Done) > 0 {
		resources, err := a.Store.ResourcesForRequest(ctx, r.ID)
		if err != nil {
			return info, err
		}
		for _, res := range resources {
			var p struct {
				Address  string `json:"address"`
				Hostname string `json:"hostname"`
				NIC      string `json:"nic"`
				VM       struct {
					Moref string `json:"moref"`
				} `json:"vm"`
			}
			_ = json.Unmarshal(res.Properties, &p)
			if p.NIC != "" {
				continue // an added adapter's: found again by its VM's steps
			}
			if info.Built == nil {
				info.Built, info.BuiltVMs = map[string]Built{}, map[string]Built{}
			}
			b, bv := info.Built[res.DeploymentID], Built{}
			switch res.Type {
			case "vsphere.drs_rule":
				if info.Rules == nil {
					info.Rules = map[string]bool{}
				}
				info.Rules[res.Name] = true
			case "ipam.address":
				b.IP = p.Address
				bv = info.BuiltVMs[p.Hostname]
				bv.IP = p.Address
				info.BuiltVMs[p.Hostname] = bv
			case "vsphere.vm":
				b.Moref = p.VM.Moref
				bv = info.BuiltVMs[res.Name]
				bv.Moref = p.VM.Moref
				info.BuiltVMs[res.Name] = bv
			}
			info.Built[res.DeploymentID] = b
		}
	}
	return info, nil
}

func (a *Activities) publishAll(ctx context.Context, projectID, requestID string) {
	steps, err := a.Store.ListSteps(ctx, requestID)
	if err == nil {
		for _, s := range steps {
			if s.ParentID == nil {
				a.Events.Step(ctx, projectID, requestID, s)
			}
		}
	}
	a.Events.Request(ctx, requestID)
	if deps, err := a.Store.DeploymentsForRequest(ctx, requestID); err == nil {
		for _, d := range deps {
			a.Events.Deployment(ctx, d.ID)
		}
	}
}

// MarkRunning moves the request to running. It returns false when the
// request was cancelled in the meantime.
func (a *Activities) MarkRunning(ctx context.Context, requestID string) (bool, error) {
	r, err := a.Store.GetRequest(ctx, requestID)
	if err != nil {
		return false, err
	}
	switch r.Status {
	case "queued", "scheduled", "running":
	default:
		return false, nil
	}
	// Running, and the provisioning, day-2 or delete pre event (ADR-0021),
	// in one transaction.
	pre, _ := outbox.RequestTopics(r.Kind, deref(r.Action))
	err = a.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := a.Store.SetRequestStatus(ctx, tx, requestID, "running"); err != nil {
			return err
		}
		if r.DeploymentID != nil {
			var plan Plan
			_ = json.Unmarshal(r.Plan, &plan)
			during := "updating"
			if plan.Day2 != nil && plan.Day2.During != "" {
				during = plan.Day2.During
			}
			if err := a.Store.SetDeploymentStatus(ctx, tx, *r.DeploymentID, during); err != nil {
				return err
			}
		}
		return outbox.ForRequest(ctx, tx, pre, requestID, runKey(ctx, requestID, pre), "")
	})
	if err != nil {
		return false, err
	}
	if r.DeploymentID != nil {
		a.Events.Deployment(ctx, *r.DeploymentID)
	}
	a.Events.Request(ctx, requestID)
	return true, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// runKey dedupes an event written by an activity: the same in a retried
// activity, new in a retried request (another workflow run).
func runKey(ctx context.Context, requestID, topic string) string {
	run := "direct"
	if activity.IsActivity(ctx) {
		run = activity.GetInfo(ctx).WorkflowExecution.RunID
	}
	return requestID + ":" + topic + ":" + run
}

func (a *Activities) subnet(vm VMUnit) efficientip.Subnet {
	return efficientip.Subnet{ID: vm.SubnetID, CIDR: vm.Subnet, Gateway: vm.Gateway}
}

func (a *Activities) findResource(ctx context.Context, requestID, deploymentID, typ, name string) *store.Resource {
	list, err := a.Store.ResourcesForRequest(ctx, requestID)
	if err != nil {
		return nil
	}
	for i := range list {
		if list[i].DeploymentID == deploymentID && list[i].Type == typ && (name == "" || list[i].Name == name) {
			return &list[i]
		}
	}
	return nil
}

// addressOf is the address the request reserved for a VM's adapter (nic
// "" for its first), or "" when there is none yet. A deployment may hold
// several VMs (ADR-0024): the address is the VM's by its host name.
func (a *Activities) addressOf(ctx context.Context, requestID string, vm VMUnit, nic string) string {
	list, err := a.Store.ResourcesForRequest(ctx, requestID)
	if err != nil {
		return ""
	}
	for _, r := range list {
		var p struct {
			Address  string `json:"address"`
			Hostname string `json:"hostname"`
			NIC      string `json:"nic"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		if r.Type == "ipam.address" && r.DeploymentID == vm.DeploymentID && p.NIC == nic && p.Hostname == vm.Name {
			return p.Address
		}
	}
	return ""
}

// reserve reserves an address for one of a VM's adapters and records it.
func (a *Activities) reserve(ctx context.Context, in VMInput, sn efficientip.Subnet, subnet, fqdn, nic string) (string, error) {
	addr, err := a.IPAM.Reserve(ctx, sn, fqdn, in.VM.DeploymentID)
	if errors.Is(err, efficientip.ErrSubnetFull) {
		return "", permanent("Subnet %s has no free address", subnet)
	}
	if errors.Is(err, efficientip.ErrNameTaken) {
		return "", permanent("EfficientIP: %v; rvav did not reserve it for this deployment", err)
	}
	if err != nil {
		return "", fmt.Errorf("EfficientIP: %w", err)
	}
	ext := addr.ID
	if ext == "" {
		ext = "ip:" + addr.IP
	}
	p := map[string]any{"address": addr.IP, "subnet": subnet, "efficientip_id": addr.ID, "hostname": in.VM.Name, "fqdn": fqdn}
	if nic != "" {
		p["nic"] = nic
	}
	props, _ := json.Marshal(p)
	res, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: in.VM.DeploymentID,
		RequestID: &in.RequestID, Type: "ipam.address", Name: addr.IP, ExternalID: &ext, Properties: props})
	if err != nil {
		return "", err
	}
	a.Events.Resource(ctx, in.ProjectID, res)
	return addr.IP, nil
}

// ReserveIP reserves the VM's address, and one for each added adapter with
// efficientip; it returns the first.
func (a *Activities) ReserveIP(ctx context.Context, in VMInput) (string, error) {
	var ip string
	err := a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		ip = a.addressOf(ctx, in.RequestID, in.VM, "")
		if ip == "" {
			var err error
			if ip, err = a.reserve(ctx, in, a.subnet(in.VM), in.VM.Subnet, cmp.Or(in.VM.FQDN, in.VM.Name), ""); err != nil {
				return "", "", err
			}
		}
		ips, subnets := []string{ip}, []string{in.VM.Subnet}
		for _, n := range in.VM.Addressed() {
			nip := a.addressOf(ctx, in.RequestID, in.VM, n.Key)
			if nip == "" {
				var err error
				sn := efficientip.Subnet{ID: n.SubnetID, CIDR: n.Subnet}
				if nip, err = a.reserve(ctx, in, sn, n.Subnet, n.FQDN, n.Key); err != nil {
					return "", "", err
				}
			}
			ips, subnets = append(ips, nip), append(subnets, n.Subnet)
		}
		if len(ips) == 1 {
			return "Reserved " + ip, "Subnet " + in.VM.Subnet, nil
		}
		return "Reserved " + strings.Join(ips, ", "), "Subnets " + strings.Join(subnets, ", "), nil
	})
	return ip, err
}

func (a *Activities) CreateDNS(ctx context.Context, in VMInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		records := []efficientip.Record{
			{FQDN: in.VM.FQDN, Type: "A", Value: in.IP, Zone: in.VM.DNSZone},
			{FQDN: efficientip.PTRName(in.IP), Type: "PTR", Value: in.VM.FQDN, Zone: "in-addr.arpa"},
		}
		// Each added adapter's address has its own name (ADR-0024).
		for _, n := range in.VM.Addressed() {
			ip := a.addressOf(ctx, in.RequestID, in.VM, n.Key)
			if ip == "" {
				return "", "", fmt.Errorf("no address reserved for %s's adapter %s", in.VM.Name, n.Key)
			}
			records = append(records,
				efficientip.Record{FQDN: n.FQDN, Type: "A", Value: ip, Zone: n.DNSZone},
				efficientip.Record{FQDN: efficientip.PTRName(ip), Type: "PTR", Value: n.FQDN, Zone: "in-addr.arpa"})
		}
		for _, rec := range records {
			if a.findResource(ctx, in.RequestID, in.VM.DeploymentID, "dns.record", rec.Type+" "+rec.FQDN) != nil {
				continue
			}
			created, err := a.IPAM.CreateRecord(ctx, rec)
			if err != nil {
				return "", "", fmt.Errorf("EfficientIP DNS: %w", err)
			}
			ext := created.ID
			if ext == "" {
				ext = rec.Type + ":" + rec.FQDN
			}
			props, _ := json.Marshal(map[string]any{"fqdn": rec.FQDN, "record_type": rec.Type, "value": rec.Value,
				"zone": rec.Zone, "efficientip_id": created.ID})
			res, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: in.VM.DeploymentID,
				RequestID: &in.RequestID, Type: "dns.record", Name: rec.Type + " " + rec.FQDN, ExternalID: &ext, Properties: props})
			if err != nil {
				return "", "", err
			}
			a.Events.Resource(ctx, in.ProjectID, res)
		}
		if len(records) > 2 {
			return "Created A and PTR records", fmt.Sprintf("%s → %s and %d more", in.VM.FQDN, in.IP, len(records)/2-1), nil
		}
		return "Created A and PTR records", in.VM.FQDN + " → " + in.IP, nil
	})
}

func (a *Activities) recordVM(ctx context.Context, vc vsphere.Driver, in VMInput, vm *vsphere.VM) (store.Resource, error) {
	props := vmprops.Build(vc.Name(), *vm, vmprops.Ours{FQDN: in.VM.FQDN, Template: in.VM.Template, Component: in.VM.Component}, nil, 0)
	// The VM as rvav built it: what drift is measured from (S13.2).
	props, _ = vmprops.Settle(nil, props, vmprops.All...)
	ext := vm.InstanceUUID
	if ext == "" {
		ext = vm.Moref
	}
	res, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: in.VM.DeploymentID,
		RequestID: &in.RequestID, Type: "vsphere.vm", Name: vm.Name, ExternalID: &ext, Properties: props})
	if err == nil {
		a.Events.Resource(ctx, in.ProjectID, res)
		a.Events.Deployment(ctx, in.VM.DeploymentID)
	}
	return res, err
}

func (a *Activities) CloneVM(ctx context.Context, in VMInput) (string, error) {
	var moref string
	err := a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		vc, err := a.vc(ctx, in.VM.VCenter)
		if err != nil {
			return "", "", err
		}
		var vm *vsphere.VM
		err = heartbeatWhile(ctx, func() error {
			var err error
			vm, err = vc.CloneVM(ctx, vsphere.VMSpec{
				Name: in.VM.Name, Template: in.VM.Template, Cluster: in.VM.Cluster, Datastore: in.VM.Datastore,
				Network: in.VM.Network, Folder: in.VM.Folder, VCPU: in.VM.VCPU, MemoryMB: in.VM.MemoryGB * 1024,
				DiskGB: in.VM.DiskGB, Annotation: "Managed by rvav. Deployment " + in.VM.DeploymentID,
				ExtraConfig: map[string]string{"guestinfo.rvav.deployment": in.VM.DeploymentID, "guestinfo.rvav.request": in.RequestID},
				HostIndex:   in.HostIndex, Disks: diskSpecs(in.VM), NICs: nicNetworks(in.VM),
				Library: in.VM.Library, StoragePolicy: in.VM.StoragePolicy,
			})
			return err
		})
		if err != nil {
			// A VM made before the clone failed (adding its disks, say) is
			// recorded, so a rollback or deletion finds it.
			if found, ferr := vc.FindVM(ctx, in.VM.Name); ferr == nil && found != nil {
				if _, rerr := a.recordVM(ctx, vc, in, found); rerr != nil {
					a.Log.Warn("record a VM whose clone failed", "vm", in.VM.Name, "err", rerr)
				}
			}
			if errors.Is(err, vsphere.ErrNotFound) {
				return "", "", permanent("vCenter: %v", err)
			}
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		moref = vm.Moref
		if _, err := a.recordVM(ctx, vc, in, vm); err != nil {
			return "", "", err
		}
		// Tags once it is recorded: a tag that can't be attached leaves a VM
		// a rollback deletes.
		if err := vc.AttachTags(ctx, vm.Moref, in.VM.Tags); errors.Is(err, vsphere.ErrNotFound) {
			return "", "", permanent("vCenter: %v. Ask an administrator to create it.", err)
		} else if err != nil {
			return "", "", fmt.Errorf("vCenter tags: %w", err)
		}
		detail := fmt.Sprintf("%s · %s · %s", in.VM.Cluster, in.VM.Datastore, vm.Moref)
		if in.VM.StoragePolicy != "" {
			detail += " · " + in.VM.StoragePolicy
		}
		if n := len(in.VM.Disks) + len(in.VM.NICs); n > 0 {
			detail += fmt.Sprintf(" · %d disks, %d adapters", 1+len(in.VM.Disks), 1+len(in.VM.NICs))
		}
		if len(in.VM.Tags) > 0 {
			detail += " · tags " + strings.Join(in.VM.Tags, ", ")
		}
		if in.VM.Library != "" {
			return "Deployed " + in.VM.Template + " from " + in.VM.Library, detail, nil
		}
		return "Cloned from " + in.VM.Template, detail, nil
	})
	return moref, err
}

func (a *Activities) Customize(ctx context.Context, in VMInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		var extra []vsphere.NICSetting
		for _, n := range in.VM.NICs {
			if n.IPAM != blueprint.IPAMEfficientIP {
				extra = append(extra, vsphere.NICSetting{DHCP: true})
				continue
			}
			ip := a.addressOf(ctx, in.RequestID, in.VM, n.Key)
			if ip == "" {
				return "", "", fmt.Errorf("no address reserved for %s's adapter %s", in.VM.Name, n.Key)
			}
			extra = append(extra, vsphere.NICSetting{IP: ip, PrefixLength: n.PrefixLength, DNSServers: n.DNSServers})
		}
		if in.Guest.CloudInit != nil && in.Guest.OSFamily != "windows" {
			return a.cloudInit(ctx, in, extra)
		}
		cu := vsphere.Customization{
			OSFamily: in.Guest.OSFamily, Hostname: in.VM.Name, Domain: in.Guest.Domain, TimeZone: in.Guest.TimeZone,
			IP: in.IP, PrefixLength: in.VM.PrefixLength, Gateway: in.VM.Gateway, DNSServers: in.VM.DNSServers,
			Spec: in.Guest.CustomizationSpec, Extra: extra,
		}
		detail := fmt.Sprintf("Hostname %s, static IP %s/%d", in.VM.Name, in.IP, in.VM.PrefixLength)
		if in.Guest.OSFamily == "windows" {
			if name := in.Guest.AdminPassword; name != "" {
				_, pw, err := a.secret(ctx, name)
				if err != nil {
					return "", "", err
				}
				cu.AdminPassword = pw
			}
			if j := in.Guest.DomainJoin; j != nil {
				user, pw, err := a.secret(ctx, j.Credential)
				if err != nil {
					return "", "", err
				}
				cu.JoinDomain, cu.DomainOU, cu.DomainUser, cu.DomainPassword = j.Domain, j.OU, user, pw
				detail += ", joins " + j.Domain
				if j.OU != "" {
					detail += " in " + j.OU
				}
			}
		}
		if cu.Spec != "" {
			detail += ", from specification " + cu.Spec
		}
		vc, err := a.vc(ctx, in.VM.VCenter)
		if err != nil {
			return "", "", err
		}
		err = vc.Customize(ctx, in.Moref, cu)
		if errors.Is(err, vsphere.ErrNotFound) {
			return "", "", permanent("vCenter: %v. Ask a catalog admin to check this item.", err)
		}
		if err != nil {
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		return "Applied guest customization", detail, nil
	})
}

// secret opens a stored secret: its user name and value. The value never
// goes into a step, a log or an error.
func (a *Activities) secret(ctx context.Context, name string) (username, value string, err error) {
	if a.Secrets == nil {
		return "", "", permanent("rvav has no secrets key (RVAV_SECRETS_KEY), so it can't open the secret %s", name)
	}
	v, err := a.Store.GetSecret(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", permanent("There is no secret %s any more. Ask a catalog admin to check this item.", name)
	}
	if err != nil {
		return "", "", err
	}
	plain, err := a.Secrets.Open(v.Name, v.KeyID, v.Nonce, v.Ciphertext)
	if err != nil {
		return "", "", permanent("The secret %s can't be opened with rvav's key", name)
	}
	return v.Username, string(plain), nil
}

// cloudInit gives a Linux VM its host name, addresses and user data
// through guestinfo, for cloud-init, instead of vCenter's customization.
func (a *Activities) cloudInit(ctx context.Context, in VMInput, extra []vsphere.NICSetting) (string, string, error) {
	vc, err := a.vc(ctx, in.VM.VCenter)
	if err != nil {
		return "", "", err
	}
	vm, err := vc.GetVM(ctx, in.Moref)
	if err != nil {
		return "", "", fmt.Errorf("vCenter: %w", err)
	}
	if vm.PowerState == "on" {
		return "", "", nil // cloud-init already ran on first boot
	}
	if len(vm.NICs) < 1+len(extra) {
		return "", "", permanent("%s has %d network adapters, %d were planned", in.VM.Name, len(vm.NICs), 1+len(extra))
	}
	// The first adapter, the template's others (DHCP), then those added,
	// last.
	nics := []ciNIC{{MAC: vm.NICs[0].MAC, IP: in.IP, PrefixLength: in.VM.PrefixLength, Gateway: in.VM.Gateway, DNSServers: in.VM.DNSServers}}
	added := len(vm.NICs) - len(extra)
	for _, n := range vm.NICs[1:added] {
		nics = append(nics, ciNIC{MAC: n.MAC})
	}
	for i, n := range extra {
		c := ciNIC{MAC: vm.NICs[added+i].MAC}
		if !n.DHCP {
			c.IP, c.PrefixLength, c.DNSServers = n.IP, n.PrefixLength, n.DNSServers
		}
		nics = append(nics, c)
	}
	meta, err := cloudInitMetadata(in.VM.DeploymentID+"/"+in.VM.Name, in.VM.Name, in.Guest.Domain, nics)
	if err != nil {
		return "", "", err
	}
	r, err := a.Store.GetRequest(ctx, in.RequestID)
	if err != nil {
		return "", "", err
	}
	inputs := map[string]any{}
	_ = json.Unmarshal(r.Inputs, &inputs)
	ips := []string{in.IP}
	for _, n := range nics[1:] {
		ips = append(ips, n.IP)
	}
	user, err := renderUserData(in.Guest.CloudInit.UserData, map[string]any{
		"vm": map[string]any{"name": in.VM.Name, "fqdn": in.VM.FQDN, "ip": in.IP, "ips": ips, "domain": in.Guest.Domain,
			"component": in.VM.Component, "deployment_id": in.VM.DeploymentID},
		"inputs": inputs, "project": r.ProjectCode, "environment": r.Environment,
		"request": map[string]any{"id": r.ID, "number": r.Number},
	})
	if err != nil {
		return "", "", permanent("cloud-init user data: %v. Ask a catalog admin to check this item.", err)
	}
	if err := vc.SetGuestInfo(ctx, in.Moref, guestInfo(meta, user)); err != nil {
		return "", "", fmt.Errorf("vCenter: %w", err)
	}
	return "Wrote cloud-init metadata and user data", fmt.Sprintf("Hostname %s, static IP %s/%d, through guestinfo",
		in.VM.Name, in.IP, in.VM.PrefixLength), nil
}

func (a *Activities) PowerOn(ctx context.Context, in VMInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		vc, err := a.vc(ctx, in.VM.VCenter)
		if err != nil {
			return "", "", err
		}
		if err := vc.PowerOn(ctx, in.Moref); err != nil {
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		// Powered on by rvav, even if its address doesn't come.
		if vm, err := vc.GetVM(ctx, in.Moref); err == nil {
			if _, err := a.recordVM(ctx, vc, in, vm); err != nil {
				a.Log.Warn("record vm", "vm", in.VM.Name, "err", err)
			}
		}
		timeout := a.IPTimeout
		if timeout == 0 {
			timeout = 10 * time.Minute
		}
		wctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var ip string
		err = heartbeatWhile(ctx, func() error {
			var err error
			ip, err = vc.WaitForIP(wctx, in.Moref)
			return err
		})
		if err != nil {
			if wctx.Err() != nil && ctx.Err() == nil {
				return "", "", permanent("VMware Tools did not report an IP address within %s. Check the console of %s.", timeout, in.VM.Name)
			}
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		if vm, err := vc.GetVM(ctx, in.Moref); err == nil {
			if _, err := a.recordVM(ctx, vc, in, vm); err != nil {
				a.Log.Warn("record vm", "vm", in.VM.Name, "err", err)
			}
		}
		detail := "VMware Tools reports " + ip
		if ip != in.IP {
			detail += " (expected " + in.IP + ")"
		}
		return "Powered on, guest IP " + ip, detail, nil
	})
}

// diskSpecs and nicNetworks are what is added to the VM after the clone.
func diskSpecs(vm VMUnit) []vsphere.DiskSpec {
	var out []vsphere.DiskSpec
	for _, d := range vm.Disks {
		out = append(out, vsphere.DiskSpec{SizeGB: d.SizeGB, Datastore: d.Datastore, StoragePolicy: d.StoragePolicy})
	}
	return out
}

func nicNetworks(vm VMUnit) []string {
	var out []string
	for _, n := range vm.NICs {
		out = append(out, n.Network)
	}
	return out
}

// CreateDRSRule keeps a component's VMs on separate hosts with a DRS
// rule, recorded on the deployment so deleting it removes the rule.
func (a *Activities) CreateDRSRule(ctx context.Context, in RuleInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		vc, err := a.vc(ctx, in.VCenter)
		if err != nil {
			return "", "", err
		}
		if err := vc.SetAntiAffinityRule(ctx, in.Cluster, in.Name, in.Morefs); err != nil {
			if errors.Is(err, vsphere.ErrNotFound) {
				return "", "", permanent("%v", err)
			}
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		props, _ := json.Marshal(map[string]any{"rule": map[string]any{"kind": "vm_anti_affinity", "cluster": in.Cluster,
			"vcenter": vc.Name(), "component": in.Component, "vms": in.VMs}})
		ext := vc.Name() + "/" + in.Cluster + "/" + in.Name
		res, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: in.DeploymentID,
			RequestID: &in.RequestID, Type: "vsphere.drs_rule", Name: in.Name, ExternalID: &ext, Properties: props})
		if err != nil {
			return "", "", err
		}
		a.Events.Resource(ctx, in.ProjectID, res)
		return "Kept " + strings.Join(in.VMs, ", ") + " on separate hosts", "DRS rule " + in.Name + " in " + in.Cluster, nil
	})
}

// waitAWX polls an AWX job until it finishes.
func (a *Activities) waitAWX(ctx context.Context, get func() (awx.Job, error)) (awx.Job, error) {
	for {
		j, err := get()
		if err != nil {
			return j, err
		}
		if j.Finished {
			return j, nil
		}
		activity.RecordHeartbeat(ctx)
		if err := vsphere.Sleep(ctx, a.poll()); err != nil {
			return j, err
		}
	}
}

func (a *Activities) poll() time.Duration {
	if a.PollInterval > 0 {
		return a.PollInterval
	}
	return time.Second
}

func (a *Activities) RefreshInventory(ctx context.Context, requestID, projectID string) error {
	return a.RefreshInventoryStep(ctx, requestID, projectID, KeyInventory)
}

// RefreshInventoryStep refreshes the AWX inventory as the step key (a
// composite step's own).
func (a *Activities) RefreshInventoryStep(ctx context.Context, requestID, projectID, key string) error {
	return a.run(ctx, requestID, projectID, key, func() (string, string, error) {
		if a.AWXInventorySource == 0 && a.AWXInventoryRequired {
			return "", "", permanent("AWX: RVAV_AWX_INVENTORY_SOURCE_ID is not set, so jobs can't run on rvav's VMs")
		}
		id, err := a.AWX.UpdateInventorySource(ctx, a.AWXInventorySource)
		if err != nil {
			return "", "", fmt.Errorf("AWX: %w", err)
		}
		j, err := a.waitAWX(ctx, func() (awx.Job, error) { return a.AWX.GetInventoryUpdate(ctx, id) })
		if err != nil {
			return "", "", fmt.Errorf("AWX: %w", err)
		}
		if j.Status != "successful" {
			return "", "", fmt.Errorf("AWX inventory update %d ended %s", id, j.Status)
		}
		kind, ext := "awx_inventory_update", fmt.Sprint(id)
		_, _ = a.Store.UpdateStep(ctx, a.Store.Pool, requestID, key, store.StepUpdate{ExternalKind: &kind, ExternalID: &ext, ExternalURL: &j.URL})
		return "Refreshed AWX inventory", fmt.Sprintf("Inventory update %d", id), nil
	})
}

// RunJob launches an AWX job template (or resumes following one already
// launched by an earlier attempt), mirrors each task as a child step and
// copies the output to the step log.
func (a *Activities) RunJob(ctx context.Context, in JobInput) error {
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "running"})
	// Properties blocking subscriptions set (ADR-0021) reach the job in rvav.properties.
	if ctxVars, ok := in.ExtraVars["rvav"].(map[string]any); ok {
		if props, err := a.Store.RequestProperties(ctx, in.RequestID); err == nil && len(props) > 0 {
			withProps := make(map[string]any, len(ctxVars)+1)
			for k, v := range ctxVars {
				withProps[k] = v
			}
			withProps["properties"] = props
			extra := make(map[string]any, len(in.ExtraVars))
			for k, v := range in.ExtraVars {
				extra[k] = v
			}
			extra["rvav"] = withProps
			in.ExtraVars = extra
		}
	}
	start := time.Now()
	err := a.runJob(ctx, in)
	observe.Step(in.StepKey, err != nil, time.Since(start))
	if err != nil {
		msg := errMessage(err)
		a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "failed", Error: &msg})
		return err
	}
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "done"})
	return nil
}

func (a *Activities) runJob(ctx context.Context, in JobInput) error {
	st, err := a.Store.GetStepByKey(ctx, in.RequestID, in.StepKey)
	if err != nil {
		return err
	}
	var jobID int
	if st.ExternalID != nil {
		fmt.Sscan(*st.ExternalID, &jobID)
	}
	// A job AWX no longer knows is launched again: rvav's database and
	// Temporal may have been restored to after AWX's, or AWX's history
	// purged (S14.3). Playbooks are written to run again.
	relaunched := false
	if jobID != 0 {
		if _, err := a.AWX.GetJob(ctx, jobID, in.Job.Workflow); errors.Is(err, awx.ErrJobNotFound) {
			a.Log.Warn("AWX no longer knows the job; launching it again", "job", jobID, "request", in.RequestID, "step", in.StepKey)
			jobID, relaunched = 0, true
		}
	}
	if jobID == 0 {
		if err := a.checkAWXAction(ctx, in.Action); err != nil {
			return err
		}
		// Launched by its ID when rvav has it: a template renamed since, or
		// another one given the name, can't be launched instead.
		tplID := in.Job.TemplateID
		if tplID == 0 {
			if tplID, err = a.AWX.LookupJobTemplate(ctx, in.Job.Template, in.Job.Workflow); err != nil {
				return permanent("AWX: %v", err)
			}
		}
		launch := awx.LaunchRequest{Limit: in.Limit, ExtraVars: in.ExtraVars, Inventory: in.Job.Inventory, Credentials: in.Job.Credentials}
		if in.Limit != "" {
			// Jobs on rvav's VMs run against rvav's inventory, refreshed before.
			if a.AWXInventorySource == 0 && a.AWXInventoryRequired {
				return permanent("AWX: RVAV_AWX_INVENTORY_SOURCE_ID is not set, so jobs can't run on rvav's VMs")
			}
			if a.AWXInventorySource != 0 {
				if launch.Inventory, err = a.AWX.InventoryOfSource(ctx, a.AWXInventorySource); err != nil {
					return fmt.Errorf("AWX: %w", err)
				}
			}
		}
		job, err := a.AWX.Launch(ctx, tplID, in.Job.Workflow, launch)
		switch {
		case errors.Is(err, awx.ErrNoInventoryPrompt):
			return permanent("AWX: %s does not prompt for an inventory on launch, so it can't run against rvav's inventory", in.Job.Template)
		case errors.Is(err, awx.ErrNoCredentialPrompt):
			return permanent("AWX: %s does not prompt for credentials on launch, so the ones chosen can't be used", in.Job.Template)
		case errors.Is(err, awx.ErrNoLimitPrompt):
			return permanent("AWX: %s does not prompt for a limit on launch, so it would run on every host", in.Job.Template)
		case errors.Is(err, awx.ErrPromptIgnored):
			return permanent("AWX: %v", err)
		}
		if err != nil {
			return fmt.Errorf("AWX: %w", err)
		}
		jobID = job.ID
		kind := "awx_job"
		if in.Job.Workflow {
			kind = "awx_workflow_job"
		}
		ext, title := fmt.Sprint(job.ID), fmt.Sprintf("AWX job %d · %s", job.ID, in.Job.Template)
		hasLog := true
		a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Title: &title, ExternalKind: &kind,
			ExternalID: &ext, ExternalURL: &job.URL, HasLog: &hasLog})
	}

	// Resume from what an earlier attempt processed; without that, start
	// over so log lines and tasks are not duplicated.
	resumed := activity.HasHeartbeatDetails(ctx) && !relaunched
	if !resumed {
		if _, err := a.Store.Pool.Exec(ctx, `delete from step_log_lines where step_id = $1`, st.ID); err != nil {
			return err
		}
		if _, err := a.Store.Pool.Exec(ctx, `delete from request_steps where parent_id = $1`, st.ID); err != nil {
			return err
		}
	}
	if in.Job.Workflow {
		return a.followWorkflow(ctx, in, st.ID, jobID, resumed)
	}
	counter := 0
	if resumed {
		_ = activity.GetHeartbeatDetails(ctx, &counter)
	}

	tasks, _ := a.countChildren(ctx, st.ID)
	running := ""
	failedMsg := ""
	for {
		evs, err := a.AWX.Events(ctx, jobID, counter)
		if err != nil {
			return fmt.Errorf("AWX: %w", err)
		}
		var lines []store.LogLine
		for _, e := range evs {
			counter = e.Counter
			for _, l := range splitLines(e.Stdout) {
				lines = append(lines, store.LogLine{Text: l, Kind: awx.LineKind(l)})
			}
			switch {
			case e.Event == "playbook_on_task_start" || e.Event == "playbook_on_handler_task_start":
				if running != "" {
					a.child(ctx, in, running, "", "done", nil)
				}
				tasks++
				running = fmt.Sprintf("%s/task:%d", in.StepKey, tasks)
				a.child(ctx, in, running, e.Task, "running", nil)
			case strings.HasPrefix(e.Event, "runner_on_failed") || e.Event == "runner_on_unreachable":
				msg := e.Message
				if msg == "" {
					msg = "Task failed on " + e.Host
				}
				failedMsg = fmt.Sprintf("%s: %s", e.Task, msg)
				if running != "" {
					a.child(ctx, in, running, e.Task, "failed", &msg)
					running = ""
				}
			case e.Event == "runner_on_skipped":
				if running != "" {
					a.child(ctx, in, running, e.Task, "skipped", nil)
					running = ""
				}
			case strings.HasPrefix(e.Event, "runner_on_ok"):
				if running != "" {
					a.child(ctx, in, running, e.Task, "done", nil)
					running = ""
				}
			case e.Event == "playbook_on_stats":
				if running != "" {
					a.child(ctx, in, running, "", "done", nil)
					running = ""
				}
			}
		}
		if len(lines) > 0 {
			next, err := a.Store.AppendLogLines(ctx, a.Store.Pool, st.ID, lines)
			if err != nil {
				return err
			}
			a.Events.Log(ctx, in.ProjectID, in.RequestID, st.ID, next)
		}
		activity.RecordHeartbeat(ctx, counter)

		job, err := a.AWX.GetJob(ctx, jobID, in.Job.Workflow)
		if err != nil {
			return fmt.Errorf("AWX: %w", err)
		}
		if job.Finished && len(evs) == 0 {
			if job.Status == "successful" {
				return nil
			}
			if failedMsg == "" {
				failedMsg = fmt.Sprintf("AWX job %d ended %s", jobID, job.Status)
			}
			return temporal.NewNonRetryableApplicationError(failedMsg, "AWXJobFailed", nil)
		}
		if err := vsphere.Sleep(ctx, a.poll()); err != nil {
			// Cancelled: stop the job too, so it does not keep changing the host.
			_ = a.AWX.Cancel(context.Background(), jobID, in.Job.Workflow)
			return err
		}
	}
}

// followWorkflow follows a workflow job node by node: each node is a task
// of the step, and a finished node's job output goes to the step's log.
func (a *Activities) followWorkflow(ctx context.Context, in JobInput, stepID string, jobID int, resumed bool) error {
	var logged []int // jobs whose output is in the log
	if resumed {
		_ = activity.GetHeartbeatDetails(ctx, &logged)
	}
	// Once the job has finished, its nodes are read once more: they may
	// have been read just before its last one ended.
	var finished *awx.Job
	for {
		nodes, err := a.AWX.WorkflowNodes(ctx, jobID)
		if err != nil {
			return fmt.Errorf("AWX: %w", err)
		}
		failedMsg := ""
		for _, n := range nodes {
			status := "pending"
			var errText *string
			switch {
			case n.DoNotRun:
				status = "skipped"
			case n.Status == "successful":
				status = "done"
			case n.Status == "failed" || n.Status == "error" || n.Status == "canceled":
				status = "failed"
				msg := fmt.Sprintf("%s ended %s", n.Name, n.Status)
				errText, failedMsg = &msg, msg
			case n.Status != "":
				status = "running"
			}
			a.child(ctx, in, fmt.Sprintf("%s/node:%d", in.StepKey, n.ID), n.Name, status, errText)
			// Only playbook runs have events; an inventory sync or an
			// approval node only has its status.
			if n.JobID == 0 || !awx.Done(n.Status) || slices.Contains(logged, n.JobID) || n.Kind != "" && n.Kind != "job" {
				continue
			}
			evs, err := a.AWX.Events(ctx, n.JobID, 0)
			if err != nil {
				return fmt.Errorf("AWX: %w", err)
			}
			lines := []store.LogLine{{Text: fmt.Sprintf("%s · AWX job %d", n.Name, n.JobID), Kind: "header"}}
			for _, e := range evs {
				for _, l := range splitLines(e.Stdout) {
					lines = append(lines, store.LogLine{Text: l, Kind: awx.LineKind(l)})
				}
				if strings.HasPrefix(e.Event, "runner_on_failed") && e.Message != "" {
					failedMsg = fmt.Sprintf("%s: %s: %s", n.Name, e.Task, e.Message)
				}
			}
			next, err := a.Store.AppendLogLines(ctx, a.Store.Pool, stepID, lines)
			if err != nil {
				return err
			}
			a.Events.Log(ctx, in.ProjectID, in.RequestID, stepID, next)
			logged = append(logged, n.JobID)
		}
		activity.RecordHeartbeat(ctx, logged)

		if job := finished; job != nil {
			if job.Status == "successful" {
				return nil
			}
			if failedMsg == "" {
				failedMsg = fmt.Sprintf("AWX workflow job %d ended %s", jobID, job.Status)
			}
			return temporal.NewNonRetryableApplicationError(failedMsg, "AWXJobFailed", nil)
		}
		job, err := a.AWX.GetJob(ctx, jobID, true)
		if err != nil {
			return fmt.Errorf("AWX: %w", err)
		}
		if job.Finished {
			finished = &job
			continue
		}
		if err := vsphere.Sleep(ctx, a.poll()); err != nil {
			_ = a.AWX.Cancel(context.Background(), jobID, true)
			return err
		}
	}
}

func (a *Activities) countChildren(ctx context.Context, stepID string) (int, error) {
	var n int
	err := a.Store.Pool.QueryRow(ctx, `select count(*) from request_steps where parent_id = $1`, stepID).Scan(&n)
	return n, err
}

func (a *Activities) child(ctx context.Context, in JobInput, key, title, status string, errText *string) {
	if title == "" {
		if st, err := a.Store.GetStepByKey(ctx, in.RequestID, key); err == nil {
			title = st.Title
		}
	}
	st, err := a.Store.UpsertChildStep(ctx, a.Store.Pool, in.RequestID, in.StepKey, key, title, status, errText)
	if err != nil {
		a.Log.Warn("record AWX task", "request", in.RequestID, "task", title, "err", err)
		return
	}
	a.Events.Step(ctx, in.ProjectID, in.RequestID, st)
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Finish records the outcome of the request and notifies the owner.
func (a *Activities) Finish(ctx context.Context, in FinishInput) error {
	r, err := a.Store.GetRequest(ctx, in.RequestID)
	if err != nil {
		return err
	}
	// Counted once, not again when the activity is retried after this.
	defer func(was string) {
		if err == nil && was != in.Outcome {
			var ran time.Duration
			if r.StartedAt != nil {
				ran = time.Since(*r.StartedAt)
			}
			observe.RequestFinished(r.Kind, in.Outcome, ran)
		}
	}(r.Status)
	title := "Ready"
	if r.ItemType != "vm_blueprint" || r.Kind == "day2" {
		title = "Done"
	}
	// The outcome and the post event (ADR-0021) in one transaction;
	// notifications once it is committed.
	_, post := outbox.RequestTopics(r.Kind, deref(r.Action))
	err = a.Store.Tx(ctx, func(db pgx.Tx) error {
		if r.DeploymentID != nil {
			// Two actions on one deployment finishing together take turns,
			// so the second sees the first's outcome below.
			if err := a.Store.LockDeployment(ctx, db, *r.DeploymentID); err != nil {
				return err
			}
		}
		switch in.Outcome {
		case "succeeded":
			if _, err := a.Store.UpdateStep(ctx, db, in.RequestID, KeyReady, store.StepUpdate{Status: "done", Title: &title}); err != nil {
				return err
			}
			if err := a.Store.SetDeploymentsStatusForRequest(ctx, db, in.RequestID, "ready"); err != nil {
				return err
			}
			if err := a.Store.SetRequestStatus(ctx, db, in.RequestID, "succeeded"); err != nil {
				return err
			}
		case "failed":
			stepID := ""
			if st, err := a.Store.GetStepByKey(ctx, in.RequestID, in.StepKey); err == nil {
				stepID = st.ID
			}
			if err := a.Store.SetRequestError(ctx, db, in.RequestID, stepID, in.Message); err != nil {
				return err
			}
			if err := a.Store.SetDeploymentsStatusForRequest(ctx, db, in.RequestID, "failed"); err != nil {
				return err
			}
			if err := a.Store.SetRequestStatus(ctx, db, in.RequestID, "failed"); err != nil {
				return err
			}
		case "cancelled":
			if err := a.Store.MarkRemainingSteps(ctx, db, in.RequestID, "cancelled"); err != nil {
				return err
			}
			if err := a.Store.SetDeploymentsStatusForRequest(ctx, db, in.RequestID, "deleted"); err != nil {
				return err
			}
			if err := a.Store.SetRequestStatus(ctx, db, in.RequestID, "cancelled"); err != nil {
				return err
			}
		default:
			return permanent("unknown outcome %q", in.Outcome)
		}
		if r.DeploymentID != nil {
			// A day-2 request: the deployment it acted on goes back to ready,
			// or to what the action made of it, unless an action on another VM
			// still runs (the deployment's lock orders two finishing together).
			var plan Plan
			_ = json.Unmarshal(r.Plan, &plan)
			status := day2Status(plan, in.Outcome, r.StartedAt != nil && !in.Untouched)
			other, err := a.Store.OtherRunningOnDeployment(ctx, db, *r.DeploymentID, r.ID)
			if err != nil {
				return err
			}
			if !other || status != "ready" {
				if err := a.Store.SetDeploymentStatus(ctx, db, *r.DeploymentID, status); err != nil {
					return err
				}
			}
		}
		return outbox.ForRequest(ctx, db, post, in.RequestID, runKey(ctx, in.RequestID, post), in.Outcome)
	})
	if err != nil {
		return err
	}
	// The owner hears how it ended, and the requester when on their behalf.
	n := store.NewNotification{Title: title + ": " + r.Title, Level: "success", Link: "/requests/" + r.Number,
		RequestID: &r.ID, Kind: "request_succeeded", Data: map[string]any{"ready": title == "Ready"}}
	if in.Outcome == "failed" {
		n.Kind, n.Title, n.Body, n.Level = "request_failed", "Failed: "+r.Title, in.Message, "error"
	}
	if in.Outcome == "succeeded" || in.Outcome == "failed" {
		n.DedupeKey = runKey(ctx, r.ID, n.Kind)
		for _, uid := range dedupe([]string{r.OwnerID, r.RequesterID}) {
			n.UserID = uid
			a.Events.Notify(ctx, n)
		}
	}
	a.publishAll(ctx, r.ProjectID, in.RequestID)
	return nil
}

// deleteResource deletes a DRS rule, VM, DNS record or IP reservation
// where it lives, then its row, and says what it did ("" for PTR records).
// Deleting something already gone is not an error.
func (a *Activities) deleteResource(ctx context.Context, r store.Resource) (string, error) {
	var err error
	what := ""
	switch r.Type {
	case "vsphere.drs_rule":
		var p struct {
			Rule struct {
				Cluster string `json:"cluster"`
				VCenter string `json:"vcenter"`
			} `json:"rule"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		var vc vsphere.Driver
		if vc, err = a.vc(ctx, p.Rule.VCenter); err == nil {
			err = vc.DeleteRule(ctx, p.Rule.Cluster, r.Name)
		}
		what = "removed DRS rule " + r.Name
	case "vsphere.vm":
		var p struct {
			VM struct {
				Moref   string `json:"moref"`
				VCenter string `json:"vcenter"`
			} `json:"vm"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		var vc vsphere.Driver
		if vc, err = a.vc(ctx, p.VM.VCenter); err == nil {
			err = heartbeatWhile(ctx, func() error { return vc.Destroy(ctx, p.VM.Moref) })
		}
		what = "deleted VM " + r.Name
	case "dns.record":
		var p struct {
			FQDN  string `json:"fqdn"`
			Type  string `json:"record_type"`
			Value string `json:"value"`
			ID    string `json:"efficientip_id"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		err = a.IPAM.DeleteRecord(ctx, efficientip.Record{ID: p.ID, FQDN: p.FQDN, Type: p.Type, Value: p.Value})
		if p.Type == "A" {
			what = "removed DNS for " + p.FQDN
		}
	case "ipam.address":
		var p struct {
			Address  string `json:"address"`
			Subnet   string `json:"subnet"`
			ID       string `json:"efficientip_id"`
			Hostname string `json:"hostname"`
			FQDN     string `json:"fqdn"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		err = a.IPAM.Release(ctx, efficientip.Address{ID: p.ID, IP: p.Address, Subnet: p.Subnet, Hostname: cmp.Or(p.FQDN, p.Hostname)})
		what = "released " + p.Address
	}
	if err != nil {
		return "", err
	}
	return what, a.Store.DeleteResource(ctx, a.Store.Pool, r.ID)
}

// Rollback deletes what the request created: DRS rules, VMs, DNS records
// and IP reservations. It is safe to run again.
func (a *Activities) Rollback(ctx context.Context, requestID, projectID string) error {
	if projectID == "" {
		if r, err := a.Store.GetRequest(ctx, requestID); err == nil {
			projectID = r.ProjectID
		}
	}
	list, err := a.Store.ResourcesForRequest(ctx, requestID)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	var done []string
	order := map[string]int{"vsphere.drs_rule": 0, "vsphere.vm": 1, "dns.record": 2, "ipam.address": 3}
	for pass := 0; pass < 4; pass++ {
		for _, r := range list {
			if order[r.Type] != pass {
				continue
			}
			what, err := a.deleteResource(ctx, r)
			if err != nil {
				return fmt.Errorf("roll back %s %s: %w", r.Type, r.Name, err)
			}
			if what != "" {
				done = append(done, what)
			}
		}
	}
	// Add a step so the timeline shows what was undone.
	steps, _ := a.Store.ListSteps(ctx, requestID)
	pos := 0
	for _, s := range steps {
		if s.ParentID == nil && s.Position >= pos {
			pos = s.Position + 1
		}
	}
	now := time.Now().UTC()
	sys := "rvav"
	st := store.Step{ID: ids.New(ids.Step), RequestID: requestID, Position: pos, Key: "rollback", Title: "Rolled back",
		Detail: strings.Join(done, ", "), System: &sys, Status: "done", StartedAt: &now, FinishedAt: &now}
	if err := a.Store.InsertSteps(ctx, a.Store.Pool, []store.Step{st}); err != nil && !store.IsUniqueViolation(err) {
		return err
	}
	a.Events.Step(ctx, projectID, requestID, st)
	return nil
}

// checkAWXAction refuses to launch the job of an AWX day-2 action turned off,
// or gone from AWX, since it was asked for: it may have waited for approval.
func (a *Activities) checkAWXAction(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	act, err := a.Store.GetAWXAction(ctx, key)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return permanent("%s is no longer a day-2 action.", key)
	case err != nil:
		return err
	case act.Gone:
		return permanent("%s is no longer in AWX, or lost its label, since it was asked for.", act.Name)
	case !act.Enabled:
		return permanent("%s was turned off since it was asked for.", act.Name)
	}
	return nil
}
