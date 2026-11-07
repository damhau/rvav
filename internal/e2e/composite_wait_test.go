package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
)

// waitLiteral is a CEL literal that a requester must never read.
const waitLiteral = "lit-w4it-secret"

// changeAPI stands in for an ITSM that a poll asks about a change: it answers
// from a script, the last answer staying.
type changeAPI struct {
	*httptest.Server
	mu      sync.Mutex
	answers []changeAnswer
	checks  []string // the Authorization of each check
	methods []string
	posted  []map[string]any
	when    atomic.Value
}

type changeAnswer struct {
	status int
	body   any
}

func newChangeAPI(t *testing.T) *changeAPI {
	t.Helper()
	c := &changeAPI{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(a changeAnswer) {
			w.WriteHeader(a.status)
			_ = json.NewEncoder(w).Encode(a.body)
		}
		switch r.URL.Path {
		case "/api/when":
			reply(changeAnswer{200, map[string]any{"at": c.when.Load()}})
		case "/api/finish":
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			c.mu.Lock()
			c.posted = append(c.posted, body)
			c.mu.Unlock()
			reply(changeAnswer{200, map[string]any{}})
		default:
			c.mu.Lock()
			c.checks = append(c.checks, r.Header.Get("Authorization"))
			c.methods = append(c.methods, r.Method)
			a := changeAnswer{200, map[string]any{"result": map[string]any{"state": "new"}}}
			if len(c.answers) > 0 {
				a = c.answers[0]
				if len(c.answers) > 1 {
					c.answers = c.answers[1:]
				}
			}
			c.mu.Unlock()
			reply(a)
		}
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *changeAPI) script(answers ...changeAnswer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answers, c.checks, c.methods, c.posted = answers, nil, nil, nil
}

func (c *changeAPI) checked() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.checks)
}

func (c *changeAPI) got() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.posted)
}

func state(s string) changeAnswer {
	return changeAnswer{200, map[string]any{"result": map[string]any{"state": s}}}
}

// waitDefinition is a composite with a form that has a time to read too.
func waitDefinition(steps ...any) api.CatalogVersionDefinition {
	def := askDefinition(false, steps...)
	def.FormSchema["properties"].(map[string]any)["window"] = map[string]any{"type": "string"}
	def.UiSchema.Fields["window"] = api.UiField{Widget: api.UiWidgetText, Label: "Window"}
	def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "window")
	return def
}

func waitFor(key, d string) map[string]any {
	return map[string]any{"key": key, "title": "Wait " + key, "wait": map[string]any{"for": d}}
}

func waitUntil(key, expr string) map[string]any {
	return map[string]any{"key": key, "title": "Wait " + key, "wait": map[string]any{"until": expr}}
}

// pollOf polls the change until its state is approved.
func pollOf(c *changeAPI, key string, fields map[string]any) map[string]any {
	poll := map[string]any{"rest": map[string]any{"method": "GET", "url": c.URL + "/api/change/${inputs.name}", "keep": "$.result.state"},
		"until": "steps." + key + ".json == 'approved'", "every": "10m", "timeout": "72h"}
	for k, v := range fields {
		poll[k] = v
	}
	return map[string]any{"key": key, "title": "Poll " + key, "poll": poll}
}

func finishing(c *changeAPI, body string) map[string]any {
	return map[string]any{"key": "note", "title": "Note it", "rest": map[string]any{"method": "POST", "url": c.URL + "/api/finish", "body": body}}
}

