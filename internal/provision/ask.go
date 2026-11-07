package provision

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// A composite step that waits for a person (ADR-0033): an approval row with
// its step, its form and the answer it got, waited for as approvals are.

// SignalAsk is the signal that a step's wait was answered: one name per wait,
// as steps of a group wait at the same time on their own channels.
func SignalAsk(approvalID string) string { return "rvav.ask." + approvalID }

const stoppedBySibling = "Another step of its group failed."

// AskInput is a step's question to open.
type AskInput struct {
	RequestID string        `json:"request_id"`
	ProjectID string        `json:"project_id"`
	StepKey   string        `json:"step_key"`
	Title     string        `json:"title"`
	Ask       blueprint.Ask `json:"ask"`
}

// AskState is an opened question and how it stands.
type AskState struct {
	ApprovalID string `json:"approval_id"`
	LevelState
}

// runAsk opens a step's question and waits for its answer. It says stopped
// when a sibling's failure ended the wait.
func runAsk(ctx, stop workflow.Context, info Info, c CompositeStep) (map[string]any, bool, error) {
	var a *Activities
	key := c.StepKey()
	var open AskState
	in := AskInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Title: c.Title, Ask: *c.Ask}
	if err := workflow.ExecuteActivity(ctx, a.OpenAsk, in).Get(ctx, &open); err != nil {
		return nil, false, err
	}
	signals := workflow.GetSignalChannel(ctx, SignalAsk(open.ApprovalID))
	st, stopped, err := awaitLevel(ctx, stop, signals, open.ApprovalID, open.LevelState)
	if err != nil {
		return nil, false, err
	}
	// Cancelled with the request, whose cancel is on its way.
	if st.Status == "cancelled" || ctx.Err() != nil {
		return nil, false, temporal.NewCanceledError()
	}
	if stopped {
		err := workflow.ExecuteActivity(ctx, a.StopAsk, info.RequestID, info.ProjectID, key, open.ApprovalID).Get(ctx, nil)
		return nil, true, err
	}
	var out map[string]any
	err = workflow.ExecuteActivity(ctx, a.AskOutcome, info.RequestID, info.ProjectID, key, open.ApprovalID).Get(ctx, &out)
	return out, false, err
}

// OpenAsk opens what a step asks and tells those it names, once. Called again
// (an activity retried, a workflow continued as new), it finds what it opened.
func (a *Activities) OpenAsk(ctx context.Context, in AskInput) (AskState, error) {
	r, err := a.Store.GetRequest(ctx, in.RequestID)
	if err != nil {
		return AskState{}, err
	}
	if r.Status == "cancelling" {
		return AskState{LevelState: LevelState{Status: "cancelled"}}, nil // nobody is to be asked
	}
	form, err := json.Marshal(in.Ask.Form)
	if err != nil {
		return AskState{}, err
	}
	wf := WorkflowID(r.ID)
	lv := store.Approval{ID: ids.New(ids.Approval), RequestID: r.ID, Name: in.Title, WorkflowID: &wf, StepKey: &in.StepKey,
		Message: in.Ask.Message, Form: form, ApproverIDs: askApprovers(in.Ask, r.RequesterID, r.OwnerID)}
	if in.Ask.Group != "" {
		lv.GroupName = &in.Ask.Group
	}
	if d, err := time.ParseDuration(in.Ask.Timeout); err == nil && d > 0 {
		secs := int(d / time.Second)
		lv.TimeoutSeconds = &secs
		action := askOnTimeout(in.Ask.OnTimeout)
		lv.OnTimeout = &action
	}
	if e := in.Ask.EscalateTo; e != nil {
		lv.EscalateIDs = e.Approvers
		if e.Group != "" {
			lv.EscalateGroup = &e.Group
		}
	}
	lv, opened, err := a.Store.OpenStepApproval(ctx, lv)
	if err != nil {
		return AskState{}, err
	}
	st := AskState{ApprovalID: lv.ID, LevelState: LevelState{Status: lv.Status, DueAt: lv.DueAt}}
	if lv.Status != "pending" {
		return st, nil
	}
	detail := "Waiting for a person"
	a.update(ctx, r.ID, r.ProjectID, in.StepKey, store.StepUpdate{Status: "running", Detail: &detail})
	recipients := a.approvers(ctx, lv, r.RequesterID)
	if opened {
		_ = a.Store.Audit(ctx, a.Store.Pool, store.AuditEvent{Action: "approval.opened", TargetType: "approval", TargetID: lv.ID,
			ProjectID: &r.ProjectID, Detail: map[string]any{"request": r.Number, "step": in.StepKey}})
	}
	// Also when a retry finds it open: the dedupe key makes a second
	// notification nothing, and a first one isn't lost.
	body := cmpOr(in.Ask.Message, r.Number+" · "+r.Title)
	for _, id := range recipients {
		a.Events.Notify(ctx, store.NewNotification{UserID: id, Kind: "approval_needed", Title: "Your answer is needed: " + in.Title,
			Body: body, Level: "warning", Link: "/requests/" + r.Number, RequestID: &r.ID, ApprovalID: askCardID(lv),
			DedupeKey: "approval_needed:" + lv.ID})
	}
	a.Events.Approval(ctx, append(recipients, r.RequesterID), lv.ID, r.ID, lv.Status)
	a.publishAll(ctx, r.ProjectID, r.ID)
	return st, nil
}

