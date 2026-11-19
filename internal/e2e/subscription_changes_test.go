package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
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
