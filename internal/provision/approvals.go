package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/store"
)

// Approval activities (ADR-0011). The API records decisions in the database
// and signals the workflow; these open levels, apply timeouts and record the
// outcome on the request.

// OpenApproval makes a level pending, starts its timeout and tells its
// approvers. A level decided meanwhile is returned with its outcome.
func (a *Activities) OpenApproval(ctx context.Context, approvalID string) (LevelState, error) {
	lv, opened, err := a.Store.OpenApproval(ctx, a.Store.Pool, approvalID)
	if err != nil {
		return LevelState{}, err
	}
	if !opened {
		return LevelState{Status: lv.Status, DueAt: lv.DueAt}, nil
	}
	r, err := a.Store.GetRequest(ctx, lv.RequestID)
	if err != nil {
		return LevelState{}, err
	}
	levels, err := a.Store.ApprovalsForRequest(ctx, r.ID)
	if err != nil {
		return LevelState{}, err
	}
	detail := lv.Name
	if len(levels) > 1 {
		detail = fmt.Sprintf("%s · level %d of %d", lv.Name, lv.Seq, len(levels))
	}
	detail += " · " + strings.Join(lv.Reasons, "; ")
	title := "Waiting for approval"
	if _, err := a.Store.UpdateStep(ctx, a.Store.Pool, r.ID, KeyPolicy, store.StepUpdate{Status: "running", Title: &title, Detail: &detail}); err != nil {
		return LevelState{}, err
	}
	_ = a.Store.Audit(ctx, a.Store.Pool, store.AuditEvent{Action: "approval.opened", TargetType: "approval", TargetID: lv.ID,
		ProjectID: &r.ProjectID, Detail: map[string]any{"request": r.Number, "level": lv.Name}})
	recipients := a.approvers(ctx, lv, r.RequesterID)
	for _, id := range recipients {
		a.Events.Notify(ctx, id, "Approval needed: "+r.Title, strings.Join(lv.Reasons, "; "), "warning", "/approvals/"+lv.ID)
	}
	a.Events.Approval(ctx, recipients, lv.ID, r.ID, lv.Status)
	a.publishAll(ctx, r.ProjectID, r.ID)
	return LevelState{Status: lv.Status, DueAt: lv.DueAt}, nil
}

// ApprovalState reads a level again, in case its decision signal was lost.
func (a *Activities) ApprovalState(ctx context.Context, approvalID string) (LevelState, error) {
	lv, err := a.Store.GetApproval(ctx, a.Store.Pool, approvalID)
	if err != nil {
		return LevelState{}, err
	}
	return LevelState{Status: lv.Status, DueAt: lv.DueAt}, nil
}

// ApprovalTimedOut applies a level's on_timeout: approve, reject, or ask the
// escalation approvers for one more timeout and then reject.
func (a *Activities) ApprovalTimedOut(ctx context.Context, approvalID string) (LevelState, error) {
	var st LevelState
	var lv store.Approval
	var notify []string
	err := a.Store.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		if lv, err = a.Store.LockApproval(ctx, tx, approvalID); err != nil {
			return err
		}
		if lv.Status != "pending" {
			st = LevelState{Status: lv.Status} // decided just before the timer fired
			return nil
		}
		after := "no answer"
		if lv.TimeoutSeconds != nil {
			after = "no answer within " + humanDuration(time.Duration(*lv.TimeoutSeconds)*time.Second)
		}
		action := "reject"
		if lv.OnTimeout != nil {
			action = *lv.OnTimeout
		}
		switch {
		case action == "approve":
			st.Status = "approved"
			_, err = a.Store.FinishApproval(ctx, tx, lv.ID, "approved", "Approved automatically: "+after)
		case action == "escalate" && !lv.Escalated:
			if lv, err = a.Store.EscalateApproval(ctx, tx, lv.ID); err != nil {
				return err
			}
			st = LevelState{Status: "pending", DueAt: lv.DueAt}
			notify = lv.EscalateIDs
			if lv.EscalateGroup != nil {
				members, err := a.Store.GroupMembers(ctx, []string{*lv.EscalateGroup})
				if err != nil {
					return err
				}
				notify = append(notify, members...)
			}
		case action == "escalate":
			st.Status = "rejected"
			_, err = a.Store.FinishApproval(ctx, tx, lv.ID, "rejected", "Rejected automatically: "+after+", even after escalation")
		default:
			st.Status = "rejected"
			_, err = a.Store.FinishApproval(ctx, tx, lv.ID, "rejected", "Rejected automatically: "+after)
		}
		if err != nil {
			return err
		}
		auditAction := "approval.timed_out"
		if st.Status == "pending" {
			auditAction = "approval.escalated"
		}
		return a.Store.Audit(ctx, tx, store.AuditEvent{Action: auditAction, TargetType: "approval", TargetID: lv.ID,
			Detail: map[string]any{"on_timeout": action, "status": st.Status}})
	})
	if err != nil {
		return LevelState{}, err
	}
	r, err := a.Store.GetRequest(ctx, lv.RequestID)
	if err != nil {
		return st, err
	}
	for _, id := range dedupe(notify) {
		if id != r.RequesterID {
			a.Events.Notify(ctx, id, "Approval escalated to you: "+r.Title, "No decision from "+lv.Name+" in time.", "warning", "/approvals/"+lv.ID)
		}
	}
	a.Events.Approval(ctx, a.approvers(ctx, lv, r.RequesterID), lv.ID, r.ID, st.Status)
	return st, nil
}

