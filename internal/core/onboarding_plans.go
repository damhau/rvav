package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// Onboarding plans (X1.5): a saved filter of the inventory's VMs and where
// what it matches goes. A run, by hand or on the plan's schedule, adopts
// the VMs it matches that no deployment has, one by one, and says what it
// did to each: adopted, skipped (a deployment has it, a request builds it)
// or failed, with why. Runs are Temporal workflows (provision.OnboardingRun).

// keepRuns is how many runs of a plan are kept.
const keepRuns = 20

func vmFilter(f api.OnboardingFilter) store.InvFilter {
	out := store.InvFilter{VCenter: deref(f.Vcenter), Cluster: deref(f.Cluster), Search: strings.TrimSpace(deref(f.Q)),
		Folder: strings.TrimSpace(deref(f.Folder)), Tag: strings.TrimSpace(deref(f.Tag))}
	if f.PowerState != nil {
		out.PowerState = string(*f.PowerState)
	}
	return out
}

func (s *Service) ListOnboardingPlans(ctx context.Context, p *auth.Principal) (api.OnboardingPlanList, error) {
	out := api.OnboardingPlanList{Items: []api.OnboardingPlan{}}
	if !p.Everywhere(auth.ImportManage) {
		return out, ErrForbidden
	}
	plans, err := s.Store.ListOnboardingPlans(ctx)
	if err != nil {
		return out, err
	}
	last, err := s.Store.LastOnboardingRuns(ctx)
	if err != nil {
		return out, err
	}
	for _, pl := range plans {
		var run *store.OnboardingRun
		if r, ok := last[pl.ID]; ok {
			run = &r
		}
		item, err := s.presentPlan(ctx, pl, run)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *Service) GetOnboardingPlan(ctx context.Context, p *auth.Principal, id string) (api.OnboardingPlan, error) {
	if !p.Everywhere(auth.ImportManage) {
		return api.OnboardingPlan{}, ErrForbidden
	}
	pl, err := s.Store.GetOnboardingPlan(ctx, s.Store.Pool, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.OnboardingPlan{}, ErrNotFound
	}
	if err != nil {
		return api.OnboardingPlan{}, err
	}
	runs, err := s.Store.ListOnboardingRuns(ctx, id, 1)
	if err != nil {
		return api.OnboardingPlan{}, err
	}
	var last *store.OnboardingRun
	if len(runs) > 0 {
		last = &runs[0]
	}
	return s.presentPlan(ctx, pl, last)
}

func (s *Service) presentPlan(ctx context.Context, pl store.OnboardingPlan, last *store.OnboardingRun) (api.OnboardingPlan, error) {
	out := api.OnboardingPlan{Id: pl.ID, Name: pl.Name, Description: pl.Description, EveryHours: pl.EveryHours,
		NextRunAt: pl.NextRunAt, CreatedAt: pl.CreatedAt, UpdatedAt: pl.UpdatedAt}
	_ = json.Unmarshal(pl.Filter, &out.Filter)
	_ = json.Unmarshal(pl.Target, &out.Target)
	n, err := s.Store.CountUnmanagedVMs(ctx, vmFilter(out.Filter))
	if err != nil {
		return out, err
	}
	out.Matching = n
	if pl.DeploymentID != nil && pl.DeploymentName != "" {
		out.Deployment = &api.DeploymentRef{Id: *pl.DeploymentID, Name: pl.DeploymentName}
	}
	if pl.UpdatedBy != nil {
		out.UpdatedBy = &api.UserRef{Id: *pl.UpdatedBy, Name: pl.UpdatedByName}
	}
	if last != nil {
		r := presentOnboardingRun(*last, nil)
		out.LastRun, out.Running = &r, last.Status == "running"
	}
	return out, nil
}

func presentOnboardingRun(r store.OnboardingRun, vms []store.OnboardingRunVM) api.OnboardingRun {
	out := api.OnboardingRun{Id: r.ID, PlanId: r.PlanID, PlanName: r.PlanName, Trigger: api.OnboardingRunTrigger(r.Trigger),
		Status: api.OnboardingRunStatus(r.Status), StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Adopted: r.Adopted,
		Skipped: r.Skipped, Failed: r.Failed}
	if r.Error != "" {
		out.Error = &r.Error
	}
	if r.StartedBy != nil {
		out.StartedBy = &api.UserRef{Id: *r.StartedBy, Name: r.StartedByName}
	}
	if vms != nil {
		list := make([]api.OnboardingRunVm, len(vms))
		for i, v := range vms {
			list[i] = api.OnboardingRunVm{Vcenter: v.VCenter, Moref: v.Moref, Name: v.Name,
				Outcome: api.OnboardingRunVmOutcome(v.Outcome), Code: v.Code, Reason: v.Reason}
			if v.DeploymentID != nil && v.DeploymentName != "" {
				list[i].Deployment = &api.DeploymentRef{Id: *v.DeploymentID, Name: v.DeploymentName}
			}
		}
		out.Vms = &list
	}
	return out
}

// SaveOnboardingPlan creates a plan (id "") or changes one.
func (s *Service) SaveOnboardingPlan(ctx context.Context, p *auth.Principal, id string, w api.OnboardingPlanWrite) (api.OnboardingPlan, error) {
	if !p.Everywhere(auth.ImportManage) {
		return api.OnboardingPlan{}, ErrForbidden
	}
	pl := store.OnboardingPlan{ID: id, CreatedBy: &p.User.ID}
	var old *store.OnboardingPlan
	if id == "" {
		pl.ID = ids.New(ids.OnboardingPlan)
	} else {
		o, err := s.Store.GetOnboardingPlan(ctx, s.Store.Pool, id)
		if errors.Is(err, store.ErrNotFound) {
			return api.OnboardingPlan{}, ErrNotFound
		}
		if err != nil {
			return api.OnboardingPlan{}, err
		}
		old, pl = &o, o
	}
	var errs []api.FieldError
	add := func(at, code, msg string) { errs = append(errs, fieldErr(at, code, msg)) }
	w.Name = strings.TrimSpace(w.Name)
	if w.Name == "" || utf8.RuneCountInString(w.Name) > 80 {
		add("/name", "invalid", "Name the plan, in 80 characters at most.")
	}
	if w.Description != nil && utf8.RuneCountInString(*w.Description) > 500 {
		add("/description", "too_long", "500 characters at most.")
	}
	for _, x := range []struct {
		at  string
		v   *string
		max int
	}{{"/filter/q", w.Filter.Q, 200}, {"/filter/folder", w.Filter.Folder, 500}, {"/filter/tag", w.Filter.Tag, 200}} {
		if x.v != nil && utf8.RuneCountInString(*x.v) > x.max {
			add(x.at, "too_long", fmt.Sprintf("%d characters at most.", x.max))
		}
	}
	if vmFilter(w.Filter) == (store.InvFilter{}) {
		add("/filter", "required", "Give the filter a criterion at least: without one, the plan would adopt every VM rvav doesn't manage.")
	}
	if w.Filter.PowerState != nil && !w.Filter.PowerState.Valid() {
		add("/filter/power_state", "invalid", "On, off or suspended.")
	}
	if w.EveryHours != nil && (*w.EveryHours < 1 || *w.EveryHours > 168) {
		add("/every_hours", "out_of_range", "From every hour to every week (168 hours).")
	}
	if _, err := s.planDestination(ctx, p.User.ID, nil, w.Target, add); err != nil {
		return api.OnboardingPlan{}, err
	}
	if len(errs) > 0 {
		return api.OnboardingPlan{}, &ValidationError{Errors: errs}
	}

	pl.Name, pl.Description, pl.EveryHours, pl.UpdatedBy = w.Name, deref(w.Description), w.EveryHours, &p.User.ID
	pl.Filter, _ = json.Marshal(w.Filter)
	pl.Target, _ = json.Marshal(w.Target)
	switch {
	case w.EveryHours == nil:
		pl.NextRunAt = nil
	case old == nil || old.EveryHours == nil || *old.EveryHours != *w.EveryHours || old.NextRunAt == nil:
		next := time.Now().UTC().Add(time.Duration(*w.EveryHours) * time.Hour).Truncate(time.Second)
		pl.NextRunAt = &next
	}
	// The deployment its runs add to: the one it names, or the one its
	// first run made, unless where its VMs go changed.
	switch {
	case deref(w.Target.DeploymentId) != "":
		pl.DeploymentID = w.Target.DeploymentId
	case old != nil && sameNewTarget(old.Target, w.Target):
	default:
		pl.DeploymentID = nil
	}
	action := "onboarding_plan.created"
	if old != nil {
		action = "onboarding_plan.updated"
	}
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.SaveOnboardingPlan(ctx, tx, pl); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: action, TargetType: "onboarding_plan",
			TargetID: pl.ID, Detail: map[string]any{"name": pl.Name, "filter": w.Filter, "target": w.Target, "every_hours": w.EveryHours}})
	})
	if store.IsUniqueViolation(err) {
		return api.OnboardingPlan{}, &ValidationError{Errors: []api.FieldError{fieldErr("/name", "taken", "A plan has this name already.")}}
	}
	if err != nil {
		return api.OnboardingPlan{}, err
	}
	return s.GetOnboardingPlan(ctx, p, pl.ID)
}

