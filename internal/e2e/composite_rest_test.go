package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
)

const (
	restToken = "tok-Sup3r-s3cret"
	restUser  = "svc-cmdb"
	restPass  = "b4sic-pa55w0rd"
	restKey   = "h3ader-k3y-value"
	restInput = "Inp4t-s3cret"
)

// restCall is one request the CMDB stand-in received.
type restCall struct {
	method, path, query, auth, key string
	body                           map[string]any
}

// restCMDB stands in for a CMDB that echoes what it is sent: in its answer's
// JSON, in its headers and in its errors, in each form a secret travels in.
type restCMDB struct {
	*httptest.Server
	mu    sync.Mutex
	calls []restCall
	// fail makes POST /finish answer 500.
	fail atomic.Bool
	// flaky answers 503 to the first PUT /flaky.
	flaky atomic.Int32
	// elsewhere counts the calls that followed a redirect.
	elsewhere atomic.Int32
}

func newRESTCMDB(t *testing.T) *restCMDB {
	t.Helper()
	c := &restCMDB{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		auth := r.Header.Get("Authorization")
		c.mu.Lock()
		c.calls = append(c.calls, restCall{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, auth, r.Header.Get("X-Api-Key"), body})
		c.mu.Unlock()
		w.Header().Set("X-Echoed-Authorization", auth)
		w.Header().Set("Set-Cookie", "session="+auth)
		w.Header().Set("Content-Type", "application/json")
		answer := func(status int, v any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/ci/"):
			answer(http.StatusCreated, map[string]any{"result": map[string]any{
				"id": 42, "auth": auth, "as_base64": base64.StdEncoding.EncodeToString([]byte(restToken)),
				"password": body["password"], restToken: "as a key"}, "dropped": "not kept"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/check/"):
			answer(http.StatusOK, map[string]any{"seen": []any{"one"}, "auth": auth})
		case r.Method == http.MethodPut && r.URL.Path == "/api/flaky":
			if c.flaky.Add(1) == 1 {
				answer(http.StatusServiceUnavailable, map[string]any{"error": "wait"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/finish":
			if c.fail.Load() {
				answer(http.StatusInternalServerError, map[string]any{"error": "refused " + r.Header.Get("X-Api-Key")})
				return
			}
			answer(http.StatusOK, map[string]any{"key": r.Header.Get("X-Api-Key")})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/redirect":
			http.Redirect(w, r, "/api/elsewhere", http.StatusFound)
		case r.URL.Path == "/api/elsewhere":
			c.elsewhere.Add(1)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/text":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("not JSON " + auth))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *restCMDB) seen(method, pathPrefix string) []restCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(c.calls), func(x restCall) bool {
		return x.method != method || !strings.HasPrefix(x.path, pathPrefix)
	})
}

// restDefinition is a composite of REST calls only: one registers, then
// reads its answer back, one is retried, one may fail.
func restDefinition(base string) api.CatalogVersionDefinition {
	fields := []string{"name", "api_password"}
	def := api.CatalogVersionDefinition{
		FormSchema: map[string]any{"type": "object", "required": fields, "properties": map[string]any{
			"name": map[string]any{"type": "string"}, "api_password": map[string]any{"type": "string"}}},
		UiSchema: api.UiSchema{Fields: map[string]api.UiField{
			"name":         {Widget: api.UiWidgetText, Label: "Name"},
			"api_password": {Widget: api.UiWidgetSecret, Label: "Password"},
		}},
		InitialValues: map[string]any{},
		Blueprint: map[string]any{"kind": "composite", "rollback_on_failure": true, "steps": []any{
			map[string]any{"key": "register", "title": "Register in the CMDB", "rest": map[string]any{
				"method": "POST", "url": base + "/api/ci/${inputs.name}?env=${environment}", "secret": "cmdb-token",
				"body":   "{'name': inputs.name, 'password': inputs.api_password, 'source': 'rvav-internal'}",
				"accept": []any{201}, "keep": "$.result"},
				"undo": map[string]any{"rest": map[string]any{"method": "DELETE", "url": base + "/api/ci/${steps.register.json.id}",
					"secret": "cmdb-token", "accept": []any{200, 204, 404}}}},
			map[string]any{"key": "check", "title": "Read it back", "when": "steps.register.status == 201", "rest": map[string]any{
				"method": "GET", "url": base + "/api/check/${steps.register.json.id}", "secret": "cmdb-basic", "keep": "$.seen[*]"}},
			map[string]any{"key": "flaky", "title": "Flaky", "rest": map[string]any{"method": "PUT", "url": base + "/api/flaky"}},
			map[string]any{"key": "finish", "title": "Finish", "rest": map[string]any{"method": "POST", "url": base + "/api/finish",
				"secret": "cmdb-key", "body": "{'id': steps.register.json.id, 'seen': steps.check.json, 'status': steps.check.status}"}},
		}},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "CMDB", Fields: fields}}
	return def
}

// TestCompositeREST: a composite's REST calls (ADR-0033). Checked when saved
// and when requested; what they answer read by later steps; a status not
// accepted fails the step, which a person retries, and undoes what was done
// with another call; no secret stays in anything they leave behind.
func TestCompositeREST(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		cmdb := newRESTCMDB(t)
		h.svc.REST.Hosts = []string{cmdb.URL}
		var logged bytes.Buffer
		h.acts.Log = slog.New(slog.NewTextHandler(&logged, nil))
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-token", Kind: api.SecretWriteKindToken, Value: new(restToken)}, 201, nil)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-basic", Kind: api.SecretWriteKindBasic, Username: new(restUser), Value: new(restPass)}, 201, nil)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-key", Kind: api.SecretWriteKindHeader, Header: new("X-Api-Key"), Value: new(restKey)}, 201, nil)

		// Checked when saved.
		var problem struct{ Errors []api.FieldError }
		bad := restDefinition(cmdb.URL)
		bad.Blueprint = map[string]any{"kind": "composite", "steps": []any{
			map[string]any{"key": "a", "with": map[string]any{"x": "1"}, "rest": map[string]any{
				"method": "FETCH", "url": cmdb.URL + "/x", "keep": "$[", "accept": []any{99}, "body": "inputs.x =="}},
			map[string]any{"key": "b", "rest": map[string]any{"method": "GET", "url": "https://${inputs.host}/x", "body": "1"}},
			map[string]any{"key": "c", "rest": map[string]any{"method": "POST", "url": "https://elsewhere.example/${steps.c.id}/${steps.zz.id}", "secret": "nope"},
				"undo": map[string]any{"job_template": "x", "rest": map[string]any{"method": "DELETE", "url": cmdb.URL + "/${steps.c.ok}/${steps.later.id}"}}},
			map[string]any{"key": "d", "rest": map[string]any{"method": "POST", "url": "http://user:pw@" + strings.TrimPrefix(cmdb.URL, "http://") + "/x"}},
			map[string]any{"key": "e", "rest": map[string]any{"method": "GET", "url": "ftp://host/${"}},
			map[string]any{"key": "later", "rest": map[string]any{"method": "GET"}, "item": "x"},
		}}
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-rest", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.Composite, Definition: &bad}, 422, &problem)
		prefix := "/definition/blueprint/steps/"
		for _, want := range []string{"0/with", "0/rest/method", "0/rest/keep", "0/rest/accept/0", "0/rest/body", "1/rest/url", "1/rest/body",
			"2/rest/url", "2/rest/secret", "2/undo", "2/undo/rest/url", "3/rest/url", "4/rest/url", "5", "5/rest/url"} {
			if !slices.Contains(fieldsOf(problem.Errors), prefix+want) {
				t.Errorf("a bad composite = %v, no %s", fieldsOf(problem.Errors), want)
			}
		}
		for _, e := range problem.Errors {
			if e.Field == prefix+"2/rest/url" && strings.Contains(e.Message, "steps.c ") && deref(e.Code) != "unknown_step" {
				t.Errorf("a step reading its own outputs = %+v", e)
			}
		}
		if t.Failed() {
			t.FailNow()
		}

		def := restDefinition(cmdb.URL)
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "cmdb-register", Name: "CMDB entry",
			Summary: "An entry in the CMDB.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/cmdb-register/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/cmdb-register/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		// A secret a REST call uses isn't deleted.
		for _, name := range []string{"cmdb-token", "cmdb-basic", "cmdb-key"} {
			h.json("DELETE", "/api/v1/admin/secrets/"+name, admin, nil, 409, nil)
		}

		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"name": "web 01/a", "api_password": restInput}}

		// What the workflow's activities carry and return, which Temporal's
		// history keeps.
		var carried bytes.Buffer
		record := func(env *testsuite.TestWorkflowEnvironment) {
			env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, args converter.EncodedValues) {
				if info.ActivityType.Name == "RunREST" {
					var in provision.RESTInput
					if err := args.Get(&in); err != nil {
						t.Errorf("RunREST's input: %v", err)
					}
					b, _ := json.Marshal(in)
					carried.Write(b)
				}
			})
			env.SetOnActivityCompletedListener(func(info *activity.Info, result converter.EncodedValue, err error) {
				if info.ActivityType.Name != "RunREST" {
					return
				}
				if err != nil {
					carried.WriteString(err.Error())
				} else if result != nil && result.HasValue() {
					var out map[string]any
					_ = result.Get(&out)
					b, _ := json.Marshal(out)
					carried.Write(b)
				}
			})
		}
		request := func() api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", requester, draft, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, record)
			h.json("GET", "/api/v1/requests/"+req.Id, requester, nil, 200, &req)
			return req
		}
		timeline := func(id string) map[string]api.Step {
			t.Helper()
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+id+"/steps", requester, nil, 200, &steps)
			out := map[string]api.Step{}
			for _, s := range steps.Items {
				out[s.Key] = s
				for _, ch := range s.Children {
					out[ch.Key] = ch
				}
			}
			return out
		}
		outputs := func(id, key string) map[string]any {
			t.Helper()
			var raw []byte
			if err := h.store.Pool.QueryRow(ctx, `select outputs from request_steps where request_id = $1 and key = $2`, id, key).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var out map[string]any
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("%s's outputs = %s: %v", key, raw, err)
			}
			return out
		}

		// All done: each call as it should be, what it answered read by the next.
		ok := request()
		if ok.Status != api.RequestStatusSucceeded {
			t.Fatalf("the composite = %s %s (steps %v)", ok.Status, errorOf(ok), keysOf(timeline(ok.Id)))
		}
		steps := timeline(ok.Id)
		for _, key := range []string{"step:register", "step:check", "step:flaky", "step:finish"} {
			if s := steps[key]; s.Status != api.StepStatusDone || s.System == nil || *s.System != api.StepSystemRest {
				t.Fatalf("%s = %+v", key, s)
			}
		}
		if got := deref(steps["step:register"].Detail); got != "POST answered 201" {
			t.Fatalf("register's detail = %q", got)
		}
		posted := cmdb.seen("POST", "/api/ci/")
		if len(posted) != 1 || posted[0].path != "/api/ci/web%2001%2Fa" || posted[0].query != "env=dev" || posted[0].auth != "Bearer "+restToken {
			t.Fatalf("the POST = %+v", posted)
		}
		if posted[0].body["name"] != "web 01/a" || posted[0].body["password"] != restInput || posted[0].body["source"] != "rvav-internal" {
			t.Fatalf("its body = %v", posted[0].body)
		}
		read := cmdb.seen("GET", "/api/check/")
		if len(read) != 1 || read[0].path != "/api/check/42" || read[0].auth != "Basic "+base64.StdEncoding.EncodeToString([]byte(restUser+":"+restPass)) {
			t.Fatalf("the GET = %+v", read)
		}
		finished := cmdb.seen("POST", "/api/finish")
		if len(finished) != 1 || finished[0].key != restKey || finished[0].body["id"] != float64(42) || finished[0].body["status"] != float64(200) {
			t.Fatalf("the last POST = %+v", finished)
		}
		if seen, _ := finished[0].body["seen"].([]any); len(seen) != 1 || seen[0] != "one" {
			t.Fatalf("the list kept = %v", finished[0].body["seen"])
		}
		// A status retried: PUT can be repeated, and the answer has no body.
		if flaky := cmdb.seen("PUT", "/api/flaky"); len(flaky) != 2 {
			t.Fatalf("the PUT was called %d times", len(flaky))
		}
		if out := outputs(ok.Id, "step:flaky"); out["status"] != float64(204) || out["json"] != nil {
			t.Fatalf("flaky's outputs = %v", out)
		}
		// What the step keeps: its status and the JSON picked, without the secrets it echoed.
		out := outputs(ok.Id, "step:register")
		if out["status"] != float64(201) {
			t.Fatalf("register's outputs = %v", out)
		}
		kept, _ := out["json"].(map[string]any)
		if kept["id"] != float64(42) || kept["auth"] != "Bearer [removed]" || kept["as_base64"] != "[removed]" || kept["password"] != "[removed]" ||
			kept["[removed]"] != "as a key" {
			t.Fatalf("register kept %v", kept)
		}
		if kept["dropped"] != nil || len(out) != 2 {
			t.Fatalf("register kept too much: %v", out)
		}
		if back := outputs(ok.Id, "step:check"); back["json"].([]any)[0] != "one" {
			t.Fatalf("check's outputs = %v", back)
		}

		// A status not accepted: the step fails, with no retry for a POST
		// and nothing the answer said, and what was done is undone.
		cmdb.fail.Store(true)
		cmdb.flaky.Store(1)
		before := len(cmdb.seen("POST", "/api/finish"))
		failed := request()
		if failed.Status != api.RequestStatusFailed {
			t.Fatalf("a call refused = %s", failed.Status)
		}
		if n := len(cmdb.seen("POST", "/api/finish")); n != before+1 {
			t.Fatalf("the POST refused was made %d times", n-before)
		}
		steps = timeline(failed.Id)
		finish := steps["step:finish"]
		if finish.Status != api.StepStatusFailed || deref(finish.Error) != "POST "+strings.TrimPrefix(cmdb.URL, "http://")+" answered 500 Internal Server Error." {
			t.Fatalf("the failed step = %+v, %q", finish, deref(finish.Error))
		}
		if !strings.Contains(errorOf(failed), "answered 500") {
			t.Fatalf("the request's error = %s", errorOf(failed))
		}
		deleted := cmdb.seen("DELETE", "/api/ci/")
		if len(deleted) != 1 || deleted[0].path != "/api/ci/42" || deleted[0].auth != "Bearer "+restToken {
			t.Fatalf("the undo = %+v", deleted)
		}
		if steps["step:register/undo"].Status != api.StepStatusDone || steps["step:register"].Status != api.StepStatusCancelled {
			t.Fatalf("register %s, its undo %s (steps %v)", steps["step:register"].Status, steps["step:register/undo"].Status, keysOf(steps))
		}

		// Retried by a person: the undone step runs again, and it all ends well.
		cmdb.fail.Store(false)
		h.json("POST", "/api/v1/requests/"+failed.Id+"/retry", requester, nil, 202, nil)
		h.runWorkflow(failed.Id, record)
		h.json("GET", "/api/v1/requests/"+failed.Id, requester, nil, 200, &failed)
		steps = timeline(failed.Id)
		if failed.Status != api.RequestStatusSucceeded || steps["step:register"].Status != api.StepStatusDone || steps["step:finish"].Status != api.StepStatusDone {
			t.Fatalf("the retried request = %s %s (steps %v)", failed.Status, errorOf(failed), keysOf(steps))
		}

		// A requester's topology: its method and what it reads. No host, no
		// path, no literal, no secret's name, nor what it accepts or keeps.
		var top api.RequestTopology
		h.json("GET", "/api/v1/requests/"+ok.Number+"/topology", requester, nil, 200, &top)
		if top.Topology == nil {
			t.Fatal("no topology")
		}
		b, _ := json.Marshal(*top.Topology)
		list := (*top.Topology)["steps"].([]any)
		register := list[0].(map[string]any)
		rest := register["rest"].(map[string]any)
		if rest["method"] != "POST" || rest["url"] != "…${inputs.name}…${environment}" ||
			rest["body"] != "{'…': inputs.name, '…': inputs.api_password, '…': '…'}" || rest["secret"] != nil || rest["accept"] != nil || rest["keep"] != nil {
			t.Fatalf("the topology's REST call = %v", rest)
		}
		if undo := register["undo"].(map[string]any)["rest"].(map[string]any); undo["url"] != "…${steps.register.json.id}" || undo["method"] != "DELETE" {
			t.Fatalf("the topology's undo = %v", undo)
		}
		for _, hidden := range []string{"127.0.0.1", "/api/", "cmdb-", "$.result", "rvav-internal", restToken} {
			if bytes.Contains(b, []byte(hidden)) {
				t.Errorf("the topology has %q: %s", hidden, b)
			}
		}

		// No secret, in any form, anywhere it could stay.
		forms := []string{restToken, restPass, restKey, restInput,
			base64.StdEncoding.EncodeToString([]byte(restToken)),
			base64.StdEncoding.EncodeToString([]byte(restUser + ":" + restPass))}
		for _, secret := range forms {
			var n int
			if err := h.store.Pool.QueryRow(ctx, `select (select count(*) from requests t where t::text like $1)
				+ (select count(*) from request_steps t where t::text like $1)
				+ (select count(*) from step_log_lines t where t::text like $1)
				+ (select count(*) from events t where t::text like $1)
				+ (select count(*) from integration_events t where t::text like $1)
				+ (select count(*) from audit_events t where t::text like $1)
				+ (select count(*) from notifications t where t::text like $1)`, "%"+secret+"%").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%q is in %d rows", secret, n)
			}
			if strings.Contains(logged.String(), secret) {
				t.Errorf("%q is in the logs", secret)
			}
			if strings.Contains(carried.String(), secret) {
				t.Errorf("%q is in what the activities carry: %s", secret, carried.String())
			}
			_, raw, _ := h.do("GET", "/api/v1/requests/"+ok.Id+"/steps", requester, nil)
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%q is in the steps the API gives", secret)
			}
		}
		if carried.Len() == 0 || !strings.Contains(carried.String(), "cmdb-token") {
			t.Fatalf("the activities carried %q", carried.String())
		}

		// The answer's headers aren't kept, however secret-bearing.
		if strings.Contains(carried.String(), "session=") || strings.Contains(carried.String(), "X-Echoed") {
			t.Fatalf("an answer's headers are kept: %s", carried.String())
		}

		// When the hosts or the secrets have changed since it was saved, a
		// request is refused, in words that name neither.
		h.svc.REST.Hosts = nil
		h.json("POST", "/api/v1/requests", requester, draft, 422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) == 0 || !strings.Contains(problem.Errors[0].Message, "may not call any more") || strings.Contains(problem.Errors[0].Message, "127.0.0.1") {
			t.Fatalf("a host no longer allowed = %+v", problem.Errors)
		}
		h.svc.REST.Hosts = []string{cmdb.URL}
	})
}

