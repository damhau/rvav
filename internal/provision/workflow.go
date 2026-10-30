package provision

import (
	"errors"
	"slices"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
)

// SignalApproval tells a waiting workflow that an approval level was decided.
const SignalApproval = "approval"

// ApprovalSignal names the level and how it ended: approved or rejected.
type ApprovalSignal struct {
	ApprovalID string `json:"approval_id"`
	Outcome    string `json:"outcome"`
}

// activityOptions: external calls are retried with backoff; errors marked
// Permanent (bad configuration, full subnet, failed AWX job) are not.
var activityOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 30 * time.Minute,
	HeartbeatTimeout:    2 * time.Minute,
	RetryPolicy: &temporal.RetryPolicy{
		InitialInterval:        2 * time.Second,
		BackoffCoefficient:     2,
		MaximumInterval:        time.Minute,
		MaximumAttempts:        4,
		NonRetryableErrorTypes: []string{"Permanent", "AWXJobFailed"},
	},
}

// jobCtx gives an AWX job up to six hours: the activity heartbeats while it
// follows the job, so a worker that dies is still noticed in minutes.
func jobCtx(ctx workflow.Context) workflow.Context {
	o := activityOptions
	o.StartToCloseTimeout = 6 * time.Hour
	return workflow.WithActivityOptions(ctx, o)
}

// Provision runs one request: approval, then the plan, then the outcome.
func Provision(ctx workflow.Context, requestID string) error {
	ctx = workflow.WithActivityOptions(ctx, activityOptions)
	var a *Activities

	info := Info{RequestID: requestID}
	if err := workflow.ExecuteActivity(ctx, a.LoadPlan, requestID).Get(ctx, &info); err != nil {
		if temporal.IsCanceledError(err) {
			return finishCancelled(ctx, info, false)
		}
		return err
	}
	switch info.Status {
	case "pending_approval", "scheduled", "queued", "running":
	default:
		return nil // cancelled before the workflow started
	}

	if info.Plan.ApprovalRequired && info.Status == "pending_approval" {
		approved, cancelled, err := awaitApprovals(ctx, info)
		if errors.Is(err, errContinueAsNew) {
			// A long wait: start over with an empty history. Decisions are in
			// the database, so the new run picks up where this one was.
			return workflow.NewContinueAsNewError(ctx, Provision, requestID)
		}
		if cancelled || temporal.IsCanceledError(err) {
			return finishCancelled(ctx, info, false)
		}
		if err != nil || !approved {
			return err
		}
		// Approvers may have changed inputs: provision what the request says now.
		if err := workflow.ExecuteActivity(ctx, a.LoadPlan, requestID).Get(ctx, &info); err != nil {
			if temporal.IsCanceledError(err) {
				return finishCancelled(ctx, info, false)
			}
			return err
		}
	}

	if info.ScheduledAt != nil {
		if wait := info.ScheduledAt.Sub(workflow.Now(ctx)); wait > 0 {
			if err := workflow.Sleep(ctx, wait); err != nil {
				return finishCancelled(ctx, info, false)
			}
		}
	}

	var running bool
	if err := workflow.ExecuteActivity(ctx, a.MarkRunning, requestID).Get(ctx, &running); err != nil {
		if temporal.IsCanceledError(err) {
			return finishCancelled(ctx, info, false)
		}
		return err
	}
	if !running {
		return nil
	}

	// Blocking subscriptions (ADR-0021) may stop the request before it
	// builds anything; versioned for workflows that started without them.
	if workflow.GetVersion(ctx, "blocking-subscriptions", workflow.DefaultVersion, 1) == 1 && !info.done(KeyChecks) {
		stepKey, reason, err := runChecks(ctx, info)
		switch {
		case temporal.IsCanceledError(err) || errors.Is(ctx.Err(), workflow.ErrCanceled):
			return finishCancelledUntouched(ctx, info)
		case err != nil:
			reason = errMessage(err)
			fallthrough
		case reason != "":
			return workflow.ExecuteActivity(ctx, a.Finish, FinishInput{RequestID: requestID, ProjectID: info.ProjectID,
				Outcome: "failed", StepKey: stepKey, Message: reason, Untouched: true}).Get(ctx, nil)
		}
	}

	failedStep, err := runPlan(ctx, info)
	switch {
	case err == nil:
		return workflow.ExecuteActivity(ctx, a.Finish, FinishInput{RequestID: requestID, ProjectID: info.ProjectID, Outcome: "succeeded"}).Get(ctx, nil)
	case temporal.IsCanceledError(err) || errors.Is(ctx.Err(), workflow.ErrCanceled):
		return finishCancelled(ctx, info, true)
	default:
		if info.Plan.Rollback {
			if rbErr := workflow.ExecuteActivity(ctx, a.Rollback, requestID, info.ProjectID).Get(ctx, nil); rbErr != nil {
				workflow.GetLogger(ctx).Error("rollback after failure", "err", rbErr)
			}
		}
		return workflow.ExecuteActivity(ctx, a.Finish, FinishInput{RequestID: requestID, ProjectID: info.ProjectID,
			Outcome: "failed", StepKey: failedStep, Message: errMessage(err)}).Get(ctx, nil)
	}
}

