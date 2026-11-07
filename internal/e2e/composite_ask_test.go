package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
)

// askAnswer marks what a person typed, to find where it went.
const askAnswer = "sentinel-answer-7731"

func askDefinition(rollback bool, steps ...any) api.CatalogVersionDefinition {
	def := api.CatalogVersionDefinition{
		FormSchema: map[string]any{"type": "object", "required": []string{"name"}, "properties": map[string]any{
			"name": map[string]any{"type": "string"}, "api_password": map[string]any{"type": "string"}}},
		UiSchema: api.UiSchema{Fields: map[string]api.UiField{
			"name":         {Widget: api.UiWidgetText, Label: "Name"},
			"api_password": {Widget: api.UiWidgetSecret, Label: "Password"},
		}},
		InitialValues: map[string]any{},
		Blueprint:     map[string]any{"kind": "composite", "rollback_on_failure": rollback, "steps": steps},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Main", Fields: []string{"name", "api_password"}}}
	return def
}

func askStep(key string, ask map[string]any) map[string]any {
	return map[string]any{"key": key, "title": "Ask " + key, "ask": ask}
}

// askForm is a form of each type of question.
func askForm() []any {
	return []any{
		map[string]any{"key": "reason", "label": "Why this size?", "type": "string", "required": true},
		map[string]any{"key": "size", "label": "Which size?", "type": "choice", "options": []any{"S", "M"}, "default": "S"},
		map[string]any{"key": "count", "label": "How many?", "type": "number"},
		map[string]any{"key": "urgent", "label": "Urgent?", "type": "boolean"},
	}
}

// askRig runs composites of waits: it publishes items, requests them and
// answers at moments of the workflow's mocked time.
type askRig struct {
	t     *testing.T
	h     *harness
	ctx   context.Context
	admin string
}

func (r askRig) publish(slug string, rollback bool, steps ...any) api.RequestDraft {
	r.t.Helper()
	return r.publishDefinition(slug, askDefinition(rollback, steps...))
}

func (r askRig) publishDefinition(slug string, def api.CatalogVersionDefinition) api.RequestDraft {
	r.t.Helper()
	var item api.AdminCatalogItem
	r.h.json("POST", "/api/v1/admin/catalog/items", r.admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: "A wait.",
		Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
	r.h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", r.admin, nil, 200, nil)
	r.h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", r.admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
	return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
		Inputs: map[string]any{"name": "web"}}
}

// moment is something done while a workflow waits.
type moment struct {
	after time.Duration
	do    func(env *testsuite.TestWorkflowEnvironment)
}

func (r askRig) run(id string, moments ...moment) {
	r.t.Helper()
	r.h.runWorkflow(id, func(env *testsuite.TestWorkflowEnvironment) {
		for _, m := range moments {
			env.RegisterDelayedCallback(func() { m.do(env) }, m.after)
		}
	})
}

// deliver hands the workflow what the API signalled.
func (r askRig) deliver(env *testsuite.TestWorkflowEnvironment) {
	r.h.wf.mu.Lock()
	sigs := r.h.wf.byID
	r.h.wf.byID = nil
	r.h.wf.mu.Unlock()
	for _, s := range sigs {
		env.SignalWorkflow(s.name, s.arg)
	}
}

func (r askRig) submit(user string, d api.RequestDraft) api.Request {
	r.t.Helper()
	var req api.Request
	r.h.json("POST", "/api/v1/requests", user, d, 202, &req, "Idempotency-Key", uuid.NewString())
	return req
}

func (r askRig) get(id, user string) api.Request {
	r.t.Helper()
	var req api.Request
	r.h.json("GET", "/api/v1/requests/"+id, user, nil, 200, &req)
	return req
}

func (r askRig) steps(id, user string) map[string]api.Step {
	r.t.Helper()
	var list struct{ Items []api.Step }
	r.h.json("GET", "/api/v1/requests/"+id+"/steps", user, nil, 200, &list)
	out := map[string]api.Step{}
	for _, s := range list.Items {
		out[s.Key] = s
		for _, ch := range s.Children {
			out[ch.Key] = ch
		}
	}
	return out
}

func (r askRig) outputs(id, key string) map[string]any {
	r.t.Helper()
	var raw []byte
	if err := r.h.store.Pool.QueryRow(r.ctx, `select outputs from request_steps where request_id = $1 and key = $2`, id, key).Scan(&raw); err != nil {
		r.t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		r.t.Fatalf("%s's outputs = %s: %v", key, raw, err)
	}
	return out
}

// rows are a request's questions, as this person reads them.
func (r askRig) rows(id, user string) []api.Approval {
	r.t.Helper()
	var list struct{ Items []api.Approval }
	r.h.json("GET", "/api/v1/requests/"+id+"/approvals", user, nil, 200, &list)
	return list.Items
}

// open is the question a person has in their inbox for a request.
func (r askRig) open(id, user string) api.ApprovalSummary {
	r.t.Helper()
	var inbox api.ApprovalPage
	r.h.json("GET", "/api/v1/approvals", user, nil, 200, &inbox)
	for _, it := range inbox.Items {
		if it.Request.Id == id {
			return it
		}
	}
	r.t.Fatalf("%s isn't asked anything about %s: %+v", user, id, inbox.Items)
	return api.ApprovalSummary{}
}

func (r askRig) notAsked(id, user string) {
	r.t.Helper()
	var inbox api.ApprovalPage
	r.h.json("GET", "/api/v1/approvals", user, nil, 200, &inbox)
	if slices.ContainsFunc(inbox.Items, func(it api.ApprovalSummary) bool { return it.Request.Id == id }) {
		r.t.Fatalf("%s is still asked about %s", user, id)
	}
}

func (r askRig) answer(id, user string, want int, answer map[string]any, out any) {
	r.t.Helper()
	d := api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove}
	if answer != nil {
		d.Answer = &answer
	}
	r.h.json("POST", "/api/v1/approvals/"+id+"/decision", user, d, want, out)
}

