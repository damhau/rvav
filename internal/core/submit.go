package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// Submit validates a draft again, records the request with its pending steps
// and deployments, and starts its workflow.
func (s *Service) Submit(ctx context.Context, p *auth.Principal, d api.RequestDraft) (store.RequestRow, error) {
	ev, err := s.Evaluate(ctx, p, d, true)
	if err != nil {
		return store.RequestRow{}, err
	}
	if len(ev.Errors) > 0 {
		return store.RequestRow{}, &ValidationError{Errors: ev.Errors}
	}
	owner := p.User.ID
	if ev.OnBehalfOf != nil {
		owner = ev.OnBehalfOf.ID
	}

	req := store.Request{
		ID: ids.New(ids.Request), ItemID: ev.Item.ID, VersionID: ev.Version.ID, ProjectID: ev.Project.ID,
		Environment: ev.Env.Key, RequesterID: p.User.ID, OwnerID: owner, Count: ev.Count,
		LeaseDays: ev.Lease.Days, ApprovalRequired: ev.Approval.Required, SubmittedAt: time.Now().UTC(),
		ScheduledAt: d.ScheduledAt,
	}
	if ev.OnBehalfOf != nil {
		req.OnBehalfOfID = &ev.OnBehalfOf.ID
	}
	if d.Justification != nil {
		req.Justification = strings.TrimSpace(*d.Justification)
	}
	wf := provision.WorkflowID(req.ID)
	req.WorkflowID = &wf
	switch {
	case ev.Approval.Required:
		req.Status = "pending_approval"
	case d.ScheduledAt != nil:
		req.Status = "scheduled"
	default:
		req.Status = "queued"
	}

	plan := provision.Plan{Kind: ev.Blueprint.Kind, ApprovalRequired: ev.Approval.Required, AutoApproved: ev.AutoApproved,
		Quota: ev.QuotaAdd, Guest: ev.Blueprint.Guest, Rollback: ev.Blueprint.RollbackOnFailure}
	var deployments []store.Deployment

	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		number, err := s.Store.NextRequestNumber(ctx, tx)
		if err != nil {
			return err
		}
		req.Number = number

		switch ev.Blueprint.Kind {
		case blueprint.KindVM:
			deps, vms, comps, err := s.reserveVMs(ctx, tx, ev, req.ID, owner, req.Number)
			if err != nil {
				return err
			}
			deployments, plan.VMs, plan.Components = append(deployments, deps...), vms, comps
			if len(comps) > 0 {
				// A multi-VM deployment (ADR-0024): post-configuration is each component's.
				req.Title = ev.Item.Name + " · " + deps[0].Name
				break
			}
			if plan.PostConfig, err = postConfigJobs(ev, ev.VM, optionOrder(ev.Version.UISchema, "post_config")); err != nil {
				return err
			}
			req.Title = ev.Item.Name + " · " + titleNames(plan.VMs)
		case blueprint.KindAWX:
			job, vars := awxLaunch(ev)
			plan.AWX, plan.ExtraVars = &job, vars
			req.Title = ev.Item.Name + titleSuffix(ev.Version.UISchema, ev.Inputs)
		case blueprint.KindComposite:
			for _, part := range ev.Composite {
				c := provision.CompositeStep{Key: part.step.Key, Title: part.title, Group: part.group, When: part.step.When,
					Undo: part.undo}
				switch {
				case part.sub != nil:
					// The composite's lease is its VMs'.
					part.sub.Lease = ev.Lease
					deps, vms, comps, err := s.reserveVMs(ctx, tx, part.sub, req.ID, owner, req.Number)
					if err != nil {
						return err
					}
					deployments, c.VMs, c.Guest, c.Components = append(deployments, deps...), vms, part.sub.Blueprint.Guest, comps
					// A multi-VM item's post-configuration is its components' (ADR-0024).
					if len(comps) == 0 {
						if c.PostConfig, err = postConfigJobs(part.sub, part.sub.VM, optionOrder(part.sub.Version.UISchema, "post_config")); err != nil {
							return err
						}
					}
				case part.job != nil:
					c.AWX, c.With = part.job, part.step.With
				case part.call != nil:
					c.Workflow, c.With = part.call, part.step.With
				}
				plan.Composite = append(plan.Composite, c)
			}
			req.Title = ev.Item.Name + titleSuffix(ev.Version.UISchema, ev.Inputs)
		case blueprint.KindWorkflow:
			w := ev.Workflow
			plan.Workflow = &provision.WorkflowCall{Key: w.Key, Title: w.Title, Type: w.WorkflowType, TaskQueue: w.TaskQueue,
				Timeout: w.TimeoutSeconds, Inputs: ev.Inputs}
			req.Title = ev.Item.Name + titleSuffix(ev.Version.UISchema, ev.Inputs)
		}
		plan.Context = map[string]any{
			"request_id": req.ID, "request_number": req.Number, "project": ev.Project.Code,
			"environment": ev.Env.Key, "requester": p.User.Email,
		}
		req.Plan, _ = json.Marshal(plan)
		req.Inputs, _ = json.Marshal(ev.Inputs)
		req.Approval, _ = json.Marshal(ev.Approval)

		if err := s.Store.InsertRequest(ctx, tx, req); err != nil {
			return err
		}
		if err := s.Store.InsertSteps(ctx, tx, stepsOf(req.ID, plan.Steps(ev.Env.Name))); err != nil {
			return err
		}
		if err := s.Store.InsertApprovals(ctx, tx, chainRows(req.ID, ev.Chain)); err != nil {
			return err
		}
		for _, dep := range deployments {
			if err := s.Store.InsertDeployment(ctx, tx, dep); err != nil {
				return err
			}
		}
		return outbox.ForRequest(ctx, tx, outbox.RequestSubmitted, req.ID, req.ID+":"+outbox.RequestSubmitted, "")
	})
	if err != nil {
		return store.RequestRow{}, err
	}

	if err := s.Workflows.Start(ctx, req.ID); err != nil {
		// Nothing has run yet: remove what was stored so the 502 ("try again")
		// is true and a retry doesn't leave failed requests behind.
		s.Log.Error("start workflow", "request", req.Number, "err", err)
		if derr := s.Store.DeleteUnstartedRequest(ctx, req.ID); derr != nil {
			s.Log.Error("remove unstarted request", "request", req.Number, "err", derr)
		}
		return store.RequestRow{}, &UpstreamError{System: "Temporal", Err: err}
	}
	// Approvers hear about it when the workflow opens their level.
	s.publishRequest(ctx, req.ID)
	stored, err := s.Store.GetRequest(ctx, req.ID)
	if err == nil && s.Events != nil {
		// The requester, and the owner when on their behalf (ADR-0023).
		for _, uid := range []string{stored.RequesterID, stored.OwnerID} {
			s.Events.Notify(ctx, store.NewNotification{UserID: uid, Kind: "request_submitted", Title: "Submitted: " + stored.Title,
				Level: "info", Link: "/requests/" + stored.Number, RequestID: &stored.ID, DedupeKey: "request_submitted:" + stored.ID})
		}
	}
	return stored, err
}

