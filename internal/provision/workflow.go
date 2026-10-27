package provision

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
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
// database, in case a decision signal was lost.
const approvalRecheck = 5 * time.Minute

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
		}
		if st.Status != "approved" {
			break
		}
	}
	err = workflow.ExecuteActivity(ctx, a.FinishApprovals, info.RequestID).Get(ctx, &approved)
	return approved, false, err
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

// runPlan executes the steps and returns the key of the step that failed.
func runPlan(ctx workflow.Context, info Info) (string, error) {
	var a *Activities
	p := info.Plan

	if p.Kind == blueprint.KindAWX && p.AWX != nil {
		key := KeyJob + p.AWX.Key
		vars := map[string]any{}
		for k, v := range p.ExtraVars {
			vars[k] = v
		}
		vars["rvav"] = p.Context
		in := JobInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Job: *p.AWX, ExtraVars: vars}
		return key, workflow.ExecuteActivity(ctx, a.RunJob, in).Get(ctx, nil)
	}

	type built struct {
		vm    VMUnit
		ip    string
		moref string
	}
	var vms []built
	for _, vm := range p.VMs {
		in := VMInput{RequestID: info.RequestID, ProjectID: info.ProjectID, VM: vm}
		in.Guest.OSFamily, in.Guest.Domain, in.Guest.TimeZone = p.Guest.OSFamily, p.Guest.Domain, p.Guest.TimeZone

		in.Key = p.VMKey(KeyIPAM, vm)
		if err := workflow.ExecuteActivity(ctx, a.ReserveIP, in).Get(ctx, &in.IP); err != nil {
			return in.Key, err
		}
		in.Key = p.VMKey(KeyDNS, vm)
		if err := workflow.ExecuteActivity(ctx, a.CreateDNS, in).Get(ctx, nil); err != nil {
			return in.Key, err
		}
		in.Key = p.VMKey(KeyClone, vm)
		if err := workflow.ExecuteActivity(ctx, a.CloneVM, in).Get(ctx, &in.Moref); err != nil {
			return in.Key, err
		}
		in.Key = p.VMKey(KeyCustomize, vm)
		if err := workflow.ExecuteActivity(ctx, a.Customize, in).Get(ctx, nil); err != nil {
			return in.Key, err
		}
		in.Key = p.VMKey(KeyPowerOn, vm)
		if err := workflow.ExecuteActivity(ctx, a.PowerOn, in).Get(ctx, nil); err != nil {
			return in.Key, err
		}
		vms = append(vms, built{vm: vm, ip: in.IP, moref: in.Moref})
	}

	if len(p.PostConfig) > 0 {
		if err := workflow.ExecuteActivity(ctx, a.RefreshInventory, info.RequestID, info.ProjectID).Get(ctx, nil); err != nil {
			return KeyInventory, err
		}
		resources := make([]map[string]any, len(vms))
		for i, b := range vms {
			resources[i] = map[string]any{"name": b.vm.Name, "ip": b.ip, "fqdn": b.vm.FQDN, "moref": b.moref,
				"deployment_id": b.vm.DeploymentID}
		}
		rvav := map[string]any{"resources": resources}
		for k, v := range p.Context {
			rvav[k] = v
		}
		for _, job := range p.PostConfig {
			key := KeyJob + job.Key
			in := JobInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Job: job,
				Limit: p.Limit(), ExtraVars: map[string]any{"rvav": rvav}}
			if err := workflow.ExecuteActivity(ctx, a.RunJob, in).Get(ctx, nil); err != nil {
				return key, err
			}
		}
	}
	return "", nil
}
