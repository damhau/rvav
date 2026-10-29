package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// Approvals (ADR-0011): the inbox, one level with what an approver needs,
// deciding and delegating. The workflow opens levels and applies timeouts;
// here decisions are recorded and the workflow is told when a level ends.

// level is a loaded approval level with its request and who may decide.
type loadedLevel struct {
	lv        store.Approval
	req       store.RequestRow
	count     int
	delegates []store.ApprovalDelegate
}

func (l loadedLevel) isApprover(p *auth.Principal) bool {
	if slices.Contains(l.lv.NamedApprovers(), p.User.ID) {
		return true
	}
	for _, g := range l.lv.Groups() {
		if slices.Contains(p.Groups, g) {
			return true
		}
	}
	return slices.ContainsFunc(l.delegates, func(d store.ApprovalDelegate) bool { return d.UserID == p.User.ID })
}

func (l loadedLevel) canDecide(p *auth.Principal, votes []store.ApprovalVote) bool {
	return l.lv.Status == "pending" && l.isApprover(p) && l.req.RequesterID != p.User.ID &&
		!slices.ContainsFunc(votes, func(v store.ApprovalVote) bool { return v.UserID == p.User.ID })
}

// load finds a level the principal may see: approvers, people who can see
// the request, admins and auditors.
func (s *Service) loadLevel(ctx context.Context, p *auth.Principal, id string) (loadedLevel, error) {
	lv, err := s.Store.GetApproval(ctx, s.Store.Pool, id)
	if errors.Is(err, store.ErrNotFound) {
		return loadedLevel{}, ErrNotFound
	}
	if err != nil {
		return loadedLevel{}, err
	}
	l, err := s.levelContext(ctx, lv)
	if err != nil {
		return l, err
	}
	if !l.isApprover(p) && !CanSeeRequest(p, l.req) && !p.SeesEverything() {
		return l, ErrNotFound
	}
	return l, nil
}

func (s *Service) levelContext(ctx context.Context, lv store.Approval) (loadedLevel, error) {
	r, err := s.Store.GetRequest(ctx, lv.RequestID)
	if err != nil {
		return loadedLevel{}, err
	}
	all, err := s.Store.ApprovalsForRequest(ctx, r.ID)
	if err != nil {
		return loadedLevel{}, err
	}
	dels, err := s.Store.Delegates(ctx, []string{lv.ID})
	if err != nil {
		return loadedLevel{}, err
	}
	return loadedLevel{lv: lv, req: r, count: len(all), delegates: dels[lv.ID]}, nil
}

// ListApprovals is the inbox: levels the caller may decide, or every level
// for admins and auditors with view=all.
func (s *Service) ListApprovals(ctx context.Context, p *auth.Principal, params api.ListApprovalsParams) (api.ApprovalPage, error) {
	all := params.View != nil && *params.View == api.ListApprovalsParamsViewAll
	if all && !p.SeesEverything() {
		return api.ApprovalPage{}, fmt.Errorf("%w: only administrators and auditors can see every approval", ErrForbidden)
	}
	statuses := []string{"pending"}
	if params.Status != nil && len(*params.Status) > 0 {
		statuses = nil
		for _, st := range *params.Status {
			statuses = append(statuses, string(st))
		}
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *store.Cursor
	if params.Cursor != nil {
		c, err := store.DecodeCursor(*params.Cursor)
		if err != nil {
			return api.ApprovalPage{}, &BadRequestError{Msg: "The cursor is not valid. Start again from the first page."}
		}
		after = c
	}
	page, total, err := s.Store.ListApprovals(ctx, store.ApprovalFilter{All: all, ViewerID: p.User.ID, Groups: p.Groups,
		Statuses: statuses, Limit: limit, After: after})
	if err != nil {
		return api.ApprovalPage{}, err
	}
	ids := make([]string, len(page.Items))
	for i, lv := range page.Items {
		ids[i] = lv.ID
	}
	votes, err := s.Store.Votes(ctx, s.Store.Pool, ids)
	if err != nil {
		return api.ApprovalPage{}, err
	}
	out := api.ApprovalPage{Items: []api.ApprovalSummary{}, Total: total}
	for _, lv := range page.Items {
		l, err := s.levelContext(ctx, lv)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, summary(l, l.canDecide(p, votes[lv.ID])))
	}
	if page.Next != nil {
		out.NextCursor = present.Ptr(page.Next.Encode())
	}
	return out, nil
}