// TestCompositeWait: a composite step that waits for a time (ADR-0033). What it
// may say, what it waits until, what it gives, a time already passed, a time
// that can't be read, and what cancelling, a sibling and a Retry do to it.
func TestCompositeWait(t *testing.T) {
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
		bad := askDefinition(false,
			map[string]any{"key": "nothing", "wait": map[string]any{}},
			map[string]any{"key": "both", "wait": map[string]any{"for": "1h", "until": "inputs.name"}},
			waitFor("soon", "soon"),
			waitFor("zero", "0s"),
			waitUntil("notcel", "inputs.name +"),
			waitUntil("later", "steps.after.x"),
			map[string]any{"key": "inputs", "with": map[string]any{"x": "1"}, "wait": map[string]any{"for": "1h"}},
			map[string]any{"key": "undone", "wait": map[string]any{"for": "1h"}, "undo": map[string]any{"job_template": "x"}},
			map[string]any{"key": "two", "wait": map[string]any{"for": "1h"}, "workflow": "x"},
			waitFor("after", "1h"),
		)
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-wait", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.Composite, Definition: &bad}, 422, &problem)
		const prefix = "/definition/blueprint/steps/"
		var want []string
		for _, w := range []string{"0/wait", "1/wait", "2/wait/for", "3/wait/for", "4/wait/until", "5/wait/until", "6/with", "7/undo", "8"} {
			want = append(want, prefix+w)
		}
		wantFields(t, "a bad wait", problem.Errors, want...)
		if t.Failed() {
			t.FailNow()
		}

		// For a duration: the timeline says it, a later step reads until when.
		timed := rig.publishDefinition("wait-timed", waitDefinition(waitFor("keep", "72h"),
			finishing(change, "{'until': steps.keep.until}")))
		req := rig.submit(requester, timed)
		var waited api.Step
		rig.run(req.Id, moment{30 * time.Minute, func(*testsuite.TestWorkflowEnvironment) {
			waited = rig.steps(req.Id, requester)["step:keep"]
		}})
		if waited.Status != api.StepStatusRunning || deref(waited.Detail) != "Waiting for 72h" || waited.System == nil || *waited.System != api.StepSystemWait {
			t.Fatalf("waiting = %+v", waited)
		}
		if waited.DueAt == nil || time.Until(*waited.DueAt) < 71*time.Hour || time.Until(*waited.DueAt) > 73*time.Hour {
			t.Fatalf("waits until %v", waited.DueAt)
		}
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("waited = %s %q", r.Status, errorOf(r))
		}
		until := waited.DueAt.UTC().Format(time.RFC3339)
		if out := rig.outputs(req.Id, "step:keep"); out["until"] != until {
			t.Fatalf("its outputs = %v, want until %s", out, until)
		}
		if got := change.got(); len(got) != 1 || got[0]["until"] != until {
			t.Fatalf("what the later step read = %v", got)
		}
		if s := rig.steps(req.Id, requester)["step:keep"]; s.DueAt == nil || s.Status != api.StepStatusDone {
			t.Fatalf("done = %+v", s)
		}

		// Until a time from the inputs, and CEL's own, each read as it says.
		window := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
		until = window.Format(time.RFC3339)
		fromInput := rig.publishDefinition("wait-input", waitDefinition(waitUntil("window", "inputs.window"),
			waitUntil("after", "timestamp(inputs.window) + duration('1h')")))
		fromInput.Inputs["window"] = until
		req = rig.submit(requester, fromInput)
		var dues [2]*time.Time
		rig.run(req.Id,
			moment{30 * time.Minute, func(*testsuite.TestWorkflowEnvironment) { dues[0] = rig.steps(req.Id, requester)["step:window"].DueAt }},
			moment{3*time.Hour + 10*time.Minute, func(*testsuite.TestWorkflowEnvironment) { dues[1] = rig.steps(req.Id, requester)["step:after"].DueAt }})
		if dues[0] == nil || !dues[0].Equal(window) {
			t.Fatalf("until the input's %s = %v", until, dues[0])
		}
		if dues[1] == nil || !dues[1].Equal(window.Add(time.Hour)) {
			t.Fatalf("an hour after it = %v", dues[1])
		}
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("waited until = %s %q", r.Status, errorOf(r))
		}

		// Until a time an earlier step answered, in a form with no zone.
		zoneless := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Minute)
		change.when.Store(zoneless.Format("2006-01-02T15:04"))
		fromStep := rig.publishDefinition("wait-step", waitDefinition(
			map[string]any{"key": "when", "rest": map[string]any{"method": "GET", "url": change.URL + "/api/when"}},
			waitUntil("window", "steps.when.json.at")))
		req = rig.submit(requester, fromStep)
		rig.run(req.Id, moment{30 * time.Minute, func(*testsuite.TestWorkflowEnvironment) { dues[0] = rig.steps(req.Id, requester)["step:window"].DueAt }})
		if dues[0] == nil || !dues[0].Equal(zoneless) {
			t.Fatalf("until the step's %s = %v", zoneless, dues[0])
		}

		// A time already passed goes on at once, and says so.
		passed := rig.publishDefinition("wait-passed", waitDefinition(waitUntil("window", "inputs.window"), finishing(change, "{'until': steps.window.until}")))
		passed.Inputs["window"] = "2020-01-02T03:04:05Z"
		req = rig.submit(requester, passed)
		var ran time.Duration
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			started := env.Now()
			env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
				if info.ActivityType.Name == "Finish" {
					ran = env.Now().Sub(started)
				}
			})
		})
		if ran > time.Minute {
			t.Fatalf("a passed time took %s", ran)
		}
		if s := rig.steps(req.Id, requester)["step:window"]; s.Status != api.StepStatusDone || !strings.Contains(deref(s.Detail), "already passed") || s.DueAt == nil {
			t.Fatalf("a passed time = %+v", s)
		}
		if out := rig.outputs(req.Id, "step:window"); out["until"] != "2020-01-02T03:04:05Z" {
			t.Fatalf("a passed time's outputs = %v", out)
		}

		// What gives no time fails the step, and reads no literal or value.
		for slug, expr := range map[string]string{
			"wait-number": "inputs.name.size()",
			"wait-text":   "'" + waitLiteral + "' + inputs.name",
			"wait-cel":    "timestamp('" + waitLiteral + "')",
			"wait-secret": "inputs.api_password",
			"wait-key":    "inputs.window",
		} {
			d := rig.publishDefinition(slug, waitDefinition(waitUntil("window", expr)))
			d.Inputs["api_password"] = restInput
			req = rig.submit(requester, d)
			rig.run(req.Id)
			r := rig.get(req.Id, requester)
			s := rig.steps(req.Id, requester)["step:window"]
			msg := errorOf(r)
			if r.Status != api.RequestStatusFailed || s.Status != api.StepStatusFailed || !strings.Contains(msg, "doesn't give a time") {
				t.Errorf("%s = %s %q, the step %+v", slug, r.Status, msg, s)
			}
			for _, hidden := range []string{waitLiteral, restInput} {
				if strings.Contains(msg, hidden) || strings.Contains(deref(s.Error), hidden) {
					t.Errorf("%s reads %q: %q", slug, hidden, msg)
				}
			}
		}

		// A condition false skips it, as any step.
		skips := rig.publishDefinition("wait-skips", waitDefinition(map[string]any{"key": "keep", "when": "inputs.name == 'other'", "wait": map[string]any{"for": "72h"}}))
		req = rig.submit(requester, skips)
		rig.run(req.Id)
		if s := rig.steps(req.Id, requester)["step:keep"]; s.Status != api.StepStatusSkipped || s.DueAt != nil {
			t.Fatalf("skipped = %+v", s)
		}

		// Cancelling stops it at once: not 72 hours later.
		cancels := rig.publishDefinition("wait-cancels", waitDefinition(waitFor("keep", "72h"), finishing(change, "{}")))
		req = rig.submit(requester, cancels)
		before := len(change.got())
		var cancelled, ended time.Time
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				h.json("POST", "/api/v1/requests/"+req.Id+"/cancel", requester, nil, 202, nil)
				cancelled = env.Now()
				env.CancelWorkflow()
			}, 30*time.Minute)
			env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
				if info.ActivityType.Name == "Finish" {
					ended = env.Now()
				}
			})
		})
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusCancelled {
			t.Fatalf("cancelled = %s %q", r.Status, errorOf(r))
		}
		if s := rig.steps(req.Id, requester)["step:keep"]; s.Status != api.StepStatusCancelled {
			t.Fatalf("the cancelled wait = %+v", s)
		}
		if ended.IsZero() || ended.Sub(cancelled) > time.Minute {
			t.Fatalf("cancelled at %s, ended at %s", cancelled, ended)
		}
		if n := len(change.got()); n != before {
			t.Fatal("a later step ran after the cancel")
		}

		// A sibling's failure stops it, and a Retry starts it again.
		sibling := rig.publishDefinition("wait-sibling", waitDefinition(map[string]any{"parallel": []any{
			waitFor("keep", "72h"),
			map[string]any{"key": "two", "title": "Two", "rest": map[string]any{"method": "GET", "url": change.URL + "/api/gone"}},
		}}))
		change.script(changeAnswer{404, map[string]any{}})
		req = rig.submit(requester, sibling)
		rig.run(req.Id)
		r := rig.get(req.Id, requester)
		first := rig.steps(req.Id, requester)["step:keep"]
		if r.Status != api.RequestStatusFailed || first.Status != api.StepStatusCancelled || first.DueAt == nil {
			t.Fatalf("a sibling failed = %s, the wait %+v", r.Status, first)
		}
		// Most of its time gone: a Retry that kept it would hardly wait.
		if _, err := h.store.Pool.Exec(ctx, `update request_steps set due_at = now() + interval '1 hour' where request_id = $1 and key = 'step:keep'`, req.Id); err != nil {
			t.Fatal(err)
		}
		change.script(state("new"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		if s := rig.steps(req.Id, requester)["step:keep"]; s.DueAt != nil || s.Status != api.StepStatusPending {
			t.Fatalf("reset for a retry = %+v", s)
		}
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("retried = %s %q", r.Status, errorOf(r))
		}
		second := rig.steps(req.Id, requester)["step:keep"]
		if second.DueAt == nil || time.Until(*second.DueAt) < 71*time.Hour {
			t.Fatalf("a Retry's wait until %v", second.DueAt)
		}

		// A later step's Retry doesn't wait again.
		later := rig.publishDefinition("wait-later", waitDefinition(waitFor("keep", "72h"),
			map[string]any{"key": "two", "title": "Two", "rest": map[string]any{"method": "GET", "url": change.URL + "/api/gone"}}))
		change.script(changeAnswer{404, map[string]any{}})
		req = rig.submit(requester, later)
		rig.run(req.Id)
		first = rig.steps(req.Id, requester)["step:keep"]
		change.script(state("new"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		rig.run(req.Id)
		second = rig.steps(req.Id, requester)["step:keep"]
		if second.Status != api.StepStatusDone || second.DueAt == nil || !second.DueAt.Equal(*first.DueAt) {
			t.Fatalf("a wait already done = %+v, it was %+v", second, first)
		}

		// What a requester's form draws of it masks what its CEL compares.
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/wait-cel/form?project_id=prj_web", requester, nil, 200, &form)
		topology, err := json.Marshal(form.Topology)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(topology), waitLiteral) || !strings.Contains(string(topology), "timestamp(") {
			t.Fatalf("the requester's topology = %s", topology)
		}
	})
}

