package e2e

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
)

func loopOf(step map[string]any, items string, parallel int) map[string]any {
	step["for_each"] = map[string]any{"items": items, "parallel": parallel}
	return step
}

func caught(step map[string]any, catch ...any) map[string]any {
	step["catch"] = catch
	return step
}

func signalOf(key string, signal map[string]any) map[string]any {
	return map[string]any{"key": key, "title": "Wait for " + key, "signal": signal}
}

// failing is a REST step that fails: the change API answers its check 500.
func failing(c *changeAPI, key string) map[string]any {
	return map[string]any{"key": key, "title": "Check " + key, "rest": map[string]any{"method": "GET", "url": c.URL + "/api/change/" + key, "accept": []any{200}}}
}

// TestCompositeFlow: a composite's loops, catch paths, error handler and
// waits for a signal (ADR-0057): what is refused when saved, what each pass,
// path and handler runs and reads, and a signal sent through the API.
func TestCompositeFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		change := newChangeAPI(t)
		h.svc.REST.Hosts = []string{change.URL}

		var problem struct{ Errors []api.FieldError }
		bad := askDefinition(false,
			loopOf(waitFor("loopwait", "1h"), "[1, 2]", 0),
			loopOf(posting(change, "loopundo", "{}"), "[1]", 0),
			loopOf(posting(change, "toowide", "{}"), "[1]", 11),
			loopOf(posting(change, "nolist", "{}"), "", 0),
			posting(change, "noeach", "{'x': each}"),
			posting(change, "noerror", "{'x': error.message}"),
			caught(posting(change, "waitcatch", "{}"), waitFor("waits", "1h")),
			signalOf("both", map[string]any{"name": "go", "event": "request.submitted", "timeout": "1h"}),
			signalOf("short", map[string]any{"name": "Go Now", "timeout": "1s"}),
			signalOf("dup1", map[string]any{"name": "same", "timeout": "1h"}),
			signalOf("dup2", map[string]any{"name": "same", "timeout": "1h", "when": "true"}),
			map[string]any{"parallel": []any{caught(posting(change, "inner", "{}"), messageOf("tellinner", map[string]any{"requester": true, "message": "x"}))}},
		)
		bad.Blueprint["steps"].([]any)[1].(map[string]any)["undo"] = map[string]any{"job_template": "x"}
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-flow", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.Composite, Definition: &bad}, 422, &problem)
		const prefix = "/definition/blueprint/steps/"
		var want []string
		for _, w := range []string{"0/for_each", "1/undo", "2/for_each/parallel", "3/for_each/items", "4/rest/body", "5/rest/body",
			"6/catch/0", "7/signal", "8/signal/name", "8/signal/timeout", "10/signal/name", "10/signal/when", "11/parallel/0/catch"} {
			want = append(want, prefix+w)
		}
		wantFields(t, "a bad flow", problem.Errors, want...)
		if t.Failed() {
			t.FailNow()
		}

		// A loop, two at a time: each pass posts its item, the next step
		// reads them all; the catch path and the handler aren't needed.
		looped := rig.publishDefinition("flow-loop", askDefinition(false,
			caught(loopOf(posting(change, "each", "{'host': each, 'at': index}"), "['a', 'b', 'c']", 2),
				posting(change, "fix", "{'fixed': error.step}")),
			posting(change, "after", "{'count': steps.each.count, 'first': steps.each.items[0]}"),
		))
		change.script()
		req := rig.submit(requester, looped)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("the loop = %s", r.Status)
		}
		got := change.got()
		var hosts []string
		for _, b := range got[:3] {
			hosts = append(hosts, b["host"].(string))
		}
		slices.Sort(hosts)
		if len(got) != 4 || !slices.Equal(hosts, []string{"a", "b", "c"}) || got[3]["count"] != float64(3) {
			t.Fatalf("what the loop posted = %v", got)
		}
		steps := rig.steps(req.Id, requester)
		for _, k := range []string{"step:each/each:0", "step:each/each:1", "step:each/each:2", "step:each"} {
			if steps[k].Status != api.StepStatusDone {
				t.Fatalf("%s = %+v", k, steps[k])
			}
		}
		if s := steps["step:fix"]; s.Status != api.StepStatusSkipped || deref(s.Detail) != "Nothing failed before it." {
			t.Fatalf("the catch path not needed = %+v", s)
		}
		if out := rig.outputs(req.Id, "step:each"); len(out["items"].([]any)) != 3 {
			t.Fatalf("the loop's outputs = %v", out)
		}

		// A pass fails, in order: the one after doesn't begin, the catch path
		// reads what failed, and the composite goes on.
		change.script(changeAnswer{200, map[string]any{}}, changeAnswer{500, map[string]any{}})
		passes := rig.publishDefinition("flow-pass", askDefinition(false,
			caught(loopOf(failing(change, "pass"), "['x', 'y', 'z']", 1),
				posting(change, "fix", "{'fixed': error.step, 'why': error.message}")),
			posting(change, "after", "{'caught': steps.pass.caught}"),
		))
		req = rig.submit(requester, passes)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("a pass caught = %s", r.Status)
		}
		steps = rig.steps(req.Id, requester)
		if steps["step:pass/each:0"].Status != api.StepStatusDone || steps["step:pass/each:1"].Status != api.StepStatusFailed ||
			steps["step:pass/each:2"].Status != api.StepStatusSkipped || steps["step:pass"].Status != api.StepStatusFailed {
			t.Fatalf("the passes = %+v %+v %+v %+v", steps["step:pass/each:0"], steps["step:pass/each:1"], steps["step:pass/each:2"], steps["step:pass"])
		}
		got = change.got()
		if len(got) != 2 || got[0]["fixed"] != "pass" || got[1]["caught"] != true {
			t.Fatalf("the catch path posted = %v", got)
		}

		// A parallel group's catch path; then a failure nothing catches: the
		// handler runs and reads it, and the request fails.
		change.script(changeAnswer{500, map[string]any{}})
		withHandler := askDefinition(false,
			map[string]any{"parallel": []any{posting(change, "one", "{'one': 1}"), failing(change, "two")},
				"catch": []any{posting(change, "groupfix", "{'group': error.step}")}},
			failing(change, "three"))
		withHandler.Blueprint["on_error"] = []any{posting(change, "ticket", "{'failed': error.step}")}
		handled := rig.publishDefinition("flow-handled", withHandler)
		req = rig.submit(requester, handled)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusFailed {
			t.Fatalf("a failure nothing caught = %s", r.Status)
		}
		got = change.got()
		var said []string
		for _, b := range got {
			for k, v := range b {
				if s, ok := v.(string); ok {
					said = append(said, k+"="+s)
				}
			}
		}
		slices.Sort(said)
		if !slices.Equal(said, []string{"failed=three", "group=two"}) {
			t.Fatalf("the group's catch path and the handler posted %v", got)
		}
		steps = rig.steps(req.Id, requester)
		if steps["step:ticket"].Status != api.StepStatusDone || steps["step:groupfix"].Status != api.StepStatusDone || steps["step:two"].Status != api.StepStatusFailed {
			t.Fatalf("ticket %+v, groupfix %+v, two %+v", steps["step:ticket"], steps["step:groupfix"], steps["step:two"])
		}

		// A signal by its name: the requester can't send it, a project admin
		// does, what it brings is its outputs; nothing else waits for one.
		signalled := rig.publishDefinition("flow-signal", askDefinition(false,
			signalOf("cab", map[string]any{"name": "cab-approved", "timeout": "72h"}),
			posting(change, "after", "{'by': steps.cab.by}"),
		))
		change.script()
		req = rig.submit(requester, signalled)
		rig.run(req.Id, moment{time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			if s := rig.steps(req.Id, requester)["step:cab"]; s.Status != api.StepStatusRunning || deref(s.Detail) != "Waiting for the signal cab-approved" {
				t.Errorf("waiting = %+v", s)
			}
			h.json("POST", "/api/v1/requests/"+req.Id+"/signal/cab-approved", requester, map[string]any{"by": "me"}, 403, nil)
			h.json("POST", "/api/v1/requests/"+req.Id+"/signal/other", admin, map[string]any{}, 409, nil)
			var receipt api.SignalReceipt
			h.json("POST", "/api/v1/requests/"+req.Id+"/signal/cab-approved", admin, map[string]any{"by": "the board"}, 202, &receipt)
			if receipt.Step != "step:cab" {
				t.Errorf("receipt = %+v", receipt)
			}
			h.json("POST", "/api/v1/requests/"+req.Id+"/signal/cab-approved", admin, map[string]any{}, 409, nil)
			rig.deliver(env)
		}})
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("signalled = %s", r.Status)
		}
		if got := change.got(); len(got) != 1 || got[0]["by"] != "the board" {
			t.Fatalf("after the signal = %v", got)
		}
		if n := rig.count(`select count(*) from audit_events where action = 'request.signal' and target_id = $1`, req.Id); n != 1 {
			t.Fatalf("audited %d times", n)
		}

		// Nothing comes: on_timeout continue goes on with timed_out.
		quiet := rig.publishDefinition("flow-quiet", askDefinition(false,
			signalOf("quiet", map[string]any{"name": "never", "timeout": "1h", "on_timeout": "continue"}),
			posting(change, "after", "{'timed_out': steps.quiet.timed_out}"),
		))
		change.script()
		req = rig.submit(requester, quiet)
		rig.run(req.Id)
		if got := change.got(); rig.get(req.Id, requester).Status != api.RequestStatusSucceeded || len(got) != 1 || got[0]["timed_out"] != true {
			t.Fatalf("timed out: %s, %v", rig.get(req.Id, requester).Status, got)
		}
		if s := rig.steps(req.Id, requester)["step:quiet"]; deref(s.Detail) != "No signal never came in 1h, so it went on." {
			t.Fatalf("timed out = %+v", s)
		}
	})
}

