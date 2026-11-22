package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/secretinput"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vmprops"
)

// Updates of a deployment to its item's version and new inputs
// (ADR-0042). What changes is what the new version makes of the new
// inputs against what the deployment's version made of its own; it is
// applied to the VMs as they are now, with the operations of day-2 actions,
// and VMs are added as a new request builds them.

// updatePlan is what an update of a deployment does.
type updatePlan struct {
	d      store.DeploymentRow
	from   store.ItemVersion
	ev     *Evaluation
	action *day2.Action
	target day2.Target
	// reason is why the caller can't update the deployment now.
	reason string
	dec    decision
	// same: the version and inputs are the deployment's, which isn't failed.
	same bool
	// vms are the IDs of the deployment's VMs it was planned on.
	vms []string
	// change is what the update counts against the quotas and the limits.
	change *quotaChange
	componentPlan
}

// componentPlan is what an update does to the deployment's components.
type componentPlan struct {
	changes []api.UpdateChange
	refused []api.UpdateRefusal
	ops     []day2.Op
	added   []api.UpdateChange
	removed []api.UpdateChange
	remove  []day2.Op
	jobs    []provision.UpdateJobs
	ran     []api.UpdateChange
	kept    []provision.KeptVM
	add     []addVMs
	delta   map[string]int
	// marks are, by VM resource, its kept disks' places and keys in the
	// new version, where they change (ADR-0052).
	marks map[string][]vmprops.DiskMark
	// resources are the custom resources made or updated, removeFirst the
	// deletes of those dropped, before the VMs they may use (ADR-0056).
	resources   []provision.ResourceUnit
	removeFirst []day2.Op
	custom      []api.UpdateChange
}

func (u *componentPlan) mark(resource string, m vmprops.DiskMark) {
	if u.marks == nil {
		u.marks = map[string][]vmprops.DiskMark{}
	}
	u.marks[resource] = append(u.marks[resource], m)
}

// addVMs are VMs an update adds to a component, from its index first.
type addVMs struct {
	part  vmPart
	first int
	n     int
}

// existingVM is one of the deployment's VMs.
type existingVM struct {
	t             day2.Target
	preventDelete bool
}

func (v existingVM) name() string { return v.t.ResourceName }

// adopted: rvav didn't clone the VM, which has no template recorded.
func (v existingVM) adopted() bool { return deref(v.t.VM.Template) == "" }

// byIndex orders the VMs rvav built by their index, then those adopted:
// scale in removes the last.
func byIndex(a, b existingVM) int {
	switch {
	case a.adopted() == b.adopted():
		return naturalCompare(a.name(), b.name())
	case a.adopted():
		return 1
	}
	return -1
}

// componentChange compares one component's VMs as the deployment's
// version made them with what the new version makes of them.
type componentChange struct {
	key, title string
	// had: the deployment's version has the component.
	had      bool
	old, new blueprint.VMPlan
	// oldGuest and newGuest are its guest settings.
	oldGuest, newGuest blueprint.Guest
	jobs               []provision.Job
	count              int
	vms                []existingVM
	resources          []day2.Resource
	// failed: the deployment failed, so its jobs run again on the VMs kept.
	failed       bool
	antiAffinity bool
	// cluster is where new VMs go; segments: they would be on NSX
	// segments made for the deployment.
	cluster  string
	segments bool
}

// PreviewUpdate says what updating the deployment to this version and
// these inputs would do.
func (s *Service) PreviewUpdate(ctx context.Context, p *auth.Principal, deploymentID string, draft api.UpdateDraft) (api.UpdatePreview, error) {
	u, err := s.planUpdate(ctx, p, deploymentID, draft, false)
	if err != nil {
		return api.UpdatePreview{}, err
	}
	ev := u.ev
	sort.SliceStable(ev.Errors, func(i, j int) bool { return ev.Errors[i].Field < ev.Errors[j].Field })
	out := api.UpdatePreview{
		FromVersion: versionRef(u.from), ToVersion: versionRef(ev.Version), Allowed: u.reason == "", Unchanged: u.same,
		Changes: nonNilChanges(u.all()), Refused: u.refused, Errors: ev.Errors, Quota: ev.Quota, Approval: ev.Approval,
	}
	if out.Refused == nil {
		out.Refused = []api.UpdateRefusal{}
	}
	if out.Errors == nil {
		out.Errors = []api.FieldError{}
	}
	if len(ev.Fields) > 0 {
		out.Fields = &ev.Fields
	}
	if u.reason != "" {
		out.Reason = &u.reason
	}
	if len(u.dec.Warnings) > 0 {
		out.Warnings = &u.dec.Warnings
	}
	if len(ev.Limits) > 0 {
		out.Limits = &ev.Limits
	}
	return out, nil
}

// UpdateDeployment asks for the update of a deployment to this version and
// these inputs: a request of kind update, approved and run as a new one.
func (s *Service) UpdateDeployment(ctx context.Context, p *auth.Principal, deploymentID string, draft api.UpdateDraft) (store.RequestRow, error) {
	return s.placing(ctx, func() (store.RequestRow, error) { return s.updateDeployment(ctx, p, deploymentID, draft) })
}

