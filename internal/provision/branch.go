package provision

import (
	"context"
	"encoding/json"
	"fmt"

	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// A composite step that chooses between sides (ADR-0033). It chooses once: its
// row is done with the side taken as its outputs, so a run continued as new, a
// worker back after a while and a Retry find that, and the steps of the sides
// not taken are skipped. Only a rollback that finished, after which everything
// runs again from earlier outputs, has it choose again.

const notTaken = "Its side of the branch wasn't taken."

// BranchPlan is a branch's sides in a plan.
type BranchPlan struct {
	Cases []BranchSide    `json:"cases"`
	Else  []CompositeStep `json:"else,omitempty"`
}

// BranchSide is one case of a branch.
type BranchSide struct {
	Title string          `json:"title,omitempty"`
	When  string          `json:"when"`
	Steps []CompositeStep `json:"steps,omitempty"`
}

// Sides are each case's steps in order, then its else's.
func (b BranchPlan) Sides() [][]CompositeStep {
	out := make([][]CompositeStep, 0, len(b.Cases)+1)
	for _, c := range b.Cases {
		out = append(out, c.Steps)
	}
	return append(out, b.Else)
}

// side is the steps of the side taken: taken is 1 for the first case, 0 for
// else or nothing.
func (b BranchPlan) side(taken int) []CompositeStep {
	if taken >= 1 && taken <= len(b.Cases) {
		return b.Cases[taken-1].Steps
	}
	return b.Else
}

// BranchInput is a branch's choice to make.
type BranchInput struct {
	RequestID string         `json:"request_id"`
	ProjectID string         `json:"project_id"`
	StepKey   string         `json:"step_key"`
	Cases     []BranchChoice `json:"cases"`
	// Else says it has an else, by name on the timeline.
	Else bool `json:"else,omitempty"`
	// Sides are the keys of each side's steps, its else's last.
	Sides [][]string     `json:"sides,omitempty"`
	Steps map[string]any `json:"steps,omitempty"`
}

// BranchChoice is a case as a branch chooses it: not its steps.
type BranchChoice struct {
	Title string `json:"title,omitempty"`
	When  string `json:"when"`
}

// takeBranch gives the side a branch takes and its outputs, choosing if it
// hasn't. A branch done chose before this run: nothing is evaluated again.
func takeBranch(ctx workflow.Context, info Info, c CompositeStep, outputs map[string]any) ([]CompositeStep, map[string]any, error) {
	var a *Activities
	key := c.StepKey()
	if info.done(key) {
		taken, _ := branchCase(info.Outputs[key])
		return c.Branch.side(taken), map[string]any{"case": taken}, nil
	}
	in := BranchInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Else: len(c.Branch.Else) > 0, Steps: outputs}
	for _, s := range c.Branch.Cases {
		in.Cases = append(in.Cases, BranchChoice{Title: s.Title, When: s.When})
	}
	for _, side := range c.Branch.Sides() {
		keys := make([]string, len(side))
		for i, s := range side {
			keys[i] = s.StepKey()
		}
		in.Sides = append(in.Sides, keys)
	}
	var taken int
	if err := workflow.ExecuteActivity(ctx, a.DecideBranch, in).Get(ctx, &taken); err != nil {
		// A timeout never reached the activity's own record of it.
		_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "failed", errMessage(err)).Get(ctx, nil)
		return nil, nil, err
	}
	return c.Branch.side(taken), map[string]any{"case": taken}, nil
}

// branchCase reads the side taken from a branch's outputs.
func branchCase(out map[string]any) (int, bool) {
	switch n := out["case"].(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

// DecideBranch chooses a branch's side, or finds the one it chose: called
// again (an activity retried), it doesn't evaluate anything. The first case
// whose condition holds is taken. A condition that can't be read fails the
// step, its literals masked: a requester reads it.
func (a *Activities) DecideBranch(ctx context.Context, in BranchInput) (int, error) {
	st, err := a.Store.GetStepByKey(ctx, in.RequestID, in.StepKey)
	if err != nil {
		return 0, err
	}
	var kept map[string]any
	if len(st.Outputs) > 0 {
		_ = json.Unmarshal(st.Outputs, &kept)
	}
	taken, chose := branchCase(kept)
	if !chose {
		a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "running"})
		vars, err := a.stepVars(ctx, in.RequestID, in.Steps)
		if err != nil {
			return 0, err
		}
		if taken, err = chooseSide(in.Cases, vars); err != nil {
			msg := errMessage(err)
			a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "failed", Error: &msg})
			return 0, err
		}
		b, err := json.Marshal(map[string]any{"case": taken})
		if err != nil {
			return 0, err
		}
		// Whichever attempt wrote first chose.
		raw, err := a.Store.KeepStepOutputs(ctx, in.RequestID, in.StepKey, b)
		if err != nil {
			return 0, err
		}
		kept = nil
		_ = json.Unmarshal(raw, &kept)
		taken, _ = branchCase(kept)
	}
	detail := branchDetail(in, taken)
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "done", Detail: &detail})
	why := notTaken
	for i, side := range in.Sides {
		// The sides are the cases in order, then the else: taken 0 is the last.
		if i == (taken+len(in.Sides)-1)%len(in.Sides) {
			continue
		}
		for _, key := range side {
			a.update(ctx, in.RequestID, in.ProjectID, key, store.StepUpdate{Status: "skipped", Detail: &why})
		}
	}
	return taken, nil
}

// chooseSide evaluates the cases in order: 1 for the first that holds, 0 for
// none.
func chooseSide(cases []BranchChoice, vars policy.Vars) (int, error) {
	for i, c := range cases {
		holds, err := stepBool(c.When, vars)
		if err != nil {
			return 0, permanent("Its side couldn't be chosen: case %d's condition ${%s}: %v", i+1, policy.MaskLiterals(c.When, false), err)
		}
		if holds {
			return i + 1, nil
		}
	}
	return 0, nil
}

// branchDetail says on the timeline which side was taken, as the canvas names
// it.
func branchDetail(in BranchInput, taken int) string {
	switch {
	case taken >= 1 && taken <= len(in.Cases):
		if t := in.Cases[taken-1].Title; t != "" {
			return "Took: " + t
		}
		return fmt.Sprintf("Took: case %d", taken)
	case in.Else:
		return "Took: otherwise"
	}
	return "No case held: nothing ran here."
}
