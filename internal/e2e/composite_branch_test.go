package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
)

func caseOf(title, when string, steps ...any) map[string]any {
	c := map[string]any{"when": when}
	if title != "" {
		c["title"] = title
	}
	if len(steps) > 0 {
		c["steps"] = steps
	}
	return c
}

func branchOf(key string, cases []any, otherwise ...any) map[string]any {
	b := map[string]any{"cases": cases}
	if len(otherwise) > 0 {
		b["else"] = otherwise
	}
	return map[string]any{"key": key, "title": "Choose " + key, "branch": b}
}

// posting is a step that says something to the change API, which keeps it.
func posting(c *changeAPI, key, body string) map[string]any {
	return map[string]any{"key": key, "title": "Post " + key, "rest": map[string]any{"method": "POST", "url": c.URL + "/api/finish", "body": body}}
}

// checking asks the change API for its state, and failing is a call the
// script answers too: a 404 fails it.
func checking(c *changeAPI, key string) map[string]any {
	return map[string]any{"key": key, "title": "Check " + key, "rest": map[string]any{"method": "GET", "url": c.URL + "/api/change/" + key, "keep": "$.result.state"}}
}

func undoing(step map[string]any, c *changeAPI, body string) map[string]any {
	step["undo"] = map[string]any{"rest": map[string]any{"method": "POST", "url": c.URL + "/api/finish", "body": body}}
	return step
}

// withVM builds a VM first, which a rollback that finishes deletes, and gives
// the inputs it needs: Rollback says so only when it deleted something.
func withVM(rig askRig, slug string, steps ...any) api.RequestDraft {
	def := vmWithDBUser()
	vm := def.Blueprint["steps"].([]any)[0]
	def.Blueprint = map[string]any{"kind": "composite", "rollback_on_failure": true, "steps": append([]any{vm}, steps...)}
	d := rig.publishDefinition(slug, def)
	d.Inputs = map[string]any{"role": "db", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
		"network": "dvpg-app-dev-vl310", "database": "orders", "role_name": "orders_ro"}
	return d
}

// decisions counts a branch's choices in a run.
func decisions(env *testsuite.TestWorkflowEnvironment) *atomic.Int32 {
	var n atomic.Int32
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		if info.ActivityType.Name == "DecideBranch" {
			n.Add(1)
		}
	})
	return &n
}