// sameNewTarget: the deployment a plan's first run made still is where
// its VMs go.
func sameNewTarget(old json.RawMessage, t api.OnboardingTarget) bool {
	var o api.OnboardingTarget
	_ = json.Unmarshal(old, &o)
	return deref(o.DeploymentId) == "" && deref(o.DeploymentName) != "" && deref(o.DeploymentName) == deref(t.DeploymentName) &&
		o.ProjectId == t.ProjectId && deref(o.Environment) == deref(t.Environment) && deref(o.CatalogItemId) == deref(t.CatalogItemId)
}

// planDestination checks where a plan's VMs go. Into a deployment that
// exists, it gives none of what the deployment has of its own. made is
// the deployment its first run made, if any, which later runs add to.
func (s *Service) planDestination(ctx context.Context, userID string, made *store.OnboardingPlan, t api.OnboardingTarget, add addFunc) (*destination, error) {
	if id := deref(t.DeploymentId); id != "" {
		for _, f := range []struct {
			at    string
			given bool
		}{{"environment", t.Environment != nil}, {"owner_id", t.OwnerId != nil}, {"catalog_item_id", t.CatalogItemId != nil},
			{"lease_days", t.LeaseDays != nil}, {"deployment_name", t.DeploymentName != nil}, {"inputs", t.Inputs != nil}} {
			if f.given {
				add("/target/"+f.at, "not_allowed", "VMs joining a deployment take its own: leave it out.")
			}
		}
		return s.existingDestination(ctx, id, t.ProjectId, deref(t.Component), "/target", add)
	}
	nt := newTarget{projectID: t.ProjectId, environment: deref(t.Environment), ownerID: deref(t.OwnerId),
		itemID: deref(t.CatalogItemId), leaseDays: t.LeaseDays, deploymentName: deref(t.DeploymentName), component: deref(t.Component)}
	if t.Inputs != nil {
		nt.inputs = *t.Inputs
	}
	d, err := s.newDestination(ctx, userID, nt, "/target", add)
	if err != nil || d == nil || made == nil || made.DeploymentID == nil || made.DeploymentName == "" || d.name == "" {
		return d, err
	}
	dep, err := s.Store.GetDeployment(ctx, *made.DeploymentID)
	if errors.Is(err, store.ErrNotFound) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	return s.joining(ctx, dep, deref(t.Component), "/target", add)
}

