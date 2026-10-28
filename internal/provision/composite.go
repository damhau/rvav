package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// Composite items (ADR-0022): their steps run group by group, a group's at
// the same time; later steps bind their inputs to earlier ones' outputs.

type stepResult struct {
	out    map[string]any
	failed string
	err    error
}

// runComposite runs a composite item's steps. On a failure with rollback,
// the AWX and workflow steps done are undone in reverse order first (the
// request's VMs, addresses and records are deleted after, as for others).
func runComposite(ctx workflow.Context, info Info) (string, error) {
	p := info.Plan
	outputs := map[string]any{}
	var done []CompositeStep
	for g := 0; ; g++ {
		var group []CompositeStep
		for _, c := range p.Composite {
			if c.Group == g {
				group = append(group, c)
			}
		}
		if len(group) == 0 {
			break
		}
		results := make([]stepResult, len(group))
		if len(group) == 1 {
			results[0] = runCompositeStep(ctx, info, group[0], outputs)
		} else {
			// Read only while the group runs.
			before := make(map[string]any, len(outputs))
			for k, v := range outputs {
				before[k] = v
			}
			wg := workflow.NewWaitGroup(ctx)
			for i, c := range group {
				wg.Add(1)
				workflow.Go(ctx, func(gctx workflow.Context) {
					defer wg.Done()
					results[i] = runCompositeStep(gctx, info, c, before)
				})
			}
			wg.Wait(ctx)
		}
		var failed *stepResult
		for i, c := range group {
			if results[i].err != nil {
				if failed == nil {
					failed = &results[i]
				}
				continue
			}
			if results[i].out != nil {
				outputs[c.Key] = results[i].out
			}
			done = append(done, c)
		}
		if failed != nil {
			if p.Rollback {
				undoComposite(ctx, info, done, outputs)
			}
			return failed.failed, failed.err
		}
	}
	return "", nil
}

// runCompositeStep runs one step and returns its outputs.
func runCompositeStep(ctx workflow.Context, info Info, c CompositeStep, outputs map[string]any) stepResult {
	var a *Activities
	key := c.StepKey()
	if info.done(key) {
		return stepResult{out: info.Outputs[key]}
	}
	values := map[string]any{}
	if c.When != "" || len(c.With) > 0 {
		var r Resolved
		in := ResolveInput{RequestID: info.RequestID, When: c.When, With: c.With, Steps: outputs}
		if err := workflow.ExecuteActivity(ctx, a.ResolveStep, in).Get(ctx, &r); err != nil {
			_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "failed", errMessage(err)).Get(ctx, nil)
			return stepResult{failed: key, err: err}
		}
		if r.Skip {
			err := workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "skipped", "Its condition is false.").Get(ctx, nil)
			return stepResult{err: err, failed: key}
		}
		values = r.Values
	}
	var out map[string]any
	switch {
	case len(c.VMs) > 0:
		if err := workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "running", "").Get(ctx, nil); err != nil {
			return stepResult{failed: key, err: err}
		}
		vms, failed, err := buildVMs(ctx, info, c.VMs, c.Guest, c.VMKey)
		if err == nil {
			failed, err = postConfig(ctx, info, vms, c.PostConfig, key+"/"+KeyInventory,
				func(j Job) string { return key + "/" + KeyJob + j.Key }, c.Limit(), info.Plan.Context)
		}
		if err != nil {
			_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "failed", errMessage(err)).Get(ctx, nil)
			return stepResult{failed: failed, err: err}
		}
		out = vmOutputs(vms)
	case c.AWX != nil:
		vars := values
		ctxVars := map[string]any{"steps": outputs}
		for k, v := range info.Plan.Context {
			ctxVars[k] = v
		}
		vars["rvav"] = ctxVars
		in := JobInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Job: *c.AWX, ExtraVars: vars}
		if err := workflow.ExecuteActivity(jobCtx(ctx), a.RunJob, in).Get(ctx, nil); err != nil {
			return stepResult{failed: key, err: err}
		}
	case c.Workflow != nil:
		call := *c.Workflow
		call.Inputs = values
		result, err := runCustom(ctx, info, key, call, outputs)
		if err != nil {
			return stepResult{failed: key, err: err}
		}
		out = result
	}
	var recorded map[string]any
	if err := workflow.ExecuteActivity(ctx, a.FinishStep, info.RequestID, info.ProjectID, key, out).Get(ctx, &recorded); err != nil {
		return stepResult{failed: key, err: err}
	}
	return stepResult{out: recorded}
}

// vmOutputs are what a VM step gives later steps: its VM's name, FQDN,
// address and moref, and all of them in vms.
func vmOutputs(vms []builtVM) map[string]any {
	list := make([]any, len(vms))
	for i, b := range vms {
		list[i] = map[string]any{"name": b.vm.Name, "fqdn": b.vm.FQDN, "ip": b.ip, "moref": b.moref, "deployment_id": b.vm.DeploymentID}
	}
	out := map[string]any{"vms": list}
	if len(vms) > 0 {
		for k, v := range list[0].(map[string]any) {
			out[k] = v
		}
	}
	return out
}

