package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
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

	plan := provision.Plan{Kind: ev.Blueprint.Kind, ApprovalRequired: ev.Approval.Required, Quota: ev.QuotaAdd,
		Guest: ev.Blueprint.Guest, Rollback: ev.Blueprint.RollbackOnFailure}
	var deployments []store.Deployment

	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		number, err := s.Store.NextRequestNumber(ctx, tx)
		if err != nil {
			return err
		}
		req.Number = number

		switch ev.Blueprint.Kind {
		case blueprint.KindVM:
			taken, err := s.Store.InventoryVMNames(ctx, ev.NamePrefix)
			if err != nil {
				return err
			}
			order := optionOrder(ev.Version.UISchema, "post_config")
			for i := 0; i < ev.Count; i++ {
				seq, err := s.Store.NextName(ctx, tx, ev.NamePrefix, func(n int) bool { return taken[ev.Naming.Name(ev.NamePrefix, n)] })
				if err != nil {
					return err
				}
				name := ev.Naming.Name(ev.NamePrefix, seq)
				dep := store.Deployment{
					ID: ids.New(ids.Deployment), Name: name, Status: "provisioning", ProjectID: ev.Project.ID,
					Environment: ev.Env.Key, ItemID: ev.Item.ID, VersionID: ev.Version.ID, OwnerID: owner,
					CreatedByRequestID: &req.ID, LeaseExpiresAt: ev.Lease.ExpiresAt, ExpiryAction: orDefault(ev.Lease.ExpiryAction, "notify_only"),
				}
				if ev.Lease.ExpiresAt != nil {
					dep.LeaseExtensionsMax = &ev.Lease.MaxExtensions
				}
				dep.Inputs, _ = json.Marshal(ev.Inputs)
				deployments = append(deployments, dep)
				plan.VMs = append(plan.VMs, provision.VMUnit{
					DeploymentID: dep.ID, Name: name, FQDN: name + "." + ev.Subnet.DNSZone,
					VCPU: ev.VM.VCPU, MemoryGB: ev.VM.MemoryGB, DiskGB: ev.VM.DiskGB,
					Cluster: ev.VM.Cluster, Datastore: ev.VM.Datastore, Network: ev.VM.Network,
					Template: ev.VM.Template, Folder: ev.Rule.Folder,
					Subnet: ev.Subnet.Subnet, SubnetID: ev.Subnet.EfficientIPSubnetID, Gateway: ev.Subnet.Gateway,
					PrefixLength: prefixLength(ev.Subnet.Subnet), DNSZone: ev.Subnet.DNSZone, DNSServers: ev.Subnet.DNSServers,
				})
			}
			for _, key := range ev.Blueprint.OrderedPostConfig(ev.VM.PostConfig, order) {
				t, ok := ev.Blueprint.AWXTemplates[key]
				if !ok {
					return &ValidationError{Errors: []api.FieldError{fieldErr("/inputs/post_config", "unknown_template",
						fmt.Sprintf("%s is not configured for this item.", key))}}
				}
				plan.PostConfig = append(plan.PostConfig, provision.Job{Key: key, Title: t.Title, Template: t.JobTemplate, Workflow: t.Workflow})
			}
			req.Title = ev.Item.Name + " · " + titleNames(plan.VMs)
		case blueprint.KindAWX:
			a := ev.Blueprint.AWX
			plan.AWX = &provision.Job{Key: a.JobTemplate, Title: a.Title, Template: a.JobTemplate, Workflow: a.Workflow}
			plan.ExtraVars = ev.Inputs
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
		now := time.Now().UTC()
		var steps []store.Step
		for i, sd := range plan.Steps(ev.Env.Name) {
			st := store.Step{ID: ids.New(ids.Step), RequestID: req.ID, Position: i, Key: sd.Key, Title: sd.Title,
				Detail: sd.Detail, System: &sd.System, Status: sd.Status}
			if sd.Status != "pending" {
				st.StartedAt = &now
			}
			if sd.Status == "done" {
				st.FinishedAt = &now
			}
			steps = append(steps, st)
		}
		if err := s.Store.InsertSteps(ctx, tx, steps); err != nil {
			return err
		}
		for _, dep := range deployments {
			if err := s.Store.InsertDeployment(ctx, tx, dep); err != nil {
				return err
			}
		}
		return nil
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
	s.publishRequest(ctx, req.ID)
	if ev.Approval.Required {
		for _, pol := range ev.Approval.Policies {
			for _, l := range pol.Levels {
				for _, a := range l.Approvers {
					s.Events.Notify(ctx, a.Id, "Approval needed: "+req.Title, pol.Reason, "warning", "/requests/"+req.Number)
				}
			}
		}
	}
	return s.Store.GetRequest(ctx, req.ID)
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

// Decide approves or rejects a request waiting for approval. Slice 1 has no
// approvals API; `rvav approve` uses this.
func (s *Service) Decide(ctx context.Context, idOrNumber string, approved bool, by, comment string) error {
	r, err := s.Store.GetRequest(ctx, idOrNumber)
	if err != nil {
		return err
	}
	if r.Status != "pending_approval" {
		return &ConflictError{Msg: fmt.Sprintf("%s is not waiting for approval (%s).", r.Number, r.Status)}
	}
	return s.Workflows.Decide(ctx, r.ID, approved, by, comment)
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