func (r askRig) reject(id, user, comment string, want int) {
	r.t.Helper()
	r.h.json("POST", "/api/v1/approvals/"+id+"/decision", user,
		api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionReject, Comment: &comment}, want, nil)
}

// count runs a query counting rows, whose errors surface: a check that can't
// read what it looks for must not pass.
func (r askRig) count(query string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.h.store.Pool.QueryRow(r.ctx, query, args...).Scan(&n); err != nil {
		r.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func wantFields(t *testing.T, what string, errs []api.FieldError, want ...string) {
	t.Helper()
	got := fieldsOf(errs)
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s = %v, no %s", what, got, w)
		}
	}
}

// TestCompositeAsk: a composite step that waits for a person (ADR-0033). Who
// may answer and how often, what it checks, what later steps read, what its
// timeout does, and what cancelling and retrying do to the question.
func TestCompositeAsk(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		cmdb := newRESTCMDB(t)
		h.svc.REST.Hosts = []string{cmdb.URL}
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-token", Kind: api.SecretWriteKindToken, Value: new(restToken)}, 201, nil)
		later := func(when string) map[string]any {
			return map[string]any{"key": "note", "title": "Note it", "when": when, "rest": map[string]any{
				"method": "POST", "url": cmdb.URL + "/api/finish", "secret": "cmdb-token",
				"body": "{'answered': steps.who.answered, 'by': steps.who.by, 'comment': steps.who.comment, 'answer': steps.who.answer}"}}
		}

		// Refused when saved: above all, a form that asks for a secret.
		var problem struct{ Errors []api.FieldError }
		bad := askDefinition(false,
			askStep("nobody", map[string]any{}),
			askStep("times", map[string]any{"group": "g", "timeout": "soon", "on_timeout": "wait"}),
			askStep("untimed", map[string]any{"group": "g", "on_timeout": "continue"}),
			askStep("escalates", map[string]any{"group": "g", "timeout": "1h", "on_timeout": "escalate"}),
			askStep("unknown", map[string]any{"approvers": []any{"usr_nobody"}}),
			map[string]any{"key": "inputs", "with": map[string]any{"x": "1"}, "ask": map[string]any{"group": "g"}},
			map[string]any{"key": "undone", "ask": map[string]any{"group": "g"}, "undo": map[string]any{"job_template": "x"}},
			map[string]any{"key": "both", "ask": map[string]any{"group": "g"}, "workflow": "x"},
			askStep("forms", map[string]any{"group": "g", "form": []any{
				map[string]any{"key": "Bad Key", "label": "x", "type": "string"},
				map[string]any{"key": "nolabel", "type": "string"},
				map[string]any{"key": "kind", "label": "x", "type": "date"},
				map[string]any{"key": "pick", "label": "x", "type": "choice"},
				map[string]any{"key": "pick", "label": "x", "type": "choice", "options": []any{"a"}, "default": "b"},
				map[string]any{"key": "num", "label": "x", "type": "number", "default": "ten"},
			}}),
			askStep("secrets", map[string]any{"group": "g", "form": []any{
				map[string]any{"key": "pw", "label": "Password", "type": "string", "secret": true},
				map[string]any{"key": "token", "label": "Token", "type": "secret"},
			}}),
		)
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-ask", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.Composite, Definition: &bad}, 422, &problem)
		const prefix = "/definition/blueprint/steps/"
		var want []string
		for _, w := range []string{"0/ask", "1/ask/timeout", "1/ask/on_timeout", "2/ask/on_timeout", "3/ask/escalate_to", "4/ask", "5/with", "6/undo", "7",
			"8/ask/form/0/key", "8/ask/form/1/label", "8/ask/form/2/type", "8/ask/form/3/options", "8/ask/form/4/key", "8/ask/form/4/default", "8/ask/form/5/default",
			"9/ask/form/0", "9/ask/form/1"} {
			want = append(want, prefix+w)
		}
		wantFields(t, "a bad wait", problem.Errors, want...)
		for _, e := range problem.Errors {
			if strings.HasPrefix(e.Field, prefix+"9/") && !strings.Contains(e.Message, "Temporal's history") {
				t.Errorf("the refusal of a secret field = %+v", e)
			}
		}
		if t.Failed() {
			t.FailNow()
		}

		// A form, to a group: who may answer, what it may be, and once only.
		form := rig.publish("ask-form", false,
			map[string]any{"key": "who", "title": "Why this size", "ask": map[string]any{"group": "rvav-data", "message": "Why does it need this much?",
				"form": askForm(), "timeout": "48h", "on_timeout": "fail"}},
			later("steps.who.answered"))
		// What the requester's form draws of it: no group, message, label or option.
		var requestForm api.RequestForm
		h.json("GET", "/api/v1/catalog/items/ask-form/form?project_id=prj_web", requester, nil, 200, &requestForm)
		topology, err := json.Marshal(requestForm.Topology)
		if err != nil {
			t.Fatal(err)
		}
		for _, hidden := range []string{"rvav-data", "Why does it need", "Why this size?", "Which size?"} {
			if strings.Contains(string(topology), hidden) {
				t.Errorf("the requester's topology reads %q: %s", hidden, topology)
			}
		}
		if !strings.Contains(string(topology), `"on_timeout":"fail"`) || !strings.Contains(string(topology), `"key":"reason"`) {
			t.Errorf("the requester's topology = %s", topology)
		}

		req := rig.submit(requester, form)
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			step := rig.steps(req.Id, requester)["step:who"]
			if step.Status != api.StepStatusRunning || deref(step.Detail) != "Waiting for a person" || step.System == nil || *step.System != api.StepSystemPerson {
				t.Errorf("the waiting step = %+v", step)
			}
			row := rig.open(req.Id, "sbrunner")
			if !row.CanDecide || row.StepKey == nil || *row.StepKey != "step:who" || row.Level.Name != "Why this size" || row.DueAt == nil {
				t.Errorf("the question = %+v", row)
			}
			// Told as for an approval: its page is the request's, and a card
			// that can't hold a form has no buttons.
			var linked, noCard int
			if err := h.store.Pool.QueryRow(ctx, `select count(*) filter (where link = $2), count(*) filter (where approval_id is null) from notifications
				where request_id = $1 and kind = 'approval_needed' and user_id = $3`, req.Id, "/requests/"+req.Number, seed.UserID("sbrunner")).Scan(&linked, &noCard); err != nil {
				t.Error(err)
			}
			if linked != 1 || noCard != 1 {
				t.Errorf("sbrunner's notification: %d linked, %d without a card's buttons", linked, noCard)
			}
			// Only those it names, administrators included; others don't learn it exists.
			rig.notAsked(req.Id, requester)
			rig.notAsked(req.Id, admin)
			given := map[string]any{"reason": "because"}
			rig.answer(row.Id, requester, 403, given, nil)
			rig.answer(row.Id, admin, 403, given, nil)
			rig.answer(row.Id, "lmartin", 403, given, nil)
			rig.answer(row.Id, "afavre", 404, given, nil)
			// The question is for those who answer it and those who see everything.
			for user, wantQuestion := range map[string]bool{"sbrunner": true, admin: true, requester: false, "lmartin": false} {
				rows := rig.rows(req.Id, user)
				if len(rows) != 1 || (rows[0].Question != nil) != wantQuestion {
					t.Errorf("%s reads the question %+v", user, rows)
				}
			}
			q := rig.rows(req.Id, "sbrunner")[0].Question
			if q == nil || deref(q.Message) != "Why does it need this much?" || len(q.Form) != 4 || q.Form[1].Key != "size" || q.Form[1].Default != "S" {
				t.Errorf("the question reads %+v", q)
			}
			// Checked by the server.
			for name, tc := range map[string]struct {
				answer map[string]any
				field  string
			}{
				"required":  {map[string]any{"size": "M"}, "/answer/reason"},
				"blank":     {map[string]any{"reason": ""}, "/answer/reason"},
				"unknown":   {map[string]any{"reason": "x", "other": 1}, "/answer/other"},
				"a type":    {map[string]any{"reason": 4}, "/answer/reason"},
				"a choice":  {map[string]any{"reason": "x", "size": "XXL"}, "/answer/size"},
				"a number":  {map[string]any{"reason": "x", "count": "two"}, "/answer/count"},
				"a boolean": {map[string]any{"reason": "x", "urgent": "yes"}, "/answer/urgent"},
				"too long":  {map[string]any{"reason": strings.Repeat("x", 4097)}, "/answer/reason"},
			} {
				var p struct{ Errors []api.FieldError }
				rig.answer(row.Id, "sbrunner", 422, tc.answer, &p)
				wantFields(t, name, p.Errors, tc.field)
			}
			rig.answer(row.Id, "sbrunner", 422, nil, nil) // reason is required
			rig.reject(row.Id, "sbrunner", "no", 422)     // a form is answered
			// It can't be handed on.
			h.json("POST", "/api/v1/approvals/"+row.Id+"/delegate", "sbrunner", api.ApprovalDelegation{UserId: seed.UserID("afavre")}, 403, nil)

			var done api.Approval
			rig.answer(row.Id, "sbrunner", 200, map[string]any{"reason": askAnswer, "size": "M", "urgent": false}, &done)
			if done.Status != api.ApprovalStatusApproved || deref(done.Outcome) != "Answered by Sophie Brunner" || done.Question == nil || done.Question.Answer == nil {
				t.Errorf("answered = %+v", done)
			}
			// Once.
			rig.answer(row.Id, "sbrunner", 409, map[string]any{"reason": "again"}, nil)
			rig.deliver(env)
		}})
		req = rig.get(req.Id, requester)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("answered = %s %s (%v)", req.Status, errorOf(req), keysOf(rig.steps(req.Id, requester)))
		}
		if step := rig.steps(req.Id, requester)["step:who"]; step.Status != api.StepStatusDone || deref(step.Detail) != "Answered by Sophie Brunner" {
			t.Fatalf("the answered step = %+v", step)
		}
		// The later step read it in CEL.
		posted := cmdb.seen("POST", "/api/finish")
		if len(posted) != 1 || posted[0].body["answered"] != true || posted[0].body["by"] != "sophie.brunner@corp.example" {
			t.Fatalf("what the later step read = %+v", posted)
		}
		got := posted[0].body["answer"].(map[string]any)
		if got["reason"] != askAnswer || got["size"] != "M" || got["urgent"] != false {
			t.Fatalf("the answer it read = %v", got)
		}
		if v, ok := got["count"]; !ok || v != nil {
			t.Fatalf("a question left blank = %v, %v", v, ok)
		}
		// An answer is the step's outputs and the row's, and nowhere else: no
		// audit, no notification, no event. And no stored secret anywhere near.
		like := "%" + askAnswer + "%"
		if n := rig.count(`select count(*) from approvals t where t::text like $1`, like); n != 1 {
			t.Fatalf("%d rows hold the answer", n)
		}
		if n := rig.count(`select count(*) from request_steps t where t::text like $1 and key = 'step:who'`, like); n != 1 {
			t.Fatalf("%d steps hold the answer", n)
		}
		for _, table := range []string{"audit_events", "notifications", "notification_deliveries", "events", "integration_events"} {
			if n := rig.count(`select count(*) from `+table+` t where t::text like $1`, like); n != 0 {
				t.Errorf("%s holds %d rows with the answer", table, n)
			}
		}
		for _, table := range []string{"approvals", "request_steps", "audit_events", "notifications", "events", "integration_events"} {
			if n := rig.count(`select count(*) from `+table+` t where t::text like $1`, "%"+restToken+"%"); n != 0 {
				t.Errorf("%s holds %d rows with the stored secret", table, n)
			}
		}
		// Reports count the request's approvals, not a step's.
		if n := rig.count(`select count(*) from approvals where request_id = $1 and step_key is not null and signalled_at is not null`, req.Id); n != 1 {
			t.Errorf("%d answers signalled", n)
		}

		// Approve or reject: a requester's own secret input and a stored secret
		// stay out of what Temporal holds, and a rejection fails the step and
		// undoes what was done.
		yes := rig.publish("ask-yes", true,
			map[string]any{"key": "first", "title": "First", "rest": map[string]any{"method": "POST", "url": cmdb.URL + "/api/ci/${inputs.name}", "secret": "cmdb-token"},
				"undo": map[string]any{"rest": map[string]any{"method": "DELETE", "url": cmdb.URL + "/api/ci/${inputs.name}", "secret": "cmdb-token"}}},
			askStep("who", map[string]any{"approvers": []any{seed.UserID("sbrunner")}, "message": "Is it right?"}),
			later(""))
		yes.Inputs["api_password"] = restInput
		req = rig.submit(requester, yes)
		var carried strings.Builder
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			watched := []string{"OpenAsk", "AskOutcome", "StopAsk", "ApprovalState", "ApprovalTimedOut"}
			env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, args converter.EncodedValues) {
				if !slices.Contains(watched, info.ActivityType.Name) {
					return
				}
				var in provision.AskInput
				if info.ActivityType.Name == "OpenAsk" {
					if err := args.Get(&in); err != nil {
						t.Errorf("OpenAsk's input: %v", err)
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
			env.RegisterDelayedCallback(func() {
				row := rig.open(req.Id, "sbrunner")
				// A card can answer this one: it has no form.
				if n := rig.count(`select count(*) from notifications where approval_id = $1 and user_id = $2`, row.Id, seed.UserID("sbrunner")); n != 1 {
					t.Errorf("%d notifications with buttons", n)
				}
				rig.answer(row.Id, "sbrunner", 422, map[string]any{"x": 1}, nil) // it has no form
				rig.reject(row.Id, "sbrunner", "", 422)                          // a rejection says why
				rig.reject(row.Id, "sbrunner", "Too big", 200)
				rig.deliver(env)
			}, 30*time.Minute)
		})
		if carried.Len() == 0 {
			t.Fatal("no wait's input or result was read")
		}
		for _, secret := range []string{restToken, restInput} {
			if strings.Contains(carried.String(), secret) {
				t.Fatalf("Temporal would hold %q: %s", secret, carried.String())
			}
		}
		req = rig.get(req.Id, requester)
		if req.Status != api.RequestStatusFailed || !strings.Contains(errorOf(req), "Rejected by Sophie Brunner: Too big") {
			t.Fatalf("rejected = %s %q", req.Status, errorOf(req))
		}
		steps := rig.steps(req.Id, requester)
		if s := steps["step:who"]; s.Status != api.StepStatusFailed {
			t.Fatalf("the rejected step = %+v", s)
		}
		if s := steps["step:first"]; s.Status != api.StepStatusCancelled {
			t.Fatalf("what was done before = %+v (%v)", s, keysOf(steps))
		}
		if len(cmdb.seen("DELETE", "/api/ci/")) != 1 {
			t.Fatalf("the undo = %+v", cmdb.seen("DELETE", "/api/ci/"))
		}

		// A Retry asks again, with the earlier question kept as history.
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			row := rig.open(req.Id, "sbrunner")
			if len(rig.rows(req.Id, "sbrunner")) != 2 {
				t.Errorf("the questions = %+v", rig.rows(req.Id, "sbrunner"))
			}
			rig.answer(row.Id, "sbrunner", 200, nil, nil)
			rig.deliver(env)
		}})
		req = rig.get(req.Id, requester)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("retried = %s %q (%v)", req.Status, errorOf(req), keysOf(rig.steps(req.Id, requester)))
		}
		if out := rig.outputs(req.Id, "step:who"); out["answered"] != true || out["by"] != "sophie.brunner@corp.example" || len(out["answer"].(map[string]any)) != 0 {
			t.Fatalf("an approval's outputs = %v", out)
		}

		// A timeout that fails: nothing to answer any more.
		fails := rig.publish("ask-fails", false, askStep("who", map[string]any{"group": "rvav-data", "timeout": "1h", "on_timeout": "fail"}))
		req = rig.submit(requester, fails)
		rig.run(req.Id)
		req = rig.get(req.Id, requester)
		if req.Status != api.RequestStatusFailed || !strings.Contains(errorOf(req), "No answer within") {
			t.Fatalf("timed out = %s %q", req.Status, errorOf(req))
		}
		rig.notAsked(req.Id, "sbrunner")
		if rows := rig.rows(req.Id, "sbrunner"); len(rows) != 1 || rows[0].Status != api.ApprovalStatusRejected {
			t.Fatalf("its row = %+v", rows)
		}

		// A timeout that goes on: a later step reads that nobody answered.
		goes := rig.publish("ask-goes", false,
			askStep("who", map[string]any{"group": "rvav-data", "form": askForm(), "timeout": "1h", "on_timeout": "continue"}),
			later("!steps.who.answered"))
		before := len(cmdb.seen("POST", "/api/finish"))
		req = rig.submit(requester, goes)
		rig.run(req.Id)
		req = rig.get(req.Id, requester)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("went on = %s %q (%v)", req.Status, errorOf(req), keysOf(rig.steps(req.Id, requester)))
		}
		out := rig.outputs(req.Id, "step:who")
		answer := out["answer"].(map[string]any)
		if out["answered"] != false || out["by"] != "" || out["comment"] != "" || answer["size"] != "S" || len(answer) != 4 {
			t.Fatalf("what a later step reads with no answer = %v", out)
		}
		if v, ok := answer["reason"]; !ok || v != nil {
			t.Fatalf("a question with no default = %v, %v", v, ok)
		}
		if step := rig.steps(req.Id, requester)["step:who"]; step.Status != api.StepStatusDone || !strings.Contains(deref(step.Detail), "it went on") {
			t.Fatalf("the step that went on = %+v", step)
		}
		if posted := cmdb.seen("POST", "/api/finish"); len(posted) != before+1 || posted[before].body["answered"] != false {
			t.Fatalf("the later step = %+v", posted)
		}

		// A timeout that escalates: those it escalates to are told and may
		// answer; with no answer after that, it fails.
		escalates := rig.publish("ask-escalates", false, askStep("who", map[string]any{"group": "rvav-data", "timeout": "1h", "on_timeout": "escalate",
			"escalate_to": map[string]any{"approvers": []any{seed.UserID("afavre")}}}))
		req = rig.submit(requester, escalates)
		// Due times are the database's clock and this workflow's is mocked, so
		// the second timeout comes at once: it answers as the first one ends.
		escalated := false
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.SetOnActivityCompletedListener(func(info *activity.Info, _ converter.EncodedValue, _ error) {
				if info.ActivityType.Name != "ApprovalTimedOut" || escalated {
					return
				}
				escalated = true
				row := rig.open(req.Id, "afavre")
				if row.Escalated == nil || !*row.Escalated {
					t.Errorf("escalated = %+v", row)
				}
				if n := rig.count(`select count(*) from notifications where request_id = $1 and user_id = $2 and kind = 'approval_needed' and link = $3`,
					req.Id, seed.UserID("afavre"), "/requests/"+req.Number); n != 1 {
					t.Errorf("%d notifications for the escalation", n)
				}
				rig.answer(row.Id, "afavre", 200, nil, nil)
				rig.deliver(env)
			})
		})
		req = rig.get(req.Id, requester)
		if req.Status != api.RequestStatusSucceeded || rig.outputs(req.Id, "step:who")["by"] != "alain.favre@corp.example" {
			t.Fatalf("answered after escalating = %s %q", req.Status, errorOf(req))
		}
		req = rig.submit(requester, escalates)
		rig.run(req.Id)
		req = rig.get(req.Id, requester)
		if req.Status != api.RequestStatusFailed || !strings.Contains(errorOf(req), "even after escalation") {
			t.Fatalf("not answered even after escalating = %s %q", req.Status, errorOf(req))
		}

		// Cancelling stops the wait, takes the question out of view and undoes
		// what was done.
		cancels := rig.publish("ask-cancels", true,
			map[string]any{"key": "first", "title": "First", "rest": map[string]any{"method": "POST", "url": cmdb.URL + "/api/ci/${inputs.name}"},
				"undo": map[string]any{"rest": map[string]any{"method": "DELETE", "url": cmdb.URL + "/api/ci/cancelled"}}},
			askStep("who", map[string]any{"group": "rvav-data", "form": askForm()}))
		req = rig.submit(requester, cancels)
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			row := rig.open(req.Id, "sbrunner")
			h.json("POST", "/api/v1/requests/"+req.Id+"/cancel", requester, nil, 202, nil)
			rig.notAsked(req.Id, "sbrunner")
			rig.answer(row.Id, "sbrunner", 409, map[string]any{"reason": "late"}, nil)
			env.CancelWorkflow()
		}})
		req = rig.get(req.Id, requester)
		if req.Status != api.RequestStatusCancelled {
			t.Fatalf("cancelled = %s %q", req.Status, errorOf(req))
		}
		if s := rig.steps(req.Id, requester)["step:who"]; s.Status != api.StepStatusCancelled {
			t.Fatalf("the cancelled wait = %+v", s)
		}
		if len(cmdb.seen("DELETE", "/api/ci/cancelled")) != 1 {
			t.Fatal("cancelling undid nothing")
		}

		// The requester: not by being in its group, nor by name alone, and
		// only as what the step asks them as. Nobody else is left to answer
		// a step that names only the requester by ID: refused when requested.
		group := rig.publish("ask-group", false, askStep("who", map[string]any{"group": "rvav-web", "timeout": "1h", "on_timeout": "fail"}))
		req = rig.submit(requester, group)
		rig.run(req.Id, moment{30 * time.Minute, func(*testsuite.TestWorkflowEnvironment) {
			rig.notAsked(req.Id, requester)
			rows := rig.rows(req.Id, requester)
			if len(rows) != 1 || rows[0].CanDecide {
				t.Errorf("the requester in its group = %+v", rows)
			}
			rig.answer(rows[0].Id, requester, 403, nil, nil)
		}})
		byName := rig.publish("ask-name", false, askStep("who", map[string]any{"approvers": []any{seed.UserID(requester)}}))
		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/requests", requester, byName, 422, &refused, "Idempotency-Key", uuid.NewString())
		if len(refused.Errors) != 1 || !strings.Contains(refused.Errors[0].Message, "asks only you") {
			t.Fatalf("asking only the requester = %+v", refused.Errors)
		}
		asked := rig.publish("ask-requester", false, askStep("who", map[string]any{"requester": true, "form": []any{
			map[string]any{"key": "ok", "label": "Is it right?", "type": "boolean", "required": true}}}))
		req = rig.submit(requester, asked)
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			row := rig.open(req.Id, requester)
			rig.answer(row.Id, "lmartin", 403, map[string]any{"ok": true}, nil)
			var done api.Approval
			rig.answer(row.Id, requester, 200, map[string]any{"ok": false}, &done) // false is an answer
			if done.Question == nil {
				t.Errorf("the answer they read back = %+v", done)
			}
			rig.deliver(env)
		}})
		if out := rig.outputs(req.Id, "step:who"); out["by"] != "thomas.nguyen@corp.example" || out["answer"].(map[string]any)["ok"] != false {
			t.Fatalf("the requester's answer = %v", out)
		}
		// Who owns what was requested on their behalf.
		owner := rig.publish("ask-owner", false, askStep("who", map[string]any{"owner": true}))
		owner.OnBehalfOf = strPtr(seed.UserID(requester))
		req = rig.submit("lmartin", owner)
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			row := rig.open(req.Id, requester)
			rig.notAsked(req.Id, "lmartin")
			rig.answer(row.Id, requester, 200, nil, nil)
			rig.deliver(env)
		}})
		if r := rig.get(req.Id, "lmartin"); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("the owner's answer = %s %q", r.Status, errorOf(r))
		}

		// Two waits of a group wait side by side; each hears its own answer.
		both := rig.publish("ask-both", false, map[string]any{"parallel": []any{
			askStep("one", map[string]any{"group": "rvav-data"}),
			askStep("two", map[string]any{"approvers": []any{seed.UserID("afavre")}}),
		}})
		req = rig.submit(requester, both)
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			rig.answer(rig.open(req.Id, "afavre").Id, "afavre", 200, nil, nil)
			rig.deliver(env)
		}}, moment{45 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			if s := rig.steps(req.Id, requester); s["step:two"].Status != api.StepStatusDone || s["step:one"].Status != api.StepStatusRunning {
				t.Errorf("one of two answered = %v", keysOf(s))
			}
			rig.answer(rig.open(req.Id, "sbrunner").Id, "sbrunner", 200, nil, nil)
			rig.deliver(env)
		}})
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("both answered = %s %q", r.Status, errorOf(r))
		}
		// A step of its group failing stops the wait, and a Retry asks again.
		cmdb.fail.Store(true)
		sibling := rig.publish("ask-sibling", false, map[string]any{"parallel": []any{
			askStep("one", map[string]any{"group": "rvav-data"}),
			map[string]any{"key": "two", "title": "Two", "rest": map[string]any{"method": "POST", "url": cmdb.URL + "/api/finish"}},
		}})
		req = rig.submit(requester, sibling)
		rig.run(req.Id)
		cmdb.fail.Store(false)
		req = rig.get(req.Id, requester)
		if s := rig.steps(req.Id, requester)["step:one"]; req.Status != api.RequestStatusFailed || s.Status != api.StepStatusCancelled {
			t.Fatalf("a sibling failed = %s, the wait %+v", req.Status, s)
		}
		rig.notAsked(req.Id, "sbrunner")
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			rig.answer(rig.open(req.Id, "sbrunner").Id, "sbrunner", 200, nil, nil)
			rig.deliver(env)
		}})
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("retried after a sibling failed = %s %q", r.Status, errorOf(r))
		}

		// An answer whose signal was lost: the wait reads the row again.
		lost := rig.publish("ask-lost", false, askStep("who", map[string]any{"group": "rvav-data"}))
		req = rig.submit(requester, lost)
		rig.run(req.Id, moment{30 * time.Minute, func(*testsuite.TestWorkflowEnvironment) {
			rig.answer(rig.open(req.Id, "sbrunner").Id, "sbrunner", 200, nil, nil)
			h.wf.mu.Lock()
			h.wf.byID = nil
			h.wf.mu.Unlock()
		}})
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("a lost signal = %s %q", r.Status, errorOf(r))
		}

		// A wait that goes on long continues as new, and asks nothing twice.
		long := rig.publish("ask-long", false, askStep("who", map[string]any{"group": "rvav-data"}))
		req = rig.submit(requester, long)
		var s testsuite.WorkflowTestSuite
		s.SetLogger(nil)
		env := s.NewTestWorkflowEnvironment()
		env.RegisterActivity(h.acts)
		provision.RegisterContract(env, h.acts)
		env.SetContinueAsNewSuggested(true)
		env.ExecuteWorkflow(provision.Provision, req.Id)
		if !env.IsWorkflowCompleted() || !workflow.IsContinueAsNewError(env.GetWorkflowError()) {
			t.Fatalf("a long wait = %v", env.GetWorkflowError())
		}
		rig.run(req.Id, moment{30 * time.Minute, func(env *testsuite.TestWorkflowEnvironment) {
			if n := rig.count(`select count(*) from notifications where request_id = $1 and user_id = $2 and kind = 'approval_needed'`, req.Id, seed.UserID("sbrunner")); n != 1 {
				t.Errorf("%d notifications after continuing as new", n)
			}
			rig.answer(rig.open(req.Id, "sbrunner").Id, "sbrunner", 200, nil, nil)
			rig.deliver(env)
		}})
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusSucceeded || len(rig.rows(req.Id, "sbrunner")) != 1 {
			t.Fatalf("continued as new = %s %q, %d questions", r.Status, errorOf(r), len(rig.rows(req.Id, "sbrunner")))
		}
	})
}