// approvalRecheck is how often a waiting level is read again from the
// database, in case a decision signal was lost. Each check adds to the
// workflow history, so it stays rare; continue-as-new keeps long waits small.
const approvalRecheck = time.Hour

var errContinueAsNew = errors.New("continue as new")

// awaitApprovals runs the approval levels in order. Each waits for its
// decision signal or its timeout; the database holds the decisions, and
// FinishApprovals records the outcome on the request.
func awaitApprovals(ctx workflow.Context, info Info) (approved, cancelled bool, err error) {
	var a *Activities
	signals := workflow.GetSignalChannel(ctx, SignalApproval)
	for _, id := range info.Approvals {
		var st LevelState
		if err := workflow.ExecuteActivity(ctx, a.OpenApproval, id).Get(ctx, &st); err != nil {
			return false, false, err
		}
		for st.Status == "pending" {
			tctx, cancelTimer := workflow.WithCancel(ctx)
			wait, due, fired := approvalRecheck, false, false
			if st.DueAt != nil {
				if d := st.DueAt.Sub(workflow.Now(ctx)); d <= wait {
					wait, due = max(d, time.Millisecond), true
				}
			}
			sel := workflow.NewSelector(ctx)
			sel.AddReceive(signals, func(c workflow.ReceiveChannel, _ bool) {
				var sig ApprovalSignal
				c.Receive(ctx, &sig)
				if sig.ApprovalID == id {
					st.Status = sig.Outcome
				}
			})
			sel.AddReceive(ctx.Done(), func(workflow.ReceiveChannel, bool) { cancelled = true })
			sel.AddFuture(workflow.NewTimer(tctx, wait), func(workflow.Future) { fired = true })
			sel.Select(ctx)
			cancelTimer()
			if cancelled {
				return false, true, nil
			}
			if fired {
				next := a.ApprovalState
				if due {
					next = a.ApprovalTimedOut
				}
				if err := workflow.ExecuteActivity(ctx, next, id).Get(ctx, &st); err != nil {
					return false, false, err
				}
			}
			if st.Status == "pending" && workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
				return false, false, errContinueAsNew
			}
		}
		if st.Status != "approved" {
			break
		}
	}
	err = workflow.ExecuteActivity(ctx, a.FinishApprovals, info.RequestID).Get(ctx, &approved)
	return approved, false, err
}

// RollbackWorkflow deletes what a failed request created, then records
// the outcome on its deployments.
func RollbackWorkflow(ctx workflow.Context, requestID string) error {
	ctx = workflow.WithActivityOptions(ctx, activityOptions)
	var a *Activities
	msg := ""
	if err := workflow.ExecuteActivity(ctx, a.Rollback, requestID, "").Get(ctx, nil); err != nil {
		msg = errMessage(err)
	}
	return workflow.ExecuteActivity(ctx, a.RolledBack, requestID, msg).Get(ctx, nil)
}

// finishCancelled rolls back (when something may have been created) and
// records the cancellation, outside the cancelled context.
func finishCancelled(ctx workflow.Context, info Info, rollback bool) error {
	var a *Activities
	dctx, _ := workflow.NewDisconnectedContext(ctx)
	dctx = workflow.WithActivityOptions(dctx, activityOptions)
	if rollback {
		if err := workflow.ExecuteActivity(dctx, a.Rollback, info.RequestID, info.ProjectID).Get(dctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("rollback after cancel", "err", err)
		}
	}
	return workflow.ExecuteActivity(dctx, a.Finish, FinishInput{RequestID: info.RequestID, ProjectID: info.ProjectID, Outcome: "cancelled"}).Get(dctx, nil)
}

// finishCancelledUntouched ends a request cancelled before it built or
// changed anything: a day-2 deployment keeps its status.
func finishCancelledUntouched(ctx workflow.Context, info Info) error {
	var a *Activities
	dctx, _ := workflow.NewDisconnectedContext(ctx)
	dctx = workflow.WithActivityOptions(dctx, activityOptions)
	return workflow.ExecuteActivity(dctx, a.Finish, FinishInput{RequestID: info.RequestID, ProjectID: info.ProjectID,
		Outcome: "cancelled", Untouched: true}).Get(dctx, nil)
}

