// Package rvavflow helps write custom Temporal workflows for rvav
// (docs/adr/0022-custom-workflows-and-composites.md): it decodes the context
// rvav starts a workflow with, and wraps the contract's activities, which a
// workflow calls on rvav's task queue.
//
//	func Decommission(ctx workflow.Context, rc rvavflow.Context) (map[string]any, error) {
//		if err := rvavflow.Step(ctx, rc, "cmdb", "Retire the CMDB entry", func() error { ... }); err != nil {
//			return nil, err
//		}
//		d, err := rvavflow.RequestApproval(ctx, rc, rvavflow.Approval{Title: "Delete the data", Group: "rvav-infra-leads"})
//		...
//	}
//
// Register the workflow with its type, which ends with its major version
// (decommission.v1), and run a worker on the task queue registered in rvav.
package rvavflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Contract is the version of rvav's side these helpers speak.
const Contract = "rvav.v1"

// Signal is the signal rvav sends when an approval is decided.
const Signal = "rvav.approval"

// Context is what rvav starts a workflow with. Fields are only added
// within rvav.v1: decode it leniently.
type Context struct {
	Contract    string         `json:"contract"`
	TaskQueue   string         `json:"rvav_task_queue"`
	Request     *Request       `json:"request"`
	Item        *Item          `json:"item"`
	Project     *Project       `json:"project"`
	Environment string         `json:"environment"`
	Production  bool           `json:"production"`
	Requester   *User          `json:"requester"`
	Owner       *User          `json:"owner"`
	Deployment  *Deployment    `json:"deployment"`
	Deployments []Deployment   `json:"deployments"`
	Resources   []Resource     `json:"resources"`
	Inputs      map[string]any `json:"inputs"`
	// Steps are the outputs of a composite item's earlier steps.
	Steps map[string]any `json:"steps"`
	// Event is the event of a subscription (rvav.event.v1), else nil.
	Event map[string]any `json:"event"`
}

type Request struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
}

type Item struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Version string `json:"version"`
}

type Project struct {
	ID         string `json:"id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	CostCenter string `json:"cost_center"`
}

type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Deployment struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Properties map[string]string `json:"properties"`
}

type Resource struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	FQDN         string `json:"fqdn"`
	IP           string `json:"ip"`
	Moref        string `json:"moref"`
	DeploymentID string `json:"deployment_id"`
}

// Step statuses.
const (
	Running = "running"
	Done    = "done"
	Failed  = "failed"
	Skipped = "skipped"
)

func (rc Context) activity(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:           rc.TaskQueue,
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumInterval: time.Minute},
	})
}

// ReportStep adds or updates a step of the workflow, shown under its step
// in the request's timeline. detail is why when it failed.
func ReportStep(ctx workflow.Context, rc Context, key, title, status, detail string) error {
	in := map[string]any{"key": key, "title": title, "status": status, "detail": detail}
	return workflow.ExecuteActivity(rc.activity(ctx), "rvav.ReportStep", in).Get(ctx, nil)
}

// AppendLog adds lines to a step's log: the workflow's own step when
// stepKey is empty.
func AppendLog(ctx workflow.Context, rc Context, stepKey string, lines ...string) error {
	in := map[string]any{"step_key": stepKey, "lines": lines}
	return workflow.ExecuteActivity(rc.activity(ctx), "rvav.AppendLog", in).Get(ctx, nil)
}

// Step runs fn as a step: running, then done, or failed with fn's error.
func Step(ctx workflow.Context, rc Context, key, title string, fn func() error) error {
	if err := ReportStep(ctx, rc, key, title, Running, ""); err != nil {
		return err
	}
	if err := fn(); err != nil {
		_ = ReportStep(ctx, rc, key, title, Failed, err.Error())
		return err
	}
	return ReportStep(ctx, rc, key, title, Done, "")
}

// Approval asks people to approve, in rvav's inbox.
type Approval struct {
	Title     string
	Reason    string
	Group     string
	Approvers []string
	Mode      string // any (default) or all
	// Wait, when set, is how long to wait for a decision: after it the
	// decision is TimedOut, and the approval stays open in rvav.
	Wait time.Duration
}

// Decision is how an approval ended.
type Decision struct {
	ApprovalID string
	Approved   bool
	TimedOut   bool
	Comment    string
}

type signal struct {
	ApprovalID string `json:"approval_id"`
	Outcome    string `json:"outcome"`
	Comment    string `json:"comment"`
}

// RequestApproval opens an approval on the request and waits for its
// decision.
func RequestApproval(ctx workflow.Context, rc Context, a Approval) (Decision, error) {
	in := map[string]any{"title": a.Title, "reason": a.Reason, "group": a.Group, "approvers": a.Approvers, "mode": a.Mode}
	var out struct {
		ApprovalID string `json:"approval_id"`
	}
	if err := workflow.ExecuteActivity(rc.activity(ctx), "rvav.RequestApproval", in).Get(ctx, &out); err != nil {
		return Decision{}, err
	}
	d := Decision{ApprovalID: out.ApprovalID}
	ch := workflow.GetSignalChannel(ctx, Signal)
	var deadline workflow.Future
	tctx, cancel := workflow.WithCancel(ctx)
	defer cancel()
	if a.Wait > 0 {
		deadline = workflow.NewTimer(tctx, a.Wait)
	}
	for {
		sel := workflow.NewSelector(ctx)
		var got signal
		received := false
		sel.AddReceive(ch, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, &got)
			received = true
		})
		if deadline != nil {
			sel.AddFuture(deadline, func(workflow.Future) { d.TimedOut = true })
		}
		sel.Select(ctx)
		if d.TimedOut {
			return d, nil
		}
		if received && got.ApprovalID == out.ApprovalID {
			d.Approved, d.Comment = got.Outcome == "approved", got.Comment
			return d, nil
		}
	}
}
