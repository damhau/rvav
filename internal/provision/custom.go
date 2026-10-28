package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/store"
)

// Custom workflows (ADR-0022): rvav starts them as child workflows of the
// request's, gives them a context, and serves the contract's activities.

// ContractVersion is rvav's side of the custom workflow contract.
const ContractVersion = "rvav.v1"

// SignalWorkflowApproval tells a custom workflow an approval it asked for
// was decided.
const SignalWorkflowApproval = "rvav.approval"

// WorkflowApprovalSignal is the decision.
type WorkflowApprovalSignal struct {
	ApprovalID string `json:"approval_id"`
	Outcome    string `json:"outcome"` // approved or rejected
	Comment    string `json:"comment,omitempty"`
}

// KeyWorkflow prefixes the step of a custom workflow run.
const KeyWorkflow = "workflow:"

// WorkflowCall is a registered custom workflow a request runs.
type WorkflowCall struct {
	Key       string         `json:"key"`
	Title     string         `json:"title"`
	Type      string         `json:"type"`
	TaskQueue string         `json:"task_queue"`
	Timeout   int            `json:"timeout_seconds"`
	Inputs    map[string]any `json:"inputs,omitempty"`
}

// StartRunInput starts a run of a custom workflow as a request step.
type StartRunInput struct {
	RequestID  string         `json:"request_id"`
	ProjectID  string         `json:"project_id"`
	StepKey    string         `json:"step_key"`
	WorkflowID string         `json:"workflow_id"`
	Call       WorkflowCall   `json:"call"`
	Steps      map[string]any `json:"steps,omitempty"`
}