// GetApproval returns one level with the facts, editable inputs and votes.
func (s *Service) GetApproval(ctx context.Context, p *auth.Principal, id string) (api.Approval, error) {
	l, err := s.loadLevel(ctx, p, id)
	if err != nil {
		return api.Approval{}, err
	}
	return s.detail(ctx, p, l)
}

// RequestApprovals is the approval history of a request.
func (s *Service) RequestApprovals(ctx context.Context, p *auth.Principal, idOrNumber string) ([]api.Approval, error) {
	r, err := s.Store.GetRequest(ctx, idOrNumber)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ok, err := s.CanReadRequest(ctx, p, r); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotFound
	}
	levels, err := s.Store.AllApprovalsForRequest(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	out := []api.Approval{}
	for _, lv := range levels {
		l, err := s.levelContext(ctx, lv)
		if err != nil {
			return nil, err
		}
		a, err := s.detail(ctx, p, l)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// hidden stands for a secret input's value in what the API returns.
const hidden = "••••"

type inputChange struct {
	Field string `json:"field"`
	Label string `json:"label"`
	From  any    `json:"from"`
	To    any    `json:"to"`
}

// DecideApproval records an approver's decision. With mode any it decides
// the level; with mode all the level is approved once every named approver
// approved. Edited inputs re-evaluate the request and replace its plan.
func (s *Service) DecideApproval(ctx context.Context, p *auth.Principal, id string, d api.ApprovalDecision) (api.Approval, error) {
	l, err := s.loadLevel(ctx, p, id)
	if err != nil {
		return api.Approval{}, err
	}
	// Who may act comes before the level's state, which others need not learn.
	if !l.isApprover(p) {
		return api.Approval{}, fmt.Errorf("%w: you are not an approver of this level", ErrForbidden)
	}
	if l.req.RequesterID == p.User.ID {
		return api.Approval{}, fmt.Errorf("%w: you can't approve your own request", ErrForbidden)
	}
	if l.lv.Status != "pending" {
		return api.Approval{}, &ConflictError{Msg: fmt.Sprintf("This level is %s; it can't be decided now.", l.lv.Status)}
	}
	approve := d.Decision == api.ApprovalDecisionDecisionApprove
	if !approve && d.Decision != api.ApprovalDecisionDecisionReject {
		return api.Approval{}, &BadRequestError{Msg: "decision must be approve or reject."}
	}
	comment := ""
	if d.Comment != nil {
		comment = strings.TrimSpace(*d.Comment)
	}
	if !approve && comment == "" {
		return api.Approval{}, &ValidationError{Errors: []api.FieldError{fieldErr("/comment", "required",
			"Say why you reject it: the requester sees this comment.")}}
	}

	edits := map[string]any{}
	if d.Inputs != nil {
		edits = *d.Inputs
	}
	if len(edits) > 0 && !approve {
		return api.Approval{}, &ValidationError{Errors: []api.FieldError{fieldErr("/inputs", "not_allowed",
			"Inputs can only be changed when approving.")}}
	}
	// A workflow's approval comes while it runs, and composites and custom
	// workflows aren't planned again: their inputs stay as submitted.
	if len(edits) > 0 && (l.lv.WorkflowID != nil || l.req.ItemType == "composite" || l.req.ItemType == "workflow") {
		return api.Approval{}, &ValidationError{Errors: []api.FieldError{fieldErr("/inputs", "not_allowed",
			"The inputs of this request can't be changed when approving: reject it with what should change.")}}
	}

	var changes []inputChange
	var decided bool
	var status, outcome string
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		lv, err := s.Store.LockApproval(ctx, tx, id)
		if err != nil {
			return err
		}
		if lv.Status != "pending" {
			return &ConflictError{Msg: fmt.Sprintf("This level was %s meanwhile.", lv.Status)}
		}
		// Read the request again under the lock: another approver of this
		// level may have just changed its inputs.
		req, err := s.Store.GetRequestIn(ctx, tx, l.req.ID)
		if err != nil {
			return err
		}
		var newInputs, newPlan json.RawMessage
		var newSteps []store.Step
		if len(edits) > 0 {
			if changes, newInputs, newPlan, newSteps, err = s.applyEdits(ctx, req, edits); err != nil {
				return err
			}
		}
		changeJSON, _ := json.Marshal(nonNilSlice(changes))
		vote := store.ApprovalVote{ApprovalID: id, UserID: p.User.ID, Decision: string(d.Decision), Comment: comment, Changes: changeJSON}
		if err := s.Store.InsertVote(ctx, tx, vote); err != nil {
			if store.IsUniqueViolation(err) {
				return &ConflictError{Msg: "You have already decided on this level."}
			}
			return err
		}
		if len(changes) > 0 {
			if err := s.Store.ReplaceInputs(ctx, tx, l.req.ID, newInputs, newPlan); err != nil {
				return err
			}
			if err := s.Store.ReplacePendingSteps(ctx, tx, l.req.ID, newSteps); err != nil {
				return err
			}
			if err := s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "request.inputs_changed",
				TargetType: "request", TargetID: l.req.ID, ProjectID: &l.req.ProjectID,
				Detail: map[string]any{"approval": id, "changes": changes}}); err != nil {
				return err
			}
		}
		votes, err := s.Store.Votes(ctx, tx, []string{id})
		if err != nil {
			return err
		}
		switch {
		case !approve:
			decided, status, outcome = true, "rejected", "Rejected by "+p.User.Name
		case lv.Mode == "all" && lv.Escalated && l.isEscalationApprover(p):
			// Escalation exists because someone did not answer: the escalation
			// approver decides for the level.
			decided, status, outcome = true, "approved", "Approved by "+p.User.Name+" after escalation"
		case lv.Mode == "all":
			if names, done := allApproved(lv, l.req.RequesterID, votes[id], l.delegates); done {
				if len(names) == 0 {
					names = []string{p.User.Name}
				}
				decided, status, outcome = true, "approved", "Approved by "+joinNames(names)
			}
		default:
			decided, status, outcome = true, "approved", "Approved by "+p.User.Name
		}
		if decided {
			ok, err := s.Store.FinishApproval(ctx, tx, id, status, outcome)
			if err != nil {
				return err
			}
			if !ok {
				return &ConflictError{Msg: "This level was decided meanwhile."}
			}
		}
		action := "approval.voted"
		if decided {
			action = "approval." + status
		}
		detail := map[string]any{"request": l.req.Number, "decision": d.Decision, "comment": comment}
		if ch := fromChannel(ctx); ch != "" {
			detail["channel"] = ch // decided from a Teams or Slack card (ADR-0023)
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: action, TargetType: "approval",
			TargetID: id, ProjectID: &l.req.ProjectID, Detail: detail})
	})
	if err != nil {
		return api.Approval{}, err
	}
	if decided && l.lv.WorkflowID != nil {
		// An approval a custom workflow asked for (ADR-0022) goes to it.
		// One that fails is sent again by the worker (ResendApprovalSignals).
		if err := s.Workflows.Signal(ctx, *l.lv.WorkflowID, provision.SignalWorkflowApproval,
			provision.WorkflowApprovalSignal{ApprovalID: id, Outcome: status, Comment: comment}); err != nil {
			s.Log.Warn("signal workflow approval", "request", l.req.Number, "approval", id, "err", err)
		} else if err := s.Store.MarkApprovalSignalled(ctx, id); err != nil {
			s.Log.Warn("mark approval signalled", "approval", id, "err", err)
		}
	} else if decided {
		// If the signal is lost, the workflow still finds the decision when it
		// checks the level again (see provision.awaitApprovals).
		if err := s.Workflows.Approval(ctx, l.req.ID, id, status); err != nil {
			s.Log.Warn("signal approval", "request", l.req.Number, "approval", id, "err", err)
		}
	}
	s.publishApproval(ctx, l)
	if len(changes) > 0 {
		s.publishRequest(ctx, l.req.ID)
	}
	return s.GetApproval(ctx, p, id)
}