func (s *Service) DeleteOnboardingPlan(ctx context.Context, p *auth.Principal, id string) error {
	if !p.Everywhere(auth.ImportManage) {
		return ErrForbidden
	}
	pl, err := s.Store.GetOnboardingPlan(ctx, s.Store.Pool, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	runs, err := s.Store.ListOnboardingRuns(ctx, id, 1)
	if err != nil {
		return err
	}
	if len(runs) > 0 && runs[0].Status == "running" {
		return &ConflictError{Msg: "A run of " + pl.Name + " goes on: delete the plan once it ends."}
	}
	return s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.DeleteOnboardingPlan(ctx, tx, id); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "onboarding_plan.deleted",
			TargetType: "onboarding_plan", TargetID: id, TargetName: pl.Name, Detail: map[string]any{"name": pl.Name}})
	})
}

func (s *Service) ListOnboardingRuns(ctx context.Context, p *auth.Principal, planID string) (api.OnboardingRunList, error) {
	out := api.OnboardingRunList{Items: []api.OnboardingRun{}}
	if !p.Everywhere(auth.ImportManage) {
		return out, ErrForbidden
	}
	if _, err := s.Store.GetOnboardingPlan(ctx, s.Store.Pool, planID); errors.Is(err, store.ErrNotFound) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	runs, err := s.Store.ListOnboardingRuns(ctx, planID, keepRuns)
	for _, r := range runs {
		out.Items = append(out.Items, presentOnboardingRun(r, nil))
	}
	return out, err
}

func (s *Service) GetOnboardingRun(ctx context.Context, p *auth.Principal, id string) (api.OnboardingRun, error) {
	if !p.Everywhere(auth.ImportManage) {
		return api.OnboardingRun{}, ErrForbidden
	}
	r, err := s.Store.GetOnboardingRun(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.OnboardingRun{}, ErrNotFound
	}
	if err != nil {
		return api.OnboardingRun{}, err
	}
	vms, err := s.Store.RunVMs(ctx, id)
	if vms == nil {
		vms = []store.OnboardingRunVM{}
	}
	return presentOnboardingRun(r, vms), err
}

