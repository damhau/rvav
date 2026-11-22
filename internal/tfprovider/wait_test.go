package tfprovider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/damhau/rvav/pkg/rvavclient"
)

// fakeRvav answers each call with the next of its answers for that call
// ("GET /api/v1/requests/req_1"), the last one again once they run out.
type fakeRvav struct {
	mu      sync.Mutex
	answers map[string][]func(w http.ResponseWriter)
	calls   map[string]int
}

func newFakeRvav(t *testing.T, answers map[string][]func(w http.ResponseWriter)) (*fakeRvav, *conn) {
	f := &fakeRvav{answers: answers, calls: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		key := r.Method + " " + r.URL.Path
		list := f.answers[key]
		n := f.calls[key]
		f.calls[key]++
		f.mu.Unlock()
		if len(list) == 0 {
			http.NotFound(w, r)
			return
		}
		list[min(n, len(list)-1)](w)
	}))
	t.Cleanup(srv.Close)
	api, err := rvavclient.Connect(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f, &conn{api: api, server: srv.URL, poll: time.Millisecond}
}

func (f *fakeRvav) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func answer(status int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		if strings.HasPrefix(body, "{") {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func dropped(w http.ResponseWriter) {
	conn, _, _ := w.(http.Hijacker).Hijack()
	conn.Close()
}

func problem(status int, detail string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"title":"Conflict","status":409,"detail":"` + detail + `"}`))
	}
}

func requestIs(status string) func(w http.ResponseWriter) {
	return answer(200, `{"id":"req_1","number":"REQ-1","status":"`+status+`"}`)
}

func deploymentIs(status string) func(w http.ResponseWriter) {
	return answer(200, `{"id":"dep_1","status":"`+status+`","project":{"id":"prj_web"},"catalog_item":{"id":"itm_1","slug":"app"},`+
		`"version":{"id":"ver_1","number":"1.0"},"owner":{"id":"usr_1"},"inputs":{}}`)
}

func TestWaitRetries(t *testing.T) {
	f, c := newFakeRvav(t, map[string][]func(http.ResponseWriter){
		"GET /api/v1/requests/req_1": {answer(502, "<html>502 Bad Gateway</html>"), dropped, answer(503, ""),
			requestIs("running"), requestIs("succeeded")},
	})
	if err := c.wait(context.Background(), map[string]any{"id": "req_1", "number": "REQ-1", "status": "running"}); err != nil {
		t.Fatalf("a wait through a 502, a dropped connection and a 503 = %v", err)
	}
	if n := f.count("GET /api/v1/requests/req_1"); n != 5 {
		t.Errorf("read the request %d times, want 5", n)
	}

	_, c = newFakeRvav(t, map[string][]func(http.ResponseWriter){
		"GET /api/v1/requests/req_1": {answer(404, `{"title":"Not found","status":404}`)},
	})
	err := c.wait(context.Background(), map[string]any{"id": "req_1", "number": "REQ-1", "status": "running"})
	if !rvavclient.IsStatus(err, http.StatusNotFound) {
		t.Errorf("a wait on a request rvav doesn't know = %v", err)
	}

	_, c = newFakeRvav(t, map[string][]func(http.ResponseWriter){
		"GET /api/v1/requests/req_1": {requestIs("failed")},
	})
	if err := c.wait(context.Background(), map[string]any{"id": "req_1", "number": "REQ-1", "status": "running"}); err == nil ||
		err.Error() != "REQ-1 failed" {
		t.Errorf("a failed request = %v", err)
	}
}

func TestWaitStopped(t *testing.T) {
	_, c := newFakeRvav(t, map[string][]func(http.ResponseWriter){
		"GET /api/v1/requests/req_1": {requestIs("running")},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := c.wait(ctx, map[string]any{"id": "req_1", "number": "REQ-1", "status": "running"})
	var stop *interrupted
	if !errors.As(err, &stop) || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("a wait its timeout ends = %v", err)
	}
}

// TestCreateStopped stops an apply while its request runs: the deployment
// stays in the state, with a warning rather than an error, so that
// Terraform doesn't taint it.
func TestCreateStopped(t *testing.T) {
	_, c := newFakeRvav(t, aBuild())
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	obj, err := createDeployment(ctx, c, map[string]any{"project_id": "prj_web", "environment": "dev", "catalog_item": "app",
		"inputs": map[string]any{"size": "S"}})
	var w *warning
	if !errors.As(err, &w) || !strings.Contains(w.detail, "rvavctl requests wait REQ-1") {
		t.Fatalf("a create stopped while it waits = %v", err)
	}
	if str(obj, "id") != "dep_1" || str(obj, "status") != "provisioning" {
		t.Errorf("the deployment kept = %v", obj)
	}
}

// TestCreateTimeout ends a create at its timeout: an error, which taints
// the deployment, as Terraform does for a create that didn't finish.
func TestCreateTimeout(t *testing.T) {
	_, c := newFakeRvav(t, aBuild())
	obj, err := createDeployment(context.Background(), c, map[string]any{"project_id": "prj_web", "environment": "dev",
		"catalog_item": "app", "timeouts": map[string]any{"create": "30ms"}})
	var w *warning
	if err == nil || errors.As(err, &w) || !strings.Contains(err.Error(), "REQ-1 hasn't finished before the resource's timeout") {
		t.Fatalf("a create its timeout ends = %v", err)
	}
	if str(obj, "id") != "dep_1" {
		t.Errorf("the deployment kept = %v", obj)
	}
}

// aBuild is a request that builds dep_1 and doesn't end.
func aBuild() map[string][]func(http.ResponseWriter) {
	return map[string][]func(http.ResponseWriter){
		"GET /api/v1/catalog/items/app": {answer(200, `{"id":"itm_1","version":{"id":"ver_1","number":"1.0"}}`)},
		"POST /api/v1/requests":         {answer(202, `{"id":"req_1","number":"REQ-1","status":"running","deployment_ids":["dep_1"]}`)},
		"GET /api/v1/requests/req_1":    {requestIs("running")},
		"GET /api/v1/deployments/dep_1": {deploymentIs("provisioning")},
	}
}

func TestDeleteWaitsForTheBuild(t *testing.T) {
	building := map[string][]func(http.ResponseWriter){
		"POST /api/v1/deployments/dep_1/actions": {problem(409, "REQ-1 is still running on this deployment."),
			answer(202, `{"id":"req_2","number":"REQ-2","status":"running"}`)},
		"GET /api/v1/deployments/dep_1": {deploymentIs("provisioning"), answer(502, ""), deploymentIs("provisioning"),
			deploymentIs("ready")},
		"GET /api/v1/requests/req_2": {answer(200, `{"id":"req_2","number":"REQ-2","status":"succeeded"}`)},
	}
	f, c := newFakeRvav(t, building)
	if err := deleteDeployment(context.Background(), c, "dep_1", nil); err != nil {
		t.Fatalf("deleting a deployment still being built = %v", err)
	}
	if n := f.count("POST /api/v1/deployments/dep_1/actions"); n != 2 {
		t.Errorf("asked to delete %d times, want 2", n)
	}

	_, c = newFakeRvav(t, map[string][]func(http.ResponseWriter){
		"POST /api/v1/deployments/dep_1/actions": {problem(409, "REQ-1 is still running on this deployment.")},
		"GET /api/v1/deployments/dep_1":          {deploymentIs("deleting"), deploymentIs("deleted")},
	})
	if err := deleteDeployment(context.Background(), c, "dep_1", nil); err != nil {
		t.Fatalf("deleting a deployment being deleted = %v", err)
	}

	f, c = newFakeRvav(t, map[string][]func(http.ResponseWriter){
		"POST /api/v1/deployments/dep_1/actions": {problem(409, "REQ-1 is still running on this VM.")},
		"GET /api/v1/deployments/dep_1":          {deploymentIs("ready")},
	})
	err := deleteDeployment(context.Background(), c, "dep_1", nil)
	if !rvavclient.IsStatus(err, http.StatusConflict) || f.count("POST /api/v1/deployments/dep_1/actions") != 1 {
		t.Errorf("a refusal that doesn't pass = %v, after %d tries", err, f.count("POST /api/v1/deployments/dep_1/actions"))
	}
}

func TestGiven(t *testing.T) {
	got, err := given(map[string]any{"inputs": map[string]any{"size": "M"}, "secret_inputs": map[string]any{"password": "s3cret"}})
	if err != nil || len(got) != 2 || got["password"] != "s3cret" || got["size"] != "M" {
		t.Errorf("given = %v, %v", got, err)
	}
	if _, err := given(map[string]any{"inputs": map[string]any{"password": "x"}, "secret_inputs": map[string]any{"password": "y"}}); err == nil {
		t.Error("an input in both inputs and secret_inputs isn't refused")
	}
}

func TestSameJSONAsState(t *testing.T) {
	for _, tc := range []struct {
		name, state, config string
		keep                bool
	}{
		{"formatting", `{"a":1,"b":[1,2]}`, "{\n  \"b\": [1, 2],\n  \"a\": 1\n}", true},
		{"a change", `{"a":1}`, `{"a":2}`, false},
	} {
		req := planmodifier.StringRequest{StateValue: jsontypes.NewNormalizedValue(tc.state).StringValue,
			ConfigValue: jsontypes.NewNormalizedValue(tc.config).StringValue,
			PlanValue:   jsontypes.NewNormalizedValue(tc.config).StringValue}
		resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
		sameJSONAsState{}.PlanModifyString(context.Background(), req, &resp)
		if kept := resp.PlanValue.ValueString() == tc.state; kept != tc.keep {
			t.Errorf("%s: planned %s", tc.name, resp.PlanValue.ValueString())
		}
	}
}

// TestModifyPlan keeps the state when the configuration says the same
// thing otherwise: the framework's unknown computed values aren't a change.
func TestModifyPlan(t *testing.T) {
	r := &restResource{name: "thing", schema: schema.Schema{Attributes: map[string]schema.Attribute{
		"slug":   schema.StringAttribute{Required: true},
		"status": schema.StringAttribute{Computed: true},
	}}}
	typ := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"slug": tftypes.String, "status": tftypes.String}}
	obj := func(slug, status any) tftypes.Value {
		return tftypes.NewValue(typ, map[string]tftypes.Value{"slug": tftypes.NewValue(tftypes.String, slug),
			"status": tftypes.NewValue(tftypes.String, status)})
	}
	state := obj("app", "ready")
	for _, tc := range []struct {
		name         string
		plan, config tftypes.Value
		keep         bool
	}{
		{"the same but for unknown computed values", obj("app", tftypes.UnknownValue), obj("itm_1", nil), true},
		{"a change", obj("other", tftypes.UnknownValue), obj("other", nil), false},
		{"a configured value unknown", obj(tftypes.UnknownValue, tftypes.UnknownValue), obj(tftypes.UnknownValue, nil), false},
	} {
		req := resource.ModifyPlanRequest{State: tfsdk.State{Schema: r.schema, Raw: state},
			Plan: tfsdk.Plan{Schema: r.schema, Raw: tc.plan}, Config: tfsdk.Config{Schema: r.schema, Raw: tc.config}}
		resp := resource.ModifyPlanResponse{Plan: req.Plan}
		r.ModifyPlan(context.Background(), req, &resp)
		if kept := resp.Plan.Raw.Equal(state); kept != tc.keep {
			t.Errorf("%s: planned %v", tc.name, resp.Plan.Raw)
		}
	}
}
