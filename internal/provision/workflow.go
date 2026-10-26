package provision

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
)

// SignalDecision carries an approval decision to a waiting workflow.
const SignalDecision = "decision"

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
		var d Decision
		cancelled := false
		sel := workflow.NewSelector(ctx)
		sel.AddReceive(workflow.GetSignalChannel(ctx, SignalDecision), func(c workflow.ReceiveChannel, _ bool) { c.Receive(ctx, &d) })
		sel.AddReceive(ctx.Done(), func(workflow.ReceiveChannel, bool) { cancelled = true })
		sel.Select(ctx)
		if cancelled {
			return finishCancelled(ctx, info, false)
		}
		if err := workflow.ExecuteActivity(ctx, a.RecordDecision, requestID, d).Get(ctx, nil); err != nil {
			if temporal.IsCanceledError(err) {
				return finishCancelled(ctx, info, false)
			}
			return err
		}
		if !d.Approved {
			return nil
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
