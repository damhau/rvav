package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// A composite's loops, catch paths and default error handler (ADR-0057).
// Only plans saved since have them, and their code runs behind versionFlow.

const versionFlow = "composite-flow"

func flowOn(ctx workflow.Context) bool {
	return workflow.GetVersion(ctx, versionFlow, workflow.DefaultVersion, 1) != workflow.DefaultVersion
}

// versionLimits: a loop stops starting passes once a sibling of its group
// failed, and outputs too large to carry between steps fail the step that
// gave them (E1.6).
const versionLimits = "flow-limits"

func limitsOn(ctx workflow.Context) bool {
	return workflow.GetVersion(ctx, versionLimits, workflow.DefaultVersion, 1) != workflow.DefaultVersion
}

// Every later activity of a composite carries its steps' outputs, in a
// Temporal payload of 2 MB at most: a loop gives 512 KiB, the steps 1.5 MiB
// in all (a REST step keeps 256 KiB, and composites had six such before).
const (
	maxLoopOutputs  = 512 << 10
	maxStepsOutputs = 3 << 19
)

func outputsSize(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

// overflow fails a step whose outputs bring its composite's over what later
// steps can carry, and takes them out again.
func overflow(ctx workflow.Context, info Info, outputs map[string]any, c CompositeStep) error {
	if !limitsOn(ctx) {
		return nil
	}
	n := outputsSize(outputs)
	if n <= maxStepsOutputs {
		return nil
	}
	delete(outputs, c.Key)
	why := fmt.Sprintf("With its outputs, the steps' come to %d KiB: later steps can be given %d KiB at most. Keep less of what it answers.",
		n>>10, maxStepsOutputs>>10)
	var a *Activities
	_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, c.StepKey(), "failed", why).Get(ctx, nil)
	return permanent("%s", why)
}

const (
	notFailed    = "Nothing failed before it."
	passStopped  = "An earlier pass failed."
	keyPass      = "/each:"
	caughtOutput = "caught"
)

// PassKey is the step key of a loop's pass.
func PassKey(stepKey string, i int) string { return fmt.Sprintf("%s%s%d", stepKey, keyPass, i) }

// forEachPlanned is a loop's words before it begins.
func forEachPlanned(f *blueprint.ForEach) string {
	if f.Parallel > 1 {
		return fmt.Sprintf("For each item of a list, %d at a time", f.Parallel)
	}
	return "For each item of a list, in order"
}

// caughtStep says a step's failure was caught: it counts as done.
func caughtStep(outputs json.RawMessage) bool {
	var out struct {
		Caught bool `json:"caught"`
	}
	return len(outputs) > 0 && json.Unmarshal(outputs, &out) == nil && out.Caught
}

func stepKeys(list []CompositeStep) []string {
	keys := make([]string, len(list))
	for i, c := range list {
		keys[i] = c.StepKey()
	}
	return keys
}

// LoopInput is a loop to open: its list and its passes' title.
type LoopInput struct {
	RequestID string         `json:"request_id"`
	ProjectID string         `json:"project_id"`
	StepKey   string         `json:"step_key"`
	Title     string         `json:"title"`
	Items     string         `json:"items"`
	Parallel  int            `json:"parallel,omitempty"`
	Steps     map[string]any `json:"steps,omitempty"`
}

