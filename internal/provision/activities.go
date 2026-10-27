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

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/store"
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
	Events             *events.Publisher
	Log                *slog.Logger
	// PollInterval is how often AWX jobs and inventory updates are polled.
	PollInterval time.Duration
	// IPTimeout bounds the wait for VMware Tools to report the guest IP.
	IPTimeout time.Duration
	// ShutdownTimeout bounds the wait for a guest OS to shut down.
	ShutdownTimeout time.Duration
	// Housekeeper applies retention and lease policies (the core service).
	Housekeeper Housekeeper
	// Now is the housekeeping clock (time.Now when nil); tests move it on.
	Now func() time.Time
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
	Guest     struct {
		OSFamily string `json:"os_family"`
		Domain   string `json:"domain"`
		TimeZone string `json:"time_zone"`
	} `json:"guest"`
	IP    string `json:"ip,omitempty"`
	Moref string `json:"moref,omitempty"`
}

type JobInput struct {
	RequestID string         `json:"request_id"`
	ProjectID string         `json:"project_id"`
	StepKey   string         `json:"step_key"`
	Job       Job            `json:"job"`
	Limit     string         `json:"limit"`
	ExtraVars map[string]any `json:"extra_vars"`
}

type FinishInput struct {
	RequestID string `json:"request_id"`
	ProjectID string `json:"project_id"`
	Outcome   string `json:"outcome"` // succeeded, failed, cancelled
	StepKey   string `json:"step_key,omitempty"`
	Message   string `json:"message,omitempty"`
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
	title, detail, err := fn()
	if err != nil {
		msg := errMessage(err)
		a.update(ctx, requestID, projectID, key, store.StepUpdate{Status: "failed", Error: &msg})
		return err
	}
	u := store.StepUpdate{Status: "done"}
	if title != "" {
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
		if s.ParentID == nil && s.Status == "done" && s.Key != KeySubmit && s.Key != KeyPolicy {
			info.Done = append(info.Done, s.Key)
		}
	}
	if len(info.Done) > 0 {
		resources, err := a.Store.ResourcesForRequest(ctx, r.ID)
		if err != nil {
			return info, err
		}
		for _, res := range resources {
			var p struct {
				Address string `json:"address"`
				VM      struct {
					Moref string `json:"moref"`
				} `json:"vm"`
			}
			_ = json.Unmarshal(res.Properties, &p)
			if info.Built == nil {
				info.Built = map[string]Built{}
			}
			b := info.Built[res.DeploymentID]
			switch res.Type {
			case "ipam.address":
				b.IP = p.Address
			case "vsphere.vm":
				b.Moref = p.VM.Moref
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
	if err := a.Store.SetRequestStatus(ctx, a.Store.Pool, requestID, "running"); err != nil {
		return false, err
	}
	if r.DeploymentID != nil {
		var plan Plan
		_ = json.Unmarshal(r.Plan, &plan)
		during := "updating"
		if plan.Day2 != nil && plan.Day2.During != "" {
			during = plan.Day2.During
		}
		if err := a.Store.SetDeploymentStatus(ctx, a.Store.Pool, *r.DeploymentID, during); err != nil {
			return false, err
		}
		a.Events.Deployment(ctx, *r.DeploymentID)
	}
	a.Events.Request(ctx, requestID)
	return true, nil
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

func (a *Activities) ReserveIP(ctx context.Context, in VMInput) (string, error) {
	var ip string
	err := a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		if r := a.findResource(ctx, in.RequestID, in.VM.DeploymentID, "ipam.address", ""); r != nil {
			var p struct {
				Address string `json:"address"`
			}
			_ = json.Unmarshal(r.Properties, &p)
			ip = p.Address
			return "Reserved " + ip, "Subnet " + in.VM.Subnet, nil
		}
		addr, err := a.IPAM.Reserve(ctx, a.subnet(in.VM), cmp.Or(in.VM.FQDN, in.VM.Name))
		if errors.Is(err, efficientip.ErrSubnetFull) {
			return "", "", permanent("Subnet %s has no free address", in.VM.Subnet)
		}
		if err != nil {
			return "", "", fmt.Errorf("EfficientIP: %w", err)
		}
		ip = addr.IP
		ext := addr.ID
		if ext == "" {
			ext = "ip:" + addr.IP
		}
		props, _ := json.Marshal(map[string]any{"address": addr.IP, "subnet": in.VM.Subnet, "efficientip_id": addr.ID,
			"hostname": in.VM.Name, "fqdn": in.VM.FQDN})
		res, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: in.VM.DeploymentID,
			RequestID: &in.RequestID, Type: "ipam.address", Name: addr.IP, ExternalID: &ext, Properties: props})
		if err != nil {
			return "", "", err
		}
		a.Events.Resource(ctx, in.ProjectID, res)
		return "Reserved " + addr.IP, "Subnet " + in.VM.Subnet, nil
	})
	return ip, err
}