// TestCompositeBranch: a composite step that runs one side of a choice
// (ADR-0033). What it may say and read, which side runs, what the others show,
// what a condition that can't be read does, what is undone, and what a Retry
// finds.
func TestCompositeBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		change := newChangeAPI(t)
		h.svc.REST.Hosts = []string{change.URL}

		// Refused when saved.
		var problem struct{ Errors []api.FieldError }
		one := []any{caseOf("", "true")}
		bad := askDefinition(false,
			branchOf("none", []any{}),
			branchOf("nowhen", []any{caseOf("Nothing", "")}),
			branchOf("badcel", []any{caseOf("", "inputs.name +")}),
			map[string]any{"key": "haswhen", "when": "true", "branch": map[string]any{"cases": one}},
			map[string]any{"key": "hasinputs", "with": map[string]any{"x": "1"}, "branch": map[string]any{"cases": one}},
			map[string]any{"key": "hasundo", "undo": map[string]any{"job_template": "x"}, "branch": map[string]any{"cases": one}},
			map[string]any{"parallel": []any{branchOf("inparallel", one), waitFor("sibling", "1h")}},
			branchOf("parallelon", []any{caseOf("", "true", map[string]any{"parallel": []any{waitFor("p", "1h")}})}),
			branchOf("nested", []any{caseOf("", "true", branchOf("inner", one))}),
			branchOf("sides", []any{caseOf("", "true", waitFor("one", "1h")), caseOf("", "true", posting(change, "two", "{'a': steps.one.until}"))}),
			branchOf("elsereads", []any{caseOf("", "true", waitFor("three", "1h"))}, posting(change, "four", "{'a': steps.three.until}")),
			branchOf("ownside", []any{caseOf("", "steps.five.until != ''", waitFor("five", "1h"))}),
			branchOf("later", []any{caseOf("", "true", posting(change, "six", "{'a': steps.seven.until}"), waitFor("seven", "1h"))}),
			branchOf("dup", []any{caseOf("", "true", waitFor("same", "1h")), caseOf("", "true", waitFor("same", "1h"))}),
			map[string]any{"key": "two_kinds", "workflow": "x", "branch": map[string]any{"cases": one}},
		)
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-branch", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.Composite, Definition: &bad}, 422, &problem)
		const prefix = "/definition/blueprint/steps/"
		var want []string
		for _, w := range []string{"0/branch/cases", "1/branch/cases/0/when", "2/branch/cases/0/when", "3/when", "4/with", "5/undo", "6/parallel/0",
			"7/branch/cases/0/steps/0", "8/branch/cases/0/steps/0", "9/branch/cases/1/steps/0/rest/body", "10/branch/else/0/rest/body",
			"11/branch/cases/0/when", "12/branch/cases/0/steps/0/rest/body", "13/branch/cases/1/steps/0/key", "14"} {
			want = append(want, prefix+w)
		}
		wantFields(t, "a bad branch", problem.Errors, want...)
		sideMessages := 0
		for _, e := range problem.Errors {
			if strings.Contains(e.Message, "another side") {
				sideMessages++
			}
		}
		if sideMessages != 2 {
			t.Errorf("two read another side: %+v", problem.Errors)
		}
		if t.Failed() {
			t.FailNow()
		}

		// The first case that holds runs; a side reads its branch and its own
		// earlier steps, a later step reads a side as it reads a step `when`
		// skipped, and the others are skipped, in order under their branch.
		decides := func() api.CatalogVersionDefinition {
			return waitDefinition(checking(change, "check"),
				branchOf("choose", []any{
					caseOf("Refused", "steps.check.json == 'refused'", posting(change, "no", "{'side': 'refused'}")),
					caseOf("Approved", "steps.check.json == 'approved'",
						posting(change, "yes", "{'side': 'approved', 'case': steps.choose.case}"),
						posting(change, "yes2", "{'side': 'approved2', 'status': steps.yes.status}")),
					caseOf("", "steps.check.json == 'approved'", posting(change, "shadow", "{'side': 'shadow'}")),
					caseOf("", "steps.check.json == 'third'"),
				}, posting(change, "other", "{'side': 'else'}")),
				posting(change, "after", "{'case': steps.choose.case, 'ran': has(steps.yes), 'skipped': has(steps.no)}"))
		}
		picks := rig.publishDefinition("branch-picks", decides())
		change.script(state("approved"))
		req := rig.submit(requester, picks)
		var planned struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", requester, nil, 200, &planned)
		var order []string
		for _, s := range planned.Items {
			if strings.HasPrefix(s.Key, "step:") {
				order = append(order, strings.TrimPrefix(s.Key, "step:"))
			}
		}
		if !slices.Equal(order, []string{"check", "choose", "no", "yes", "yes2", "shadow", "other", "after"}) {
			t.Fatalf("the steps, in order = %v", order)
		}
		for _, s := range planned.Items {
			if s.Key == "step:choose" && (s.System == nil || *s.System != api.StepSystemBranch || s.BranchCase != nil || deref(s.Detail) != "Chooses which side runs") {
				t.Fatalf("before it chose = %+v", s)
			}
		}
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("picked = %s %q", r.Status, errorOf(r))
		}
		steps := rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.Status != api.StepStatusDone || s.BranchCase == nil || *s.BranchCase != 2 || deref(s.Detail) != "Took: Approved" {
			t.Fatalf("the branch = %+v", s)
		}
		if out := rig.outputs(req.Id, "step:choose"); out["case"] != float64(2) || len(out) != 1 {
			t.Fatalf("its outputs = %v", out)
		}
		for _, key := range []string{"no", "shadow", "other"} {
			if s := steps["step:"+key]; s.Status != api.StepStatusSkipped || !strings.Contains(deref(s.Detail), "wasn't taken") {
				t.Fatalf("%s, not taken = %+v", key, s)
			}
		}
		for _, key := range []string{"yes", "yes2", "after"} {
			if s := steps["step:"+key]; s.Status != api.StepStatusDone {
				t.Fatalf("%s = %+v", key, s)
			}
		}
		got := change.got()
		if len(got) != 3 || got[0]["side"] != "approved" || got[0]["case"] != float64(2) || got[1]["side"] != "approved2" || got[1]["status"] != float64(200) ||
			got[2]["case"] != float64(2) || got[2]["ran"] != true || got[2]["skipped"] != false {
			t.Fatalf("what they read = %v", got)
		}
		// Called again (an activity retried), it finds what it chose.
		taken, err := h.acts.DecideBranch(ctx, provision.BranchInput{RequestID: req.Id, ProjectID: req.Project.Id, StepKey: "step:choose",
			Cases: []provision.BranchChoice{{When: "true"}, {When: "false"}}, Steps: map[string]any{}})
		if err != nil || taken != 2 {
			t.Fatalf("chosen again = %d, %v", taken, err)
		}

		// Its else; and with no title, the case's number.
		change.script(state("new"))
		req = rig.submit(requester, picks)
		rig.run(req.Id)
		steps = rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.BranchCase == nil || *s.BranchCase != 0 || deref(s.Detail) != "Took: otherwise" {
			t.Fatalf("else = %+v", s)
		}
		if steps["step:other"].Status != api.StepStatusDone || steps["step:yes"].Status != api.StepStatusSkipped {
			t.Fatalf("else ran = %+v, %+v", steps["step:other"], steps["step:yes"])
		}
		change.script(state("third"))
		req = rig.submit(requester, picks)
		rig.run(req.Id)
		if s := rig.steps(req.Id, requester)["step:choose"]; s.BranchCase == nil || *s.BranchCase != 4 || deref(s.Detail) != "Took: case 4" {
			t.Fatalf("a case with no title = %+v", s)
		}

		// No case holds, and no else: it says so and goes on.
		none := rig.publishDefinition("branch-none", waitDefinition(branchOf("choose", []any{caseOf("Never", "inputs.name == 'other'", posting(change, "no", "{}"))}),
			posting(change, "after", "{'case': steps.choose.case}")))
		req = rig.submit(requester, none)
		rig.run(req.Id)
		steps = rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.Status != api.StepStatusDone || s.BranchCase == nil || *s.BranchCase != 0 || !strings.Contains(deref(s.Detail), "No case held") {
			t.Fatalf("none = %+v", s)
		}
		if steps["step:no"].Status != api.StepStatusSkipped || steps["step:after"].Status != api.StepStatusDone {
			t.Fatalf("none ran = %+v, %+v", steps["step:no"], steps["step:after"])
		}

		// A person's answer chooses, and a step's own `when` still skips it.
		asks := rig.publishDefinition("branch-asks", waitDefinition(
			askStep("who", map[string]any{"requester": true, "form": []any{map[string]any{"key": "size", "label": "Size", "type": "choice", "options": []any{"S", "M"}}}}),
			branchOf("choose", []any{caseOf("Big", "steps.who.answer.size == 'M'",
				map[string]any{"key": "skips", "when": "inputs.name == 'other'", "wait": map[string]any{"for": "1h"}},
				posting(change, "big", "{'read': has(steps.skips)}"))})))
		req = rig.submit(requester, asks)
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			rig.answer(rig.open(req.Id, requester).Id, requester, 200, map[string]any{"size": "M"}, nil)
			rig.deliver(env)
		}})
		steps = rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.BranchCase == nil || *s.BranchCase != 1 {
			t.Fatalf("an answer chose = %+v", s)
		}
		if s := steps["step:skips"]; s.Status != api.StepStatusSkipped || deref(s.Detail) != "Its condition is false." {
			t.Fatalf("its `when` = %+v", s)
		}
		if steps["step:big"].Status != api.StepStatusDone {
			t.Fatalf("the side's next step = %+v", steps["step:big"])
		}

		// A condition that can't be read fails the branch, masked; nothing of
		// its sides runs, and a Retry chooses again. cel-go's own errors quote
		// a key a map lacks and a pattern's text: neither reaches the
		// requester, on the request, its steps or their notifications.
		for slug, when := range map[string]string{
			"branch-cel":    "int('" + waitLiteral + "') == 1",
			"branch-text":   "'" + waitLiteral + "'",
			"branch-absent": "inputs.absent == '" + waitLiteral + "'",
			"branch-key":    "inputs['" + waitLiteral + "'] == 'x'",
			"branch-regex":  "inputs.name.matches('" + waitLiteral + "(')",
		} {
			d := rig.publishDefinition(slug, waitDefinition(branchOf("choose", []any{caseOf("Reads", when, posting(change, "no", "{}"))}, posting(change, "other", "{}"))))
			before := len(change.got())
			req = rig.submit(requester, d)
			rig.run(req.Id)
			r := rig.get(req.Id, requester)
			steps = rig.steps(req.Id, requester)
			s := steps["step:choose"]
			msg := errorOf(r)
			if r.Status != api.RequestStatusFailed || s.Status != api.StepStatusFailed || !strings.Contains(msg, "couldn't be chosen") {
				t.Errorf("%s = %s %q, the step %+v", slug, r.Status, msg, s)
			}
			if strings.Contains(msg, waitLiteral) || strings.Contains(deref(s.Error), waitLiteral) {
				t.Errorf("%s reads its literal: %q", slug, msg)
			}
			for _, path := range []string{"/api/v1/requests/" + req.Id, "/api/v1/requests/" + req.Id + "/steps", "/api/v1/me/notifications"} {
				if _, body, _ := h.do("GET", path, requester, nil); strings.Contains(string(body), waitLiteral) {
					t.Errorf("%s: GET %s reads its literal: %s", slug, path, body)
				}
			}
			if len(change.got()) != before || steps["step:no"].Status != api.StepStatusPending {
				t.Errorf("%s ran a side: %+v", slug, steps["step:no"])
			}
		}

		// A failure on the side taken undoes what ran, newest first, and only
		// that: the side not taken shows skipped, not failed.
		undoes := rig.publishDefinition("branch-undoes", askDefinition(true,
			undoing(posting(change, "first", "{'ran': 'first'}"), change, "{'undid': 'first'}"),
			branchOf("choose", []any{caseOf("Taken", "true",
				undoing(posting(change, "here", "{'ran': 'here'}"), change, "{'undid': 'here'}"),
				checking(change, "breaks"))},
				undoing(posting(change, "there", "{'ran': 'there'}"), change, "{'undid': 'there'}")),
			posting(change, "after", "{'ran': 'after'}")))
		change.script(changeAnswer{404, map[string]any{}})
		req = rig.submit(requester, undoes)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusFailed {
			t.Fatalf("rolled back = %s", r.Status)
		}
		got = change.got()
		var said []string
		for _, b := range got {
			for _, k := range []string{"ran", "undid"} {
				if v, ok := b[k].(string); ok {
					said = append(said, k+" "+v)
				}
			}
		}
		if !slices.Equal(said, []string{"ran first", "ran here", "undid here", "undid first"}) {
			t.Fatalf("what ran and was undone = %v", said)
		}
		steps = rig.steps(req.Id, requester)
		if steps["step:there"].Status != api.StepStatusSkipped || steps["step:here"].Status != api.StepStatusCancelled || steps["step:breaks"].Status != api.StepStatusFailed {
			t.Fatalf("after the undo = there %+v, here %+v, breaks %+v", steps["step:there"], steps["step:here"], steps["step:breaks"])
		}
		if s := steps["step:choose"]; s.Status != api.StepStatusDone || s.BranchCase == nil || *s.BranchCase != 1 {
			t.Fatalf("the branch has nothing to undo = %+v", s)
		}

		var decided *atomic.Int32
		// Only its calls were undone: a Retry runs those again, on the same side.
		change.script(state("new"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) { decided = decisions(env) })
		steps = rig.steps(req.Id, requester)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded || decided.Load() != 0 {
			t.Fatalf("retried after an undo = %s %q, it chose %d times", r.Status, errorOf(r), decided.Load())
		}
		if steps["step:here"].Status != api.StepStatusDone || steps["step:there"].Status != api.StepStatusSkipped {
			t.Fatalf("retried after an undo = here %+v, there %+v", steps["step:here"], steps["step:there"])
		}

		// Everything was rolled back: a Retry runs everything again, so it
		// chooses again from what it now reads, its details anew.
		again := withVM(rig, "branch-rollback", checking(change, "check"),
			branchOf("choose", []any{caseOf("A", "steps.check.json == 'a'", checking(change, "breaks"))},
				posting(change, "other", "{'side': 'else'}")))
		change.script(state("a"), changeAnswer{404, map[string]any{}})
		req = rig.submit(requester, again)
		rig.run(req.Id)
		steps = rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.BranchCase == nil || *s.BranchCase != 1 {
			t.Fatalf("first chose = %+v", s)
		}
		if s := steps["rollback"]; s.Status != api.StepStatusDone {
			t.Fatalf("a rollback that finished = %+v", s)
		}
		change.script(state("b"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		steps = rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.Status != api.StepStatusPending || s.BranchCase != nil || deref(s.Detail) != "Chooses which side runs" {
			t.Fatalf("reset for a retry = %+v", s)
		}
		if s := steps["step:other"]; s.Status != api.StepStatusPending || strings.Contains(deref(s.Detail), "wasn't taken") {
			t.Fatalf("the other side, reset = %+v", s)
		}
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) { decided = decisions(env) })
		if decided.Load() != 1 {
			t.Fatalf("it chose %d times after a rollback", decided.Load())
		}
		steps = rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.BranchCase == nil || *s.BranchCase != 0 {
			t.Fatalf("chose again = %+v", s)
		}
		if steps["step:other"].Status != api.StepStatusDone || steps["step:breaks"].Status != api.StepStatusSkipped {
			t.Fatalf("after a rollback = other %+v, breaks %+v", steps["step:other"], steps["step:breaks"])
		}
		if s := steps["step:other"]; strings.Contains(deref(s.Detail), "wasn't taken") {
			t.Fatalf("it ran, and says %q", deref(s.Detail))
		}

		// A Retry of a failed step keeps the choice, however the world changed:
		// what is done stays done, the side not taken stays skipped.
		keeps := rig.publishDefinition("branch-keeps", askDefinition(false, checking(change, "check"),
			branchOf("choose", []any{caseOf("A", "steps.check.json == 'a'", posting(change, "here", "{'side': 'here'}"), checking(change, "breaks"))},
				posting(change, "other", "{'side': 'else'}")),
			posting(change, "after", "{'case': steps.choose.case}")))
		change.script(state("a"), changeAnswer{404, map[string]any{}})
		req = rig.submit(requester, keeps)
		rig.run(req.Id)
		if s := rig.steps(req.Id, requester)["step:breaks"]; s.Status != api.StepStatusFailed {
			t.Fatalf("the step that breaks = %+v", s)
		}
		change.script(state("b"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) { decided = decisions(env) })
		if decided.Load() != 0 {
			t.Fatalf("a Retry chose again, %d times", decided.Load())
		}
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("retried = %s %q", r.Status, errorOf(r))
		}
		steps = rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.BranchCase == nil || *s.BranchCase != 1 || steps["step:other"].Status != api.StepStatusSkipped || steps["step:breaks"].Status != api.StepStatusDone {
			t.Fatalf("retried: choose %+v, other %+v, breaks %+v", s, steps["step:other"], steps["step:breaks"])
		}
		if got := change.got(); len(got) != 1 || got[0]["case"] != float64(1) {
			t.Fatalf("what the Retry ran = %v", got)
		}

		// A cancel stops a wait on its side at once, and nothing after runs.
		cancels := rig.publishDefinition("branch-cancels", waitDefinition(branchOf("choose", []any{caseOf("", "true", waitFor("keep", "72h"), posting(change, "here", "{}"))})))
		before := len(change.got())
		req = rig.submit(requester, cancels)
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				h.json("POST", "/api/v1/requests/"+req.Id+"/cancel", requester, nil, 202, nil)
				env.CancelWorkflow()
			}, 30*time.Minute)
		})
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusCancelled || len(change.got()) != before {
			t.Fatalf("cancelled = %s %q, %d calls after", r.Status, errorOf(r), len(change.got())-before)
		}

		// A VM is planned at submit: it can't be on a side.
		onSide := rig.publishDefinition("branch-vm", askDefinition(false, branchOf("choose", []any{caseOf("", "true",
			map[string]any{"key": "vm", "item": "rhel9-server", "with": map[string]any{"size": "'small'"}})})))
		h.json("POST", "/api/v1/requests", requester, onSide, 422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) == 0 || !strings.Contains(problem.Errors[0].Message, "can't be on a side") {
			t.Fatalf("a VM on a side = %+v", problem.Errors)
		}
	})
}

