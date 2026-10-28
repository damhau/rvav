package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/provision"
)

// fakeCMDB knows applications by role: it stops requests for roles it
// doesn't know and gives the others a configuration item.
type fakeCMDB struct {
	mu    sync.Mutex
	seen  []string
	delay time.Duration
}

func (c *fakeCMDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var ev outbox.Event
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &ev)
	c.mu.Lock()
	c.seen = append(c.seen, ev.Topic)
	delay := c.delay
	c.mu.Unlock()
	time.Sleep(delay)
	inputs, _ := ev.Data["inputs"].(map[string]any)
	if role, _ := inputs["role"].(string); role == "unknown" {
		_ = json.NewEncoder(w).Encode(map[string]any{"outcome": "stop", "reason": "The CMDB has no application for the role unknown."})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"outcome": "continue",
		"properties": map[string]string{"cmdb_id": "CI0042", "cost_center": "CC-9100"}})
}

// TestSubscriptions: administrators subscribe webhooks and AWX templates to
// topics; a blocking subscription checks requests before they build, and
// can stop them or set properties (S10.3, ADR-0021).
func TestSubscriptions(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		cmdb := &fakeCMDB{}
		srv := httptest.NewServer(cmdb)
		defer srv.Close()
		h.allowWebhooks(srv.URL)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-hook", Kind: api.SecretWriteKindToken,
			Value: new("hook-secret")}, 201, nil)
		var problem struct{ Errors []api.FieldError }

		// Who may, and what is checked.
		webhook := api.SubscriptionTarget{}
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+srv.URL+`/rvav", "secret": "cmdb-hook"}}`), &webhook)
		check := api.SubscriptionWrite{Name: "CMDB check", Topic: api.ProvisioningPre, Blocking: new(true),
			Condition: new(`environment == "dev"`), TimeoutSeconds: new(5), Target: webhook}
		h.json("POST", "/api/v1/admin/subscriptions", "lmartin", check, 403, nil)
		h.json("GET", "/api/v1/admin/subscriptions", "svc-awx", nil, 200, nil) // an auditor reads them
		h.json("POST", "/api/v1/admin/subscriptions", "svc-awx", check, 403, nil)
		bad := check
		bad.Topic, bad.Condition = api.ProvisioningPost, new("vm.cpus > 8")
		elsewhere := api.SubscriptionTarget{}
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "https://evil.example/x", "secret": "nope"}, "awx": {"job_template": "x"}}`), &elsewhere)
		bad.Target = elsewhere
		h.json("POST", "/api/v1/admin/subscriptions", admin, bad, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Contains(fields, "/blocking") || !slices.Contains(fields, "/condition") ||
			!slices.Contains(fields, "/target") {
			t.Fatalf("a bad subscription = %v", fields)
		}
		var onlyElsewhere api.SubscriptionTarget
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "https://evil.example/x", "secret": "nope"}}`), &onlyElsewhere)
		bad.Target, bad.Blocking, bad.Condition = onlyElsewhere, nil, nil
		h.json("POST", "/api/v1/admin/subscriptions", admin, bad, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Contains(fields, "/target/webhook/url") || !slices.Contains(fields, "/target/webhook/secret") {
			t.Fatalf("a webhook elsewhere = %v", fields)
		}
		var sub api.Subscription
		h.json("POST", "/api/v1/admin/subscriptions", admin, check, 201, &sub)

		// A test delivery of the sample event.
		var test api.SubscriptionRunDetail
		h.json("POST", "/api/v1/admin/subscriptions/"+sub.Id+"/test", admin, nil, 200, &test)
		if !test.Test || test.Status != api.SubscriptionRunStatusDelivered || test.Outcome == nil || *test.Outcome != "continue" ||
			len(test.History) != 1 || test.History[0].StatusCode == nil {
			t.Fatalf("test run = %+v", test)
		}

		request := func(role string) api.Request {
			t.Helper()
			var item api.CatalogItem
			h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", "tnguyen", nil, 200, &item)
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": role, "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
				202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
			return req
		}
		step := func(requestID, key string) api.Step {
			t.Helper()
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+requestID+"/steps", "tnguyen", nil, 200, &steps)
			for _, s := range steps.Items {
				if s.Key == key {
					return s
				}
				for _, c := range s.Children {
					if c.Key == key {
						return c
					}
				}
			}
			t.Fatalf("no step %s", key)
			return api.Step{}
		}

		// Known to the CMDB: built, with its configuration item.
		ok := request("api")
		if ok.Status != api.RequestStatusSucceeded {
			t.Fatalf("a request the CMDB knows = %s", ok.Status)
		}
		if s := step(ok.Id, "check:"+sub.Id); s.Status != api.StepStatusDone || !strings.Contains(deref(s.Detail), "cmdb_id, cost_center") {
			t.Fatalf("its check = %+v", s)
		}
		var dep api.Deployment
		h.json("GET", "/api/v1/deployments/"+(*ok.DeploymentIds)[0], "tnguyen", nil, 200, &dep)
		var props map[string]string
		_ = h.store.Pool.QueryRow(ctx, `select properties from deployments where id = $1`, dep.Id).Scan(&props)
		if props["cmdb_id"] != "CI0042" {
			t.Fatalf("properties = %v", props)
		}
		job := step(ok.Id, provision.KeyJob+"linux-baseline")
		jobID, _ := strconv.Atoi(job.ExternalRef.Id)
		vars := h.awx.Launched(jobID).ExtraVars["rvav"].(map[string]any)
		if p, _ := vars["properties"].(map[string]string); p["cmdb_id"] != "CI0042" {
			t.Fatalf("the job's rvav context = %v", vars)
		}

		// Unknown to the CMDB: stopped before anything is built.
		stopped := request("unknown")
		if stopped.Status != api.RequestStatusFailed || !strings.Contains(errorOf(stopped), "CMDB check stopped the request") {
			t.Fatalf("a request the CMDB doesn't know = %s %s", stopped.Status, errorOf(stopped))
		}
		if s := step(stopped.Id, provision.KeyClone); s.Status != api.StepStatusPending {
			t.Fatalf("clone after a stop = %s", s.Status)
		}
		var name string
		_ = h.store.Pool.QueryRow(ctx, `select name from deployments where created_by_request_id = $1`, stopped.Id).Scan(&name)
		if _, err := find.NewFinder(c).VirtualMachine(ctx, name); err == nil {
			t.Fatalf("%s was built", name)
		}
		var runs api.SubscriptionRunPage
		h.json("GET", "/api/v1/admin/subscriptions/"+sub.Id+"/runs?status=blocked", admin, nil, 200, &runs)
		if len(runs.Items) != 1 || runs.Items[0].Request == nil || runs.Items[0].Request.Id != stopped.Id {
			t.Fatalf("blocked runs = %+v", runs.Items)
		}
		h.json("POST", "/api/v1/admin/subscription-runs/"+runs.Items[0].Id+"/retry", admin, nil, 409, nil) // a request's own

		// A CMDB too slow to answer: stopped, unless the subscription says go on.
		cmdb.mu.Lock()
		cmdb.delay = 6 * time.Second
		cmdb.mu.Unlock()
		check.TimeoutSeconds = new(1)
		h.json("PUT", "/api/v1/admin/subscriptions/"+sub.Id, admin, check, 200, nil)
		if slow := request("api"); slow.Status != api.RequestStatusFailed || !strings.Contains(errorOf(slow), "couldn't check") {
			t.Fatalf("a request the CMDB didn't answer = %s %s", slow.Status, errorOf(slow))
		}
		check.OnFailure = new(api.ContinueRequest)
		h.json("PUT", "/api/v1/admin/subscriptions/"+sub.Id, admin, check, 200, nil)
		went := request("api")
		if went.Status != api.RequestStatusSucceeded || step(went.Id, "check:"+sub.Id).Status != api.StepStatusSkipped {
			t.Fatalf("a request that went on = %s", went.Status)
		}
		cmdb.mu.Lock()
		cmdb.delay = 0
		cmdb.mu.Unlock()

		// Not for production: no check there.
		h.json("DELETE", "/api/v1/admin/subscriptions/"+sub.Id, admin, nil, 204, nil)

		// After the fact: an AWX template, delivered once its job succeeded.
		awxTarget := api.SubscriptionTarget{}
		_ = json.Unmarshal([]byte(`{"awx": {"job_template": "monitoring-agent"}}`), &awxTarget)
		var notify api.Subscription
		h.json("POST", "/api/v1/admin/subscriptions", admin, api.SubscriptionWrite{Name: "Tell monitoring", Topic: api.ProvisioningPost,
			Condition: new(`request.outcome == "succeeded"`), Target: awxTarget}, 201, &notify)
		if notify.Target.Awx == nil || notify.Target.Awx.TemplateId == nil {
			t.Fatalf("the AWX target is kept by ID: %+v", notify.Target)
		}
		done := request("web")
		if _, err := h.svc.Dispatcher.Once(ctx); err != nil {
			t.Fatal(err)
		}
		h.json("GET", "/api/v1/admin/subscriptions/"+notify.Id+"/runs", admin, nil, 200, &runs)
		if len(runs.Items) != 1 || runs.Items[0].Status != api.SubscriptionRunStatusDelivered || runs.Items[0].Request.Id != done.Id {
			t.Fatalf("AWX runs = %+v", runs.Items)
		}
		var detail api.SubscriptionRunDetail
		h.json("GET", "/api/v1/admin/subscription-runs/"+runs.Items[0].Id, admin, nil, 200, &detail)
		ev := h.awx.Launched(*detail.History[0].JobId).ExtraVars["rvav_event"].(map[string]any)
		if detail.Payload.Topic != api.ProvisioningPost || ev["id"] != detail.EventId {
			t.Fatalf("the job got %v", ev["id"])
		}

		// Day-2: a blocking check on deletion stops it, the deployment stays.
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+srv.URL+`/rvav", "secret": "cmdb-hook"}}`), &webhook)
		var guard api.Subscription
		h.json("POST", "/api/v1/admin/subscriptions", admin, api.SubscriptionWrite{Name: "Deletion guard", Topic: api.DeletePre,
			Blocking: new(true), Condition: new(`inputs.reason == "unknown" || true`), Target: webhook}, 201, &guard)
		cmdbStops := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"outcome": "stop", "reason": "Still in the CMDB as live."})
		}))
		defer cmdbStops.Close()
		h.allowWebhooks(cmdbStops.URL)
		webhook = api.SubscriptionTarget{}
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+cmdbStops.URL+`", "secret": "cmdb-hook"}}`), &webhook)
		h.json("PUT", "/api/v1/admin/subscriptions/"+guard.Id, admin, api.SubscriptionWrite{Name: "Deletion guard", Topic: api.DeletePre,
			Blocking: new(true), Target: webhook}, 200, nil)
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+dep.Id+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.json("GET", "/api/v1/requests/"+del.Id, "tnguyen", nil, 200, &del)
		h.json("GET", "/api/v1/deployments/"+dep.Id, "tnguyen", nil, 200, &dep)
		if del.Status != api.RequestStatusFailed || dep.Status != api.DeploymentStatusReady {
			t.Fatalf("a stopped deletion: request %s, deployment %s", del.Status, dep.Status)
		}

		var audited int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action like 'subscription.%'`).Scan(&audited)
		if audited < 6 {
			t.Fatalf("audited %d subscription changes", audited)
		}
	})
}

func errorOf(r api.Request) string {
	if r.Error == nil {
		return ""
	}
	return r.Error.Message
}