// TestCompositeAskThroughTemporal: the wait is durable through a real Temporal
// server, as approvals are: a wait that history makes long continues as new,
// an answer given with no worker up is heard by the next one, a timeout comes
// due with none, and a later step reads the answer in CEL. What was done
// before the wait isn't done again.
func TestCompositeAskThroughTemporal(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	// A low limit, so that this history is "long" and Temporal suggests
	// continuing as new at the first round.
	ts := startTemporal(t, "--dynamic-config-value", "limit.historyCount.suggestContinueAsNew=5")

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester, queue = "mkeller", "tnguyen", "rvav-ask-e2e"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		cmdb := newRESTCMDB(t)
		h.svc.REST.Hosts = []string{cmdb.URL}
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

		def := rig.publish("ask-durable", false,
			map[string]any{"key": "first", "title": "First", "rest": map[string]any{"method": "POST", "url": cmdb.URL + "/api/ci/${inputs.name}"}},
			askStep("who", map[string]any{"group": "rvav-data", "form": askForm()}),
			map[string]any{"key": "note", "title": "Note it", "when": "steps.who.answered", "rest": map[string]any{"method": "POST", "url": cmdb.URL + "/api/finish",
				"body": "{'reason': steps.who.answer.reason, 'first': steps.first.status}"}})
		req := rig.submit(requester, def)
		row := h.waitInbox("sbrunner", 1).Items[0]
		wf := provision.WorkflowID(req.Id)
		desc, err := tc.DescribeWorkflowExecution(ctx, wf, "")
		if err != nil {
			t.Fatal(err)
		}
		first := desc.WorkflowExecutionInfo.Execution.RunId

		// Woken by a signal that isn't its answer, with a long history: it
		// continues as new, the same run to everyone else.
		if err := tc.SignalWorkflow(ctx, wf, "", provision.SignalAsk(row.Id), provision.ApprovalSignal{ApprovalID: "apr_other"}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for {
			if desc, err = tc.DescribeWorkflowExecution(ctx, wf, ""); err != nil {
				t.Fatal(err)
			}
			if desc.WorkflowExecutionInfo.Execution.RunId != first {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the long wait didn't continue as new")
			}
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(2 * time.Second) // the new run has reached its wait, or would have asked again
		if again := h.waitInbox("sbrunner", 1).Items[0]; again.Id != row.Id {
			t.Fatalf("it asked again: %s, then %s", row.Id, again.Id)
		}

		// The worker goes away; the API stays up and the answer reaches Temporal.
		stop()
		rig.answer(row.Id, "sbrunner", 200, map[string]any{"reason": askAnswer}, nil)
		rig.answer(row.Id, "sbrunner", 409, map[string]any{"reason": "again"}, nil)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusRunning {
			t.Fatalf("with no worker = %s", r.Status)
		}
		stop = worker()
		h.waitStatus(req.Id, requester, api.RequestStatusSucceeded)
		posted := cmdb.seen("POST", "/api/finish")
		if len(posted) != 1 || posted[0].body["reason"] != askAnswer || fmt.Sprint(posted[0].body["first"]) != "201" {
			t.Fatalf("what the later step read = %+v", posted)
		}
		if n := len(cmdb.seen("POST", "/api/ci/")); n != 1 {
			t.Fatalf("what came before the wait ran %d times", n)
		}
		if n := rig.count(`select count(*) from notifications where request_id = $1 and user_id = $2 and kind = 'approval_needed'`, req.Id, seed.UserID("sbrunner")); n != 1 {
			t.Fatalf("%d notifications for one question", n)
		}
		if n := rig.count(`select count(*) from approvals where request_id = $1`, req.Id); n != 1 {
			t.Fatalf("%d questions", n)
		}

		// A timeout comes due with no worker up, and goes on when one is back.
		timed := rig.publish("ask-timed", false, askStep("who", map[string]any{"group": "rvav-data", "timeout": "3s", "on_timeout": "continue", "form": askForm()}))
		req = rig.submit(requester, timed)
		h.waitInbox("sbrunner", 1)
		stop()
		time.Sleep(4 * time.Second)
		stop = worker()
		h.waitStatus(req.Id, requester, api.RequestStatusSucceeded)
		if out := rig.outputs(req.Id, "step:who"); out["answered"] != false {
			t.Fatalf("timed out with no worker = %v", out)
		}

		// Cancelling through Temporal ends the wait and the question.
		cancels := rig.publish("ask-cancels", false, askStep("who", map[string]any{"group": "rvav-data"}))
		req = rig.submit(requester, cancels)
		row = h.waitInbox("sbrunner", 1).Items[0]
		h.json("POST", "/api/v1/requests/"+req.Id+"/cancel", requester, nil, 202, nil)
		rig.notAsked(req.Id, "sbrunner")
		h.waitStatus(req.Id, requester, api.RequestStatusCancelled)
		rig.answer(row.Id, "sbrunner", 409, nil, nil)
	})
}