// RunOnboardingPlanNow starts a run of a plan.
func (s *Service) RunOnboardingPlanNow(ctx context.Context, p *auth.Principal, id string) (api.OnboardingRun, error) {
	if !p.Everywhere(auth.ImportManage) {
		return api.OnboardingRun{}, ErrForbidden
	}
	pl, err := s.Store.GetOnboardingPlan(ctx, s.Store.Pool, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.OnboardingRun{}, ErrNotFound
	}
	if err != nil {
		return api.OnboardingRun{}, err
	}
	runID, err := s.startRun(ctx, pl, "manual", &p.User.ID)
	if err != nil {
		return api.OnboardingRun{}, err
	}
	r, err := s.Store.GetOnboardingRun(ctx, runID)
	return presentOnboardingRun(r, nil), err
}

// startRun records a run of a plan and starts its workflow.
func (s *Service) startRun(ctx context.Context, pl store.OnboardingPlan, trigger string, actor *string) (string, error) {
	run := store.OnboardingRun{ID: ids.New(ids.OnboardingRun), PlanID: pl.ID, Trigger: trigger, StartedBy: actor}
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.StartOnboardingRun(ctx, tx, run); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: actor, Action: "onboarding_plan.run_started",
			TargetType: "onboarding_plan", TargetID: pl.ID, Detail: map[string]any{"name": pl.Name, "run": run.ID, "trigger": trigger}})
	})
	if errors.Is(err, store.ErrRunning) {
		return "", &ConflictError{Msg: "A run of " + pl.Name + " goes on: wait for it to end."}
	}
	if err != nil {
		return "", err
	}
	if err := s.Workflows.StartOnboarding(ctx, run.ID); err != nil {
		if ferr := s.finishRun(ctx, run.ID, "failed", "The run couldn't start: "+err.Error()); ferr != nil {
			return "", errors.Join(err, ferr)
		}
		return "", &UpstreamError{System: "Temporal", Err: err}
	}
	return run.ID, nil
}