// TestCompositeRESTEdges: what a call does with a redirect, an answer that
// isn't JSON and a URL that would change what it names.
func TestCompositeRESTEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		cmdb := newRESTCMDB(t)
		h.svc.REST.Hosts = []string{cmdb.URL}
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-token", Kind: api.SecretWriteKindToken, Value: new(restToken)}, 201, nil)

		run := func(slug string, steps ...any) (api.Request, map[string]api.Step) {
			t.Helper()
			def := restDefinition(cmdb.URL)
			def.Blueprint = map[string]any{"kind": "composite", "rollback_on_failure": true, "steps": steps}
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: "A call.",
				Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			var req api.Request
			h.json("POST", "/api/v1/requests", requester, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: "dev", Inputs: map[string]any{"name": "x", "api_password": "p"}}, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, requester, nil, 200, &req)
			var list struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps", requester, nil, 200, &list)
			out := map[string]api.Step{}
			for _, s := range list.Items {
				out[s.Key] = s
			}
			return req, out
		}

		// A redirect isn't followed: it is a status like another, not accepted.
		req, steps := run("rest-redirect", map[string]any{"key": "go", "rest": map[string]any{
			"method": "GET", "url": cmdb.URL + "/api/redirect", "secret": "cmdb-token"}})
		if req.Status != api.RequestStatusFailed || !strings.Contains(deref(steps["step:go"].Error), "answered 302 Found") || cmdb.elsewhere.Load() != 0 {
			t.Fatalf("a redirect: %s, %+v, %d calls elsewhere", req.Status, steps["step:go"], cmdb.elsewhere.Load())
		}

		// An answer that isn't JSON: its status is what it said; none of its text is kept.
		req, _ = run("rest-text", map[string]any{"key": "text", "rest": map[string]any{
			"method": "GET", "url": cmdb.URL + "/api/text", "secret": "cmdb-token"}})
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("an answer that isn't JSON: %s %s", req.Status, errorOf(req))
		}
		var raw []byte
		_ = h.store.Pool.QueryRow(ctx, `select outputs from request_steps where request_id = $1 and key = 'step:text'`, req.Id).Scan(&raw)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		if out["status"] != float64(200) || out["json"] != nil {
			t.Fatalf("its outputs = %s", raw)
		}

		// An empty value doesn't DELETE the collection, and a secret input
		// doesn't go in a URL; neither call is made.
		before := len(cmdb.seen("DELETE", "/"))
		req, steps = run("rest-empty", map[string]any{"key": "gone", "rest": map[string]any{
			"method": "DELETE", "url": cmdb.URL + "/api/ci/${inputs.name == 'x' ? '' : 'y'}"}})
		if req.Status != api.RequestStatusFailed || !strings.Contains(deref(steps["step:gone"].Error), "it is empty") ||
			strings.Contains(deref(steps["step:gone"].Error), "'x'") || len(cmdb.seen("DELETE", "/")) != before {
			t.Fatalf("an empty value: %s, %+v", req.Status, steps["step:gone"])
		}
		// An undo that fails says so, and its step stays done: what it did is still there.
		cmdb.fail.Store(true)
		req, _ = run("rest-undo-fails", map[string]any{"key": "made", "title": "Made", "rest": map[string]any{"method": "PUT", "url": cmdb.URL + "/api/flaky"},
			"undo": map[string]any{"rest": map[string]any{"method": "DELETE", "url": cmdb.URL + "/api/ci/${steps.made.status}", "accept": []any{200}}}},
			map[string]any{"key": "then", "title": "Then", "rest": map[string]any{"method": "POST", "url": cmdb.URL + "/api/finish"}})
		cmdb.fail.Store(false)
		var undone struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", requester, nil, 200, &undone)
		i := slices.IndexFunc(undone.Items, func(s api.Step) bool { return s.Key == "step:made" })
		if req.Status != api.RequestStatusFailed || i < 0 || undone.Items[i].Status != api.StepStatusDone || len(undone.Items[i].Children) != 1 ||
			undone.Items[i].Children[0].Status != api.StepStatusFailed || !strings.Contains(deref(undone.Items[i].Children[0].Error), "answered 204") {
			t.Fatalf("an undo that fails: %s, %+v", req.Status, undone.Items)
		}

		req, steps = run("rest-secret-in-url", map[string]any{"key": "leak", "rest": map[string]any{
			"method": "GET", "url": cmdb.URL + "/api/check/${inputs.api_password}"}})
		if req.Status != api.RequestStatusFailed || !strings.Contains(deref(steps["step:leak"].Error), "it isn't a single value") {
			t.Fatalf("a secret input in a URL: %s, %+v", req.Status, steps["step:leak"])
		}
	})
}