// undoComposite undoes the steps done, newest first; what can't be undone
// is said in its step, and the rest goes on.
func undoComposite(ctx workflow.Context, info Info, done []CompositeStep, outputs map[string]any) {
	var a *Activities
	dctx, _ := workflow.NewDisconnectedContext(ctx)
	dctx = workflow.WithActivityOptions(dctx, activityOptions)
	for i := len(done) - 1; i >= 0; i-- {
		c := done[i]
		if c.Undo == nil {
			continue
		}
		key := c.StepKey() + "/undo"
		if err := workflow.ExecuteActivity(dctx, a.AddUndoStep, info.RequestID, info.ProjectID, c.StepKey(), key).Get(dctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("undo step", "key", key, "err", err)
			continue
		}
		var r Resolved
		if err := workflow.ExecuteActivity(dctx, a.ResolveStep, ResolveInput{RequestID: info.RequestID, With: c.Undo.With, Steps: outputs}).Get(dctx, &r); err != nil {
			_ = workflow.ExecuteActivity(dctx, a.MarkStep, info.RequestID, info.ProjectID, key, "failed", errMessage(err)).Get(dctx, nil)
			continue
		}
		var err error
		switch {
		case c.Undo.AWX != nil:
			r.Values["rvav"] = info.Plan.Context
			err = workflow.ExecuteActivity(jobCtx(dctx), a.RunJob, JobInput{RequestID: info.RequestID, ProjectID: info.ProjectID,
				StepKey: key, Job: *c.Undo.AWX, ExtraVars: r.Values}).Get(dctx, nil)
		case c.Undo.Workflow != nil:
			call := *c.Undo.Workflow
			call.Inputs = r.Values
			_, err = runCustom(dctx, info, key, call, outputs)
		}
		if err != nil {
			workflow.GetLogger(ctx).Error("undo", "key", key, "err", err)
		}
	}
}

// ResolveInput is a step's condition and bindings to evaluate.
type ResolveInput struct {
	RequestID string         `json:"request_id"`
	When      string         `json:"when,omitempty"`
	With      map[string]any `json:"with,omitempty"`
	Steps     map[string]any `json:"steps,omitempty"`
}

// Resolved says whether the step runs, and with what.
type Resolved struct {
	Skip   bool           `json:"skip"`
	Values map[string]any `json:"values"`
}

// ResolveStep evaluates a step's condition and bindings over the request's
// inputs and the outputs of earlier steps.
func (a *Activities) ResolveStep(ctx context.Context, in ResolveInput) (Resolved, error) {
	r, err := a.Store.GetRequest(ctx, in.RequestID)
	if err != nil {
		return Resolved{}, err
	}
	var inputs map[string]any
	_ = json.Unmarshal(r.Inputs, &inputs)
	steps := in.Steps
	if steps == nil {
		steps = map[string]any{}
	}
	vars := policy.Vars{"inputs": inputs, "steps": steps, "environment": r.Environment,
		"project": map[string]any{"id": r.ProjectID, "code": r.ProjectCode, "name": r.ProjectName}}
	out := Resolved{Values: map[string]any{}}
	if in.When != "" {
		ok, err := policy.CompositeBool(in.When, vars)
		if err != nil {
			return out, permanent("the step's condition %s: %v", in.When, err)
		}
		if !ok {
			out.Skip = true
			return out, nil
		}
	}
	for k, v := range in.With {
		expr, ok := v.(string)
		if !ok {
			out.Values[k] = v
			continue
		}
		val, err := policy.CompositeEval(expr, vars)
		if err != nil {
			return out, permanent("%s: %s: %v", k, expr, err)
		}
		out.Values[k] = val
	}
	return out, nil
}

// MarkStep sets a composite step's status, with why when it failed or was
// skipped.
func (a *Activities) MarkStep(ctx context.Context, requestID, projectID, key, status, why string) error {
	u := store.StepUpdate{Status: status}
	switch {
	case status == "failed" && why != "":
		u.Error = &why
	case why != "":
		u.Detail = &why
	}
	a.update(ctx, requestID, projectID, key, u)
	return nil
}

// FinishStep closes a composite step and keeps its outputs (an AWX step's
// job ID among them); it returns them.
func (a *Activities) FinishStep(ctx context.Context, requestID, projectID, key string, outputs map[string]any) (map[string]any, error) {
	if outputs == nil {
		outputs = map[string]any{}
	}
	if st, err := a.Store.GetStepByKey(ctx, requestID, key); err == nil && st.ExternalID != nil && st.ExternalKind != nil &&
		(*st.ExternalKind == "awx_job" || *st.ExternalKind == "awx_workflow_job") {
		if id, err := strconv.Atoi(*st.ExternalID); err == nil {
			outputs["job_id"] = id
		}
	}
	b, err := json.Marshal(outputs)
	if err != nil {
		return nil, err
	}
	if err := a.Store.SetStepOutputs(ctx, requestID, key, b); err != nil {
		return nil, err
	}
	a.update(ctx, requestID, projectID, key, store.StepUpdate{Status: "done"})
	return outputs, nil
}

// AddUndoStep adds the step that undoes a composite step.
func (a *Activities) AddUndoStep(ctx context.Context, requestID, projectID, parentKey, key string) error {
	st, err := a.Store.GetStepByKey(ctx, requestID, parentKey)
	if err != nil {
		return err
	}
	a.child(ctx, JobInput{RequestID: requestID, ProjectID: projectID, StepKey: parentKey}, key, fmt.Sprintf("Undo %s", st.Title), "pending", nil)
	return nil
}