// askApprovers are the users a step names: never the requester, as for an
// approval, unless it asks the requester or owner by that name.
func askApprovers(ask blueprint.Ask, requesterID, ownerID string) []string {
	var named []string
	for _, id := range ask.Approvers {
		if id != requesterID {
			named = append(named, id)
		}
	}
	if ask.Requester {
		named = append(named, requesterID)
	}
	if ask.Owner {
		named = append(named, ownerID)
	}
	return dedupe(named)
}

// askOnTimeout is what a step's timeout does, as a level's on_timeout: fail
// (the default) rejects, continue approves, escalate escalates.
func askOnTimeout(action string) string {
	switch action {
	case "continue":
		return "approve"
	case "escalate":
		return "escalate"
	}
	return "reject"
}

// askCardID links a notification to its row when a Teams or Slack card can
// answer it: approve or reject. A form is answered on the request's page.
func askCardID(lv store.Approval) *string {
	var form []blueprint.AskField
	if err := json.Unmarshal(lv.Form, &form); err == nil && len(form) == 0 {
		return &lv.ID
	}
	return nil
}

// AskOutcome closes a step's wait: its outputs when it goes on, a permanent
// error (the step fails) when it was rejected or timed out with nothing to do.
func (a *Activities) AskOutcome(ctx context.Context, requestID, projectID, key, approvalID string) (map[string]any, error) {
	lv, err := a.Store.GetApproval(ctx, a.Store.Pool, approvalID)
	if err != nil {
		return nil, err
	}
	votes, err := a.Store.Votes(ctx, a.Store.Pool, []string{approvalID})
	if err != nil {
		return nil, err
	}
	var vote *store.ApprovalVote
	if list := votes[approvalID]; len(list) > 0 {
		vote = &list[len(list)-1]
	}
	outcome := ""
	if lv.Outcome != nil {
		outcome = *lv.Outcome
	}
	switch lv.Status {
	case "approved":
		a.update(ctx, requestID, projectID, key, store.StepUpdate{Detail: &outcome})
		return askOutputs(lv, vote)
	case "rejected":
		if vote != nil && vote.Comment != "" {
			outcome += ": " + vote.Comment
		}
		return nil, permanent("%s", outcome)
	}
	return nil, permanent("This step's wait is %s.", lv.Status)
}

// askOutputs are what later steps read of a wait (ADR-0033): answered, by,
// comment, and answer with every field of its form, as given, or its default
// when left blank or when the timeout went on, or else null.
func askOutputs(lv store.Approval, vote *store.ApprovalVote) (map[string]any, error) {
	var form []blueprint.AskField
	if err := json.Unmarshal(lv.Form, &form); err != nil {
		return nil, err
	}
	given := map[string]any{}
	if len(lv.Answer) > 0 {
		if err := json.Unmarshal(lv.Answer, &given); err != nil {
			return nil, err
		}
	}
	answer := map[string]any{}
	for _, f := range form {
		if v, ok := given[f.Key]; ok && v != nil {
			answer[f.Key] = v
		} else {
			answer[f.Key] = f.Default
		}
	}
	out := map[string]any{"answered": vote != nil, "by": "", "comment": "", "answer": answer}
	if vote != nil {
		out["by"], out["comment"] = vote.UserEmail, vote.Comment
	}
	return out, nil
}

// StopAsk ends a step's wait because a sibling failed: a Retry asks again.
func (a *Activities) StopAsk(ctx context.Context, requestID, projectID, key, approvalID string) error {
	if err := a.Store.StopStepApproval(ctx, approvalID); err != nil {
		return err
	}
	detail := stoppedBySibling
	a.update(ctx, requestID, projectID, key, store.StepUpdate{Status: "cancelled", Detail: &detail})
	lv, err := a.Store.GetApproval(ctx, a.Store.Pool, approvalID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	r, err := a.Store.GetRequest(ctx, requestID)
	if err != nil {
		return err
	}
	a.Events.Approval(ctx, append(a.approvers(ctx, lv, r.RequesterID), r.RequesterID), lv.ID, r.ID, lv.Status)
	return nil
}

func cmpOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// capitalize starts a text with a capital.
func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}