// chainRows turns the approval chain into one approvals row per level; all
// wait until the workflow opens them in turn.
func chainRows(requestID string, chain []ChainLevel) []store.Approval {
	var rows []store.Approval
	for i, l := range chain {
		a := store.Approval{ID: ids.New(ids.Approval), RequestID: requestID, Seq: i + 1, Name: l.Name,
			ApproverIDs: l.Approvers, Mode: l.Mode, PolicyIDs: l.PolicyIDs, Reasons: l.Reasons, Rules: l.Rules,
			Status: "waiting", EscalateIDs: l.EscalateIDs}
		if l.Group != "" {
			a.GroupName = &l.Group
		}
		if l.Timeout > 0 {
			secs := int(l.Timeout.Seconds())
			a.TimeoutSeconds, a.OnTimeout = &secs, &l.OnTimeout
			if l.EscalateGroup != "" {
				a.EscalateGroup = &l.EscalateGroup
			}
		}
		rows = append(rows, a)
	}
	return rows
}

func (s *Service) publishRequest(ctx context.Context, requestID string) {
	if s.Events == nil {
		return
	}
	s.Events.Request(ctx, requestID)
	deps, err := s.Store.DeploymentsForRequest(ctx, requestID)
	if err != nil {
		return
	}
	for _, d := range deps {
		s.Events.Deployment(ctx, d.ID)
	}
}

// CanReadRequest is CanSeeRequest, and the approvers of the request, who
// need not be in its project.
func (s *Service) CanReadRequest(ctx context.Context, p *auth.Principal, r store.RequestRow) (bool, error) {
	if CanSeeRequest(p, r) {
		return true, nil
	}
	return s.Store.IsRequestApprover(ctx, r.ID, p.User.ID, p.Groups)
}

// CanSeeRequest is true for the requester, the owner and anyone who can see the project.
func CanSeeRequest(p *auth.Principal, r store.RequestRow) bool {
	return p.CanSeeProject(r.ProjectID) || r.RequesterID == p.User.ID || r.OwnerID == p.User.ID
}