// runChecks asks the blocking subscriptions that apply, in priority order:
// the first that stops the request ends the checks, with the step and why.
func runChecks(ctx workflow.Context, info Info) (stepKey, reason string, err error) {
	var a *Activities
	var subs []BlockingSub
	if err := workflow.ExecuteActivity(ctx, a.BlockingSubscriptions, info.RequestID).Get(ctx, &subs); err != nil {
		return KeyChecks, "", err
	}
	if len(subs) == 0 {
		return "", "", nil
	}
	for _, b := range subs {
		// Its own timeout, twice (a fast failure is tried once more); not
		// retried by Temporal, which would ask the target again.
		o := activityOptions
		o.StartToCloseTimeout = 2*time.Duration(b.Timeout)*time.Second + 30*time.Second
		o.HeartbeatTimeout = 0
		o.RetryPolicy = &temporal.RetryPolicy{MaximumAttempts: 1}
		var out CheckOutcome
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, o), a.RunBlockingSubscription, info.RequestID, b).Get(ctx, &out); err != nil {
			return checkKey(b.ID), "", err
		}
		if out.Stop {
			return out.StepKey, out.Reason, nil
		}
	}
	return "", "", workflow.ExecuteActivity(ctx, a.FinishChecks, info.RequestID, info.ProjectID).Get(ctx, nil)
}

// runPlan executes the steps and returns the key of the step that failed.
func runPlan(ctx workflow.Context, info Info) (string, error) {
	var a *Activities
	p := info.Plan

	if p.Day2 != nil {
		for i, op := range p.Day2.Ops {
			// An inventory refresh done before a job that failed is run
			// again on a retry: the job must see the inventory as it is now.
			refreshAgain := op.System == day2.SystemAWX && op.Op == day2.OpRefreshInventory &&
				slices.ContainsFunc(p.Day2.Ops[i+1:], func(o day2.Op) bool { return !info.done(o.Key) })
			if info.done(op.Key) && !refreshAgain {
				continue
			}
			var err error
			switch {
			case op.Op == day2.OpWorkflow:
				// A custom workflow offered as an action (ADR-0022).
				inputs, _ := op.Args["inputs"].(map[string]any)
				call := WorkflowCall{Key: argStr(op.Args, "key"), Title: argStr(op.Args, "title"), Type: argStr(op.Args, "type"),
					TaskQueue: argStr(op.Args, "task_queue"), Timeout: argInt(op.Args, "timeout_seconds"), Inputs: inputs}
				_, err = runCustom(ctx, info, op.Key, call, nil)
			case op.System == day2.SystemAWX && op.Op == day2.OpRefreshInventory:
				err = workflow.ExecuteActivity(ctx, a.RefreshInventory, info.RequestID, info.ProjectID).Get(ctx, nil)
			case op.System == day2.SystemAWX && op.Op == day2.OpJob:
				// An AWX action (ADR-0018): the job on rvav's inventory, its
				// tasks and output followed as for post-configuration.
				vars, _ := op.Args["extra_vars"].(map[string]any)
				extra := map[string]any{}
				for k, v := range vars {
					extra[k] = v
				}
				extra["rvav"] = op.Args["rvav"] // the inputs never replace rvav's context
				workflowJob, _ := op.Args["workflow"].(bool)
				job := Job{Key: op.Key, Title: op.Title, Template: argStr(op.Args, "template"),
					TemplateID: argInt(op.Args, "template_id"), Workflow: workflowJob}
				in := JobInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: op.Key, Job: job,
					Limit: argStr(op.Args, "limit"), ExtraVars: extra, Action: argStr(op.Args, "action")}
				err = workflow.ExecuteActivity(jobCtx(ctx), a.RunJob, in).Get(ctx, nil)
			default:
				in := OpInput{RequestID: info.RequestID, ProjectID: info.ProjectID, DeploymentID: p.Day2.DeploymentID,
					ResourceID: p.Day2.ResourceID, Moref: p.Day2.Moref, Op: op}
				err = workflow.ExecuteActivity(ctx, a.RunOp, in).Get(ctx, nil)
			}
			if err != nil {
				return op.Key, err
			}
		}
		return "", nil
	}

	if len(p.Composite) > 0 {
		return runComposite(ctx, info)
	}

	if p.Kind == blueprint.KindWorkflow && p.Workflow != nil {
		key := KeyWorkflow + p.Workflow.Key
		if info.done(key) {
			return "", nil
		}
		_, err := runCustom(ctx, info, key, *p.Workflow, nil)
		return key, err
	}

	if p.Kind == blueprint.KindAWX && p.AWX != nil {
		key := KeyJob + p.AWX.Key
		if info.done(key) {
			return "", nil
		}
		vars := map[string]any{}
		for k, v := range p.ExtraVars {
			vars[k] = v
		}
		vars["rvav"] = p.Context
		in := JobInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Job: *p.AWX, ExtraVars: vars}
		return key, workflow.ExecuteActivity(jobCtx(ctx), a.RunJob, in).Get(ctx, nil)
	}

	if key, err := makeSegments(ctx, info, p.Segments, p.VMs, p.SegmentKey); err != nil {
		return key, err
	}
	if key, err := makeSecurity(ctx, info, p.Security, p.SecurityKey); err != nil {
		return key, err
	}

	if len(p.Components) > 0 {
		return runComponents(ctx, info)
	}

	vms, failed, err := buildVMs(ctx, info, p.VMs, p.Guest, p.VMKey)
	if err != nil {
		return failed, err
	}
	return postConfig(ctx, info, vms, p.PostConfig, KeyInventory, func(j Job) string { return KeyJob + j.Key }, p.Limit(), p.Context)
}