// StartDueOnboardingPlans starts the runs of the plans whose schedule is
// due (housekeeping), and lists them. A plan still running skips a round.
func (s *Service) StartDueOnboardingPlans(ctx context.Context, now time.Time) ([]string, error) {
	due, err := s.Store.ClaimDuePlans(ctx, now)
	if err != nil {
		return nil, err
	}
	var started []string
	var errs []error
	for _, id := range due {
		pl, err := s.Store.GetOnboardingPlan(ctx, s.Store.Pool, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		runID, err := s.startRun(ctx, pl, "schedule", nil)
		var ce *ConflictError
		switch {
		case errors.As(err, &ce):
		case err != nil:
			errs = append(errs, fmt.Errorf("plan %s: %w", pl.Name, err))
		default:
			started = append(started, runID)
		}
	}
	return started, errors.Join(errs...)
}

// FailOnboardingRun ends a run that couldn't go on; what it did stays.
func (s *Service) FailOnboardingRun(ctx context.Context, runID, msg string) error {
	return s.finishRun(ctx, runID, "failed", msg)
}

// finishRun ends a running run, audits it and keeps the plan's latest runs.
func (s *Service) finishRun(ctx context.Context, runID, status, msg string) error {
	ok, err := s.Store.FinishOnboardingRun(ctx, s.Store.Pool, runID, status, msg)
	if err != nil || !ok {
		return err
	}
	r, err := s.Store.GetOnboardingRun(ctx, runID)
	if err != nil {
		return err
	}
	detail := map[string]any{"name": r.PlanName, "run": r.ID, "status": r.Status, "adopted": r.Adopted, "skipped": r.Skipped,
		"failed": r.Failed}
	if msg != "" {
		detail["error"] = msg
	}
	if err := s.Store.Audit(ctx, s.Store.Pool, store.AuditEvent{ActorID: r.StartedBy, Action: "onboarding_plan.run_finished",
		TargetType: "onboarding_plan", TargetID: r.PlanID, Detail: detail}); err != nil {
		return err
	}
	return s.Store.PruneOnboardingRuns(ctx, r.PlanID, keepRuns)
}

// RunOnboarding runs a plan's run (provision.OnboardingRun's activity):
// each VM its filter matches is adopted, or skipped or failed with why.
// beat tells Temporal it goes on. Run again, it goes on where it was.
func (s *Service) RunOnboarding(ctx context.Context, runID string, beat func()) error {
	run, err := s.Store.GetOnboardingRun(ctx, runID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil || run.Status != "running" {
		return err
	}
	pl, err := s.Store.GetOnboardingPlan(ctx, s.Store.Pool, run.PlanID)
	if err != nil {
		return err
	}
	var filter api.OnboardingFilter
	var target api.OnboardingTarget
	_ = json.Unmarshal(pl.Filter, &filter)
	_ = json.Unmarshal(pl.Target, &target)
	var problems []string
	add := func(_, _, msg string) { problems = append(problems, msg) }
	dest, err := s.planDestination(ctx, "", &pl, target, add)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return s.finishRun(ctx, runID, "failed", "Where its VMs go doesn't work now: "+strings.Join(problems, " "))
	}
	if vmFilter(filter) == (store.InvFilter{}) {
		return s.finishRun(ctx, runID, "failed", "Its filter has no criterion.")
	}
	matched, err := s.Store.MatchingVMs(ctx, vmFilter(filter))
	if err != nil {
		return err
	}
	done, err := s.Store.RunVMs(ctx, runID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, v := range done {
		seen[v.VCenter+"/"+v.Moref] = true
	}
	failed := false
	for _, m := range matched {
		if seen[m.VCenter+"/"+m.Moref] {
			continue
		}
		if beat != nil {
			beat()
		}
		out, depID, err := s.adoptMatched(ctx, run, pl, dest, m)
		if err != nil {
			return err
		}
		if err := s.Store.RecordRunVM(ctx, s.Store.Pool, out); err != nil {
			return err
		}
		failed = failed || out.Outcome == "failed"
		if out.Outcome != "adopted" {
			continue
		}
		s.Events.Deployment(ctx, depID)
		// All in one deployment: the first VM made it, the next join it.
		if dest.existing == nil && dest.name != "" {
			dep, err := s.Store.GetDeployment(ctx, depID)
			if err != nil {
				return err
			}
			if dest, err = s.joining(ctx, dep, deref(target.Component), "", add); err != nil {
				return err
			}
		}
	}
	return s.finishRun(ctx, runID, map[bool]string{false: "succeeded", true: "partial"}[failed], "")
}

// adoptMatched adopts a VM a plan matched, or says why not.
func (s *Service) adoptMatched(ctx context.Context, run store.OnboardingRun, pl store.OnboardingPlan, dest *destination,
	m store.MatchedVM) (store.OnboardingRunVM, string, error) {
	out := store.OnboardingRunVM{RunID: run.ID, VCenter: m.VCenter, Moref: m.Moref, Name: m.Name}
	result := func(outcome, code, reason string) (store.OnboardingRunVM, string, error) {
		out.Outcome, out.Code, out.Reason = outcome, code, reason
		return out, "", nil
	}
	switch {
	case m.DeploymentID != "":
		out.DeploymentID = &m.DeploymentID
		return result("skipped", "managed", m.DeploymentName+" has it.")
	case m.Building:
		return result("skipped", "building", "A request is building it.")
	}
	a := adoptee{inv: m.UnmanagedVM}
	err := s.lookUp(ctx, &a)
	var gone *goneError
	var ue *UpstreamError
	switch {
	case errors.As(err, &gone):
		return result("failed", "not_found", gone.Error())
	case errors.As(err, &ue):
		return result("failed", "upstream", ue.Error())
	case err != nil:
		return out, "", err
	}
	detail := map[string]any{"plan": pl.ID, "plan_name": pl.Name, "run": run.ID}
	group := []*adoptee{&a}
	var depID string
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if dest.existing != nil {
			res, err := s.joinDeployment(ctx, tx, dest, group, run.StartedBy, detail)
			depID = res.Id
			return err
		}
		inputs, m := fit(dest.fitting(nil, group))
		if m != nil {
			return m
		}
		if inputs == nil {
			inputs = dest.inputs
		}
		name := cmp.Or(dest.name, a.inv.Name)
		res, err := s.createDeployment(ctx, tx, dest, name, group, inputs, run.StartedBy, detail)
		if err != nil {
			return err
		}
		depID = res.Id
		if dest.name != "" {
			return s.Store.SetPlanDeployment(ctx, tx, pl.ID, depID)
		}
		return nil
	})
	var taken *takenError
	var mf *misfit
	switch {
	case errors.As(err, &taken) && taken.vm:
		return result("skipped", "managed", "Another deployment took it meanwhile.")
	case errors.As(err, &taken):
		return result("failed", "taken", taken.Error())
	case errors.As(err, &mf):
		return result("failed", mf.code, mf.msg)
	case err != nil:
		return out, "", err
	}
	out.Outcome, out.DeploymentID = "adopted", &depID
	return out, depID, nil
}