func (a *Activities) CreateDNS(ctx context.Context, in VMInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		for _, rec := range []efficientip.Record{
			{FQDN: in.VM.FQDN, Type: "A", Value: in.IP, Zone: in.VM.DNSZone},
			{FQDN: efficientip.PTRName(in.IP), Type: "PTR", Value: in.VM.FQDN, Zone: "in-addr.arpa"},
		} {
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
		return "Created A and PTR records", in.VM.FQDN + " → " + in.IP, nil
	})
}

func (a *Activities) recordVM(ctx context.Context, in VMInput, vm *vsphere.VM) (store.Resource, error) {
	props := vmprops.Build(a.VSphere.Name(), *vm, in.VM.FQDN, in.VM.Template, nil, 0)
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
		var vm *vsphere.VM
		err := heartbeatWhile(ctx, func() error {
			var err error
			vm, err = a.VSphere.CloneVM(ctx, vsphere.VMSpec{
				Name: in.VM.Name, Template: in.VM.Template, Cluster: in.VM.Cluster, Datastore: in.VM.Datastore,
				Network: in.VM.Network, Folder: in.VM.Folder, VCPU: in.VM.VCPU, MemoryMB: in.VM.MemoryGB * 1024,
				DiskGB: in.VM.DiskGB, Annotation: "Managed by rvav. Deployment " + in.VM.DeploymentID,
				ExtraConfig: map[string]string{"guestinfo.rvav.deployment": in.VM.DeploymentID, "guestinfo.rvav.request": in.RequestID},
			})
			return err
		})
		if errors.Is(err, vsphere.ErrNotFound) {
			return "", "", permanent("%v", err)
		}
		if err != nil {
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		moref = vm.Moref
		if _, err := a.recordVM(ctx, in, vm); err != nil {
			return "", "", err
		}
		return "Cloned from " + in.VM.Template, fmt.Sprintf("%s · %s · %s", in.VM.Cluster, in.VM.Datastore, vm.Moref), nil
	})
	return moref, err
}

func (a *Activities) Customize(ctx context.Context, in VMInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		err := a.VSphere.Customize(ctx, in.Moref, vsphere.Customization{
			OSFamily: in.Guest.OSFamily, Hostname: in.VM.Name, Domain: in.Guest.Domain, TimeZone: in.Guest.TimeZone,
			IP: in.IP, PrefixLength: in.VM.PrefixLength, Gateway: in.VM.Gateway, DNSServers: in.VM.DNSServers,
		})
		if err != nil {
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		return "Applied guest customization", fmt.Sprintf("Hostname %s, static IP %s/%d", in.VM.Name, in.IP, in.VM.PrefixLength), nil
	})
}

