package provision

import (
	"context"
	"errors"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
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

// Rollback starts the rollback of a failed request, under the request's
// workflow ID (its earlier run has ended).
// Signal sends a signal to any workflow by its ID.
// Signal sends a signal; a workflow that has ended needs it no more.
func (t *Temporal) Signal(ctx context.Context, workflowID, name string, arg any) error {
	err := t.Client.SignalWorkflow(ctx, workflowID, "", name, arg)
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return nil
	}
	return err
}

func (t *Temporal) Rollback(ctx context.Context, requestID string) error {
	_, err := t.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        WorkflowID(requestID),
		TaskQueue: t.TaskQueue,
	}, RollbackWorkflow, requestID)
	return err
}

// NewWorker registers the workflow and activities on the task queue.
func NewWorker(c client.Client, taskQueue string, a *Activities) worker.Worker {
	// The client's tracing interceptor (app.Temporal) applies to the
	// worker too: traces go on into workflows and activities (S14.5).
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(Provision)
	w.RegisterWorkflow(Housekeeping)
	w.RegisterWorkflow(RollbackWorkflow)
	w.RegisterActivity(a)
	RegisterContract(w, a)
	return w
}
