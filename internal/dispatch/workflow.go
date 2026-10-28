package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/damhau/rvav/internal/store"
)

// Workflow starts a registered custom workflow (ADR-0022) with the event
// in its context; the run is delivered when the workflow completes.
type Workflow struct {
	Client client.Client
	Store  *store.Store
	// TaskQueue is rvav's: the workflow calls the contract's activities on it.
	TaskQueue string
}

func (w *Workflow) Deliver(ctx context.Context, d Delivery) Result {
	var t struct {
		Workflow *struct {
			Key string `json:"key"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(d.Sub.Target, &t); err != nil || t.Workflow == nil {
		return Result{Err: "not a custom workflow"}
	}
	reg, err := w.Store.GetWorkflowRegistration(ctx, t.Workflow.Key)
	if err != nil || !reg.Enabled {
		return Result{Err: "The custom workflow " + t.Workflow.Key + " is not registered or is disabled."}
	}
	var ev map[string]any
	_ = json.Unmarshal(d.Event, &ev)
	data, _ := ev["data"].(map[string]any)
	rctx := map[string]any{"contract": "rvav.v1", "rvav_task_queue": w.TaskQueue, "steps": map[string]any{}, "event": ev}
	for _, k := range []string{"request", "item", "project", "environment", "production", "requester", "owner",
		"deployment", "deployments", "resources", "inputs"} {
		rctx[k] = data[k]
	}
	// A blocking run reports its steps under its check in the request's timeline.
	var requestID, stepKey *string
	if req, ok := data["request"].(map[string]any); ok {
		if id, ok := req["id"].(string); ok && id != "req_sample" {
			requestID = &id
			if d.Blocking {
				key := "check:" + d.Sub.ID
				stepKey = &key
			}
		}
	}
	id := "rvav-subscription/" + d.RunID
	if err := w.Store.StartWorkflowRun(ctx, id, reg.Key, requestID, stepKey); err != nil {
		return Result{Err: err.Error()}
	}
	timeout := time.Duration(d.Sub.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// A retried run finds the workflow its first attempt started.
	run, err := w.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: reg.TaskQueue,
		WorkflowExecutionTimeout: time.Duration(reg.TimeoutSeconds) * time.Second,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING}, reg.WorkflowType, rctx)
	if err != nil {
		return Result{WorkflowID: id, Err: "Temporal refused to start it: " + err.Error()}
	}
	var result map[string]any
	err = run.Get(ctx, &result)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return Result{WorkflowID: id, Err: fmt.Sprintf("The workflow is still running after %s.", timeout)}
	case err != nil:
		msg := err.Error()
		var app *temporal.ApplicationError
		if errors.As(err, &app) {
			msg = app.Error()
		}
		_ = w.Store.FinishWorkflowRun(ctx, id, "failed", nil, &msg)
		return Result{WorkflowID: id, Err: "The workflow failed: " + msg}
	}
	b, _ := json.Marshal(result)
	_ = w.Store.FinishWorkflowRun(ctx, id, "succeeded", b, nil)
	res := Result{OK: true, WorkflowID: id, Response: string(b)}
	if d.Blocking {
		return answer(res)
	}
	return res
}
