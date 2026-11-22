package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// changeHook answers blocking subscriptions by path, as a CMDB or an IPAM
// of the customer's would, and records what it was asked.
type changeHook struct {
	mu      sync.Mutex
	answers map[string]string // a JSON answer, or "fail" for a 500
	calls   []string          // path, the VM's name, " recovery" for a recovery
	events  map[string]map[string]any
}

func (h *changeHook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var ev map[string]any
	_ = json.NewDecoder(r.Body).Decode(&ev)
	h.mu.Lock()
	defer h.mu.Unlock()
	data, _ := ev["data"].(map[string]any)
	m, _ := data["machine"].(map[string]any)
	call := r.URL.Path + " " + str(m["name"])
	if _, ok := ev["recovery"]; ok {
		call += " recovery"
	}
	h.calls, h.events[r.URL.Path] = append(h.calls, call), ev
	if a := h.answers[r.URL.Path]; a == "fail" {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		_, _ = w.Write([]byte(a))
	}
}

func (h *changeHook) answer(path, body string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.answers[path] = body
}

// took returns the calls made since the last time, and forgets them.
func (h *changeHook) took() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.calls
	h.calls = nil
	return out
}

func (h *changeHook) event(path string) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.events[path]
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// TestSubscriptionChanges: blocking subscriptions on a VM's topics rename
// it, move it and give it an address, checked again before it is built;
// refused changes fail the request; a recovery target decides when a
// subscription fails; a retry asks nobody again (ADR-0054).
func TestSubscriptionChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner = "mkeller", "tnguyen"
		hook := &changeHook{answers: map[string]string{}, events: map[string]map[string]any{}}
		srv := httptest.NewServer(hook)
		defer srv.Close()
		h.allowWebhooks(srv.URL)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-hook", Kind: api.SecretWriteKindToken,
			Value: new("hook-signing-secret")}, 201, nil)
		target := func(path string) api.SubscriptionTarget {
			var t api.SubscriptionTarget
			_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+srv.URL+path+`", "secret": "cmdb-hook"}}`), &t)
			return t
		}
		subscribe := func(w api.SubscriptionWrite) api.Subscription {
			t.Helper()
			w.Blocking, w.TimeoutSeconds = new(true), new(5)
			var sub api.Subscription
			h.json("POST", "/api/v1/admin/subscriptions", admin, w, 201, &sub)
			return sub
		}
		request := func() api.Request {
			t.Helper()
			var item api.CatalogItem
			h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", owner, nil, 200, &item)
			var req api.Request
			h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
				202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, owner, nil, 200, &req)
			return req
		}
		step := func(requestID, key string) api.Step {
			t.Helper()
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+requestID+"/steps", owner, nil, 200, &steps)
			var find func([]api.Step) *api.Step
			find = func(list []api.Step) *api.Step {
				for i := range list {
					if list[i].Key == key {
						return &list[i]
					}
					if s := find(list[i].Children); s != nil {
						return s
					}
				}
				return nil
			}
			if s := find(steps.Items); s != nil {
				return *s
			}
			t.Fatalf("no step %s", key)
			return api.Step{}
		}
		runs := func(sub api.Subscription) []api.SubscriptionRun {
			t.Helper()
			var list struct{ Items []api.SubscriptionRun }
			h.json("GET", "/api/v1/admin/subscriptions/"+sub.Id+"/runs", admin, nil, 200, &list)
			return list.Items
		}
		const allocKey, netKey = "checks:compute.allocation.pre", "checks:network.configure"

		// Which topics may block, and what their answers may change.
		var topics struct{ Items []api.EventTopic }
		h.json("GET", "/api/v1/admin/event-topics", admin, nil, 200, &topics)
		mayChange := map[string][]api.SubscriptionChange{}
		for _, tp := range topics.Items {
			if tp.MayBlock {
				mayChange[string(tp.Key)] = tp.MayChange
			}
		}
		if len(mayChange) != 5 || !slices.Equal(mayChange["compute.allocation.pre"], []api.SubscriptionChange{api.ChangeName, api.ChangePlacement}) ||
			!slices.Equal(mayChange["network.configure"], []api.SubscriptionChange{api.ChangeNetworks}) ||
			!slices.Equal(mayChange["provisioning.pre"], []api.SubscriptionChange{api.ChangeProperties}) || len(mayChange["day2.pre"]) != 0 {
			t.Fatalf("topics that may block = %v", mayChange)
		}
		// A recovery target is a blocking subscription's, checked as its
		// target is.
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/subscriptions", admin, api.SubscriptionWrite{Name: "Told", Topic: api.ComputeAllocationPre,
			Target: target("/alloc"), Recovery: new(target("/recover"))}, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Equal(fields, []string{"/recovery"}) {
			t.Fatalf("a recovery target without blocking = %v", fields)
		}
		var elsewhere api.SubscriptionTarget
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "https://evil.example/x", "secret": "cmdb-hook"}}`), &elsewhere)
		h.json("POST", "/api/v1/admin/subscriptions", admin, api.SubscriptionWrite{Name: "Told", Topic: api.ComputeAllocationPre,
			Blocking: new(true), Target: target("/alloc"), Recovery: &elsewhere}, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Equal(fields, []string{"/recovery/webhook/url"}) {
			t.Fatalf("a recovery target elsewhere = %v", fields)
		}

		// The CMDB names and places the VM, the customer's IPAM gives its
		// address. The first run fails after building it, and is rolled
		// back.
		if _, err := h.store.Pool.Exec(ctx, `update catalog_item_versions set blueprint = blueprint || '{"rollback_on_failure": true}'
			where item_id = (select id from catalog_items where slug = 'rhel9-server')`); err != nil {
			t.Fatal(err)
		}
		naming := subscribe(api.SubscriptionWrite{Name: "CMDB naming", Topic: api.ComputeAllocationPre, Target: target("/alloc")})
		addressing := subscribe(api.SubscriptionWrite{Name: "IPAM address", Topic: api.NetworkConfigure, Target: target("/net")})
		hook.answer("/alloc", `{"machine": {"name": "web-dev-app742", "placement": {"datastore": "nfs-gva-bronze-01", "resource_pool": "rvav/dev"}}}`)
		hook.answer("/net", `{"outcome": "continue", "machine": {"networks": [{"index": 0, "address": "10.31.10.50"}]}}`)
		h.awx.FailTask = "baseline : register with Satellite"
		req := request()
		h.awx.FailTask = ""
		if req.Status != api.RequestStatusFailed || step(req.Id, "rollback").Status != api.StepStatusDone {
			t.Fatalf("the first run = %s %s", req.Status, errorOf(req))
		}
		if got := hook.took(); !slices.Equal(got, []string{"/alloc web-dev-app001", "/net web-dev-app742"}) {
			t.Fatalf("asked %v", got)
		}
		// network.configure saw what compute.allocation.pre changed.
		m := hook.event("/net")["data"].(map[string]any)["machine"].(map[string]any)
		if pl := m["placement"].(map[string]any); pl["datastore"] != "nfs-gva-bronze-01" || pl["resource_pool"] != "rvav/dev" ||
			m["fqdn"] != "web-dev-app742.corp.example" {
			t.Fatalf("network.configure's machine = %v", m)
		}
		alloc := step(req.Id, allocKey)
		if alloc.Title != "Allocation checks" || alloc.Status != api.StepStatusDone || len(alloc.Children) != 1 ||
			!strings.HasPrefix(deref(alloc.Children[0].Detail), "Went on, renaming it web-dev-app742, placing it on GVA-DEV-CL01, resource pool rvav/dev, datastore nfs-gva-bronze-01") {
			t.Fatalf("allocation checks = %+v", alloc)
		}
		if n := step(req.Id, netKey).Children[0]; deref(n.Detail) != "Went on, giving adapter 0 the address 10.31.10.50." {
			t.Fatalf("network checks = %+v", n)
		}
		var detail api.SubscriptionRunDetail
		h.json("GET", "/api/v1/admin/subscription-runs/"+runs(naming)[0].Id, admin, nil, 200, &detail)
		if detail.Changes == nil || deref(detail.Changes.Name) != "web-dev-app742" || detail.Changes.Placement == nil ||
			deref(detail.Changes.Placement.Datastore) != "nfs-gva-bronze-01" || detail.Recovery {
			t.Fatalf("the naming run = %+v", detail)
		}

		// Retried, it is built as changed, and nobody is asked again.
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", owner, nil, 202, nil)
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, owner, nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("retried = %s %s", req.Status, errorOf(req))
		}
		if got := hook.took(); len(got) != 0 {
			t.Fatalf("a retry asked %v", got)
		}
		if a := step(req.Id, allocKey); a.Status != api.StepStatusDone {
			t.Fatalf("allocation checks after the retry = %+v", a)
		}
		if n, a := runs(naming), runs(addressing); len(n) != 1 || len(a) != 1 || n[0].Status != api.SubscriptionRunStatusDelivered {
			t.Fatalf("runs = %+v / %+v", n, a)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+(*req.DeploymentIds)[0], owner, nil, 200, &dep)
		if dep.Name != "web-dev-app742" || !vmExists(ctx, c, "web-dev-app742") || vmExists(ctx, c, "web-dev-app001") {
			t.Fatalf("deployment %s; web-dev-app742 in vCenter: %v", dep.Name, vmExists(ctx, c, "web-dev-app742"))
		}
		if _, pool, ds := vmPlacement(t, ctx, c, "web-dev-app742"); !slices.Equal(ds, []string{"nfs-gva-bronze-01"}) || pool != "rvav/dev" {
			t.Fatalf("web-dev-app742 is on %v in pool %s", ds, pool)
		}
		resources, err := h.store.ResourcesForRequest(ctx, req.Id)
		if err != nil {
			t.Fatal(err)
		}
		var addresses []string
		for _, r := range resources {
			if r.Type == "ipam.address" {
				addresses = append(addresses, r.Name)
			}
		}
		if !slices.Equal(addresses, []string{"10.31.10.50"}) {
			t.Fatalf("addresses recorded = %v", addresses)
		}
		// Reserved in EfficientIP under its new name; the address it would
		// have had stays free.
		found, _, err := h.svc.IPAM.Find(ctx, []string{"10.31.10.50", "10.31.10.20"}, nil)
		if err != nil || len(found) != 1 || found[0].IP != "10.31.10.50" || found[0].Hostname != "web-dev-app742.corp.example" {
			t.Fatalf("EfficientIP has %+v (%v)", found, err)
		}

		// Changes rvav refuses fail the request, before anything is made.
		refused := func(why string) {
			t.Helper()
			r := request()
			if r.Status != api.RequestStatusFailed || !strings.Contains(errorOf(r), why) {
				t.Fatalf("refused = %s %q, want %q", r.Status, errorOf(r), why)
			}
			if rs, _ := h.store.ResourcesForRequest(ctx, r.Id); len(rs) != 0 {
				t.Fatalf("a refused change made %+v", rs)
			}
		}
		hook.answer("/alloc", `{"machine": {"name": "web-dev-app742"}}`)
		refused("CMDB naming asked for a change rvav refuses: web-dev-app742 is taken.")
		hook.answer("/alloc", `{"machine": {"name": "db-dev-app743"}}`)
		refused("db-dev-app743 doesn't follow the project's naming policy")
		hook.answer("/alloc", "")
		hook.answer("/net", `{"machine": {"networks": [{"index": 0, "address": "10.31.99.5"}]}}`)
		refused("IPAM address asked for a change rvav refuses: 10.31.99.5 is not in 10.31.10.0/24, the subnet of dvpg-app-dev-vl310.")
		hook.answer("/net", `{"machine": {"networks": [{"index": 0, "address": "10.31.10.50"}]}}`)
		refused("10.31.10.50 is taken")
		hook.answer("/net", "")
		if _, err := h.store.Pool.Exec(ctx, `update inv_resource_pools set memory_limit_mb = 1024 where cluster = 'GVA-DEV-CL01' and path = 'rvav/dev'`); err != nil {
			t.Fatal(err)
		}
		hook.answer("/alloc", `{"machine": {"placement": {"resource_pool": "rvav/dev"}}}`)
		refused("Resource pool rvav/dev of GVA-DEV-CL01 has 1024 MB of memory free; the VM needs 4096 MB.")
		hook.answer("/alloc", `{"machine": {"placement": {"cluster": "GVA-PRD-CL01"}}}`)
		refused("Cluster GVA-PRD-CL01 is not available to Web Platform in Development.")
		hook.answer("/alloc", `{"machine": {"networks": [{"index": 0, "network": "dvpg-db-dev-vl320"}]}}`)
		refused("a VM's networks and addresses change on network.configure, not compute.allocation.pre.")
		h.json("DELETE", "/api/v1/admin/subscriptions/"+addressing.Id, admin, nil, 204, nil)
		hook.took()

		// When the CMDB fails, its recovery target is told and decides.
		h.json("PUT", "/api/v1/admin/subscriptions/"+naming.Id, admin, api.SubscriptionWrite{Name: "CMDB naming", Topic: api.ComputeAllocationPre,
			Blocking: new(true), TimeoutSeconds: new(5), Target: target("/alloc"), Recovery: new(target("/recover"))}, 200, nil)
		hook.answer("/alloc", "fail")
		hook.answer("/recover", `{"outcome": "stop", "reason": "The CMDB is down: try again later."}`)
		stopped := request()
		if stopped.Status != api.RequestStatusFailed || !strings.Contains(errorOf(stopped), "Its recovery stopped the request: The CMDB is down: try again later.") {
			t.Fatalf("stopped by its recovery = %s %q", stopped.Status, errorOf(stopped))
		}
		// Tried once more after failing fast, then its recovery.
		if got := hook.took(); len(got) != 3 || got[0] != got[1] || !strings.HasPrefix(got[0], "/alloc web-dev-app") ||
			got[2] != "/recover"+strings.TrimPrefix(got[0], "/alloc")+" recovery" {
			t.Fatalf("asked %v", got)
		}
		rec, _ := hook.event("/recover")["recovery"].(map[string]any)
		var failedRun, recoveryRun api.SubscriptionRun
		for _, r := range runs(naming) {
			if r.Request != nil && r.Request.Id == stopped.Id {
				if r.Recovery {
					recoveryRun = r
				} else {
					failedRun = r
				}
			}
		}
		if failedRun.Status != api.SubscriptionRunStatusFailed || recoveryRun.Status != api.SubscriptionRunStatusBlocked ||
			rec["run_id"] != failedRun.Id || !strings.HasSuffix(str(rec["reason"]), "answered 500") {
			t.Fatalf("runs %+v / %+v; recovery told %v", failedRun, recoveryRun, rec)
		}
		if s := step(stopped.Id, allocKey+"/"+naming.Id+"/0/recovery"); s.Status != api.StepStatusDone ||
			deref(s.Detail) != "Stopped the request: The CMDB is down: try again later." {
			t.Fatalf("the recovery's step = %+v", s)
		}
		// Its decision wins over the subscription's setting.
		hook.answer("/recover", `{"outcome": "continue"}`)
		goes := request()
		if goes.Status != api.RequestStatusSucceeded {
			t.Fatalf("let go on by its recovery = %s %s", goes.Status, errorOf(goes))
		}
		if s := step(goes.Id, allocKey+"/"+naming.Id+"/0"); s.Status != api.StepStatusSkipped ||
			!strings.HasSuffix(deref(s.Detail), "its recovery let the request go on.") {
			t.Fatalf("the failed check = %+v", s)
		}
		h.json("DELETE", "/api/v1/admin/subscriptions/"+naming.Id, admin, nil, 204, nil)

		// An AWX job answers with its rvav_answer artifact.
		var awxTarget api.SubscriptionTarget
		_ = json.Unmarshal([]byte(`{"awx": {"job_template": "ddi-dns-record"}}`), &awxTarget)
		subscribe(api.SubscriptionWrite{Name: "AWX naming", Topic: api.ComputeAllocationPre, Target: awxTarget})
		h.awx.Artifacts = map[string]map[string]any{
			"ddi-dns-record": {"rvav_answer": map[string]any{"machine": map[string]any{"name": "web-dev-app750"}}},
		}
		named := request()
		h.json("GET", "/api/v1/deployments/"+(*named.DeploymentIds)[0], owner, nil, 200, &dep)
		if named.Status != api.RequestStatusSucceeded || dep.Name != "web-dev-app750" || !vmExists(ctx, c, "web-dev-app750") {
			t.Fatalf("named by AWX = %s %s, deployment %s", named.Status, errorOf(named), dep.Name)
		}
	})
}

// flakyChanger fails its first change, as a lock timeout would.
type flakyChanger struct {
	provision.MachineChanger
	failed atomic.Bool
}

func (c *flakyChanger) ChangeMachine(ctx context.Context, ch provision.MachineChange) (provision.VMUnit, []string, error) {
	if c.failed.CompareAndSwap(false, true) {
		return ch.VM, nil, errors.New("canceling statement due to lock timeout")
	}
	return c.MachineChanger.ChangeMachine(ctx, ch)
}

// TestBlockingCheckRetries: rvav's own failure while a blocking check runs
// is tried again, asking the target again with the same run, which counts
// both calls (#196), and isn't the subscription's: neither its recovery nor
// its on_failure decides; a run the same delivery finished stands (E1.6,
// ADR-0054).
func TestBlockingCheckRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner = "mkeller", "tnguyen"
		hook := &changeHook{answers: map[string]string{}, events: map[string]map[string]any{}}
		srv := httptest.NewServer(hook)
		defer srv.Close()
		h.allowWebhooks(srv.URL)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-hook", Kind: api.SecretWriteKindToken,
			Value: new("hook-signing-secret")}, 201, nil)
		target := func(path string) api.SubscriptionTarget {
			var t api.SubscriptionTarget
			_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+srv.URL+path+`", "secret": "cmdb-hook"}}`), &t)
			return t
		}
		cont := api.ContinueRequest
		var naming api.Subscription
		h.json("POST", "/api/v1/admin/subscriptions", admin, api.SubscriptionWrite{Name: "CMDB naming", Topic: api.ComputeAllocationPre,
			Blocking: new(true), TimeoutSeconds: new(5), OnFailure: &cont, Target: target("/alloc"), Recovery: new(target("/recover"))},
			201, &naming)
		hook.answer("/alloc", `{"machine": {"name": "web-dev-app760"}}`)
		hook.answer("/recover", `{"outcome": "continue"}`)
		h.acts.Changer = &flakyChanger{MachineChanger: h.svc}

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", owner, nil, 200, &item)
		var req api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
			202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, owner, nil, 200, &req)
		var dep api.DeploymentDetail
		if req.Status == api.RequestStatusSucceeded {
			h.json("GET", "/api/v1/deployments/"+(*req.DeploymentIds)[0], owner, nil, 200, &dep)
		}
		if req.Status != api.RequestStatusSucceeded || dep.Name != "web-dev-app760" || !vmExists(ctx, c, "web-dev-app760") {
			t.Fatalf("the request = %s %s, deployment %q", req.Status, errorOf(req), dep.Name)
		}
		if got := hook.took(); len(got) != 2 || got[0] != got[1] || !strings.HasPrefix(got[0], "/alloc web-dev-app") {
			t.Fatalf("asked %v, want the CMDB twice and no recovery", got)
		}
		var list struct{ Items []api.SubscriptionRun }
		h.json("GET", "/api/v1/admin/subscriptions/"+naming.Id+"/runs", admin, nil, 200, &list)
		if len(list.Items) != 1 || list.Items[0].Status != api.SubscriptionRunStatusDelivered || list.Items[0].Recovery {
			t.Fatalf("runs = %+v", list.Items)
		}
		// Both calls count: the first one's answer was never recorded.
		var detail api.SubscriptionRunDetail
		h.json("GET", "/api/v1/admin/subscription-runs/"+list.Items[0].Id, admin, nil, 200, &detail)
		if hist := detail.History; detail.Attempts != 2 || len(hist) != 2 || hist[0].Ok || deref(hist[0].Error) != store.Interrupted ||
			!hist[1].Ok || hist[1].At.Before(hist[0].At) {
			t.Fatalf("the run counts %d attempts: %+v", detail.Attempts, hist)
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", owner, nil, 200, &steps)
		var check *api.Step
		for i := range steps.Items {
			if steps.Items[i].Key == "checks:compute.allocation.pre" && len(steps.Items[i].Children) == 1 {
				check = &steps.Items[i].Children[0]
			}
		}
		if check == nil || check.Status != api.StepStatusDone || !strings.HasPrefix(deref(check.Detail), "Went on, renaming it web-dev-app760") {
			t.Fatalf("the check = %+v", check)
		}

		// The same delivery gets the run it finished; another starts it again.
		run := list.Items[0]
		again := func(delivery string) store.SubscriptionRun {
			t.Helper()
			got, err := h.store.StartBlockingRun(ctx, store.SubscriptionRun{ID: "run_unused", SubscriptionID: naming.Id,
				EventID: run.EventId, RequestID: &req.Id}, delivery)
			if err != nil || got.ID != run.Id {
				t.Fatalf("started %+v (%v)", got, err)
			}
			return got
		}
		if got := again("wf-1/7"); got.Status != "delivering" || got.FinishedAt != nil {
			t.Fatalf("another delivery's run = %+v", got)
		}
		stop := "stop"
		if err := h.store.RecordAttempt(ctx, run.Id, store.Attempt{At: time.Now(), OK: true},
			store.RunUpdate{Status: "blocked", Outcome: &stop, Reason: new("No.")}); err != nil {
			t.Fatal(err)
		}
		if got := again("wf-1/7"); got.Status != "blocked" || deref(got.Reason) != "No." || got.FinishedAt == nil {
			t.Fatalf("the same delivery's run = %+v", got)
		}
		if got := again("wf-2/7"); got.Status != "delivering" || got.Reason != nil {
			t.Fatalf("a new delivery's run = %+v", got)
		}
	})
}