// FinishApprovals records how the approval ended on the request: queued to
// provision when every level was approved, rejected otherwise. It reports
// whether the request may go on.
func (a *Activities) FinishApprovals(ctx context.Context, requestID string) (bool, error) {
	r, err := a.Store.GetRequest(ctx, requestID)
	if err != nil {
		return false, err
	}
	levels, err := a.Store.ApprovalsForRequest(ctx, requestID)
	if err != nil {
		return false, err
	}
	ids := make([]string, len(levels))
	for i, l := range levels {
		ids[i] = l.ID
	}
	votes, err := a.Store.Votes(ctx, a.Store.Pool, ids)
	if err != nil {
		return false, err
	}
	var outcomes, notes []string
	approved := len(levels) > 0
	for _, l := range levels {
		if l.Status != "approved" {
			approved = false
			if l.Status == "rejected" && l.Outcome != nil {
				outcomes = []string{*l.Outcome}
				notes = nil
				for _, v := range votes[l.ID] {
					if v.Decision == "reject" && v.Comment != "" {
						notes = append(notes, "“"+v.Comment+"”")
					}
				}
			}
			break
		}
		if l.Outcome != nil {
			outcomes = append(outcomes, *l.Outcome)
		}
		for _, v := range votes[l.ID] {
			if v.Comment != "" {
				notes = append(notes, "“"+v.Comment+"”")
			}
			var changes []struct {
				Label string `json:"label"`
				From  any    `json:"from"`
				To    any    `json:"to"`
			}
			_ = json.Unmarshal(v.Changes, &changes)
			for _, c := range changes {
				notes = append(notes, fmt.Sprintf("%s changed from %v to %v by %s", c.Label, c.From, c.To, v.UserName))
			}
		}
	}
	if len(levels) == 0 {
		outcomes = []string{"Rejected: this request has no approval levels (it was submitted before approvals existed)"}
	}
	title := strings.Join(outcomes, "; ")
	if title == "" {
		title = "Rejected"
	}
	u := store.StepUpdate{Status: "done", Title: &title}
	if len(notes) > 0 {
		d := strings.Join(notes, " · ")
		u.Detail = &d
	} else {
		empty := ""
		u.Detail = &empty
	}
	err = a.Store.Tx(ctx, func(tx pgx.Tx) error {
		if !approved {
			u.Status = "failed"
			if err := a.Store.CancelApprovals(ctx, tx, requestID); err != nil {
				return err
			}
			if err := a.Store.MarkRemainingSteps(ctx, tx, requestID, "cancelled"); err != nil {
				return err
			}
			if err := a.Store.SetDeploymentsStatusForRequest(ctx, tx, requestID, "deleted"); err != nil {
				return err
			}
		}
		if _, err := a.Store.UpdateStep(ctx, tx, requestID, KeyPolicy, u); err != nil {
			return err
		}
		status := "queued"
		if !approved {
			status = "rejected"
		}
		return a.Store.SetRequestStatus(ctx, tx, requestID, status)
	})
	if err != nil {
		return false, err
	}
	if !approved {
		a.Events.Notify(ctx, r.RequesterID, "Rejected: "+r.Title, strings.Join(notes, " "), "error", "/requests/"+r.Number)
	} else {
		a.Events.Notify(ctx, r.RequesterID, "Approved: "+r.Title, title, "success", "/requests/"+r.Number)
	}
	a.publishAll(ctx, r.ProjectID, requestID)
	return approved, nil
}

// approvers lists who may decide a level: named users, group members seen
// so far and delegates, never the requester.
func (a *Activities) approvers(ctx context.Context, lv store.Approval, requesterID string) []string {
	ids := lv.NamedApprovers()
	if groups := lv.Groups(); len(groups) > 0 {
		members, err := a.Store.GroupMembers(ctx, groups)
		if err != nil {
			a.Log.Warn("approval group members", "approval", lv.ID, "err", err)
		}
		ids = append(ids, members...)
	}
	if dels, err := a.Store.Delegates(ctx, []string{lv.ID}); err == nil {
		for _, d := range dels[lv.ID] {
			ids = append(ids, d.UserID)
		}
	}
	return slices.DeleteFunc(dedupe(ids), func(id string) bool { return id == requesterID })
}

func dedupe(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// humanDuration writes 48h as "48 h" and 90m as "90 min".
func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d h", d/time.Hour)
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d min", d/time.Minute)
	default:
		return d.String()
	}
}