func (a *Activities) PowerOn(ctx context.Context, in VMInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		if err := a.VSphere.PowerOn(ctx, in.Moref); err != nil {
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		timeout := a.IPTimeout
		if timeout == 0 {
			timeout = 10 * time.Minute
		}
		wctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var ip string
		err := heartbeatWhile(ctx, func() error {
			var err error
			ip, err = a.VSphere.WaitForIP(wctx, in.Moref)
			return err
		})
		if err != nil {
			if wctx.Err() != nil && ctx.Err() == nil {
				return "", "", permanent("VMware Tools did not report an IP address within %s. Check the console of %s.", timeout, in.VM.Name)
			}
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		if vm, err := a.VSphere.GetVM(ctx, in.Moref); err == nil {
			_, _ = a.recordVM(ctx, in, vm)
		}
		detail := "VMware Tools reports " + ip
		if ip != in.IP {
			detail += " (expected " + in.IP + ")"
		}
		return "Powered on, guest IP " + ip, detail, nil
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
	return a.run(ctx, requestID, projectID, KeyInventory, func() (string, string, error) {
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
		_, _ = a.Store.UpdateStep(ctx, a.Store.Pool, requestID, KeyInventory, store.StepUpdate{ExternalKind: &kind, ExternalID: &ext, ExternalURL: &j.URL})
		return "Refreshed AWX inventory", fmt.Sprintf("Inventory update %d", id), nil
	})
}

// RunJob launches an AWX job template (or resumes following one already
// launched by an earlier attempt), mirrors each task as a child step and
// copies the output to the step log.
func (a *Activities) RunJob(ctx context.Context, in JobInput) error {
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "running"})
	err := a.runJob(ctx, in)
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
	if jobID == 0 {
		tplID, err := a.AWX.LookupJobTemplate(ctx, in.Job.Template, in.Job.Workflow)
		if err != nil {
			return permanent("AWX: %v", err)
		}
		job, err := a.AWX.Launch(ctx, tplID, in.Job.Workflow, awx.LaunchRequest{Limit: in.Limit, ExtraVars: in.ExtraVars})
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
	resumed := activity.HasHeartbeatDetails(ctx)
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
			if n.JobID == 0 || !awx.Done(n.Status) || slices.Contains(logged, n.JobID) {
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

		job, err := a.AWX.GetJob(ctx, jobID, true)
		if err != nil {
			return fmt.Errorf("AWX: %w", err)
		}
		if job.Finished {
			if job.Status == "successful" {
				return nil
			}
			if failedMsg == "" {
				failedMsg = fmt.Sprintf("AWX workflow job %d ended %s", jobID, job.Status)
			}
			return temporal.NewNonRetryableApplicationError(failedMsg, "AWXJobFailed", nil)
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
	db := a.Store.Pool
	switch in.Outcome {
	case "succeeded":
		title := "Ready"
		if r.ItemType != "vm_blueprint" || r.Kind == "day2" {
			title = "Done"
		}
		if _, err := a.Store.UpdateStep(ctx, db, in.RequestID, KeyReady, store.StepUpdate{Status: "done", Title: &title}); err != nil {
			return err
		}
		if err := a.Store.SetDeploymentsStatusForRequest(ctx, db, in.RequestID, "ready"); err != nil {
			return err
		}
		if err := a.Store.SetRequestStatus(ctx, db, in.RequestID, "succeeded"); err != nil {
			return err
		}
		a.Events.Notify(ctx, r.OwnerID, title+": "+r.Title, "", "success", "/requests/"+r.Number)
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
		a.Events.Notify(ctx, r.OwnerID, "Failed: "+r.Title, in.Message, "error", "/requests/"+r.Number)
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
		// still runs. This request's own outcome is recorded first, so of two
		// finishing together at least one sees the other done.
		var plan Plan
		_ = json.Unmarshal(r.Plan, &plan)
		status := day2Status(plan, in.Outcome, r.StartedAt != nil)
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
	a.publishAll(ctx, r.ProjectID, in.RequestID)
	return nil
}

// deleteResource deletes a VM, DNS record or IP reservation where it lives,
// then its row, and says what it did ("" for PTR records). Deleting
// something already gone is not an error.
func (a *Activities) deleteResource(ctx context.Context, r store.Resource) (string, error) {
	var err error
	what := ""
	switch r.Type {
	case "vsphere.vm":
		var p struct {
			VM struct {
				Moref string `json:"moref"`
			} `json:"vm"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		err = heartbeatWhile(ctx, func() error { return a.VSphere.Destroy(ctx, p.VM.Moref) })
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
			Address string `json:"address"`
			Subnet  string `json:"subnet"`
			ID      string `json:"efficientip_id"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		err = a.IPAM.Release(ctx, efficientip.Address{ID: p.ID, IP: p.Address, Subnet: p.Subnet})
		what = "released " + p.Address
	}
	if err != nil {
		return "", err
	}
	return what, a.Store.DeleteResource(ctx, a.Store.Pool, r.ID)
}

// Rollback deletes what the request created: VMs, DNS records and IP
// reservations. It is safe to run again.
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
	order := map[string]int{"vsphere.vm": 0, "dns.record": 1, "ipam.address": 2}
	for pass := 0; pass < 3; pass++ {
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
