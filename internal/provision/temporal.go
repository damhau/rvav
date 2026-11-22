package provision

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
)

// Temporal starts and controls request workflows. It implements core.Workflows.
type Temporal struct {
	Client    client.Client
	TaskQueue string
}

func (t *Temporal) Start(ctx context.Context, requestID string) error {
	_, err := t.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        WorkflowID(requestID),
		TaskQueue: t.TaskQueue,
	}, Provision, requestID)
	var started *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &started) {
		return nil
	}
	return err
}

func (t *Temporal) Cancel(ctx context.Context, requestID string) error {
	err := t.Client.CancelWorkflow(ctx, WorkflowID(requestID), "")
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return nil
	}
	return err
}

// Approval tells the request's workflow that an approval level was decided.
func (t *Temporal) Approval(ctx context.Context, requestID, approvalID, outcome string) error {
	return t.Client.SignalWorkflow(ctx, WorkflowID(requestID), "", SignalApproval, ApprovalSignal{ApprovalID: approvalID, Outcome: outcome})
}

// Signal sends a signal; a workflow that has ended needs it no more.
func (t *Temporal) Signal(ctx context.Context, workflowID, name string, arg any) error {
	err := t.Client.SignalWorkflow(ctx, workflowID, "", name, arg)
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return nil
	}
	return err
}

// StartOnboarding starts a run of an onboarding plan.
func (t *Temporal) StartOnboarding(ctx context.Context, runID string) error {
	_, err := t.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        OnboardingWorkflowID(runID),
		TaskQueue: t.TaskQueue,
	}, OnboardingRun, runID)
	var started *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &started) {
		return nil
	}
	return err
}

// Restart runs a failed request's workflow again, under the request's
// workflow ID.
func (t *Temporal) Restart(ctx context.Context, requestID string) error {
	return t.replace(ctx, requestID, Provision)
}

// Rollback starts the rollback of a failed request, under the request's
// workflow ID.
func (t *Temporal) Rollback(ctx context.Context, requestID string) error {
	return t.replace(ctx, requestID, RollbackWorkflow)
}

// replace starts a new run for a failed request, ending its last run if
// that is still open. The request reads failed once that run's last
// activity commits, a moment before the run closes; by default, a start in
// between returns the old run and runs nothing (#180). Retry and roll back
// take the failed request under its lock first, so one start per failure
// gets here.
//
// A start that errs, such as one that timed out, may still have started
// the run: it is nil when the request's run is this start's, which its
// memo tells, so the caller doesn't undo what the run is doing.
func (t *Temporal) replace(ctx context.Context, requestID string, run any) error {
	start := uuid.NewString()
	_, err := t.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       WorkflowID(requestID),
		TaskQueue:                t.TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_TERMINATE_EXISTING,
		Memo:                     map[string]any{memoStart: start},
	}, run, requestID)
	if err == nil || t.startedBy(ctx, requestID, start) {
		return nil
	}
	return err
}

// memoStart is the memo field that names the start of a run.
const memoStart = "rvav_start"

func (t *Temporal) startedBy(ctx context.Context, requestID, start string) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	desc, err := t.Client.DescribeWorkflowExecution(ctx, WorkflowID(requestID), "")
	if err != nil {
		return false
	}
	p := desc.GetWorkflowExecutionInfo().GetMemo().GetFields()[memoStart]
	var got string
	return p != nil && converter.GetDefaultDataConverter().FromPayload(p, &got) == nil && got == start
}

// NewWorker registers the workflow and activities on the task queue.
func NewWorker(c client.Client, taskQueue string, a *Activities) worker.Worker {
	return NewDrainingWorker(c, taskQueue, a, 0)
}

// NewDrainingWorker is a worker whose Stop lets running activities finish
// for up to stopTimeout (ADR-0030): those still running then are cancelled
// and tried again by the next worker.
func NewDrainingWorker(c client.Client, taskQueue string, a *Activities, stopTimeout time.Duration) worker.Worker {
	// The client's tracing interceptor (app.Temporal) applies to the
	// worker too: traces go on into workflows and activities (S14.5).
	w := worker.New(c, taskQueue, worker.Options{WorkerStopTimeout: stopTimeout})
	w.RegisterWorkflow(Provision)
	w.RegisterWorkflow(Housekeeping)
	w.RegisterWorkflow(RollbackWorkflow)
	w.RegisterWorkflow(OnboardingRun)
	w.RegisterWorkflow(ScheduledRun)
	w.RegisterActivity(a)
	RegisterContract(w, a)
	return w
}