// TestCompositePoll: a composite step that polls a REST API until a condition
// holds (ADR-0033). What it may say, what ends it, what it gives and keeps out
// of Temporal's history, its deadline, cancelling, a sibling and a Retry.
func TestCompositePoll(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		change := newChangeAPI(t)
		h.svc.REST.Hosts = []string{change.URL}
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "snow-token", Kind: api.SecretWriteKindToken, Value: new(restToken)}, 201, nil)

		// Refused when saved: above all a method that does something each time,
		// and no deadline.
		var problem struct{ Errors []api.FieldError }
		call := func(method string) map[string]any { return map[string]any{"method": method, "url": change.URL + "/x"} }
		bad := askDefinition(false,
			map[string]any{"key": "nocall", "poll": map[string]any{"until": "steps.nocall.status == 200", "every": "1m", "timeout": "1h"}},
			pollOf(change, "posts", map[string]any{"rest": call("POST")}),
			pollOf(change, "patches", map[string]any{"rest": call("PATCH")}),
			pollOf(change, "nocond", map[string]any{"until": ""}),
			pollOf(change, "input", map[string]any{"until": "inputs.name == 'x'"}),
			pollOf(change, "notcel", map[string]any{"until": "steps.notcel.status =="}),
			pollOf(change, "laterstep", map[string]any{"until": "steps.laterstep.status == 200 && steps.after.met"}),
			pollOf(change, "noevery", map[string]any{"every": ""}),
			pollOf(change, "fast", map[string]any{"every": "59s"}),
			pollOf(change, "badevery", map[string]any{"every": "often"}),
			pollOf(change, "nodeadline", map[string]any{"timeout": ""}),
			pollOf(change, "short", map[string]any{"every": "10m", "timeout": "5m"}),
			pollOf(change, "baddeadline", map[string]any{"timeout": "soon"}),
			pollOf(change, "then", map[string]any{"on_timeout": "escalate"}),
			pollOf(change, "host", map[string]any{"rest": map[string]any{"method": "GET", "url": "https://elsewhere.corp.example/x"}}),
			pollOf(change, "gone", map[string]any{"rest": map[string]any{"method": "GET", "url": change.URL + "/x", "secret": "no-such-secret"}}),
			func() map[string]any {
				s := pollOf(change, "inputs", nil)
				s["with"] = map[string]any{"x": "1"}
				return s
			}(),
			func() map[string]any {
				s := pollOf(change, "undone", nil)
				s["undo"] = map[string]any{"job_template": "x"}
				return s
			}(),
			pollOf(change, "after", nil),
		)
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-poll", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.Composite, Definition: &bad}, 422, &problem)
		const prefix = "/definition/blueprint/steps/"
		var want []string
		for _, w := range []string{"0/poll/rest", "1/poll/rest/method", "2/poll/rest/method", "3/poll/until", "4/poll/until", "5/poll/until", "6/poll/until",
			"7/poll/every", "8/poll/every", "9/poll/every", "10/poll/timeout", "11/poll/timeout", "12/poll/timeout", "13/poll/on_timeout",
			"14/poll/rest/url", "15/poll/rest/secret", "16/with", "17/undo"} {
			want = append(want, prefix+w)
		}
		wantFields(t, "a bad poll", problem.Errors, want...)
		for _, e := range problem.Errors {
			if strings.HasPrefix(e.Field, prefix+"1/") && !strings.Contains(e.Message, "repeats its call") {
				t.Errorf("the refusal of a POST = %+v", e)
			}
		}
		if t.Failed() {
			t.FailNow()
		}

		// Polled until approved: a 5xx and a 429 on the way don't fail it. What
		// a later step reads is the last answer, scrubbed.
		secret := func(s map[string]any) map[string]any {
			s["poll"].(map[string]any)["rest"].(map[string]any)["secret"] = "snow-token"
			return s
		}
		polled := rig.publishDefinition("poll-approved", waitDefinition(secret(pollOf(change, "change", map[string]any{"on_timeout": "fail"})),
			finishing(change, "{'status': steps.change.status, 'json': steps.change.json, 'met': steps.change.met}")))
		change.script(state("new"), changeAnswer{503, map[string]any{}}, changeAnswer{429, map[string]any{}},
			changeAnswer{200, map[string]any{"result": map[string]any{"state": "approved"}, "token": restToken}})
		req := rig.submit(requester, polled)
		var carried strings.Builder
		var early, flaky api.Step
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			watched := []string{"OpenPoll", "CheckPoll"}
			env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, args converter.EncodedValues) {
				if slices.Contains(watched, info.ActivityType.Name) {
					var in provision.PollInput
					if err := args.Get(&in); err != nil {
						t.Errorf("%s's input: %v", info.ActivityType.Name, err)
					}
					b, _ := json.Marshal(in)
					carried.Write(b)
				}
			})
			env.SetOnActivityCompletedListener(func(info *activity.Info, result converter.EncodedValue, err error) {
				if slices.Contains(watched, info.ActivityType.Name) && err == nil && result != nil && result.HasValue() {
					var out any
					_ = result.Get(&out)
					b, _ := json.Marshal(out)
					carried.Write(b)
				}
			})
			env.RegisterDelayedCallback(func() { early = rig.steps(req.Id, requester)["step:change"] }, 5*time.Minute)
			env.RegisterDelayedCallback(func() { flaky = rig.steps(req.Id, requester)["step:change"] }, 15*time.Minute)
		})
		if carried.Len() == 0 {
			t.Fatal("no check's input or result was read")
		}
		// An answer doesn't travel with each check.
		for _, hidden := range []string{restToken, `"new"`, `"result"`} {
			if strings.Contains(carried.String(), hidden) {
				t.Fatalf("Temporal would hold %q: %s", hidden, carried.String())
			}
		}
		if early.Status != api.StepStatusRunning || early.System == nil || *early.System != api.StepSystemWait || !strings.Contains(deref(early.Detail), "Last check: GET answered 200, not yet") {
			t.Fatalf("after the first check = %+v", early)
		}
		if early.DueAt == nil || time.Until(*early.DueAt) < 71*time.Hour {
			t.Fatalf("its deadline = %v", early.DueAt)
		}
		if !strings.Contains(deref(flaky.Detail), "Last check failed") || !strings.Contains(deref(flaky.Detail), "503") {
			t.Fatalf("after a 503 = %+v", flaky)
		}
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("polled = %s %q", r.Status, errorOf(r))
		}
		if change.checked() != 4 {
			t.Fatalf("%d checks", change.checked())
		}
		if change.checks[0] != "Bearer "+restToken || change.methods[0] != http.MethodGet {
			t.Fatalf("a check = %s %q", change.methods[0], change.checks[0])
		}
		out := rig.outputs(req.Id, "step:change")
		if out["status"] != float64(200) || out["json"] != "approved" || out["met"] != true {
			t.Fatalf("its outputs = %v", out)
		}
		if got := change.got(); len(got) != 1 || got[0]["status"] != float64(200) || got[0]["json"] != "approved" || got[0]["met"] != true {
			t.Fatalf("what the later step read = %v", got)
		}
		s := rig.steps(req.Id, requester)["step:change"]
		if s.Status != api.StepStatusDone || s.DueAt != nil || !strings.Contains(deref(s.Detail), "Its condition holds") {
			t.Fatalf("met = %+v", s)
		}
		// An answer that echoes the secrets: scrubbed where it is kept.
		echoed := rig.publishDefinition("poll-echo", waitDefinition(func() map[string]any {
			s := secret(pollOf(change, "change", map[string]any{"until": "steps.change.json.result.state == 'approved'"}))
			delete(s["poll"].(map[string]any)["rest"].(map[string]any), "keep")
			return s
		}()))
		echoed.Inputs["api_password"] = restInput
		change.script(changeAnswer{200, map[string]any{"result": map[string]any{"state": "approved"}, "token": restToken, "password": restInput}})
		req = rig.submit(requester, echoed)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("echoed = %s %q", r.Status, errorOf(r))
		}
		var kept string
		if err := h.store.Pool.QueryRow(ctx, `select outputs::text || detail from request_steps where request_id = $1 and key = 'step:change'`, req.Id).Scan(&kept); err != nil {
			t.Fatal(err)
		}
		for _, hidden := range []string{restToken, restInput} {
			if strings.Contains(kept, hidden) {
				t.Fatalf("the step keeps %q: %s", hidden, kept)
			}
		}

		// A status a next check wouldn't change fails it at once, unless the
		// item lists it among what it accepts.
		refused := rig.publishDefinition("poll-refused", waitDefinition(pollOf(change, "change", nil)))
		change.script(state("new"), changeAnswer{404, map[string]any{}})
		req = rig.submit(requester, refused)
		rig.run(req.Id)
		r := rig.get(req.Id, requester)
		if r.Status != api.RequestStatusFailed || !strings.Contains(errorOf(r), "404") || change.checked() != 2 {
			t.Fatalf("a 404 = %s %q after %d checks", r.Status, errorOf(r), change.checked())
		}
		notYet := rig.publishDefinition("poll-not-yet", waitDefinition(func() map[string]any {
			s := pollOf(change, "change", nil)
			s["poll"].(map[string]any)["rest"].(map[string]any)["accept"] = []any{200, 404}
			return s
		}()))
		change.script(changeAnswer{404, map[string]any{}}, state("approved"))
		req = rig.submit(requester, notYet)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded || change.checked() != 2 {
			t.Fatalf("a 404 accepted = %s %q after %d checks", r.Status, errorOf(r), change.checked())
		}

		// A condition that can't be read fails it at once, and reads no literal.
		for slug, expr := range map[string]string{
			"poll-missing":  "steps.change.json.nope == '" + waitLiteral + "'",
			"poll-notbool":  "steps.change.status",
			"poll-overload": "int(steps.change.json + '" + waitLiteral + "') == 1",
		} {
			d := rig.publishDefinition(slug, waitDefinition(pollOf(change, "change", map[string]any{"until": expr})))
			change.script(state("new"))
			req = rig.submit(requester, d)
			rig.run(req.Id)
			r := rig.get(req.Id, requester)
			if r.Status != api.RequestStatusFailed || change.checked() != 1 || !strings.Contains(errorOf(r), "condition") || strings.Contains(errorOf(r), waitLiteral) {
				t.Errorf("%s = %s %q after %d checks", slug, r.Status, errorOf(r), change.checked())
			}
		}

		// The deadline fails it by default, after one last check.
		fails := rig.publishDefinition("poll-deadline", waitDefinition(pollOf(change, "change", map[string]any{"every": "1m", "timeout": "3m"})))
		change.script(state("new"))
		req = rig.submit(requester, fails)
		rig.run(req.Id)
		r = rig.get(req.Id, requester)
		s = rig.steps(req.Id, requester)["step:change"]
		if r.Status != api.RequestStatusFailed || s.Status != api.StepStatusFailed || !strings.Contains(errorOf(r), "wasn't met within 3m") {
			t.Fatalf("the deadline = %s %q, the step %+v", r.Status, errorOf(r), s)
		}
		if n := change.checked(); n < 3 || n > 5 {
			t.Fatalf("%d checks in 3m, one each 1m", n)
		}

		// A Retry starts it again: its deadline counts from then.
		change.script(state("approved"))
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		if s := rig.steps(req.Id, requester)["step:change"]; s.Status != api.StepStatusPending || s.DueAt != nil {
			t.Fatalf("reset for a retry = %+v", s)
		}
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("retried = %s %q", r.Status, errorOf(r))
		}

		// Or it goes on: a later step reads the last answer and that it wasn't met.
		goes := rig.publishDefinition("poll-goes", waitDefinition(pollOf(change, "change", map[string]any{"every": "1m", "timeout": "2m", "on_timeout": "continue"}),
			finishing(change, "{'status': steps.change.status, 'json': steps.change.json, 'met': steps.change.met}")))
		change.script(state("new"), changeAnswer{503, map[string]any{}})
		req = rig.submit(requester, goes)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("went on = %s %q", r.Status, errorOf(r))
		}
		if got := change.got(); len(got) != 1 || got[0]["status"] != float64(200) || got[0]["json"] != "new" || got[0]["met"] != false {
			t.Fatalf("what the later step read = %v", got)
		}
		if s := rig.steps(req.Id, requester)["step:change"]; s.Status != api.StepStatusDone || s.DueAt != nil || !strings.Contains(deref(s.Detail), "went on") {
			t.Fatalf("went on = %+v", s)
		}

		// With nothing but failed checks: no answer to read.
		none := rig.publishDefinition("poll-none", waitDefinition(pollOf(change, "change", map[string]any{"every": "1m", "timeout": "2m", "on_timeout": "continue"}),
			finishing(change, "{'none': steps.change.status == null && steps.change.json == null, 'met': steps.change.met}")))
		change.script(changeAnswer{500, map[string]any{}})
		req = rig.submit(requester, none)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("no answer = %s %q", r.Status, errorOf(r))
		}
		if got := change.got(); len(got) != 1 || got[0]["none"] != true || got[0]["met"] != false {
			t.Fatalf("what the later step read of no answer = %v", got)
		}

		// A PUT may poll: it can be repeated.
		puts := rig.publishDefinition("poll-put", waitDefinition(pollOf(change, "change", map[string]any{
			"rest": map[string]any{"method": "PUT", "url": change.URL + "/api/change", "keep": "$.result.state"}})))
		change.script(state("approved"))
		req = rig.submit(requester, puts)
		rig.run(req.Id)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded || change.methods[0] != http.MethodPut {
			t.Fatalf("a PUT = %s %q", r.Status, errorOf(r))
		}

		// Cancelling stops it at once, and checks no more.
		cancels := rig.publishDefinition("poll-cancels", waitDefinition(pollOf(change, "change", nil)))
		change.script(state("new"))
		req = rig.submit(requester, cancels)
		var cancelled, ended time.Time
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				h.json("POST", "/api/v1/requests/"+req.Id+"/cancel", requester, nil, 202, nil)
				cancelled = env.Now()
				env.CancelWorkflow()
			}, 15*time.Minute)
			env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
				if info.ActivityType.Name == "Finish" {
					ended = env.Now()
				}
			})
		})
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusCancelled {
			t.Fatalf("cancelled = %s %q", r.Status, errorOf(r))
		}
		if s := rig.steps(req.Id, requester)["step:change"]; s.Status != api.StepStatusCancelled {
			t.Fatalf("the cancelled poll = %+v", s)
		}
		if ended.IsZero() || ended.Sub(cancelled) > time.Minute || change.checked() != 2 {
			t.Fatalf("cancelled at %s, ended at %s, %d checks", cancelled, ended, change.checked())
		}

		// A sibling's failure stops it; a Retry starts it again.
		sibling := rig.publishDefinition("poll-sibling", waitDefinition(map[string]any{"parallel": []any{
			pollOf(change, "change", nil),
			map[string]any{"key": "two", "title": "Two", "rest": map[string]any{"method": "POST", "url": change.URL + "/api/gone", "accept": []any{201}}},
		}}))
		change.script(state("new"))
		req = rig.submit(requester, sibling)
		rig.run(req.Id)
		r = rig.get(req.Id, requester)
		s = rig.steps(req.Id, requester)["step:change"]
		if r.Status != api.RequestStatusFailed || s.Status != api.StepStatusCancelled {
			t.Fatalf("a sibling failed = %s, the poll %+v", r.Status, s)
		}

		// What a requester's form draws of it: what it reads, not its host's
		// path, its secret's name, what it accepts or keeps.
		rig.publishDefinition("poll-masked", waitDefinition(func() map[string]any {
			s := secret(pollOf(change, "change", map[string]any{"until": "steps.change.json == '" + waitLiteral + "'"}))
			s["poll"].(map[string]any)["rest"].(map[string]any)["accept"] = []any{200, 418}
			return s
		}()))
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/poll-masked/form?project_id=prj_web", requester, nil, 200, &form)
		topology, err := json.Marshal(form.Topology)
		if err != nil {
			t.Fatal(err)
		}
		for _, hidden := range []string{waitLiteral, "snow-token", "418", "$.result", change.URL, "/api/change"} {
			if strings.Contains(string(topology), hidden) {
				t.Errorf("the requester's topology reads %q: %s", hidden, topology)
			}
		}
		if !strings.Contains(string(topology), `"every":"10m"`) || !strings.Contains(string(topology), "${inputs.name}") {
			t.Errorf("the requester's topology = %s", topology)
		}
	})
}