// TestSignalRetryAfterRollback: a Retry after a rollback runs everything
// again, a wait for a signal too: the signal the run before read is its
// own, so the step waits for another; the VM step's own steps run again
// too, its post-configuration job on the VM built again (E1.6, ADR-0057).
func TestSignalRetryAfterRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		change := newChangeAPI(t)
		h.svc.REST.Hosts = []string{change.URL}

		d := withVM(rig, "signal-retry", signalOf("cab", map[string]any{"name": "cab-approved", "timeout": "72h"}),
			checking(change, "deploy"))
		change.script(changeAnswer{404, map[string]any{}})
		req := rig.submit(requester, d)
		rig.run(req.Id, moment{time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			h.json("POST", "/api/v1/requests/"+req.Id+"/signal/cab-approved", admin, map[string]any{"change": "CHG-1"}, 202, nil)
			rig.deliver(env)
		}})
		steps := rig.steps(req.Id, requester)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusFailed || steps["step:cab"].Status != api.StepStatusDone ||
			steps["rollback"].Status != api.StepStatusDone {
			t.Fatalf("the first run: %s, cab %s, rollback %s", r.Status, steps["step:cab"].Status, steps["rollback"].Status)
		}
		baseline := func() string {
			t.Helper()
			st, err := h.store.GetStepByKey(ctx, req.Id, "step:vm/awx.job:linux-baseline")
			if err != nil || st.Status != "done" || st.ExternalID == nil {
				t.Fatalf("the VM's baseline job = %+v (%v)", st, err)
			}
			return *st.ExternalID
		}
		first := baseline()

		change.script(state("new"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		var waited bool
		rig.run(req.Id, moment{time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			waited = rig.steps(req.Id, requester)["step:cab"].Status == api.StepStatusRunning
			h.json("POST", "/api/v1/requests/"+req.Id+"/signal/cab-approved", admin, map[string]any{"change": "CHG-2"}, 202, nil)
			rig.deliver(env)
		}})
		if r := rig.get(req.Id, requester); !waited || r.Status != api.RequestStatusSucceeded || rig.outputs(req.Id, "step:cab")["change"] != "CHG-2" {
			t.Fatalf("the retry: waited %v, %s, cab outputs %v", waited, r.Status, rig.outputs(req.Id, "step:cab"))
		}
		// The VM built again is configured again.
		if again := baseline(); again == first {
			t.Fatalf("the retry kept the baseline job %s of the VM rolled back", first)
		}
		if cab := rig.steps(req.Id, requester)["step:cab"]; deref(cab.Detail) != "The signal cab-approved came." || cab.DueAt != nil {
			t.Fatalf("the step that got its signal says %q, due %v", deref(cab.Detail), cab.DueAt)
		}
	})
}