// runLoop runs a step once for each item of its list, Parallel at a time,
// each pass a step of its own reading each and index. Its outputs are each
// pass's, in order, as items. A pass that fails stops those not begun; the
// loop fails once those running end. A sibling's failure, stop, stops
// those not begun too.
func runLoop(ctx, stop workflow.Context, info Info, c CompositeStep, outputs map[string]any) stepResult {
	var a *Activities
	key := c.StepKey()
	if info.done(key) {
		return stepResult{out: info.Outputs[key]}
	}
	limits := limitsOn(ctx)
	var items []any
	in := LoopInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Title: c.Title, Items: c.ForEach.Items,
		Parallel: c.ForEach.Parallel, Steps: outputs}
	if err := workflow.ExecuteActivity(ctx, a.OpenLoop, in).Get(ctx, &items); err != nil {
		_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "failed", errMessage(err)).Get(ctx, nil)
		return stepResult{failed: key, err: err}
	}
	results := make([]stepResult, len(items))
	sem := workflow.NewSemaphore(ctx, int64(max(c.ForEach.Parallel, 1)))
	wg := workflow.NewWaitGroup(ctx)
	failed, begun := -1, 0
	for i := range items {
		if err := sem.Acquire(ctx, 1); err != nil {
			break
		}
		if failed >= 0 || limits && stop.Err() != nil {
			sem.Release(1)
			break
		}
		begun++
		wg.Add(1)
		workflow.Go(ctx, func(gctx workflow.Context) {
			defer wg.Done()
			defer sem.Release(1)
			pass := c
			pass.ForEach, pass.Catch = nil, nil
			results[i] = runStepAs(gctx, gctx, info, pass, PassKey(key, i), outputs, map[string]any{"each": items[i], "index": i})
			if results[i].err != nil && (failed < 0 || i < failed) {
				failed = i
			}
		})
	}
	wg.Wait(ctx)
	if failed >= 0 {
		if begun < len(items) {
			var left []string
			for i := begun; i < len(items); i++ {
				left = append(left, PassKey(key, i))
			}
			_ = workflow.ExecuteActivity(ctx, a.SkipSteps, info.RequestID, info.ProjectID, left, passStopped).Get(ctx, nil)
		}
		r := results[failed]
		why := fmt.Sprintf("Pass %d failed: %s", failed+1, errMessage(r.err))
		_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "failed", why).Get(ctx, nil)
		return stepResult{failed: r.failed, err: r.err}
	}
	if limits && begun < len(items) {
		// A loop has no undo, and its passes don't wait.
		if ctx.Err() != nil {
			return stepResult{failed: key, err: temporal.NewCanceledError()}
		}
		var left []string
		for i := begun; i < len(items); i++ {
			left = append(left, PassKey(key, i))
		}
		_ = workflow.ExecuteActivity(ctx, a.SkipSteps, info.RequestID, info.ProjectID, left, stoppedBySibling).Get(ctx, nil)
		_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "cancelled", stoppedBySibling).Get(ctx, nil)
		return stepResult{skipped: true, stopped: true}
	}
	list := make([]any, len(items))
	for i, r := range results {
		if !r.skipped {
			list[i] = r.out
		}
	}
	var recorded map[string]any
	out := map[string]any{"items": list, "count": len(items)}
	if limits {
		if n := outputsSize(out); n > maxLoopOutputs {
			why := fmt.Sprintf("Its passes give %d KiB of outputs: a loop gives %d KiB at most. Keep less of what each pass answers.",
				n>>10, maxLoopOutputs>>10)
			_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "failed", why).Get(ctx, nil)
			return stepResult{failed: key, err: permanent("%s", why)}
		}
	}
	if err := workflow.ExecuteActivity(ctx, a.FinishStep, info.RequestID, info.ProjectID, key, out).Get(ctx, &recorded); err != nil {
		return stepResult{failed: key, err: err}
	}
	return stepResult{out: recorded}
}

// OpenLoop evaluates a loop's list and adds a step for each pass, under the
// loop's. Called again (a Retry), it adds the passes it lacks: those done are
// kept by their position.
func (a *Activities) OpenLoop(ctx context.Context, in LoopInput) ([]any, error) {
	vars, err := a.stepVars(ctx, in.RequestID, in.Steps)
	if err != nil {
		return nil, err
	}
	v, err := stepEval(in.Items, vars)
	if err != nil {
		return nil, permanent("Its list can't be read: %s, in %s.", err, policy.MaskLiterals(in.Items, false))
	}
	items, ok := v.([]any)
	if !ok {
		return nil, permanent("Its list isn't a list: %s.", policy.MaskLiterals(in.Items, false))
	}
	if len(items) > blueprint.MaxForEachPasses {
		return nil, permanent("Its list has %d items: a loop makes %d passes at most.", len(items), blueprint.MaxForEachPasses)
	}
	parent, err := a.Store.GetStepByKey(ctx, in.RequestID, in.StepKey)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	if children, err := a.Store.ListSteps(ctx, in.RequestID); err == nil {
		for _, st := range children {
			have[st.Key] = true
		}
	} else {
		return nil, err
	}
	for i, item := range items {
		key := PassKey(in.StepKey, i)
		if have[key] {
			continue
		}
		a.child(ctx, JobInput{RequestID: in.RequestID, ProjectID: in.ProjectID, StepKey: in.StepKey}, key,
			fmt.Sprintf("%s · %d", parent.Title, i+1), "pending", nil)
		if detail := passDetail(item); detail != "" {
			a.update(ctx, in.RequestID, in.ProjectID, key, store.StepUpdate{Detail: &detail})
		}
	}
	detail := fmt.Sprintf("%d passes", len(items))
	if in.Parallel > 1 {
		detail += fmt.Sprintf(", %d at a time", in.Parallel)
	}
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "running", Detail: &detail})
	return items, nil
}