// heldHook holds its first call until the caller gives up on it, as a slow
// target does while rvav's worker stops, and lets the others go on at once.
type heldHook struct {
	calls atomic.Int32
	held  chan struct{}
}

func (h *heldHook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The server notices the caller went away only once the body is read.
	_, _ = io.Copy(io.Discard, r.Body)
	if h.calls.Add(1) == 1 {
		close(h.held)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Minute):
		}
		return
	}
	_, _ = w.Write([]byte(`{"outcome": "continue"}`))
}

// TestBlockingCheckAfterRestart: the worker stops while a blocking check
// waits for its target, on the Temporal dev server. The next worker asks the
// target again under the same run, which counts both calls: the first as
// interrupted before its answer was recorded (#196).
func TestBlockingCheckAfterRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, queue = "mkeller", "tnguyen", "rvav-check-restart-e2e"
		hook := &heldHook{held: make(chan struct{})}
		srv := httptest.NewServer(hook)
		defer srv.Close()
		h.allowWebhooks(srv.URL)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-hook", Kind: api.SecretWriteKindToken,
			Value: new("hook-signing-secret")}, 201, nil)
		var target api.SubscriptionTarget
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+srv.URL+`/cmdb", "secret": "cmdb-hook"}}`), &target)
		var sub api.Subscription
		h.json("POST", "/api/v1/admin/subscriptions", admin, api.SubscriptionWrite{Name: "CMDB check", Topic: api.ProvisioningPre,
			Blocking: new(true), TimeoutSeconds: new(5), Target: target}, 201, &sub)
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { w.Stop() }()

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", owner, nil, 200, &item)
		var req api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
			202, &req, "Idempotency-Key", uuid.NewString())
		select {
		case <-hook.held:
		case <-time.After(time.Minute):
			t.Fatal("the check didn't call its target")
		}
		// Stopping cancels the call, and Temporal gives the check to the
		// next worker: at once when the stopped one reports it, after its
		// StartToClose timeout (80 s) when it can't.
		w.Stop()
		w = provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Minute)
		for req.Status != api.RequestStatusSucceeded {
			if time.Now().After(deadline) {
				t.Fatalf("the request is %s %s", req.Status, errorOf(req))
			}
			time.Sleep(100 * time.Millisecond)
			h.json("GET", "/api/v1/requests/"+req.Id, owner, nil, 200, &req)
		}
		if n := hook.calls.Load(); n != 2 {
			t.Fatalf("the target was called %d times, want twice", n)
		}
		var list struct{ Items []api.SubscriptionRun }
		h.json("GET", "/api/v1/admin/subscriptions/"+sub.Id+"/runs", admin, nil, 200, &list)
		if len(list.Items) != 1 || list.Items[0].Status != api.SubscriptionRunStatusDelivered || list.Items[0].Attempts != 2 {
			t.Fatalf("runs = %+v", list.Items)
		}
		var detail api.SubscriptionRunDetail
		h.json("GET", "/api/v1/admin/subscription-runs/"+list.Items[0].Id, admin, nil, 200, &detail)
		if hist := detail.History; len(hist) != 2 || hist[0].Ok || deref(hist[0].Error) != store.Interrupted || !hist[1].Ok ||
			hist[1].StatusCode == nil || *hist[1].StatusCode != http.StatusOK {
			t.Fatalf("the run's history = %+v", hist)
		}
	})
}