func (s *Service) updateDeployment(ctx context.Context, p *auth.Principal, deploymentID string, draft api.UpdateDraft) (store.RequestRow, error) {
	u, err := s.planUpdate(ctx, p, deploymentID, draft, true)
	if err != nil {
		return store.RequestRow{}, err
	}
	ev, d := u.ev, u.d
	switch {
	case u.dec.Denied != "":
		return store.RequestRow{}, fmt.Errorf("%w: %s", ErrForbidden, u.dec.Denied)
	case u.reason != "":
		return store.RequestRow{}, &ConflictError{Msg: u.reason}
	case len(ev.Errors) > 0:
		return store.RequestRow{}, &ValidationError{Errors: ev.Errors}
	case len(u.refused) > 0:
		var errs []api.FieldError
		for _, r := range u.refused {
			errs = append(errs, fieldErr("", "refused", r.Reason))
		}
		return store.RequestRow{}, &ValidationError{Errors: errs}
	case u.same:
		return store.RequestRow{}, &ValidationError{Errors: []api.FieldError{fieldErr("", "no_change",
			d.Name+" already has this version and these inputs.")}}
	}
	if err := accepted(u.dec.Warnings, draft.AcceptWarnings, draft.AcceptedWarnings); err != nil {
		return store.RequestRow{}, err
	}
	chain := append(slices.Clone(u.dec.Chain), ev.Chain...)
	req := store.Request{
		ID: ids.New(ids.Request), ItemID: d.ItemID, VersionID: ev.Version.ID, ProjectID: d.ProjectID,
		Environment: d.Environment, Title: u.action.Name + " · " + d.Name, Status: "queued",
		RequesterID: p.User.ID, OwnerID: d.OwnerID, Count: 1, SubmittedAt: time.Now().UTC(),
		Kind: "update", Action: &u.action.Key, DeploymentID: &d.ID, ApprovalRequired: len(chain) > 0,
	}
	if req.ApprovalRequired {
		req.Status = "pending_approval"
	}
	if draft.Justification != nil {
		req.Justification = strings.TrimSpace(*draft.Justification)
	}
	wf := provision.WorkflowID(req.ID)
	req.WorkflowID = &wf
	req.Approval, _ = json.Marshal(ev.Approval)
	req.Approval = withWarnings(req.Approval, u.dec.Warnings)
	quota := map[string]int{}
	for k, v := range u.delta {
		quota[k] = max(v, 0)
	}
	plan := provision.Plan{Kind: blueprint.KindVM, ApprovalRequired: req.ApprovalRequired, AutoApproved: ev.AutoApproved,
		Quota: quota, Guest: ev.Blueprint.Guest, Update: &provision.Update{DeploymentID: d.ID, Before: d.Status,
			Ops: u.ops, Remove: slices.Concat(u.removeFirst, u.remove), Kept: u.kept, Jobs: u.jobs, VMs: u.vms, DiskMarks: u.marks},
		Resources: u.resources}

	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.holdChange(ctx, tx, u.change, ev.OverSoft, draft.AcceptWarnings, draft.AcceptedWarnings); err != nil {
			return err
		}
		if err := s.holdVersion(ctx, tx, ev.Version.ID); err != nil {
			return err
		}
		if err := s.Store.LockDeployment(ctx, tx, d.ID); err != nil {
			return err
		}
		active, err := s.Store.ActiveDeploymentRequest(ctx, tx, d.ID, "", "", true)
		if err != nil {
			return err
		}
		now, err := s.Store.GetDeploymentIn(ctx, tx, d.ID)
		if err != nil {
			return err
		}
		if reason := deploymentBusy(now, active, u.action, false); reason != "" {
			return &ConflictError{Msg: reason}
		}
		if now.VersionID != d.VersionID || string(now.Inputs) != string(d.Inputs) {
			return &ConflictError{Msg: d.Name + " changed while the update was planned: look at the plan again."}
		}
		if req.Number, err = s.Store.NextRequestNumber(ctx, tx); err != nil {
			return err
		}
		if _, err := s.sealSecretInputs(ctx, tx, req.ID, ev.Version.UISchema, ev.Inputs); err != nil {
			return err
		}
		if plan.VMs, plan.Components, plan.PostConfig, err = s.addVMs(ctx, tx, u); err != nil {
			return err
		}
		plan.Context = map[string]any{
			"request_id": req.ID, "request_number": req.Number, "project": ev.Project.Code,
			"environment": ev.Env.Key, "requester": p.User.Email,
		}
		req.Plan, _ = json.Marshal(plan)
		req.Inputs, _ = json.Marshal(ev.Inputs)
		if err := s.Store.InsertRequest(ctx, tx, req); err != nil {
			return err
		}
		if err := s.attachFiles(ctx, tx, req.ID, p.User.ID, ev); err != nil {
			return err
		}
		if err := s.Store.InsertSteps(ctx, tx, stepsOf(req.ID, plan.Steps(ev.Env.Name))); err != nil {
			return err
		}
		if err := s.Store.InsertApprovals(ctx, tx, chainRows(req.ID, chain)); err != nil {
			return err
		}
		if err := outbox.Submitted(ctx, tx, req.ID); err != nil {
			return err
		}
		if err := s.auditWarnings(ctx, tx, p, req, u.dec.Warnings); err != nil {
			return err
		}
		kinds := []string{}
		for _, c := range u.all() {
			kinds = append(kinds, string(c.Kind))
		}
		if err := s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "deployment.update", TargetType: "deployment",
			TargetID: d.ID, ProjectID: &d.ProjectID, Detail: map[string]any{
				"request": req.Number, "from_version": u.from.Number, "to_version": ev.Version.Number, "changes": kinds}}); err != nil {
			return err
		}
		// The VMs it adds; last, so that their clusters are held the least time.
		return s.holdPlacement(ctx, tx, ev, req.ID, plan.Reservations())
	})
	if err != nil {
		return store.RequestRow{}, err
	}
	if err := s.Workflows.Start(ctx, req.ID); err != nil {
		s.Log.Error("start workflow", "request", req.Number, "err", err)
		if derr := s.Store.DeleteUnstartedRequest(ctx, req.ID); derr != nil {
			s.Log.Error("remove unstarted request", "request", req.Number, "err", derr)
		}
		return store.RequestRow{}, &UpstreamError{System: "Temporal", Err: err}
	}
	if err := s.Store.ReleaseRequestEvents(context.WithoutCancel(ctx), req.ID); err != nil {
		s.Log.Error("release the request's events", "request", req.Number, "err", err)
	}
	s.publishRequest(ctx, req.ID)
	return s.Store.GetRequest(ctx, req.ID)
}