// TestCompositeFlowLimits: a sibling's failure stops a loop of a parallel
// group from starting passes; a loop's outputs, and all the steps', are
// bounded below Temporal's payloads, failing the step that goes over
// (E1.6, ADR-0057).
func TestCompositeFlowLimits(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		change := newChangeAPI(t)
		h.svc.REST.Hosts = []string{change.URL}
		failedWith := func(s api.Step, why string) bool {
			return s.Status == api.StepStatusFailed && strings.Contains(deref(s.Error)+deref(s.Detail), why)
		}

		grouped := rig.publishDefinition("loop-group", askDefinition(false, map[string]any{"parallel": []any{
			loopOf(checking(change, "each"), "['a', 'b', 'c', 'd']", 1),
			posting(change, "bad", "{'x': int(inputs.name)}"),
		}}))
		change.script()
		release := change.holding()
		req := rig.submit(requester, grouped)
		// A check of the loop waits until its sibling has failed.
		go func() {
			defer release()
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
				var status string
				_ = h.store.Pool.QueryRow(ctx, `select status from request_steps where request_id = $1 and key = 'step:bad'`, req.Id).Scan(&status)
				if status == "failed" {
					return
				}
			}
		}()
		rig.run(req.Id)
		steps := rig.steps(req.Id, requester)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusFailed || steps["step:bad"].Status != api.StepStatusFailed {
			t.Fatalf("the group = %s, bad %+v", r.Status, steps["step:bad"])
		}
		// The first pass may have begun before its sibling failed, no other.
		ran := 0
		for i := range 4 {
			switch s := steps[fmt.Sprintf("step:each/each:%d", i)]; {
			case i == 0 && s.Status == api.StepStatusDone:
				ran++
			case s.Status != api.StepStatusSkipped || deref(s.Detail) != "Another step of its group failed.":
				t.Fatalf("pass %d = %+v", i+1, s)
			}
		}
		if steps["step:each"].Status != api.StepStatusCancelled || change.checked() != ran {
			t.Fatalf("the loop = %+v, %d checks for %d passes", steps["step:each"], change.checked(), ran)
		}

		change.script(state(strings.Repeat("x", 200<<10)))
		looped := rig.publishDefinition("loop-big", askDefinition(false, loopOf(checking(change, "big"), "[1, 2, 3]", 1)))
		req = rig.submit(requester, looped)
		rig.run(req.Id)
		if s := rig.steps(req.Id, requester)["step:big"]; rig.get(req.Id, requester).Status != api.RequestStatusFailed ||
			!failedWith(s, "a loop gives 512 KiB at most") {
			t.Fatalf("a loop over its bound = %+v", s)
		}

		// A REST step keeps 256 KiB at most: the seventh goes over.
		change.script(state(strings.Repeat("y", 250<<10)))
		var heavySteps []any
		for i := 1; i <= 7; i++ {
			heavySteps = append(heavySteps, checking(change, fmt.Sprintf("s%d", i)))
		}
		heavy := rig.publishDefinition("steps-big", askDefinition(false, append(heavySteps, posting(change, "after", "{}"))...))
		req = rig.submit(requester, heavy)
		rig.run(req.Id)
		steps = rig.steps(req.Id, requester)
		if rig.get(req.Id, requester).Status != api.RequestStatusFailed || steps["step:s6"].Status != api.StepStatusDone ||
			!failedWith(steps["step:s7"], "later steps can be given 1536 KiB at most") || steps["step:after"].Status != api.StepStatusPending {
			t.Fatalf("steps over their bound: s6 %+v, s7 %+v, after %+v", steps["step:s6"], steps["step:s7"], steps["step:after"])
		}
	})
}
