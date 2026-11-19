package provision

import (
	"context"
	"encoding/json"
	"maps"
)

// recordInputs keeps what a step is given as it runs, for the run's data
// (ADR-0057): its inputs hold secret inputs as their markers and stored
// secrets by name, never a value. Keeping them is for people, so a failure
// never fails the step.
func (a *Activities) recordInputs(ctx context.Context, requestID, key string, inputs map[string]any) {
	if len(inputs) == 0 {
		return
	}
	b, err := json.Marshal(inputs)
	if err == nil {
		err = a.Store.SetStepInputs(ctx, requestID, key, b)
	}
	if err != nil {
		a.Log.Warn("record a step's inputs", "request", requestID, "step", key, "err", err)
	}
}

// jobInputs are what an AWX job is given: its extra variables but rvav's own
// context, which repeats the request and earlier steps, and each stored
// secret as it is written.
func jobInputs(in JobInput) map[string]any {
	out := maps.Clone(in.ExtraVars)
	if out == nil {
		out = map[string]any{}
	}
	delete(out, "rvav")
	for k, name := range in.Secrets {
		out[k] = "${secret." + name + "}"
	}
	return out
}