// allApproved reports whether every named approver (not the requester) has
// approved, themselves or through someone they delegated to, with the names
// of who did. A level with no named approvers (only a group) needs one.
func allApproved(lv store.Approval, requesterID string, votes []store.ApprovalVote, delegates []store.ApprovalDelegate) ([]string, bool) {
	var names []string
	for _, uid := range lv.ApproverIDs {
		if uid == requesterID {
			continue
		}
		i := slices.IndexFunc(votes, func(v store.ApprovalVote) bool {
			if v.Decision != "approve" {
				return false
			}
			return v.UserID == uid || slices.ContainsFunc(delegates, func(d store.ApprovalDelegate) bool {
				return d.UserID == v.UserID && d.DelegatedBy == uid
			})
		})
		if i < 0 {
			return nil, false
		}
		if !slices.Contains(names, votes[i].UserName) {
			names = append(names, votes[i].UserName)
		}
	}
	return names, true
}

func (l loadedLevel) isEscalationApprover(p *auth.Principal) bool {
	if slices.Contains(l.lv.EscalateIDs, p.User.ID) {
		return true
	}
	return l.lv.EscalateGroup != nil && slices.Contains(p.Groups, *l.lv.EscalateGroup)
}

// applyEdits checks approver changes against the form and evaluates the
// request again as its requester, keeping its names and deployments.
func (s *Service) applyEdits(ctx context.Context, req store.RequestRow, edits map[string]any) ([]inputChange, json.RawMessage, json.RawMessage, []store.Step, error) {
	if req.Kind == "day2" {
		return nil, nil, nil, nil, &ValidationError{Errors: []api.FieldError{fieldErr("/inputs", "not_editable",
			"Approvers can't change the inputs of a day-2 action.")}}
	}
	version, err := s.Store.GetVersion(ctx, req.VersionID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var ui api.UiSchema
	_ = json.Unmarshal(version.UISchema, &ui)
	inputs := map[string]any{}
	_ = json.Unmarshal(req.Inputs, &inputs)
	// Compared and evaluated with the secret inputs' values, sealed again
	// after (S14.6).
	if err := s.openSecretInputs(ctx, req.ID, inputs); err != nil {
		return nil, nil, nil, nil, err
	}
	secret := present.SecretFields(version.UISchema)
	var changes []inputChange
	var errs []api.FieldError
	for _, field := range sortedKeys(edits) {
		f, ok := ui.Fields[field]
		if !ok || f.ApproverEditable == nil || !*f.ApproverEditable {
			errs = append(errs, fieldErr("/inputs/"+field, "not_editable", "Approvers can't change this input."))
			continue
		}
		if !reflect.DeepEqual(inputs[field], edits[field]) {
			c := inputChange{Field: field, Label: f.Label, From: inputs[field], To: edits[field]}
			if secret[field] {
				c.From, c.To = hidden, hidden
			}
			changes = append(changes, c)
			inputs[field] = edits[field]
		}
	}
	if len(errs) > 0 {
		return nil, nil, nil, nil, &ValidationError{Errors: errs}
	}
	if len(changes) == 0 {
		return nil, nil, nil, nil, nil
	}
	requester, err := auth.ForUser(ctx, s.Store, req.RequesterID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	draft := api.RequestDraft{CatalogItemId: req.ItemID, VersionId: req.VersionID, ProjectId: req.ProjectID,
		Environment: api.EnvironmentKey(req.Environment), Inputs: inputs, Count: present.Ptr(req.Count),
		Justification: present.Ptr(req.Justification)}
	if req.LeaseDays != nil {
		draft.LeaseDays = req.LeaseDays
	}
	// The request keeps the version it was submitted with, even if the item
	// was republished or withdrawn since.
	ev, err := s.evaluate(ctx, requester, draft, true, true)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(ev.Errors) > 0 {
		return nil, nil, nil, nil, &ValidationError{Errors: ev.Errors}
	}
	var plan provision.Plan
	if err := json.Unmarshal(req.Plan, &plan); err != nil {
		return nil, nil, nil, nil, err
	}
	switch ev.Blueprint.Kind {
	case blueprint.KindVM:
		if err := replanVMs(ev, &plan); err != nil {
			return nil, nil, nil, nil, err
		}
	case blueprint.KindAWX:
		plan.ExtraVars = ev.Inputs
	}
	plan.Quota = ev.QuotaAdd
	// plan.ExtraVars is ev.Inputs: sealed with it.
	if err := s.sealSecretInputs(ctx, s.Store.Pool, req.ID, ev.Version.UISchema, ev.Inputs); err != nil {
		return nil, nil, nil, nil, err
	}
	newInputs, _ := json.Marshal(ev.Inputs)
	newPlan, _ := json.Marshal(plan)
	// The steps still to run follow the new plan (post-config jobs, placement).
	var steps []store.Step
	for _, st := range stepsOf(req.ID, plan.Steps(ev.Env.Name)) {
		if st.Key == provision.KeySubmit || st.Key == provision.KeyPolicy {
			continue
		}
		st.Status, st.StartedAt, st.FinishedAt = "pending", nil, nil
		steps = append(steps, st)
	}
	return changes, newInputs, newPlan, steps, nil
}

// DelegateApproval lets another user decide this level.
func (s *Service) DelegateApproval(ctx context.Context, p *auth.Principal, id string, d api.ApprovalDelegation) (api.Approval, error) {
	l, err := s.loadLevel(ctx, p, id)
	if err != nil {
		return api.Approval{}, err
	}
	if !l.isApprover(p) {
		return api.Approval{}, fmt.Errorf("%w: only an approver of this level can delegate it", ErrForbidden)
	}
	if l.req.RequesterID == p.User.ID {
		return api.Approval{}, fmt.Errorf("%w: you can't hand on the approval of your own request", ErrForbidden)
	}
	if l.lv.Status != "pending" {
		return api.Approval{}, &ConflictError{Msg: fmt.Sprintf("This level is %s; it can't be delegated now.", l.lv.Status)}
	}
	to, err := s.Store.GetUser(ctx, d.UserId)
	if errors.Is(err, store.ErrNotFound) {
		return api.Approval{}, &ValidationError{Errors: []api.FieldError{fieldErr("/user_id", "not_found", "There is no user with this ID.")}}
	}
	if err != nil {
		return api.Approval{}, err
	}
	if to.ID == l.req.RequesterID {
		return api.Approval{}, &ValidationError{Errors: []api.FieldError{fieldErr("/user_id", "requester",
			"The requester can't approve their own request.")}}
	}
	comment := ""
	if d.Comment != nil {
		comment = strings.TrimSpace(*d.Comment)
	}
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.InsertDelegate(ctx, tx, store.ApprovalDelegate{ApprovalID: id, UserID: to.ID, DelegatedBy: p.User.ID, Comment: comment}); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "approval.delegated", TargetType: "approval",
			TargetID: id, ProjectID: &l.req.ProjectID, Detail: map[string]any{"to": to.ID, "comment": comment}})
	})
	if err != nil {
		return api.Approval{}, err
	}
	s.Events.Notify(ctx, store.NewNotification{UserID: to.ID, Kind: "approval_needed", Title: "Approval delegated to you: " + l.req.Title,
		Body: p.User.Name + ": " + comment, Level: "warning", Link: "/approvals/" + id, RequestID: &l.req.ID, ApprovalID: &id,
		Data: map[string]any{"why": "delegated"}, DedupeKey: "approval_needed:" + id + ":delegated:" + to.ID})
	l.delegates = append(l.delegates, store.ApprovalDelegate{UserID: to.ID})
	s.publishApproval(ctx, l)
	return s.GetApproval(ctx, p, id)
}