func messageOf(key string, fields map[string]any) map[string]any {
	return map[string]any{"key": key, "title": "Tell " + key, "notify": fields}
}

// TestCompositeNotify: a composite step that sends a message through rvav's
// notifications (ADR-0033). What it may say, whom it reaches, what it can't
// show, what fails it and what doesn't, and that it is sent once.
func TestCompositeNotify(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		change := newChangeAPI(t)
		h.svc.REST.Hosts = []string{change.URL}
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "change-token", Kind: api.SecretWriteKindToken, Value: new(restToken)}, 201, nil)
		told := func(id string) int {
			t.Helper()
			return rig.count(`select count(*) from notifications where request_id = $1 and kind = 'step_message'`, id)
		}
		sentTo := func(id, user string) string {
			t.Helper()
			var body string
			if err := h.store.Pool.QueryRow(ctx, `select body from notifications where request_id = $1 and kind = 'step_message' and user_id = $2`,
				id, seed.UserID(user)).Scan(&body); err != nil {
				t.Fatalf("%s's message: %v", user, err)
			}
			return body
		}

		// Refused when saved.
		var problem struct{ Errors []api.FieldError }
		tell := map[string]any{"requester": true, "message": "Hello"}
		bad := askDefinition(false,
			messageOf("nobody", map[string]any{"message": "Hello"}),
			messageOf("blank", map[string]any{"requester": true, "message": "  "}),
			messageOf("long", map[string]any{"requester": true, "message": strings.Repeat("x", 2001)}),
			messageOf("unclosed", map[string]any{"requester": true, "message": "Hello ${inputs.name"}),
			messageOf("notcel", map[string]any{"requester": true, "message": "Hello ${inputs.name +}"}),
			messageOf("later", map[string]any{"requester": true, "message": "Hello ${steps.after.x}"}),
			map[string]any{"key": "inputs", "with": map[string]any{"x": "1"}, "notify": tell},
			map[string]any{"key": "undone", "undo": map[string]any{"job_template": "x"}, "notify": tell},
			map[string]any{"key": "two", "workflow": "x", "notify": tell},
			messageOf("nogroup", map[string]any{"groups": []any{" "}, "message": "Hello"}),
			waitFor("after", "1h"),
		)
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-notify", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.Composite, Definition: &bad}, 422, &problem)
		const prefix = "/definition/blueprint/steps/"
		var want []string
		for _, w := range []string{"0/notify", "1/notify/message", "2/notify/message", "3/notify/message", "4/notify/message", "5/notify/message", "6/with", "7/undo", "8", "9/notify/groups/0"} {
			want = append(want, prefix+w)
		}
		wantFields(t, "a bad message", problem.Errors, want...)

		// What it depends on: nobody it names is unknown, and a secret input,
		// by name or by index, can't be read; on a side too.
		refs := askDefinition(false,
			messageOf("gone", map[string]any{"users": []any{"usr_nobody"}, "message": "Hello"}),
			messageOf("byname", map[string]any{"requester": true, "message": "It is ${inputs.api_password}"}),
			messageOf("byindex", map[string]any{"requester": true, "message": `It is ${inputs['api_password']} and ${inputs["api_password"]}`}),
			branchOf("choose", []any{caseOf("", "true", messageOf("onside", map[string]any{"requester": true, "message": "It is ${inputs.api_password}"}))}),
		)
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-notify-refs", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.Composite, Definition: &refs}, 422, &problem)
		wantFields(t, "a message's references", problem.Errors, prefix+"0/notify/users/0", prefix+"1/notify/message", prefix+"2/notify/message",
			prefix+"3/branch/cases/0/steps/0/notify/message")
		if t.Failed() {
			t.FailNow()
		}

		// To each they name, once, in their own right: the requester, the owner,
		// a group's members, people. The timeline, its outputs and what a
		// requester's form draws say nothing of who or what.
		named := seed.UserID("lmartin")
		reaches := rig.publishDefinition("notify-reaches", waitDefinition(
			messageOf("hello", map[string]any{"requester": true, "owner": true, "groups": []any{"rvav-data"}, "users": []any{named, seed.UserID(requester)},
				"message": "Hello <b>${inputs.name}</b> in ${environment}: ${inputs.name.size()}, ${inputs.name == 'web'}, ${project.code}."}),
			posting(change, "after", "{'told': steps.hello.told}")))
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/notify-reaches/form?project_id=prj_web", requester, nil, 200, &form)
		topology, err := json.Marshal(form.Topology)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(topology), `"requester":true`) || strings.Contains(string(topology), "rvav-data") || strings.Contains(string(topology), named) || strings.Contains(string(topology), "Hello") {
			t.Fatalf("the requester's topology = %s", topology)
		}
		req := rig.submit(requester, reaches)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("told = %s %q", r.Status, errorOf(r))
		}
		for _, user := range []string{requester, "lmartin", "sbrunner"} {
			if got := sentTo(req.Id, user); got != "Hello <b>web</b> in dev: 3, true, web." {
				t.Fatalf("%s reads %q", user, got)
			}
		}
		n := told(req.Id)
		members := rig.count(`select count(distinct user_id) from user_groups where group_name = 'rvav-data' and user_id not in ($1, $2)`, named, seed.UserID(requester))
		if n != members+2 {
			t.Fatalf("%d told, %d in the group besides those named", n, members)
		}
		var title, link, level string
		if err := h.store.Pool.QueryRow(ctx, `select title, link, level from notifications where request_id = $1 and kind = 'step_message' limit 1`, req.Id).Scan(&title, &link, &level); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(title, req.Number+" · ") || link != "/requests/"+req.Number || level != "info" {
			t.Fatalf("the notification = %q %q %q", title, link, level)
		}
		s := rig.steps(req.Id, requester)["step:hello"]
		if s.Status != api.StepStatusDone || deref(s.Detail) != "Message sent." || s.System == nil || *s.System != api.StepSystemRvav {
			t.Fatalf("the step = %+v", s)
		}
		if out := rig.outputs(req.Id, "step:hello"); out["told"] != float64(n) || len(out) != 1 {
			t.Fatalf("its outputs = %v", out)
		}
		if got := change.got(); len(got) != 1 || got[0]["told"] != float64(n) {
			t.Fatalf("what the later step read = %v", got)
		}

		// Sent once: called again (an activity retried, a run continued as new),
		// nobody hears it twice, and a request being cancelled tells nobody.
		in := provision.NotifyInput{RequestID: req.Id, ProjectID: req.Project.Id, StepKey: "step:hello",
			Notify: blueprint.Notify{Requester: true, Message: "Again"}, Steps: map[string]any{}}
		if _, err := h.acts.SendStepMessage(ctx, in); err != nil {
			t.Fatal(err)
		}
		if told(req.Id) != n || sentTo(req.Id, requester) == "Again" {
			t.Fatalf("called again: %d told, reads %q", told(req.Id), sentTo(req.Id, requester))
		}
		cancelling := rig.publishDefinition("notify-cancelling", waitDefinition(waitFor("keep", "1h"), messageOf("late", tell)))
		req = rig.submit(requester, cancelling)
		if _, err := h.store.Pool.Exec(ctx, `update requests set status = 'cancelling' where id = $1`, req.Id); err != nil {
			t.Fatal(err)
		}
		in.RequestID, in.StepKey = req.Id, "step:late"
		if _, err := h.acts.SendStepMessage(ctx, in); err != nil || told(req.Id) != 0 {
			t.Fatalf("cancelling: %d told, %v", told(req.Id), err)
		}

		// What it can't reach. A secret input read some other way is a marker,
		// not its value: it fails the step, and nobody is told.
		secret := rig.publishDefinition("notify-secret", waitDefinition(messageOf("leak", map[string]any{"requester": true, "message": "It is ${inputs['api' + '_password']}"})))
		secret.Inputs["api_password"] = restInput
		req = rig.submit(requester, secret)
		rig.run(req.Id)
		r := rig.get(req.Id, requester)
		if r.Status != api.RequestStatusFailed || !strings.Contains(errorOf(r), "secret input") || told(req.Id) != 0 {
			t.Fatalf("a secret input = %s %q, %d told", r.Status, errorOf(r), told(req.Id))
		}
		// An API that echoes a secret: the step that called it kept it out. A
		// person's answer that repeats one is scrubbed when the message is made.
		echoes := rig.publishDefinition("notify-echoes", waitDefinition(
			map[string]any{"key": "check", "rest": map[string]any{"method": "POST", "url": change.URL + "/api/change/1", "secret": "change-token",
				"body": "{'password': inputs.api_password}", "keep": "$.result.state"}},
			askStep("who", map[string]any{"requester": true, "form": []any{map[string]any{"key": "reason", "label": "Why", "type": "string"}}}),
			messageOf("hello", map[string]any{"requester": true, "message": "API: ${steps.check.json}. Person: ${steps.who.answer.reason}."})))
		echoes.Inputs["api_password"] = restInput
		change.script(changeAnswer{200, map[string]any{"result": map[string]any{"state": "token " + restToken + " input " + restInput}}})
		req = rig.submit(requester, echoes)
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			rig.answer(rig.open(req.Id, requester).Id, requester, 200, map[string]any{"reason": "it is " + restInput}, nil)
			rig.deliver(env)
		}})
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("echoes = %s %q", r.Status, errorOf(r))
		}
		body := sentTo(req.Id, requester)
		if strings.Contains(body, restToken) || strings.Contains(body, restInput) || !strings.HasPrefix(body, "API: token ") || !strings.Contains(body, "Person: it is ") {
			t.Fatalf("the message reads %q", body)
		}

		// What fails it: a template that can't be filled, masked, with nobody
		// told. A long value is cut, not refused.
		for slug, expr := range map[string]string{
			"notify-absent": "inputs.absent + '" + waitLiteral + "'",
			"notify-list":   "['" + waitLiteral + "']",
			"notify-cel":    "int('" + waitLiteral + "')",
		} {
			d := rig.publishDefinition(slug, waitDefinition(messageOf("hello", map[string]any{"requester": true, "message": "Hello ${" + expr + "}"})))
			req = rig.submit(requester, d)
			rig.run(req.Id)
			r := rig.get(req.Id, requester)
			s := rig.steps(req.Id, requester)["step:hello"]
			msg := errorOf(r)
			if r.Status != api.RequestStatusFailed || s.Status != api.StepStatusFailed || !strings.Contains(msg, "wasn't sent") || told(req.Id) != 0 {
				t.Errorf("%s = %s %q, the step %+v, %d told", slug, r.Status, msg, s, told(req.Id))
			}
			if strings.Contains(msg, waitLiteral) || strings.Contains(deref(s.Error), waitLiteral) {
				t.Errorf("%s reads its literal: %q", slug, msg)
			}
		}
		long := rig.publishDefinition("notify-long", waitDefinition(messageOf("hello", map[string]any{"requester": true, "message": "${inputs.name + inputs.name}"})))
		long.Inputs["name"] = strings.Repeat("y", 2500)
		req = rig.submit(requester, long)
		rig.run(req.Id)
		if got := []rune(sentTo(req.Id, requester)); len(got) != 4001 || got[4000] != '…' {
			t.Fatalf("a long message has %d characters", len(got))
		}

		// Nobody to tell, at run time: a group nobody has signed in from. It
		// goes on, and says so.
		nobody := rig.publishDefinition("notify-nobody", waitDefinition(messageOf("hello", map[string]any{"groups": []any{"rvav-nobody"}, "message": "Hello"}),
			posting(change, "after", "{'told': steps.hello.told}")))
		req = rig.submit(requester, nobody)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("nobody = %s %q", r.Status, errorOf(r))
		}
		if s := rig.steps(req.Id, requester)["step:hello"]; s.Status != api.StepStatusDone || !strings.Contains(deref(s.Detail), "Nobody to tell") || strings.Contains(deref(s.Detail), "rvav-nobody") {
			t.Fatalf("nobody's step = %+v", s)
		}

		// On the side taken only, and a Retry of a later step doesn't send it again.
		keeps := rig.publishDefinition("notify-keeps", askDefinition(false,
			branchOf("choose", []any{caseOf("", "inputs.name == 'web'", messageOf("here", tell))}, messageOf("there", tell)),
			checking(change, "breaks")))
		change.script(changeAnswer{404, map[string]any{}})
		req = rig.submit(requester, keeps)
		rig.run(req.Id)
		if told(req.Id) != 1 {
			t.Fatalf("on a side, %d told", told(req.Id))
		}
		change.script(state("new"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded || told(req.Id) != 1 {
			t.Fatalf("retried = %s %q, %d told", r.Status, errorOf(r), told(req.Id))
		}

		// After a rollback that finished, everything runs again: so does it.
		again := withVM(rig, "notify-again", messageOf("hello", tell), checking(change, "breaks"))
		change.script(changeAnswer{404, map[string]any{}})
		req = rig.submit(requester, again)
		rig.run(req.Id)
		if s := rig.steps(req.Id, requester)["rollback"]; s.Status != api.StepStatusDone || told(req.Id) != 1 {
			t.Fatalf("rolled back = %+v, %d told", s, told(req.Id))
		}
		change.script(state("new"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded || told(req.Id) != 2 {
			t.Fatalf("after a rollback = %s %q, %d told", r.Status, errorOf(r), told(req.Id))
		}
	})
}

// TestCompositeBranchThroughTemporal: the choice stays made through a real
// server (ADR-0033). A wait on the side taken, continuing as new each round,
// its worker gone for part of it, and a Retry: nothing asks again, the other
// side doesn't run, and the side's message is sent once.
func TestCompositeBranchThroughTemporal(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t, "--dynamic-config-value", "limit.historyCount.suggestContinueAsNew=5")
	recheck := provision.WaitRecheck
	provision.WaitRecheck = time.Second
	t.Cleanup(func() { provision.WaitRecheck = recheck })

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester, queue = "mkeller", "tnguyen", "rvav-branch-e2e"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		change := newChangeAPI(t)
		h.svc.REST.Hosts = []string{change.URL}
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		worker := func() func() {
			w := provision.NewWorker(tc, queue, h.acts)
			if err := w.Start(); err != nil {
				t.Fatal(err)
			}
			return w.Stop
		}
		stop := worker()
		defer func() { stop() }()

		runOf := func(id string) string {
			t.Helper()
			desc, err := tc.DescribeWorkflowExecution(ctx, provision.WorkflowID(id), "")
			if err != nil {
				t.Fatal(err)
			}
			return desc.WorkflowExecutionInfo.Execution.RunId
		}
		until := func(what string, ok func() bool) {
			t.Helper()
			deadline := time.Now().Add(45 * time.Second)
			for !ok() {
				if time.Now().After(deadline) {
					t.Fatal(what)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		// How many times a run of the workflow chose: its history says.
		chose := func(id, run string) int {
			t.Helper()
			n := 0
			iter := tc.GetWorkflowHistory(ctx, provision.WorkflowID(id), run, false, 0)
			for iter.HasNext() {
				ev, err := iter.Next()
				if err != nil {
					t.Fatal(err)
				}
				if a := ev.GetActivityTaskScheduledEventAttributes(); a != nil && a.GetActivityType().GetName() == "DecideBranch" {
					n++
				}
			}
			return n
		}
		told := func(id string) int {
			t.Helper()
			return rig.count(`select count(*) from notifications where request_id = $1 and kind = 'step_message'`, id)
		}
		sides := func(after map[string]any) map[string]any {
			return branchOf("choose", []any{caseOf("Approved", "steps.check.json == 'approved'",
				messageOf("told", map[string]any{"requester": true, "message": "Approved: ${inputs.name}"}),
				waitFor("keep", "8s"), after)},
				posting(change, "other", "{'side': 'else'}"))
		}

		def := waitDefinition(checking(change, "check"), sides(posting(change, "here", "{'side': 'here'}")), posting(change, "after", "{'case': steps.choose.case}"))
		durable := rig.publishDefinition("branch-durable", def)
		change.script(state("approved"))
		req := rig.submit(requester, durable)
		var due *time.Time
		until("the wait didn't begin", func() bool { due = rig.steps(req.Id, requester)["step:keep"].DueAt; return due != nil })
		first := runOf(req.Id)
		until("the wait didn't continue as new", func() bool { return runOf(req.Id) != first })
		// Asked again now, the world would send it the other way.
		change.script(state("refused"))
		steps := rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.Status != api.StepStatusDone || s.BranchCase == nil || *s.BranchCase != 1 {
			t.Fatalf("continued as new, the branch = %+v", s)
		}
		if steps["step:keep"].Status != api.StepStatusRunning || !steps["step:keep"].DueAt.Equal(*due) || steps["step:other"].Status != api.StepStatusSkipped {
			t.Fatalf("continued as new: keep %+v (due %v), other %+v", steps["step:keep"], due, steps["step:other"])
		}
		if len(change.got()) != 0 {
			t.Fatal("what comes after ran during the wait")
		}
		stop()
		time.Sleep(time.Until(due.Add(time.Second))) // it comes due with no worker
		if s := rig.steps(req.Id, requester)["step:keep"]; s.Status != api.StepStatusRunning {
			t.Fatalf("with no worker = %+v", s)
		}
		stop = worker()
		h.waitStatus(req.Id, requester, api.RequestStatusSucceeded)
		steps = rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.BranchCase == nil || *s.BranchCase != 1 || steps["step:other"].Status != api.StepStatusSkipped {
			t.Fatalf("the end: branch %+v, other %+v", s, steps["step:other"])
		}
		got := change.got()
		if len(got) != 2 || got[0]["side"] != "here" || got[1]["case"] != float64(1) {
			t.Fatalf("what ran = %v", got)
		}
		if change.checked() != 0 {
			t.Fatalf("the world was asked again, %d times", change.checked())
		}
		if told(req.Id) != 1 {
			t.Fatalf("told %d times", told(req.Id))
		}
		if n := chose(req.Id, first); n != 1 {
			t.Fatalf("the first run chose %d times", n)
		}
		if n := chose(req.Id, runOf(req.Id)); n != 0 {
			t.Fatalf("the last run chose %d times", n)
		}

		// A Retry after a failure on the side keeps it too.
		retried := rig.publishDefinition("branch-retried", waitDefinition(checking(change, "check"), sides(checking(change, "breaks"))))
		change.script(state("approved"), changeAnswer{404, map[string]any{}})
		req = rig.submit(requester, retried)
		h.waitStatus(req.Id, requester, api.RequestStatusFailed)
		if s := rig.steps(req.Id, requester)["step:breaks"]; s.Status != api.StepStatusFailed {
			t.Fatalf("the step that breaks = %+v", s)
		}
		kept := rig.steps(req.Id, requester)["step:keep"].DueAt
		change.script(state("refused"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		h.waitStatus(req.Id, requester, api.RequestStatusSucceeded)
		steps = rig.steps(req.Id, requester)
		if s := steps["step:choose"]; s.BranchCase == nil || *s.BranchCase != 1 || steps["step:other"].Status != api.StepStatusSkipped || steps["step:breaks"].Status != api.StepStatusDone {
			t.Fatalf("retried: branch %+v, other %+v, breaks %+v", s, steps["step:other"], steps["step:breaks"])
		}
		if s := steps["step:keep"]; s.DueAt == nil || !s.DueAt.Equal(*kept) {
			t.Fatalf("the wait done before = %+v, it was due %v", s, kept)
		}
		if told(req.Id) != 1 {
			t.Fatalf("retried, told %d times", told(req.Id))
		}
		if n := chose(req.Id, runOf(req.Id)); n != 0 {
			t.Fatalf("the Retry chose %d times", n)
		}
	})
}