// TestHousekeepingBlockingRuns: housekeeping fails a blocking run whose
// current call began more than an hour ago, its answer lost, and leaves
// alone one a late Retry started again, however old the run is (#198). A run
// the dispatcher claimed is left to its lease, however long it waited.
func TestHousekeepingBlockingRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		target := json.RawMessage(`{"webhook": {"url": "https://cmdb.example/check"}}`)
		check := store.Subscription{ID: ids.New(ids.Subscription), Name: "CMDB check", Topic: outbox.ProvisioningPre, Blocking: true,
			OnFailure: "stop_request", Priority: 100, TimeoutSeconds: 30, Enabled: true, Target: target}
		notice := store.Subscription{ID: ids.New(ids.Subscription), Name: "CMDB notice", Topic: outbox.ProvisioningPre,
			OnFailure: "continue_request", Priority: 100, TimeoutSeconds: 30, Enabled: true, Target: target}
		event := ids.New(ids.Event)
		for _, sub := range []store.Subscription{check, notice} {
			if err := h.store.SaveSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.store.InsertTestEvent(ctx, event, outbox.ProvisioningPre, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		sql := func(q string, args ...any) {
			t.Helper()
			if _, err := h.store.Pool.Exec(ctx, q, args...); err != nil {
				t.Fatal(err)
			}
		}
		prune := func() {
			t.Helper()
			if _, err := h.store.PruneRuns(ctx, time.Now().Add(-dispatch.Retention)); err != nil {
				t.Fatal(err)
			}
		}
		run := func(id string) api.SubscriptionRunDetail {
			t.Helper()
			var out api.SubscriptionRunDetail
			h.json("GET", "/api/v1/admin/subscription-runs/"+id, admin, nil, 200, &out)
			return out
		}
		start := func(delivery string) store.SubscriptionRun {
			t.Helper()
			r, err := h.store.StartBlockingRun(ctx, store.SubscriptionRun{ID: ids.New(ids.Run), SubscriptionID: check.ID, EventID: event},
				delivery)
			if err != nil {
				t.Fatal(err)
			}
			return r
		}

		// The request's first run: the check stopped it two hours ago.
		first := start("wf-1/7")
		stop := "stop"
		if err := h.store.RecordAttempt(ctx, first.ID, store.Attempt{At: time.Now(), OK: true},
			store.RunUpdate{Status: "blocked", Outcome: &stop, Reason: new("No room in the CMDB.")}); err != nil {
			t.Fatal(err)
		}
		sql(`update subscription_runs set created_at = created_at - interval '2 hours',
			next_attempt_at = next_attempt_at - interval '2 hours', finished_at = finished_at - interval '2 hours' where id = $1`, first.ID)

		// A Retry now starts it again, with the call that begins.
		if again := start("wf-2/7"); again.ID != first.ID || again.Status != "delivering" || time.Since(again.CreatedAt) < time.Hour {
			t.Fatalf("started again: %+v", again)
		}
		prune()
		if got := run(first.ID); got.Status != api.SubscriptionRunStatusDelivering || got.Reason != nil || got.FinishedAt != nil {
			t.Fatalf("housekeeping took the run a Retry started: %s %q", got.Status, deref(got.Reason))
		}

		// Its answer is lost: an hour after that call began, the run failed.
		sql(`update subscription_runs set next_attempt_at = now() - interval '61 minutes' where id = $1`, first.ID)
		prune()
		if got := run(first.ID); got.Status != api.SubscriptionRunStatusFailed || deref(got.Reason) != store.Interrupted ||
			got.FinishedAt == nil {
			t.Fatalf("housekeeping left a lost call's run %s %q", got.Status, deref(got.Reason))
		}

		// The dispatcher's run is due since its next_attempt_at, not
		// delivered since: it is the lease's to take back.
		queued := store.SubscriptionRun{ID: ids.New(ids.Run), SubscriptionID: notice.ID, EventID: event, Status: "queued",
			NextAttemptAt: time.Now().Add(-2 * time.Hour)}
		if err := h.store.InsertRun(ctx, h.store.Pool, queued); err != nil {
			t.Fatal(err)
		}
		claimed, err := h.store.ClaimRuns(ctx, 10, time.Now())
		if err != nil || !slices.ContainsFunc(claimed, func(r store.ClaimedRun) bool { return r.ID == queued.ID }) {
			t.Fatalf("claimed %d runs (%v)", len(claimed), err)
		}
		prune()
		if got := run(queued.ID); got.Status != api.SubscriptionRunStatusDelivering || got.Reason != nil {
			t.Fatalf("housekeeping took a claimed run: %s %q", got.Status, deref(got.Reason))
		}
	})
}