// approverIDs lists who may decide a level, without the requester.
func (s *Service) approverIDs(ctx context.Context, l loadedLevel) []string {
	ids := l.lv.NamedApprovers()
	if groups := l.lv.Groups(); len(groups) > 0 {
		if members, err := s.Store.GroupMembers(ctx, groups); err == nil {
			ids = append(ids, members...)
		}
	}
	for _, d := range l.delegates {
		ids = append(ids, d.UserID)
	}
	var out []string
	for _, id := range ids {
		if id != l.req.RequesterID && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (s *Service) publishApproval(ctx context.Context, l loadedLevel) {
	if s.Events == nil {
		return
	}
	lv, err := s.Store.GetApproval(ctx, s.Store.Pool, l.lv.ID)
	if err != nil {
		return
	}
	s.Events.Approval(ctx, append(s.approverIDs(ctx, l), l.req.RequesterID), lv.ID, lv.RequestID, lv.Status)
}

func summary(l loadedLevel, canDecide bool) api.ApprovalSummary {
	r, lv := l.req, l.lv
	out := api.ApprovalSummary{Id: lv.ID, Project: present.ProjectRef(r.ProjectID, r.ProjectCode, r.ProjectName),
		Environment: api.EnvironmentKey(r.Environment), Requester: present.UserRef(r.RequesterID, r.RequesterName, r.RequesterEmail),
		SubmittedAt: r.SubmittedAt, Reasons: nonNilSlice(lv.Reasons), Status: api.ApprovalStatus(lv.Status),
		DueAt: lv.DueAt, Overdue: lv.DueAt != nil && time.Now().After(*lv.DueAt), Escalated: present.Ptr(lv.Escalated),
		CanDecide: canDecide}
	out.Request.Id, out.Request.Number, out.Request.Title, out.Request.Status = r.ID, r.Number, r.Title, api.RequestStatus(r.Status)
	out.Level.Index, out.Level.Count, out.Level.Name = lv.Seq, l.count, lv.Name
	return out
}

func (s *Service) detail(ctx context.Context, p *auth.Principal, l loadedLevel) (api.Approval, error) {
	votes, err := s.Store.Votes(ctx, s.Store.Pool, []string{l.lv.ID})
	if err != nil {
		return api.Approval{}, err
	}
	canDecide := l.canDecide(p, votes[l.lv.ID])
	sum := summary(l, canDecide)
	a := api.Approval{Id: sum.Id, Request: sum.Request, Project: sum.Project, Environment: sum.Environment,
		Requester: sum.Requester, SubmittedAt: sum.SubmittedAt, Level: sum.Level, Reasons: sum.Reasons,
		Status: sum.Status, DueAt: sum.DueAt, Overdue: sum.Overdue, Escalated: sum.Escalated, CanDecide: canDecide,
		Rules: nonNilSlice(l.lv.Rules), Mode: api.ApprovalMode(l.lv.Mode), Group: l.lv.GroupName,
		DecidedAt: l.lv.DecidedAt, Outcome: l.lv.Outcome, Votes: []api.ApprovalVote{}, Delegates: []api.UserRef{},
		Approvers: []api.UserRef{}, EditableFields: []api.ApprovalEditableField{}}
	if l.req.Justification != "" {
		a.Justification = present.Ptr(l.req.Justification)
	}
	if l.lv.OnTimeout != nil {
		a.OnTimeout = present.Ptr(api.ApprovalOnTimeout(*l.lv.OnTimeout))
	}
	users, err := s.Store.FindUsers(ctx, append(s.approverIDs(ctx, l), l.lv.EscalateIDs...))
	if err != nil {
		return a, err
	}
	for _, id := range s.approverIDs(ctx, l) {
		if u, ok := users[id]; ok {
			a.Approvers = append(a.Approvers, present.UserRef(u.ID, u.Name, u.Email))
		}
	}
	if len(l.lv.EscalateIDs) > 0 {
		esc := []api.UserRef{}
		for _, id := range l.lv.EscalateIDs {
			if u, ok := users[id]; ok {
				esc = append(esc, present.UserRef(u.ID, u.Name, u.Email))
			}
		}
		a.EscalateTo = &esc
	}
	for _, d := range l.delegates {
		a.Delegates = append(a.Delegates, present.UserRef(d.UserID, d.UserName, d.UserEmail))
	}
	for _, v := range votes[l.lv.ID] {
		av := api.ApprovalVote{User: present.UserRef(v.UserID, v.UserName, v.UserEmail),
			Decision: api.ApprovalVoteDecision(v.Decision), DecidedAt: v.DecidedAt}
		if v.Comment != "" {
			av.Comment = present.Ptr(v.Comment)
		}
		_ = json.Unmarshal(v.Changes, &av.Changes)
		if av.Changes == nil {
			av.Changes = []struct {
				Field string      `json:"field"`
				From  interface{} `json:"from"`
				Label string      `json:"label"`
				To    interface{} `json:"to"`
			}{}
		}
		a.Votes = append(a.Votes, av)
	}
	a.Facts, err = s.facts(ctx, l)
	if err != nil {
		return a, err
	}
	if canDecide {
		if a.EditableFields, err = s.editableFields(ctx, l); err != nil {
			return a, err
		}
	}
	return a, nil
}

type fact = struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// facts sums up what the request will create, for the approver.
func (s *Service) facts(ctx context.Context, l loadedLevel) ([]fact, error) {
	var plan provision.Plan
	_ = json.Unmarshal(l.req.Plan, &plan)
	out := []fact{}
	switch {
	case plan.Day2 != nil:
		on := deref(l.req.ResourceName)
		if d, err := s.Store.GetDeployment(ctx, plan.Day2.DeploymentID); err == nil {
			if on == "" {
				on = d.Name
			}
			out = append(out, fact{"Deployment", d.Name + " · " + d.Environment})
		}
		out = append(out, fact{"Action", cmp.Or(plan.Day2.Name, day2.Name(plan.Day2.Action)) + " · " + on})
		steps := make([]string, len(plan.Day2.Ops))
		for i, op := range plan.Day2.Ops {
			steps[i] = op.Title
		}
		out = append(out, fact{"Steps", strings.Join(steps, "; ")})
	case len(plan.VMs) > 0:
		vm := plan.VMs[0]
		size := fmt.Sprintf("%d vCPU · %d GB · %d GB", vm.VCPU, vm.MemoryGB, vm.DiskGB)
		if len(plan.VMs) > 1 {
			size = fmt.Sprintf("%d × %s", len(plan.VMs), size)
		}
		out = append(out, fact{"Size", size})
		names := make([]string, len(plan.VMs))
		for i, v := range plan.VMs {
			names[i] = v.Name
		}
		out = append(out, fact{"Names", strings.Join(names, ", ")},
			fact{"Placement", vm.Cluster + " · " + vm.Datastore},
			fact{"Network", vm.Network + " · " + vm.Subnet})
		if len(plan.PostConfig) > 0 {
			jobs := make([]string, len(plan.PostConfig))
			for i, j := range plan.PostConfig {
				jobs[i] = j.Key
			}
			out = append(out, fact{"After boot", strings.Join(jobs, ", ")})
		}
	case plan.AWX != nil:
		out = append(out, fact{"AWX template", plan.AWX.Template})
	}
	if l.req.LeaseDays != nil {
		out = append(out, fact{"Lease", fmt.Sprintf("%d days", *l.req.LeaseDays)})
	}
	if plan.Quota["vcpu"] > 0 || plan.Quota["memory_gb"] > 0 {
		limits, err := s.Store.QuotaLimits(ctx, l.req.ProjectID, "")
		if err != nil {
			return out, err
		}
		used, err := s.Store.QuotaUsed(ctx, l.req.ProjectID, "")
		if err != nil {
			return out, err
		}
		reserved, err := s.Store.QuotaReserved(ctx, l.req.ProjectID, "")
		if err != nil {
			return out, err
		}
		var parts []string
		for _, q := range limits {
			if q.Limit == nil || (q.Resource != "vcpu" && q.Resource != "memory_gb") {
				continue
			}
			// The request itself is among the reserved while it waits.
			after := used[q.Resource] + reserved[q.Resource]
			unit := " vCPU"
			if q.Resource == "memory_gb" {
				unit = " GB"
			}
			parts = append(parts, fmt.Sprintf("%d / %d%s", after, *q.Limit, unit))
		}
		if len(parts) > 0 {
			out = append(out, fact{"Project quota after", strings.Join(parts, " · ")})
		}
	}
	return out, nil
}

// editableFields lists the inputs approvers may change, with their limits.
func (s *Service) editableFields(ctx context.Context, l loadedLevel) ([]api.ApprovalEditableField, error) {
	if l.req.Kind == "day2" {
		return []api.ApprovalEditableField{}, nil // the catalog form is not what a day-2 action asked
	}
	version, err := s.Store.GetVersion(ctx, l.req.VersionID)
	if err != nil {
		return nil, err
	}
	var ui api.UiSchema
	_ = json.Unmarshal(version.UISchema, &ui)
	var schema struct {
		Properties map[string]struct {
			Minimum *float32 `json:"minimum"`
			Maximum *float32 `json:"maximum"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(version.FormSchema, &schema)
	inputs := map[string]any{}
	_ = json.Unmarshal(l.req.Inputs, &inputs)
	out := []api.ApprovalEditableField{}
	for _, name := range sortedKeys(ui.Fields) {
		f := ui.Fields[name]
		if f.ApproverEditable == nil || !*f.ApproverEditable {
			continue
		}
		ef := api.ApprovalEditableField{Field: name, Label: f.Label, Value: inputs[name], Options: f.Options}
		if p, ok := schema.Properties[name]; ok {
			ef.Minimum, ef.Maximum = p.Minimum, p.Maximum
		}
		out = append(out, ef)
	}
	return out, nil
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// ResendApprovalSignals sends again the decisions of approvals custom
// workflows asked for whose signal failed (#81); the helpers keep a
// decision by its approval ID, so one received twice is harmless. It
// returns how many reached their workflow.
func (s *Service) ResendApprovalSignals(ctx context.Context) (int, error) {
	list, err := s.Store.UnsignalledApprovals(ctx, time.Now().Add(-10*time.Second))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range list {
		if err := s.Workflows.Signal(ctx, a.WorkflowID, provision.SignalWorkflowApproval,
			provision.WorkflowApprovalSignal{ApprovalID: a.ID, Outcome: a.Status, Comment: a.Comment}); err != nil {
			s.Log.Warn("signal workflow approval again", "approval", a.ID, "err", err)
			continue
		}
		if err := s.Store.MarkApprovalSignalled(ctx, a.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