// Cancel stops a request. Waiting requests end at once; running ones stop
// after the current step and roll back.
func (s *Service) Cancel(ctx context.Context, p *auth.Principal, idOrNumber string) (store.RequestRow, error) {
	r, err := s.Store.GetRequest(ctx, idOrNumber)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !CanSeeRequest(p, r)) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if r.RequesterID != p.User.ID && r.OwnerID != p.User.ID && p.ProjectRole(r.ProjectID) != "project-admin" {
		return r, fmt.Errorf("%w: only the requester, the owner or a project admin can cancel this request", ErrForbidden)
	}
	switch r.Status {
	case "pending_approval", "scheduled", "queued":
		err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
			if err := s.Store.SetRequestStatus(ctx, tx, r.ID, "cancelled"); err != nil {
				return err
			}
			if err := s.Store.MarkRemainingSteps(ctx, tx, r.ID, "cancelled"); err != nil {
				return err
			}
			if err := s.Store.CancelApprovals(ctx, tx, r.ID); err != nil {
				return err
			}
			return s.Store.SetDeploymentsStatusForRequest(ctx, tx, r.ID, "deleted")
		})
		if err != nil {
			return r, err
		}
	case "running":
		if err := s.Store.SetRequestStatus(ctx, s.Store.Pool, r.ID, "cancelling"); err != nil {
			return r, err
		}
	case "cancelling":
		return r, nil
	default:
		return r, &ConflictError{Msg: fmt.Sprintf("%s has already finished (%s).", r.Number, strings.ReplaceAll(r.Status, "_", " "))}
	}
	if err := s.Workflows.Cancel(ctx, r.ID); err != nil {
		s.Log.Warn("cancel workflow", "request", r.Number, "err", err)
	}
	s.publishRequest(ctx, r.ID)
	return s.Store.GetRequest(ctx, r.ID)
}

// optionOrder returns the option values of a UI field in display order.
func optionOrder(ui json.RawMessage, field string) []string {
	var u api.UiSchema
	_ = json.Unmarshal(ui, &u)
	f, ok := u.Fields[field]
	if !ok || f.Options == nil {
		return nil
	}
	out := make([]string, len(*f.Options))
	for i, o := range *f.Options {
		out[i] = o.Value
	}
	return out
}

func titleNames(vms []provision.VMUnit) string {
	if len(vms) == 1 {
		return vms[0].Name
	}
	return fmt.Sprintf("%s +%d", vms[0].Name, len(vms)-1)
}

// titleSuffix uses the first text input in form order, for example
// "Create database user · app_reporting".
func titleSuffix(ui json.RawMessage, inputs map[string]any) string {
	var u api.UiSchema
	_ = json.Unmarshal(ui, &u)
	for _, sec := range u.Layout.Sections {
		for _, f := range sec.Fields {
			if v, ok := inputs[f].(string); ok && v != "" {
				return " · " + v
			}
		}
	}
	return ""
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// awxLaunch is an AWX item's job: launched by its template's AWX ID when
// rvav knows it (items made from a template, or linked to one with the same
// name), with the inputs the blueprint names as the launch's inventory and
// credentials (not extra variables), and the other inputs as extra
// variables.
func awxLaunch(ev *Evaluation) (provision.Job, map[string]any) {
	a := ev.Blueprint.AWX
	job := provision.Job{Key: a.JobTemplate, Title: a.Title, Template: a.JobTemplate, TemplateID: a.TemplateID, Workflow: a.Workflow}
	if job.TemplateID == 0 && ev.Item.AWXTemplateID != nil && deref(ev.Item.AWXTemplateName) == a.JobTemplate {
		job.TemplateID = *ev.Item.AWXTemplateID
	}
	vars := map[string]any{}
	for k, v := range ev.Inputs {
		vars[k] = v
	}
	toID := func(v any) int {
		n, _ := strconv.Atoi(fmt.Sprint(v))
		return n
	}
	if a.Inventory != "" {
		job.Inventory = toID(vars[a.Inventory])
		delete(vars, a.Inventory)
	}
	for _, name := range a.Credentials {
		switch v := vars[name].(type) {
		case []any:
			for _, x := range v {
				if id := toID(x); id != 0 {
					job.Credentials = append(job.Credentials, id)
				}
			}
		default:
			if id := toID(v); id != 0 {
				job.Credentials = append(job.Credentials, id)
			}
		}
		delete(vars, name)
	}
	return job, vars
}