// makeSegments makes the NSX segments of the deployments of these VMs
// (ADR-0028), or finds what an earlier run made, and gives their VMs the
// segments' subnets: the plan's, or a composite VM step's. It runs again on
// a retry: it is how the VMs get their subnet.
func makeSegments(ctx workflow.Context, info Info, segments []SegmentUnit, vms []VMUnit, keyOf func(SegmentUnit) string) (string, error) {
	var a *Activities
	for _, sg := range segments {
		key := keyOf(sg)
		in := SegmentInput{RequestID: info.RequestID, ProjectID: info.ProjectID, Key: key, Segment: sg}
		// The vCenters of its VMs, by any adapter: its port group in each.
		if in.VCenters = segmentVCenters(sg, vms); len(in.VCenters) > 0 {
			in.VCenter = in.VCenters[0]
		}
		var res SegmentResult
		if err := workflow.ExecuteActivity(ctx, a.MakeSegment, in).Get(ctx, &res); err != nil {
			return key, err
		}
		for i, vm := range vms {
			if vm.DeploymentID != sg.DeploymentID {
				continue
			}
			if vm.Segment == sg.Key {
				vms[i].Subnet, vms[i].SubnetID, vms[i].Gateway, vms[i].PrefixLength = res.CIDR, res.SubnetID, res.Gateway, res.PrefixLength
			}
			// Its added adapters on the segment.
			for j, n := range vm.NICs {
				if n.Segment == sg.Key {
					vms[i].NICs[j].Subnet, vms[i].NICs[j].SubnetID, vms[i].NICs[j].PrefixLength = res.CIDR, res.SubnetID, res.PrefixLength
				}
			}
		}
	}
	return "", nil
}

// makeSecurity makes the NSX groups and firewall rules of the plan's
// deployments (ADR-0028), before their VMs. It runs again on a retry, as
// making segments does: a rollback that stopped halfway may have deleted
// them.
func makeSecurity(ctx workflow.Context, info Info, units []SecurityUnit, keyOf func(SecurityUnit) string) (string, error) {
	var a *Activities
	for _, u := range units {
		key := keyOf(u)
		in := SecurityInput{RequestID: info.RequestID, ProjectID: info.ProjectID, Key: key, Security: u}
		if err := workflow.ExecuteActivity(ctx, a.MakeSecurity, in).Get(ctx, nil); err != nil {
			return key, err
		}
	}
	return "", nil
}

// joinGroups tags a VM so that it is in its NSX groups, if it joins any:
// again when it was built again.
func joinGroups(ctx workflow.Context, info Info, in VMInput, key string, again bool) (string, error) {
	var a *Activities
	if len(in.VM.NSXGroups) == 0 || info.done(key) && !again {
		return "", nil
	}
	in.Key = key
	return key, workflow.ExecuteActivity(ctx, a.JoinGroups, in).Get(ctx, nil)
}

// segmentVCenters are the vCenters of the VMs on a segment, by any of their
// adapters, each once, in the VMs' order.
func segmentVCenters(sg SegmentUnit, vms []VMUnit) []string {
	var out []string
	for _, vm := range vms {
		on := vm.Segment == sg.Key || slices.ContainsFunc(vm.NICs, func(n NICUnit) bool { return n.Segment == sg.Key })
		if vm.DeploymentID == sg.DeploymentID && on && !slices.Contains(out, vm.VCenter) {
			out = append(out, vm.VCenter)
		}
	}
	return out
}