// planUpdate plans the update of a deployment: who may make it now, what
// it changes, what it refuses, its quota and approval.
func (s *Service) planUpdate(ctx context.Context, p *auth.Principal, deploymentID string, draft api.UpdateDraft, strict bool) (*updatePlan, error) {
	d, err := s.visibleDeployment(ctx, p, deploymentID)
	if err != nil {
		return nil, err
	}
	u := &updatePlan{d: d, action: day2.Get(day2.KeyUpdate)}
	resources, err := s.Store.ListResources(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	all := targets(d, resources)
	u.target = all[0]
	if !u.action.Applies(u.target) {
		return nil, &ConflictError{Msg: "Only deployments of VM items can be updated."}
	}
	j, err := s.judge(ctx, p, d)
	if err != nil {
		return nil, err
	}
	active, err := s.Store.ActiveDeploymentRequest(ctx, s.Store.Pool, d.ID, "", "", true)
	if err != nil {
		return nil, err
	}
	u.reason = deploymentBusy(d, active, u.action, false)
	if !j.offered(u.action, u.target) && u.reason == "" {
		u.reason = u.action.Name + " is not offered for " + d.Name + "."
	}
	if u.from, err = s.Store.GetVersion(ctx, d.VersionID); err != nil {
		return nil, err
	}
	inputs := maps.Clone(draft.Inputs)
	if inputs == nil {
		inputs = map[string]any{}
	}
	// On its own version, a field the deployment has no input for was
	// emptied: it stays so unless the update gives it a value.
	if draft.VersionId == d.VersionID {
		var had map[string]any
		_ = json.Unmarshal(d.Inputs, &had)
		var ui api.UiSchema
		_ = json.Unmarshal(u.from.UISchema, &ui)
		keepEmptied(ui, had, inputs)
	}
	ev, err := s.evaluateKeeping(ctx, p, api.RequestDraft{CatalogItemId: d.ItemID, VersionId: draft.VersionId, ProjectId: d.ProjectID,
		Environment: d.Environment, Inputs: inputs, Justification: draft.Justification}, strict, false, strict, filesOf(d.Inputs), nil)
	if errors.Is(err, ErrNotFound) {
		return nil, &ConflictError{Msg: d.ItemName + " is no longer offered in " + d.ProjectName + ": its deployments can't be updated."}
	}
	if err != nil {
		return nil, err
	}
	u.ev = ev
	if err := s.compare(ctx, u, resources, all[1:]); err != nil {
		return nil, err
	}
	if m, ok := deploymentNSX(resources); ok && len(u.add) > 0 && ev.nsxFound && m != ev.nsxManager {
		u.refused = append(u.refused, api.UpdateRefusal{Setting: present.Ptr("count"), Reason: fmt.Sprintf(
			"The deployment's NSX security groups are on NSX Manager %s, but its vCenter is now served by %s: an update can't add VMs to them.",
			s.nsxLabel(m), s.nsxLabel(ev.nsxManager))})
	}
	// A failed deployment's update with its own inputs is a repair: it
	// builds what is missing and runs its jobs again.
	u.same = d.Status != "failed" && ev.Version.ID == d.VersionID && sameInputs(ev.Version.UISchema, d.Inputs, ev.Inputs)

	// Quota on the net change; approval policies see the update.
	u.change = &quotaChange{scope: ownerScope(p, ev.Project, ev.Env, d, nil), what: "This update", add: u.delta,
		limits: updateAsk(p, ev, d, u)}
	q, err := s.quotaCheck(ctx, s.Store.Pool, u.change.scope, u.change.what, u.change.add)
	if err != nil {
		return nil, err
	}
	ev.Quota, ev.OverSoft = q.Impacts, q.OverSoft
	ev.Errors = slices.DeleteFunc(ev.Errors, func(e api.FieldError) bool { return deref(e.Code) == "quota_exceeded" })
	for _, msg := range q.Hard {
		ev.Errors = append(ev.Errors, fieldErr("", "quota_exceeded", msg))
	}
	limits, err := s.updateLimits(ctx, ev, u)
	if err != nil {
		return nil, err
	}
	ev.QuotaAdd = map[string]int{}
	for k, v := range u.delta {
		ev.QuotaAdd[k] = max(v, 0)
	}
	ev.Update = u.vars()
	if err := s.evaluateApproval(ctx, p, ev, strict); err != nil {
		return nil, err
	}
	u.dec = j.decideQuota(u.action, u.target, nil, ev.OverSoft)
	if u.dec.Denied != "" && u.reason == "" {
		u.reason = u.dec.Denied
	}
	u.dec.Warnings = append(u.dec.Warnings, limits...)
	if err := s.day2Approval(ctx, ev, u.dec.Chain); err != nil {
		return nil, err
	}
	return u, nil
}

// day2Approval adds the approval the day-2 policies ask for an update to
// what the preview shows.
func (s *Service) day2Approval(ctx context.Context, ev *Evaluation, chain []ChainLevel) error {
	if len(chain) == 0 {
		return nil
	}
	var idList []string
	for _, l := range chain {
		idList = append(idList, l.Approvers...)
	}
	users, err := s.Store.FindUsers(ctx, idList)
	if err != nil {
		return err
	}
	type lv struct {
		Approvers []api.UserRef `json:"approvers"`
		Name      string        `json:"name,omitempty"`
		Mode      string        `json:"mode,omitempty"`
	}
	type pol struct {
		Name     string `json:"name"`
		PolicyID string `json:"policy_id"`
		Reason   string `json:"reason"`
		Levels   []lv   `json:"levels"`
	}
	var list []pol
	for _, l := range chain {
		e := pol{Name: cmp.Or(l.Name, "Day-2 policy"), Reason: strings.Join(l.Reasons, " "), Levels: []lv{{Approvers: []api.UserRef{}, Name: l.Name, Mode: l.Mode}}}
		if len(l.PolicyIDs) > 0 {
			e.PolicyID = l.PolicyIDs[0]
		}
		for _, id := range l.Approvers {
			if u, ok := users[id]; ok {
				e.Levels[0].Approvers = append(e.Levels[0].Approvers, present.UserRef(u.ID, u.Name, u.Email))
			}
		}
		list = append(list, e)
	}
	raw, _ := json.Marshal(map[string]any{"required": true, "policies": list})
	var extra api.ApprovalPreview
	if err := json.Unmarshal(raw, &extra); err != nil {
		return err
	}
	ev.Approval.Policies = append(ev.Approval.Policies, extra.Policies...)
	ev.Approval.Required = true
	return nil
}

// compare plans each component: its VMs changed in place, added or
// removed, and the jobs run on those kept.
func (s *Service) compare(ctx context.Context, u *updatePlan, resources []store.Resource, vmTargets []day2.Target) error {
	ev := u.ev
	old, err := blueprint.Parse(u.from.Blueprint)
	if err != nil {
		return err
	}
	old = old.WithContext(policy.Vars{"project": projectVars(ev.Project), "propgroup": groupVars(u.from)})
	if len(old.Sizes) == 0 {
		sizes, err := s.offeredSizes(ctx, ev.Project.ID, ev.Env.Key)
		if err != nil {
			return err
		}
		old.Sizes = map[string]blueprint.Size{}
		for _, z := range sizes {
			old.Sizes[z.Key] = blueprint.Size{Label: z.Label, VCPU: z.VCPU, MemoryGB: z.MemoryGB, DiskGB: z.DiskGB}
		}
	}
	var oldInputs map[string]any
	_ = json.Unmarshal(u.d.Inputs, &oldInputs)
	// A deployment adopted without inputs is compared as if requested with
	// its version's initial values. One with some keeps them, as a key left
	// out can be a default the requester emptied.
	if len(oldInputs) == 0 {
		_ = json.Unmarshal(u.from.InitialValues, &oldInputs)
	}
	if old.Multi() != ev.Blueprint.Multi() {
		u.refused = append(u.refused, api.UpdateRefusal{Setting: present.Ptr("components"),
			Reason: "The new version splits the VM into components, or the components into one VM: request a new deployment instead."})
		return nil
	}
	oldComps := map[string]blueprint.Component{}
	for _, c := range old.ComponentList() {
		oldComps[c.Key] = c
	}
	prevent := map[string]bool{}
	for _, r := range resources {
		prevent[r.ID] = r.PreventDelete
	}
	byComp := map[string][]existingVM{}
	var dep []day2.Resource
	u.vms = []string{}
	for _, t := range vmTargets {
		dep = t.Resources
		if t.Type != "vsphere.vm" {
			continue
		}
		u.vms = append(u.vms, t.ResourceID)
		key := cmp.Or(deref(t.VM.Component), blueprint.SingleKey)
		byComp[key] = append(byComp[key], existingVM{t: t, preventDelete: prevent[t.ResourceID]})
	}
	for _, list := range byComp {
		slices.SortFunc(list, byIndex)
	}
	order := optionOrder(ev.Version.UISchema, "post_config")
	u.delta = map[string]int{"vcpu": 0, "memory_gb": 0, "storage_gb": 0, "vm_count": 0}
	seen := map[string]bool{}
	for _, part := range ev.Parts {
		key := part.comp.Key
		seen[key] = true
		newPlan, _, _ := ev.Blueprint.EvaluateComponent(part.comp, ev.Inputs)
		jobs, err := postConfigJobs(ev, newPlan, order)
		if err != nil {
			return err
		}
		count := part.count
		// A blueprint without components has no count of its own: its
		// deployment keeps the VMs it was built or adopted with (X1.5).
		if !ev.Blueprint.Multi() {
			count = max(count, len(byComp[key]))
		}
		c := componentChange{key: key, title: part.title(), new: newPlan, newGuest: part.guest, jobs: jobs, count: count,
			vms: byComp[key], resources: dep, failed: u.d.Status == "failed", antiAffinity: part.comp.AntiAffinity,
			cluster: part.vm.Cluster, segments: part.segment != "" || slices.ContainsFunc(part.nicSegments, func(s string) bool { return s != "" })}
		if oc, ok := oldComps[key]; ok {
			c.had, c.oldGuest = true, *oc.Guest
			c.old, _, _ = old.EvaluateComponent(oc, oldInputs)
		}
		if n := c.plan(&u.componentPlan); n > 0 {
			u.add = append(u.add, addVMs{part: part, first: len(c.vms), n: n})
		}
	}
	// Components the new version no longer has lose their VMs.
	for _, key := range slices.Sorted(maps.Keys(byComp)) {
		if !seen[key] {
			c := componentChange{key: key, title: key, vms: byComp[key], resources: dep}
			c.plan(&u.componentPlan)
		}
	}
	return s.compareResources(ctx, u, old, oldInputs, resources)
}

// all are the changes in the order they run.
func (u *componentPlan) all() []api.UpdateChange {
	return slices.Concat(u.changes, u.added, u.removed, u.custom, u.ran)
}

// vars are what approval policies read of the update as `update`.
func (u *updatePlan) vars() map[string]any {
	kinds := []any{}
	destructive := false
	for _, c := range u.all() {
		if !slices.Contains(kinds, any(string(c.Kind))) {
			kinds = append(kinds, string(c.Kind))
		}
		destructive = destructive || c.Destructive
	}
	return map[string]any{
		"version_changed": u.ev.Version.ID != u.d.VersionID, "from_version": u.from.Number, "to_version": u.ev.Version.Number,
		"vcpu": u.delta["vcpu"], "memory_gb": u.delta["memory_gb"], "storage_gb": u.delta["storage_gb"], "vm_count": u.delta["vm_count"],
		"added": len(u.added), "removed": len(u.removed), "changes": kinds, "destructive": destructive,
	}
}

// requestVars are what approval policies read as `request`.
func (ev *Evaluation) requestVars() map[string]any {
	if ev.Update != nil {
		return map[string]any{"kind": "update"}
	}
	return map[string]any{"kind": "provision"}
}

// updateVars are what approval policies read as `update`.
func (ev *Evaluation) updateVars() map[string]any {
	if ev.Update != nil {
		return ev.Update
	}
	return noUpdate()
}

// noUpdate is what approval policies read as `update` on a new request.
func noUpdate() map[string]any {
	return map[string]any{"version_changed": false, "from_version": "", "to_version": "", "vcpu": 0, "memory_gb": 0,
		"storage_gb": 0, "vm_count": 0, "added": 0, "removed": 0, "changes": []any{}, "destructive": false}
}

// plan adds what the update does to the component's VMs, and returns how
// many VMs it adds.
func (c componentChange) plan(u *componentPlan) int {
	refuse := func(setting, reason string) {
		u.refused = append(u.refused, api.UpdateRefusal{Component: present.Ptr(c.key), Setting: present.Ptr(setting), Reason: c.label() + reason})
	}
	keep := min(len(c.vms), c.count)
	if c.had && keep > 0 {
		c.inPlace(u, c.vms[:keep], refuse)
	}
	for i, vm := range c.vms[:keep] {
		u.kept = append(u.kept, keptVM(c.key, vm, i))
	}
	switch n := c.count - len(c.vms); {
	case n > 0:
		if c.segments {
			refuse("count", "New VMs would be on NSX segments made for the deployment, which an update can't add VMs to.")
		}
		if c.antiAffinity && keep > 0 && c.cluster != "" && deref(c.vms[0].t.VM.Cluster) != "" && c.cluster != deref(c.vms[0].t.VM.Cluster) {
			refuse("cluster", fmt.Sprintf("New VMs would go to %s, but those they are kept apart from are in %s.", c.cluster, deref(c.vms[0].t.VM.Cluster)))
		}
		title := "Add a VM to " + c.title
		if c.key == blueprint.SingleKey {
			title = "Build the deployment's VM again"
		}
		for i := range n {
			u.added = append(u.added, api.UpdateChange{Kind: api.VmAdd, Component: present.Ptr(c.key), Title: title,
				Detail: present.Ptr(fmt.Sprintf("VM %d of %d: %d vCPU, %d GB of memory, %d GB of storage, named when the update is asked for.",
					len(c.vms)+i+1, c.count, c.new.VCPU, c.new.MemoryGB, c.new.StorageGB()))})
		}
		u.delta["vcpu"] += n * c.new.VCPU
		u.delta["memory_gb"] += n * c.new.MemoryGB
		u.delta["storage_gb"] += n * c.new.StorageGB()
		u.delta["vm_count"] += n
		return n
	case n < 0:
		c.scaleIn(u, refuse)
	}
	return 0
}

func (c componentChange) label() string {
	if c.key == blueprint.SingleKey {
		return ""
	}
	return c.title + ": "
}

// keptVM is a VM kept, index its place in its component.
func keptVM(component string, vm existingVM, index int) provision.KeptVM {
	k := provision.KeptVM{Component: component, Index: index, Name: vm.name(), FQDN: deref(vm.t.VM.Fqdn), Moref: vm.t.VM.Moref}
	for _, n := range vm.t.VM.Nics {
		if n.IpAddresses == nil {
			continue
		}
		for _, ip := range *n.IpAddresses {
			if !strings.Contains(ip, ":") && k.IP == "" {
				k.IP = ip
			}
		}
	}
	return k
}

// scaleIn removes the component's last VMs, with their own records; the
// DRS rule keeping them apart is set again on those left first.
func (c componentChange) scaleIn(u *componentPlan, refuse func(setting, reason string)) {
	keep := max(c.count, 0)
	gone := c.vms[keep:]
	for _, vm := range gone {
		if vm.preventDelete {
			refuse("count", vm.name()+" is protected from deletion: allow its deletion first.")
		}
	}
	for _, r := range c.resources {
		if r.Type != "vsphere.drs_rule" || ruleComponent(r) != c.key {
			continue
		}
		left := c.vms[:keep]
		if len(left) >= 2 {
			var names, morefs []string
			for _, vm := range left {
				names, morefs = append(names, vm.name()), append(morefs, vm.t.VM.Moref)
			}
			var p struct {
				Rule struct {
					Cluster string `json:"cluster"`
					VCenter string `json:"vcenter"`
				} `json:"rule"`
			}
			_ = json.Unmarshal(r.Properties, &p)
			u.remove = append(u.remove, day2.Op{Key: "vsphere.drs_rule@" + c.key, Title: "Keep " + strings.Join(names, ", ") + " on separate hosts",
				Detail: "DRS rule " + r.Name, System: "vcenter", Op: "set_drs_rule", Args: map[string]any{
					"name": r.Name, "cluster": p.Rule.Cluster, "vcenter": p.Rule.VCenter, "component": c.key, "vms": names, "morefs": morefs}})
		} else {
			u.remove = append(u.remove, day2.Op{Key: "vsphere.drs_rule.delete@" + c.key, Title: "Remove the DRS rule " + r.Name,
				System: "vcenter", Op: "delete_resources", Args: map[string]any{"type": "vsphere.drs_rule", "resource": r.ID}})
		}
	}
	for _, vm := range gone {
		self := day2.Resource{ID: vm.t.ResourceID, Type: "vsphere.vm", Name: vm.name(), VM: vm.t.VM}
		name := vm.name()
		if ids := day2.OwnResourceIDs(self, c.resources, "nsx.membership"); len(ids) > 0 {
			u.remove = append(u.remove, day2.Op{Key: "nsx.membership.delete@" + name, Title: "Take " + name + " out of its NSX groups",
				System: "nsx", Op: "delete_resources", Args: map[string]any{"type": "nsx.membership", "resources": ids}})
		}
		u.remove = append(u.remove, day2.Op{Key: "vsphere.destroy@" + name, Title: "Delete VM " + name, System: "vcenter",
			Op: "destroy_vm", Args: map[string]any{"resource": vm.t.ResourceID}})
		if ids := day2.OwnResourceIDs(self, c.resources, "dns.record"); len(ids) > 0 {
			u.remove = append(u.remove, day2.Op{Key: "dns.delete@" + name, Title: "Delete the DNS records of " + name,
				System: "efficientip", Op: "delete_resources", Args: map[string]any{"type": "dns.record", "resources": ids}})
		}
		if ids := day2.OwnResourceIDs(self, c.resources, "ipam.address"); len(ids) > 0 {
			u.remove = append(u.remove, day2.Op{Key: "ipam.release@" + name, Title: "Release the IP addresses of " + name,
				System: "efficientip", Op: "delete_resources", Args: map[string]any{"type": "ipam.address", "resources": ids}})
		}
		storage := 0
		for _, d := range vm.t.VM.Disks {
			storage += d.CapacityGb
		}
		u.removed = append(u.removed, api.UpdateChange{Kind: api.VmRemove, Component: present.Ptr(c.key), Vm: present.Ptr(name),
			Title: "Delete " + name, Detail: present.Ptr("With its disks, address and DNS records."), Destructive: true})
		u.delta["vcpu"] -= vm.t.VM.CpuCount
		u.delta["memory_gb"] -= vm.t.VM.MemoryMb / 1024
		u.delta["storage_gb"] -= storage
		u.delta["vm_count"]--
	}
}

func vmCluster(vm api.VmProperties) string { return deref(vm.Cluster) }

func vmDatastore(vm api.VmProperties) string {
	if len(vm.Disks) == 0 {
		return ""
	}
	return deref(vm.Disks[0].Datastore)
}

func vmNetwork(vm api.VmProperties) string {
	if len(vm.Nics) == 0 {
		return ""
	}
	return vm.Nics[0].Network
}

// vmDisks are the disks of a VM rvav recorded as its blueprint's (ADR-0042).
type vmDisks []api.VmDisk

func blueprintDisks(t day2.Target) vmDisks {
	var out vmDisks
	for _, d := range t.VM.Disks {
		if d.BlueprintDisk != nil && d.Key != nil {
			out = append(out, d)
		}
	}
	return out
}

func (v vmDisks) first() (api.VmDisk, bool) {
	i := slices.IndexFunc(v, func(d api.VmDisk) bool { return *d.BlueprintDisk == 0 })
	if i < 0 {
		return api.VmDisk{}, false
	}
	return v[i], true
}

// of is the VM's disk that is the version's extra disk d, at place i: by
// its recorded key when the disks match by key, else by its recorded place
// unless recorded with another key (ADR-0052). A disk of the old version
// recorded before that version had keys is found by its place.
func (v vmDisks) of(d blueprint.DiskPlan, i int, byKey, old bool) (api.VmDisk, bool) {
	match := func(x api.VmDisk) bool {
		return *x.BlueprintDisk == i+1 && (x.BlueprintDiskKey == nil || *x.BlueprintDiskKey == d.Key)
	}
	if byKey {
		match = func(x api.VmDisk) bool { return deref(x.BlueprintDiskKey) == d.Key }
	}
	j := slices.IndexFunc(v, match)
	if j < 0 && byKey && old {
		// Recorded before its version had keys.
		j = slices.IndexFunc(v, func(x api.VmDisk) bool { return x.BlueprintDiskKey == nil && *x.BlueprintDisk == i+1 })
	}
	if j < 0 {
		return api.VmDisk{}, false
	}
	return v[j], true
}

// diskPair is an extra disk of the old version, the new one or both, by
// its place in each; -1 in the one it isn't in.
type diskPair struct{ old, new int }

// pairDisks matches the versions' extra disks (ADR-0052): by key when
// every one of both versions has a key, else by place. The disks both
// versions have come first, then those the new one adds, then those it
// removes, each in its version's order.
func pairDisks(o, n []blueprint.DiskPlan) ([]diskPair, bool) {
	keyed := func(ds []blueprint.DiskPlan) bool {
		return !slices.ContainsFunc(ds, func(d blueprint.DiskPlan) bool { return d.Key == "" })
	}
	byKey := keyed(o) && keyed(n)
	var kept, added, removed []diskPair
	paired := map[int]bool{}
	for j := range n {
		i := slices.IndexFunc(o, func(d blueprint.DiskPlan) bool { return d.Key == n[j].Key })
		if !byKey {
			i = j
		}
		if i >= 0 && i < len(o) {
			kept, paired[i] = append(kept, diskPair{i, j}), true
		} else {
			added = append(added, diskPair{-1, j})
		}
	}
	for i := range o {
		if !paired[i] {
			removed = append(removed, diskPair{i, -1})
		}
	}
	return slices.Concat(kept, added, removed), byKey
}

// unkeyed are disks without their keys, for comparing what vCenter has.
func unkeyed(ds []blueprint.DiskPlan) []blueprint.DiskPlan {
	out := slices.Clone(ds)
	for i := range out {
		out[i].Key = ""
	}
	return out
}

// vmNICs are a VM's adapters by the blueprint adapter rvav recorded each
// is (ADR-0042); recorded: an adapter is marked as the first, as on every
// VM rvav built since it records them.
type vmNICs struct {
	byKey    map[string]api.VmNic
	recorded bool
}

func blueprintNICs(t day2.Target) vmNICs {
	out := vmNICs{byKey: map[string]api.VmNic{}}
	for _, n := range t.VM.Nics {
		switch {
		case n.BlueprintNic == nil:
		case *n.BlueprintNic == vmprops.FirstNIC:
			out.recorded = true
		default:
			out.byKey[*n.BlueprintNic] = n
		}
	}
	return out
}

// of is the VM's adapter that is the blueprint's adapter key, the i-th the
// blueprint adds: the one recorded as it, or, on a VM without the record,
// the one at its place unless recorded as another.
func (v vmNICs) of(t day2.Target, key string, i int) *api.VmNic {
	if n, ok := v.byKey[key]; ok {
		return &n
	}
	if v.recorded {
		return nil
	}
	if n := day2.NIC(t, day2.NICLabel(i+2)); n != nil && n.BlueprintNic == nil {
		return n
	}
	return nil
}

func ruleComponent(r day2.Resource) string {
	var p struct {
		Rule struct {
			Component string `json:"component"`
		} `json:"rule"`
	}
	_ = json.Unmarshal(r.Properties, &p)
	return p.Rule.Component
}

// inPlace plans the changes to the VMs the component keeps: refused where
// rvav can't make them without building the VMs again.
func (c componentChange) inPlace(u *componentPlan, vms []existingVM, refuse func(setting, reason string)) {
	o, n := c.old, c.new
	or := func(s string) string { return cmp.Or(s, "none") }
	if o.Template != n.Template || o.Library != n.Library {
		refuse("template", fmt.Sprintf("The template changes from %s to %s: rvav can't clone the existing VMs again in place.", or(o.Template), or(n.Template)))
	}
	// Never recorded, as for VMs adopted without inputs: nothing moves when
	// every VM is already there.
	moves := func(old, new string, at func(api.VmProperties) string) bool {
		return old != new && (old != "" || slices.ContainsFunc(vms, func(v existingVM) bool { return at(v.t.VM) != new }))
	}
	for _, x := range []struct {
		setting, what, old, new string
		at                      func(api.VmProperties) string
	}{
		{"cluster", "cluster", o.Cluster, n.Cluster, vmCluster}, {"datastore", "datastore", o.Datastore, n.Datastore, vmDatastore},
		{"resource_pool", "resource pool", o.ResourcePool, n.ResourcePool, nil}, {"host", "host", o.Host, n.Host, nil},
		{"datastore_cluster", "datastore cluster", o.DatastoreCluster, n.DatastoreCluster, nil}, {"folder", "folder", o.Folder, n.Folder, nil},
	} {
		if x.at == nil && x.old != x.new || x.at != nil && moves(x.old, x.new, x.at) {
			refuse(x.setting, fmt.Sprintf("The %s changes from %s to %s: rvav doesn't move existing VMs.", x.what, or(x.old), or(x.new)))
		}
	}
	if moves(o.Network, n.Network, vmNetwork) {
		refuse("network", fmt.Sprintf("The network changes from %s to %s: the first adapter carries the VMs' addresses and DNS records, which rvav doesn't move.",
			or(o.Network), or(n.Network)))
	}
	if o.StoragePolicy != n.StoragePolicy || o.Provisioning != n.Provisioning {
		refuse("storage_policy", "The storage policy or provisioning changes: the existing disks keep theirs.")
	}
	if o.DHCP != n.DHCP || o.IPv6 != n.IPv6 {
		refuse("dhcp", "How the VMs get their addresses changes: the existing VMs keep theirs.")
	}
	if !slices.Equal(sortedCopy(o.Groups), sortedCopy(n.Groups)) {
		refuse("groups", "The NSX security groups change: the existing VMs stay in theirs.")
	}
	if !maps.Equal(o.Properties, n.Properties) {
		refuse("properties", "The custom properties change: the existing VMs keep theirs.")
	}
	if !slices.Equal(sortedCopy(o.Tags), sortedCopy(n.Tags)) {
		refuse("tags", "The vSphere tags change: change them on each VM with Tags instead.")
	}
	if !reflect.DeepEqual(c.oldGuest, c.newGuest) {
		refuse("guest", "The guest settings change: they are applied only when a VM is built.")
	}

	hardware := o.VCPU != n.VCPU || o.MemoryGB != n.MemoryGB || o.DiskGB != n.DiskGB || !slices.Equal(unkeyed(o.Disks), unkeyed(n.Disks)) ||
		!reflect.DeepEqual(o.NICs, n.NICs)
	if n.DiskGB < o.DiskGB {
		refuse("disk_gb", fmt.Sprintf("The first disk shrinks from %d to %d GB: vCenter can't shrink a disk.", o.DiskGB, n.DiskGB))
	}
	pairs, byKey := pairDisks(o.Disks, n.Disks)
	for _, p := range pairs {
		if p.old < 0 || p.new < 0 {
			continue
		}
		od, nd := o.Disks[p.old], n.Disks[p.new]
		label := fmt.Sprintf("Extra disk %d", p.old+1)
		if byKey {
			label = "Extra disk " + nd.Key
		}
		if nd.SizeGB < od.SizeGB {
			refuse("disks", fmt.Sprintf("%s shrinks from %d to %d GB: vCenter can't shrink a disk.", label, od.SizeGB, nd.SizeGB))
		}
		if od.Datastore != nd.Datastore || od.StoragePolicy != nd.StoragePolicy || od.Provisioning != nd.Provisioning {
			refuse("disks", label+" changes its datastore, storage policy or provisioning: rvav doesn't move existing disks.")
		}
	}
	// Without keys, the n-th extra disk of a version is the n-th of the other.
	ambiguous := !byKey && len(n.Disks) < len(o.Disks) && !slices.Equal(unkeyed(o.Disks[:len(n.Disks)]), unkeyed(n.Disks))
	if ambiguous {
		refuse("disks", "The new version removes extra disks and changes others: rvav matches the versions' disks by their place, "+
			"so it can't tell which ones go. Give every extra disk a key, or remove them in one version and change the others in the next.")
	}
	oldNICs, newNICs := map[string]int{}, map[string]int{}
	for i, x := range o.NICs {
		oldNICs[x.Key] = i
	}
	for i, x := range n.NICs {
		newNICs[x.Key] = i
	}
	for _, x := range n.NICs {
		i, ok := oldNICs[x.Key]
		switch {
		case !ok && x.IPAM != blueprint.IPAMDHCP:
			refuse("nics", "The adapter "+x.Key+" is added with an EfficientIP address: rvav adds only DHCP adapters to existing VMs.")
		case ok && o.NICs[i].IPAM != x.IPAM:
			refuse("nics", "The adapter "+x.Key+" changes how it gets its address: the existing VMs keep theirs.")
		case ok && o.NICs[i].Network != x.Network && x.IPAM != blueprint.IPAMDHCP:
			refuse("nics", "The adapter "+x.Key+" moves to another network: it carries an address and DNS records, which rvav doesn't move.")
		}
	}
	for _, x := range o.NICs {
		if _, ok := newNICs[x.Key]; !ok && x.IPAM != blueprint.IPAMDHCP {
			refuse("nics", "The adapter "+x.Key+" is removed: it carries an address and DNS records, which rvav doesn't remove from existing VMs.")
		}
	}

	for _, vm := range vms {
		t, name := vm.t, vm.name()
		var ops []day2.Op
		var changes []api.UpdateChange
		change := func(kind api.UpdateChangeKind, title, detail string, destructive bool) {
			ch := api.UpdateChange{Kind: kind, Component: present.Ptr(c.key), Vm: present.Ptr(name), Title: title, Destructive: destructive}
			if detail != "" {
				ch.Detail = &detail
			}
			changes = append(changes, ch)
		}
		if hardware && day2.Suspended(t) != "" {
			refuse("power", name+" is suspended: power it on or off first.")
			continue
		}
		disks := func(why string) bool {
			if day2.HasSnapshots(t) {
				refuse("disks", name+" has snapshots, and vCenter can't "+why+" the disks of a VM with snapshots. Delete them first.")
				return false
			}
			return true
		}
		// Which of its disks are the blueprint's, as rvav recorded them; a
		// VM without its first disk recorded is one it didn't build so.
		own := blueprintDisks(t)
		_, known := own.first()
		unknown := func(setting, what string) {
			refuse(setting, name+" was onboarded or built before rvav recorded which of its disks are the blueprint's, so rvav can't tell "+
				"which disk to "+what+". Change its disks with the day-2 actions instead.")
		}
		grow := func(d api.VmDisk, size int) {
			ops = append(ops, day2.Get("disk_extend").Ops(t, map[string]any{"disk": d.Label, "size_gb": float64(size)})...)
			change(api.DiskExtend, fmt.Sprintf("Grow %s of %s to %d GB", d.Label, name, size), fmt.Sprintf("Was %d GB.", d.CapacityGb), false)
			u.delta["storage_gb"] += size - d.CapacityGb
		}
		if d, ok := own.first(); n.DiskGB > o.DiskGB {
			switch {
			case !ok:
				unknown("disk_gb", "grow")
			case d.CapacityGb >= n.DiskGB:
			case d.Linked != nil && *d.Linked:
				refuse("disk_gb", name+": a linked clone's disk from its template can't grow.")
			case disks("grow"):
				grow(d, n.DiskGB)
			}
		}
		for _, p := range pairs {
			switch {
			case ambiguous:
			case p.old < 0:
				nd := n.Disks[p.new]
				// Already added by an update that failed after it.
				if _, ok := own.of(nd, p.new, byKey, false); ok {
					continue
				}
				in := map[string]any{"size_gb": float64(nd.SizeGB), "datastore": nd.Datastore, "provisioning": nd.Provisioning, "storage_policy": nd.StoragePolicy}
				add := day2.Get("disk_add").Ops(t, in)
				for _, op := range add {
					op.Args["blueprint_disk"] = p.new + 1
					if nd.Key != "" {
						op.Args["blueprint_disk_key"] = nd.Key
					}
				}
				ops = append(ops, add...)
				change(api.DiskAdd, fmt.Sprintf("Add a %d GB disk to %s", nd.SizeGB, name), nd.Datastore, false)
				u.delta["storage_gb"] += nd.SizeGB
			case p.new < 0:
				switch d, ok := own.of(o.Disks[p.old], p.old, byKey, true); {
				case !ok && !known:
					unknown("disks", "remove")
				case ok && disks("remove"):
					ops = append(ops, day2.Get("disk_remove").Ops(t, map[string]any{"disk": d.Label})...)
					change(api.DiskRemove, fmt.Sprintf("Remove %s from %s", d.Label, name), fmt.Sprintf("Its %d GB and the data on it are deleted.", d.CapacityGb), true)
					u.delta["storage_gb"] -= d.CapacityGb
				}
			default:
				d, ok := own.of(o.Disks[p.old], p.old, byKey, true)
				nd := n.Disks[p.new]
				if ok && (*d.BlueprintDisk != p.new+1 || deref(d.BlueprintDiskKey) != nd.Key) {
					u.mark(t.ResourceID, vmprops.DiskMark{Device: int32(*d.Key), Disk: p.new + 1, Key: nd.Key})
				}
				if nd.SizeGB <= o.Disks[p.old].SizeGB {
					continue
				}
				switch {
				case !ok && !known:
					unknown("disks", "grow")
				case ok && d.CapacityGb < nd.SizeGB && disks("grow"):
					grow(d, nd.SizeGB)
				}
			}
		}
		ownNICs := blueprintNICs(t)
		for _, x := range n.NICs {
			i, ok := oldNICs[x.Key]
			switch {
			case x.IPAM != blueprint.IPAMDHCP:
			case !ok:
				// Already added by an update that failed after it.
				if _, done := ownNICs.byKey[x.Key]; done {
					continue
				}
				add := day2.Get("nic_add").Ops(t, map[string]any{"network": x.Network})
				for _, op := range add {
					op.Args["blueprint_nic"] = x.Key
				}
				ops = append(ops, add...)
				change(api.NicAdd, fmt.Sprintf("Add a network adapter on %s to %s", x.Network, name), "", false)
			case o.NICs[i].Network != x.Network:
				// Gone, or already moved by an update that failed after it.
				nic := ownNICs.of(t, x.Key, i)
				if nic == nil || nic.Network == x.Network {
					continue
				}
				label := deref(nic.Label)
				ops = append(ops, day2.Get("nic_move").Ops(t, map[string]any{"nic": label, "network": x.Network})...)
				change(api.NicMove, fmt.Sprintf("Move %s of %s to %s", label, name, x.Network), "Was on "+nic.Network+".", false)
			}
		}
		for i, x := range o.NICs {
			nic := ownNICs.of(t, x.Key, i)
			if _, ok := newNICs[x.Key]; ok || x.IPAM != blueprint.IPAMDHCP || nic == nil {
				continue
			}
			label := deref(nic.Label)
			ops = append(ops, day2.Get("nic_remove").Ops(t, map[string]any{"nic": label})...)
			change(api.NicRemove, fmt.Sprintf("Remove %s from %s", label, name), "The guest loses the connection on it.", true)
		}
		// Resized last: a size the VM can't take while it runs restarts it.
		if (o.VCPU != n.VCPU || o.MemoryGB != n.MemoryGB) && (t.VM.CpuCount != n.VCPU || t.VM.MemoryMb != n.MemoryGB*1024) {
			ops = append(ops, day2.Get("resize").Ops(t, map[string]any{"cpu_count": float64(n.VCPU), "memory_gb": float64(n.MemoryGB)})...)
			detail := fmt.Sprintf("Was %d vCPU and %d GB.", t.VM.CpuCount, t.VM.MemoryMb/1024)
			if why := day2.ColdChange(t, n.VCPU, n.MemoryGB); why != "" {
				detail += " Restarts the VM: " + strings.TrimSuffix(why, ".") + "."
			}
			change(api.Resize, fmt.Sprintf("Set %d vCPU and %d GB on %s", n.VCPU, n.MemoryGB, name), detail, false)
			u.delta["vcpu"] += n.VCPU - t.VM.CpuCount
			u.delta["memory_gb"] += n.MemoryGB - t.VM.MemoryMb/1024
		}
		u.ops = append(u.ops, onVM(ops, t)...)
		u.changes = append(u.changes, changes...)
	}

	// Jobs: those the new version adds; every one again on a failed
	// deployment, whose jobs may not all have run.
	var jobs []provision.Job
	for _, j := range c.jobs {
		if c.failed || !slices.Contains(o.PostConfig, j.Key) {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) > 0 {
		var names []string
		for _, vm := range vms {
			names = append(names, vm.name())
		}
		u.jobs = append(u.jobs, provision.UpdateJobs{Component: c.key, Jobs: jobs})
		for _, j := range jobs {
			u.ran = append(u.ran, api.UpdateChange{Kind: api.Job, Component: present.Ptr(c.key), Title: "Run the AWX job " + j.Template,
				Detail: present.Ptr("On " + strings.Join(names, ", ") + ".")})
		}
	}
}

// onVM retargets a day-2 action's operations onto one VM of the
// deployment, under keys of their own.
func onVM(ops []day2.Op, t day2.Target) []day2.Op {
	seen := map[string]int{}
	out := make([]day2.Op, len(ops))
	for i, op := range ops {
		args := map[string]any{}
		maps.Copy(args, op.Args)
		args["moref"], args["resource"] = t.VM.Moref, t.ResourceID
		op.Args = args
		key := op.Key
		if seen[key]++; seen[key] > 1 {
			key += ":" + strconv.Itoa(seen[key])
		}
		op.Key = key + "@" + t.ResourceName
		if !strings.Contains(op.Title, t.ResourceName) {
			op.Title += " · " + t.ResourceName
		}
		out[i] = op
	}
	return out
}

// addVMs names the VMs an update adds for good, inside its transaction,
// as reserveVMs does for a new request; a deployment of one VM gets its
// own name back.
func (s *Service) addVMs(ctx context.Context, tx pgx.Tx, u *updatePlan) ([]provision.VMUnit, []provision.ComponentUnit, []provision.Job, error) {
	ev := u.ev
	if len(u.add) == 0 {
		return nil, nil, nil, nil
	}
	if err := s.lockPrefixes(ctx, tx, ev); err != nil {
		return nil, nil, nil, err
	}
	order := optionOrder(ev.Version.UISchema, "post_config")
	if !ev.Blueprint.Multi() {
		p := u.add[0].part
		jobs, err := postConfigJobs(ev, p.vm, order)
		return []provision.VMUnit{vmUnit(ev, p, u.d.ID, u.d.Name, 0)}, nil, jobs, err
	}
	var vms []provision.VMUnit
	var comps []provision.ComponentUnit
	given := map[string]bool{}
	for _, a := range u.add {
		p := a.part
		taken, err := s.Store.InventoryVMNames(ctx, tx, p.prefix)
		if err != nil {
			return nil, nil, nil, err
		}
		ours, err := s.Store.DeploymentNames(ctx, tx, p.prefix)
		if err != nil {
			return nil, nil, nil, err
		}
		for n := range given {
			ours[n] = true
		}
		names := &nameCheck{ctx: ctx, ipam: s.IPAM, vcenter: taken, rvav: ours, zone: p.zone()}
		if p.check != nil {
			names.dns = p.check.dns
		}
		for i := range a.n {
			seq, err := s.Store.NextName(ctx, tx, p.counter, p.naming.Start(), p.naming.Step(), func(n int) bool { return names.taken(p.naming.Name(p.prefix, n)) })
			if err != nil {
				return nil, nil, nil, err
			}
			if names.err != nil {
				return nil, nil, nil, names.err
			}
			name := p.naming.Name(p.prefix, seq)
			names.rvav[name], given[name] = true, true
			vms = append(vms, vmUnit(ev, p, u.d.ID, name, a.first+i))
		}
		c, err := componentUnit(ev, p, order)
		if err != nil {
			return nil, nil, nil, err
		}
		comps = append(comps, c)
	}
	return vms, comps, nil, nil
}

// filesOf are the files inputs name.
func filesOf(inputs json.RawMessage) map[string]bool {
	var had map[string]any
	_ = json.Unmarshal(inputs, &had)
	out := map[string]bool{}
	for _, v := range had {
		if ref, ok := v.(map[string]any); ok {
			if id, ok := ref["id"].(string); ok {
				out[id] = true
			}
		}
	}
	return out
}

// sameInputs says whether the inputs are those the deployment has; secret
// ones, given again, count as the same.
func sameInputs(uiSchema json.RawMessage, had json.RawMessage, now map[string]any) bool {
	var before map[string]any
	_ = json.Unmarshal(had, &before)
	raw, _ := json.Marshal(now)
	var after map[string]any
	_ = json.Unmarshal(raw, &after)
	for field := range present.SecretFields(uiSchema) {
		if secretinput.IsMarker(before[field]) {
			delete(before, field)
			delete(after, field)
		}
	}
	if len(before) == 0 && len(after) == 0 {
		return true
	}
	return reflect.DeepEqual(before, after)
}

func versionRef(v store.ItemVersion) api.CatalogItemVersionRef {
	return api.CatalogItemVersionRef{Id: v.ID, Number: v.Number, PublishedAt: v.PublishedAt}
}

func nonNilChanges(c []api.UpdateChange) []api.UpdateChange {
	if c == nil {
		return []api.UpdateChange{}
	}
	return c
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

// naturalCompare orders names with their numbers as numbers: web9 before
// web10.
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da > 0 && db > 0 {
			na, _ := strconv.Atoi(a[:da])
			nb, _ := strconv.Atoi(b[:db])
			if c := cmp.Compare(na, nb); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		if c := cmp.Compare(a[0], b[0]); c != 0 {
			return c
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func digits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}