// passDetail names a pass's item when it is a word or a number; a list or
// an object is too long for a timeline.
func passDetail(item any) string {
	switch v := item.(type) {
	case string:
		if len(v) <= 80 && !strings.ContainsAny(v, "\n\r") {
			return v
		}
	case float64, int64, int, bool:
		return fmt.Sprint(v)
	}
	return ""
}

// runCatch runs a catch path after the steps of a group failed: it keeps
// their failure, as caught, so they count as done once the path has run, and
// gives its steps what failed as error.
func runCatch(ctx workflow.Context, info Info, failedStep, message string, stopped []string, catch []CompositeStep, outputs map[string]any) (string, error) {
	var a *Activities
	if err := workflow.ExecuteActivity(ctx, a.CatchFailure, info.RequestID, stopped, message).Get(ctx, nil); err != nil {
		return failedStep, err
	}
	for _, k := range stopped {
		outputs[strings.TrimPrefix(k, KeyStep)] = map[string]any{caughtOutput: true, "error": message}
	}
	extra := map[string]any{"error": map[string]any{"step": failedName(failedStep), "message": message}}
	for _, s := range catch {
		r := runCompositeStep(ctx, ctx, info, s, outputs, extra)
		if r.err != nil {
			return r.failed, r.err
		}
		if !r.skipped && r.out != nil {
			outputs[s.Key] = r.out
			if err := overflow(ctx, info, outputs, s); err != nil {
				return s.StepKey(), err
			}
		}
	}
	return "", nil
}

// runHandler runs a composite's default error handler: in order, each step
// reading what failed. A step of it that fails ends it; the request fails as
// it was going to. Its steps run on each failure, a Retry's too.
func runHandler(ctx workflow.Context, info Info, outputs map[string]any, failedStep string, err error) {
	var a *Activities
	run := info
	run.Done = nil
	for _, k := range info.Done {
		if !strings.HasPrefix(k, KeyStep) || !isHandlerStep(info.Plan.OnError, k) {
			run.Done = append(run.Done, k)
		}
	}
	extra := map[string]any{"error": map[string]any{"step": failedName(failedStep), "message": errMessage(err)}}
	for i, s := range info.Plan.OnError {
		r := runCompositeStep(ctx, ctx, run, s, outputs, extra)
		if r.err != nil {
			workflow.GetLogger(ctx).Error("error handler", "key", s.StepKey(), "err", r.err)
			_ = workflow.ExecuteActivity(ctx, a.SkipSteps, info.RequestID, info.ProjectID, stepKeys(info.Plan.OnError[i+1:]),
				"An earlier step of the error handler failed.").Get(ctx, nil)
			return
		}
		if !r.skipped && r.out != nil {
			outputs[s.Key] = r.out
		}
	}
}

// failedName is the key of the composite step that failed, as steps.<key>
// names it: a loop's when one of its passes failed.
func failedName(key string) string {
	name, _, _ := strings.Cut(strings.TrimPrefix(key, KeyStep), "/")
	return name
}

func isHandlerStep(list []CompositeStep, key string) bool {
	for _, c := range list {
		if c.StepKey() == key {
			return true
		}
	}
	return false
}

// failComposite is what a failure that wasn't caught does before the
// request fails: the error handler, then the undo of the steps done.
func failComposite(ctx workflow.Context, info Info, done []CompositeStep, outputs map[string]any, failedStep string, err error) {
	if len(info.Plan.OnError) > 0 && !errors.Is(err, errContinueAsNew) && !temporal.IsCanceledError(err) && flowOn(ctx) {
		runHandler(ctx, info, outputs, failedStep, err)
	}
	if info.Plan.Rollback {
		undoComposite(ctx, info, done, outputs)
	}
}

// SkipSteps marks the steps that won't run, of those still pending: a catch
// path whose step didn't fail, the error handler of a composite that
// succeeded, the passes after one that failed.
func (a *Activities) SkipSteps(ctx context.Context, requestID, projectID string, keys []string, why string) error {
	for _, k := range keys {
		st, err := a.Store.GetStepByKey(ctx, requestID, k)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Status == "pending" {
			a.update(ctx, requestID, projectID, k, store.StepUpdate{Status: "skipped", Detail: &why})
		}
	}
	return nil
}

// CatchFailure keeps on the steps that failed, or were stopped, that a catch
// path took their failure: they count as done from then on, and later steps
// read steps.<key>.caught and .error.
func (a *Activities) CatchFailure(ctx context.Context, requestID string, keys []string, message string) error {
	b, err := json.Marshal(map[string]any{caughtOutput: true, "error": message})
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := a.Store.SetStepOutputs(ctx, requestID, k, b); err != nil {
			return err
		}
	}
	return nil
}