// TestCompositeWaitThroughTemporal: waits on a real Temporal, for what its test
// environment can't show, as its mocked clock fires any due time at once: they
// really wait, continue as new with their state in the database, outlive their
// worker, act on what came due without one, and stop when cancelled.
func TestCompositeWaitThroughTemporal(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	// A low limit, so that this history is "long" and Temporal suggests
	// continuing as new at each round; and rounds that take a second.
	ts := startTemporal(t, "--dynamic-config-value", "limit.historyCount.suggestContinueAsNew=5")
	recheck := provision.WaitRecheck
	provision.WaitRecheck = time.Second
	t.Cleanup(func() { provision.WaitRecheck = recheck })

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester, queue = "mkeller", "tnguyen", "rvav-wait-e2e"
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
		// The minimum interval is a check on save: this test can't wait a minute.
		quicken := func(slug, every, timeout string) {
			t.Helper()
			if _, err := h.store.Pool.Exec(ctx, `update catalog_item_versions
				set blueprint = jsonb_set(jsonb_set(blueprint, '{steps,0,poll,every}', to_jsonb($1::text)), '{steps,0,poll,timeout}', to_jsonb($2::text))
				where item_id = (select id from catalog_items where slug = $3)`, every, timeout, slug); err != nil {
				t.Fatal(err)
			}
		}

		// A wait really waits, continuing as new each round with the time it
		// began with, its worker gone for part of it; what came after didn't run.
		waits := rig.publishDefinition("wait-durable", waitDefinition(waitFor("keep", "8s"), finishing(change, "{'until': steps.keep.until}")))
		req := rig.submit(requester, waits)
		var due *time.Time
		until("the wait didn't begin", func() bool { due = rig.steps(req.Id, requester)["step:keep"].DueAt; return due != nil })
		first := runOf(req.Id)
		until("the wait didn't continue as new", func() bool { return runOf(req.Id) != first })
		if s := rig.steps(req.Id, requester)["step:keep"]; s.Status != api.StepStatusRunning || !s.DueAt.Equal(*due) {
			t.Fatalf("continued as new = %+v, it was due %v", s, due)
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
		if got := change.got(); len(got) != 1 || got[0]["until"] != due.UTC().Format(time.RFC3339) {
			t.Fatalf("what the later step read = %v, due %v", got, due)
		}

		// A poll goes on checking across runs, its history small and its
		// deadline the first it set; stopped when it holds, with its worker gone
		// for a while, and read by what comes next.
		polls := rig.publishDefinition("poll-durable", waitDefinition(pollOf(change, "change", nil),
			finishing(change, "{'json': steps.change.json, 'met': steps.change.met}")))
		quicken("poll-durable", "1s", "72h")
		change.script(state("new"))
		req = rig.submit(requester, polls)
		until("the poll didn't begin", func() bool { due = rig.steps(req.Id, requester)["step:change"].DueAt; return due != nil })
		first = runOf(req.Id)
		until("the poll didn't continue as new", func() bool { return runOf(req.Id) != first })
		at := change.checked()
		until("it stopped checking after continuing as new", func() bool { return change.checked() >= at+3 })
		if s := rig.steps(req.Id, requester)["step:change"]; s.Status != api.StepStatusRunning || !s.DueAt.Equal(*due) {
			t.Fatalf("continued as new = %+v, it was due %v", s, due)
		}
		// However long it polls, a run's history stays short.
		iter := tc.GetWorkflowHistory(ctx, provision.WorkflowID(req.Id), "", false, 0)
		events := 0
		for iter.HasNext() {
			if _, err := iter.Next(); err != nil {
				t.Fatal(err)
			}
			events++
		}
		if events > 60 {
			t.Fatalf("a run of the poll has %d events", events)
		}
		// Not checking faster than it says: at most one a second, with a margin.
		stop()
		after := change.checked()
		time.Sleep(1500 * time.Millisecond)
		if n := change.checked(); n > after+1 {
			t.Fatalf("%d checks with no worker", n-after)
		}
		change.script(state("approved"))
		stop = worker()
		h.waitStatus(req.Id, requester, api.RequestStatusSucceeded)
		if got := change.got(); len(got) != 1 || got[0]["json"] != "approved" || got[0]["met"] != true {
			t.Fatalf("what the later step read = %v", got)
		}

		// Its deadline comes with no worker: one last check when one is back,
		// then what the step says.
		late := rig.publishDefinition("poll-late", waitDefinition(pollOf(change, "change", map[string]any{"on_timeout": "continue"}),
			finishing(change, "{'json': steps.change.json, 'met': steps.change.met}")))
		quicken("poll-late", "1s", "3s")
		change.script(state("new"))
		req = rig.submit(requester, late)
		until("the poll didn't check", func() bool { return change.checked() > 0 })
		stop()
		time.Sleep(4 * time.Second)
		change.script(state("new"))
		stop = worker()
		h.waitStatus(req.Id, requester, api.RequestStatusSucceeded)
		if change.checked() < 1 {
			t.Fatal("no last check when its worker came back")
		}
		if got := change.got(); len(got) != 1 || got[0]["met"] != false {
			t.Fatalf("what the later step read = %v", got)
		}

		// Cancelling through Temporal stops each, whatever is left to wait.
		for slug, step := range map[string]map[string]any{"wait-cancels": waitFor("keep", "72h"), "poll-cancels": pollOf(change, "keep", nil)} {
			d := rig.publishDefinition(slug, waitDefinition(step))
			if slug == "poll-cancels" {
				quicken(slug, "1h", "72h")
			}
			req = rig.submit(requester, d)
			until(slug+" didn't begin", func() bool { return rig.steps(req.Id, requester)["step:keep"].DueAt != nil })
			began := time.Now()
			h.json("POST", "/api/v1/requests/"+req.Id+"/cancel", requester, nil, 202, nil)
			h.waitStatus(req.Id, requester, api.RequestStatusCancelled)
			if took := time.Since(began); took > 15*time.Second {
				t.Errorf("%s took %s to cancel", slug, took)
			}
			if s := rig.steps(req.Id, requester)["step:keep"]; s.Status != api.StepStatusCancelled {
				t.Errorf("%s's step = %+v", slug, s)
			}
		}
	})
}