// FinishRunInput records how a run ended.
type FinishRunInput struct {
	RequestID  string         `json:"request_id"`
	ProjectID  string         `json:"project_id"`
	StepKey    string         `json:"step_key"`
	WorkflowID string         `json:"workflow_id"`
	Result     map[string]any `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	Cancelled  bool           `json:"cancelled,omitempty"`
}

// runCustom runs a custom workflow as a child of the request's workflow
// and returns its result.
func runCustom(ctx workflow.Context, info Info, stepKey string, call WorkflowCall, steps map[string]any) (map[string]any, error) {
	var a *Activities
	childID := workflow.GetInfo(ctx).WorkflowExecution.ID + "/" + stepKey
	in := StartRunInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: stepKey, WorkflowID: childID, Call: call, Steps: steps}
	var rctx map[string]any
	if err := workflow.ExecuteActivity(ctx, a.StartWorkflowRun, in).Get(ctx, &rctx); err != nil {
		return nil, err
	}
	cctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: childID, TaskQueue: call.TaskQueue,
		WorkflowExecutionTimeout: time.Duration(call.Timeout) * time.Second,
		// Cancelling the request cancels it, and waits for it to clean up.
		ParentClosePolicy:   enumspb.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		WaitForCancellation: true,
	})
	var result map[string]any
	err := workflow.ExecuteChildWorkflow(cctx, call.Type, rctx).Get(ctx, &result)
	fin := FinishRunInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: stepKey, WorkflowID: childID, Result: result}
	switch {
	case temporal.IsCanceledError(err):
		fin.Cancelled = true
	case err != nil:
		fin.Error = childError(err)
	}
	dctx, _ := workflow.NewDisconnectedContext(ctx)
	if ferr := workflow.ExecuteActivity(workflow.WithActivityOptions(dctx, activityOptions), a.FinishWorkflowRun, fin).Get(dctx, nil); ferr != nil && err == nil {
		return nil, ferr
	}
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(fin.Error, "WorkflowFailed", err)
	}
	return result, nil
}

// childError is a failed child workflow's own message.
func childError(err error) string {
	var app *temporal.ApplicationError
	if errors.As(err, &app) {
		return app.Error()
	}
	var timeout *temporal.TimeoutError
	if errors.As(err, &timeout) {
		return "The workflow didn't finish in time."
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, "): "); i >= 0 {
		msg = msg[i+3:]
	}
	return msg
}

// StartWorkflowRun records the run, sets its step running and returns the
// workflow's context.
func (a *Activities) StartWorkflowRun(ctx context.Context, in StartRunInput) (map[string]any, error) {
	reg, err := a.Store.GetWorkflowRegistration(ctx, in.Call.Key)
	if errors.Is(err, store.ErrNotFound) || err == nil && !reg.Enabled {
		return nil, permanent("the custom workflow %s is not registered or is disabled", in.Call.Key)
	} else if err != nil {
		return nil, err
	}
	data, err := outbox.RequestData(ctx, a.Store.Pool, in.RequestID)
	if err != nil {
		return nil, err
	}
	rctx := map[string]any{"contract": ContractVersion, "rvav_task_queue": a.TaskQueue}
	for _, k := range []string{"request", "item", "project", "environment", "production", "requester", "owner", "deployment",
		"deployments", "resources"} {
		rctx[k] = data[k]
	}
	rctx["inputs"] = in.Call.Inputs
	if rctx["inputs"] == nil {
		rctx["inputs"] = data["inputs"]
	}
	rctx["steps"] = in.Steps
	if rctx["steps"] == nil {
		rctx["steps"] = map[string]any{}
	}
	rctx["event"] = nil
	if err := a.Store.StartWorkflowRun(ctx, in.WorkflowID, reg.Key, &in.RequestID, &in.StepKey); err != nil {
		return nil, err
	}
	detail := reg.WorkflowType + " on " + reg.TaskQueue
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "running", Detail: &detail,
		ExternalKind: ptr("temporal_workflow"), ExternalID: &in.WorkflowID})
	return rctx, nil
}

// FinishWorkflowRun records how a run ended and closes its step.
func (a *Activities) FinishWorkflowRun(ctx context.Context, in FinishRunInput) error {
	status, step := "succeeded", store.StepUpdate{Status: "done"}
	switch {
	case in.Cancelled:
		status, step.Status = "cancelled", "cancelled"
	case in.Error != "":
		status, step.Status, step.Error = "failed", "failed", &in.Error
	}
	var result json.RawMessage
	if in.Result != nil {
		result, _ = json.Marshal(in.Result)
	}
	var errText *string
	if in.Error != "" {
		errText = &in.Error
	}
	if err := a.Store.FinishWorkflowRun(ctx, in.WorkflowID, status, result, errText); err != nil {
		return err
	}
	_ = a.Store.FinishOpenChildren(ctx, a.Store.Pool, in.RequestID, in.StepKey, map[bool]string{true: "done", false: "cancelled"}[status == "succeeded"])
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, step)
	return nil
}

func ptr[T any](v T) *T { return &v }

// ── The contract's activities, registered under their contract names ──

// Contract serves rvav.ReportStep, rvav.AppendLog and rvav.RequestApproval.
type Contract struct{ A *Activities }

// Registry is a Temporal worker or test environment.
type Registry interface {
	RegisterActivityWithOptions(a interface{}, options activity.RegisterOptions)
}

// RegisterContract registers the contract's activities.
func RegisterContract(r Registry, a *Activities) {
	c := &Contract{A: a}
	r.RegisterActivityWithOptions(c.ReportStep, activity.RegisterOptions{Name: "rvav.ReportStep"})
	r.RegisterActivityWithOptions(c.AppendLog, activity.RegisterOptions{Name: "rvav.AppendLog"})
	r.RegisterActivityWithOptions(c.RequestApproval, activity.RegisterOptions{Name: "rvav.RequestApproval"})
}

// caller is the run of the workflow calling a contract activity: rvav only
// answers workflows it started.
func (c *Contract) caller(ctx context.Context) (store.WorkflowRun, error) {
	id := activity.GetInfo(ctx).WorkflowExecution.ID
	run, err := c.A.Store.GetWorkflowRun(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return run, temporal.NewNonRetryableApplicationError("rvav didn't start the workflow "+id, "UnknownWorkflow", nil)
	}
	return run, err
}

// ReportStepInput is a step of the workflow's own.
type ReportStepInput struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Status string `json:"status"` // running, done, failed, skipped
	Detail string `json:"detail,omitempty"`
}

// ReportStep adds or updates a step under the workflow's step.
func (c *Contract) ReportStep(ctx context.Context, in ReportStepInput) error {
	run, err := c.caller(ctx)
	if err != nil {
		return err
	}
	switch in.Status {
	case "running", "done", "failed", "skipped":
	default:
		return temporal.NewNonRetryableApplicationError("a step's status is running, done, failed or skipped, not "+in.Status, "BadInput", nil)
	}
	if in.Key == "" || run.RequestID == nil || run.StepKey == nil {
		if in.Key == "" {
			return temporal.NewNonRetryableApplicationError("a step needs a key", "BadInput", nil)
		}
		return nil // a run outside a request (a subscription's) has no timeline
	}
	var errText *string
	if in.Status == "failed" && in.Detail != "" {
		errText = &in.Detail
	}
	key := *run.StepKey + "/" + in.Key
	in2 := JobInput{RequestID: *run.RequestID, ProjectID: deref(run.ProjectID), StepKey: *run.StepKey}
	c.A.child(ctx, in2, key, in.Title, in.Status, errText)
	if in.Detail != "" && in.Status != "failed" {
		if st, err := c.A.Store.UpdateStep(ctx, c.A.Store.Pool, *run.RequestID, key, store.StepUpdate{Detail: &in.Detail}); err == nil {
			c.A.Events.Step(ctx, deref(run.ProjectID), *run.RequestID, st)
		}
	}
	return nil
}

// AppendLogInput is lines for a step's log.
type AppendLogInput struct {
	StepKey string   `json:"step_key,omitempty"`
	Lines   []string `json:"lines"`
}

// AppendLog adds lines to the log of the workflow's step, or of one of its
// own steps.
func (c *Contract) AppendLog(ctx context.Context, in AppendLogInput) error {
	run, err := c.caller(ctx)
	if err != nil || run.RequestID == nil || run.StepKey == nil {
		return err
	}
	key := *run.StepKey
	if in.StepKey != "" {
		key += "/" + in.StepKey
	}
	st, err := c.A.Store.GetStepByKey(ctx, *run.RequestID, key)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("no step "+in.StepKey, "BadInput", nil)
	}
	lines := make([]store.LogLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = store.LogLine{Text: l, Kind: "plain"}
	}
	next, err := c.A.Store.AppendLogLines(ctx, c.A.Store.Pool, st.ID, lines)
	if err != nil {
		return err
	}
	hasLog := true
	if !st.HasLog {
		_, _ = c.A.Store.UpdateStep(ctx, c.A.Store.Pool, *run.RequestID, key, store.StepUpdate{HasLog: &hasLog})
	}
	c.A.Events.Log(ctx, deref(run.ProjectID), *run.RequestID, st.ID, next)
	return nil
}

// RequestApprovalInput asks people to approve.
type RequestApprovalInput struct {
	Title     string   `json:"title"`
	Reason    string   `json:"reason,omitempty"`
	Group     string   `json:"group,omitempty"`
	Approvers []string `json:"approvers,omitempty"`
	Mode      string   `json:"mode,omitempty"` // any (default) or all
}

// RequestApprovalOutput names the approval: its decision comes as the
// rvav.approval signal.
type RequestApprovalOutput struct {
	ApprovalID string `json:"approval_id"`
}

// RequestApproval opens an approval on the request, in the same inbox.
func (c *Contract) RequestApproval(ctx context.Context, in RequestApprovalInput) (RequestApprovalOutput, error) {
	run, err := c.caller(ctx)
	if err != nil {
		return RequestApprovalOutput{}, err
	}
	if run.RequestID == nil {
		return RequestApprovalOutput{}, temporal.NewNonRetryableApplicationError("only workflows of a request can ask for approval", "BadInput", nil)
	}
	if in.Group == "" && len(in.Approvers) == 0 {
		return RequestApprovalOutput{}, temporal.NewNonRetryableApplicationError("an approval needs a group or approvers", "BadInput", nil)
	}
	mode := in.Mode
	if mode == "" {
		mode = "any"
	}
	if mode != "any" && mode != "all" {
		return RequestApprovalOutput{}, temporal.NewNonRetryableApplicationError("mode is any or all", "BadInput", nil)
	}
	title := in.Title
	if title == "" {
		title = "Approval"
	}
	// The same activity retried gives the same approval.
	info := activity.GetInfo(ctx)
	sum := sha256.Sum256([]byte(*run.RequestID + "|" + info.WorkflowExecution.ID + "|" + info.WorkflowExecution.RunID + "|" + info.ActivityID))
	id := "apr_" + hex.EncodeToString(sum[:13])
	if _, err := c.A.Store.GetApproval(ctx, c.A.Store.Pool, id); err == nil {
		return RequestApprovalOutput{ApprovalID: id}, nil
	}
	lv := store.Approval{ID: id, RequestID: *run.RequestID, Name: title, ApproverIDs: in.Approvers, Mode: mode,
		Reasons: []string{in.Reason}, WorkflowID: &run.WorkflowID}
	if in.Reason == "" {
		lv.Reasons = nil
	}
	if in.Group != "" {
		lv.GroupName = &in.Group
	}
	if err := c.A.Store.InsertWorkflowApproval(ctx, lv); err != nil {
		return RequestApprovalOutput{}, err
	}
	r, err := c.A.Store.GetRequest(ctx, *run.RequestID)
	if err != nil {
		return RequestApprovalOutput{}, err
	}
	stored, err := c.A.Store.GetApproval(ctx, c.A.Store.Pool, id)
	if err != nil {
		return RequestApprovalOutput{}, err
	}
	_ = c.A.Store.Audit(ctx, c.A.Store.Pool, store.AuditEvent{Action: "approval.opened", TargetType: "approval", TargetID: id,
		ProjectID: &r.ProjectID, Detail: map[string]any{"request": r.Number, "level": title, "workflow": run.WorkflowID}})
	recipients := c.A.approvers(ctx, stored, r.RequesterID)
	for _, uid := range recipients {
		c.A.Events.Notify(ctx, uid, "Approval needed: "+r.Title, in.Reason, "warning", "/approvals/"+id)
	}
	c.A.Events.Approval(ctx, append(recipients, r.RequesterID), id, r.ID, "pending")
	return RequestApprovalOutput{ApprovalID: id}, nil
}