// builtVM is a VM a request built.
type builtVM struct {
	vm    VMUnit
	ip    string
	moref string
}

// buildVMs reserves an address, creates DNS records, clones, customises and
// powers on each VM, under the step keys key gives, and returns what it built.
func buildVMs(ctx workflow.Context, info Info, list []VMUnit, guest blueprint.Guest, key func(string, VMUnit) string) ([]builtVM, string, error) {
	var a *Activities
	var vms []builtVM
	for _, vm := range list {
		in := VMInput{RequestID: info.RequestID, ProjectID: info.ProjectID, VM: vm}
		in.Guest = guest

		// Steps done before a retry are skipped, with what they built. When
		// that is gone (deleted since), it is built again, and so is what
		// depends on it: DNS on the address, customization and power on the VM.
		before := info.Built[vm.DeploymentID]
		in.Key = key(KeyIPAM, vm)
		newIP := !info.done(in.Key) || before.IP == ""
		in.IP = before.IP
		if newIP {
			if err := workflow.ExecuteActivity(ctx, a.ReserveIP, in).Get(ctx, &in.IP); err != nil {
				return nil, in.Key, err
			}
		}
		newVM := !info.done(key(KeyClone, vm)) || before.Moref == ""
		in.Key = key(KeyDNS, vm)
		if !info.done(in.Key) || newIP || newVM {
			if err := workflow.ExecuteActivity(ctx, a.CreateDNS, in).Get(ctx, nil); err != nil {
				return nil, in.Key, err
			}
		}
		in.Key = key(KeyClone, vm)
		in.Moref = before.Moref
		if newVM {
			if err := workflow.ExecuteActivity(ctx, a.CloneVM, in).Get(ctx, &in.Moref); err != nil {
				return nil, in.Key, err
			}
		}
		for _, step := range []struct {
			key string
			fn  any
		}{{KeyCustomize, a.Customize}, {KeyPowerOn, a.PowerOn}} {
			in.Key = key(step.key, vm)
			if info.done(in.Key) && !newVM {
				continue
			}
			if err := workflow.ExecuteActivity(ctx, step.fn, in).Get(ctx, nil); err != nil {
				return nil, in.Key, err
			}
		}
		if failed, err := joinGroups(ctx, info, in, key(KeyNSXTags, vm), newVM); err != nil {
			return nil, failed, err
		}
		vms = append(vms, builtVM{vm: vm, ip: in.IP, moref: in.Moref})
	}
	return vms, "", nil
}

// postConfig refreshes the AWX inventory, then runs the post-configuration
// jobs on the VMs built, under the step keys given.
func postConfig(ctx workflow.Context, info Info, vms []builtVM, jobs []Job, inventoryKey string, jobKey func(Job) string,
	limit string, context map[string]any) (string, error) {
	var a *Activities
	if len(jobs) == 0 {
		return "", nil
	}
	// Refreshed again on a retry, before the jobs left to run.
	pending := slices.ContainsFunc(jobs, func(j Job) bool { return !info.done(jobKey(j)) })
	if !info.done(inventoryKey) || pending {
		var err error
		if inventoryKey == KeyInventory {
			err = workflow.ExecuteActivity(ctx, a.RefreshInventory, info.RequestID, info.ProjectID).Get(ctx, nil)
		} else {
			err = workflow.ExecuteActivity(ctx, a.RefreshInventoryStep, info.RequestID, info.ProjectID, inventoryKey).Get(ctx, nil)
		}
		if err != nil {
			return inventoryKey, err
		}
	}
	resources := make([]map[string]any, len(vms))
	for i, b := range vms {
		resources[i] = map[string]any{"name": b.vm.Name, "ip": b.ip, "fqdn": b.vm.FQDN, "moref": b.moref,
			"deployment_id": b.vm.DeploymentID}
	}
	rvav := map[string]any{"resources": resources}
	for k, v := range context {
		rvav[k] = v
	}
	for _, job := range jobs {
		key := jobKey(job)
		if info.done(key) {
			continue
		}
		in := JobInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Job: job,
			Limit: limit, ExtraVars: map[string]any{"rvav": rvav}}
		if err := workflow.ExecuteActivity(jobCtx(ctx), a.RunJob, in).Get(ctx, nil); err != nil {
			return key, err
		}
	}
	return "", nil
}
