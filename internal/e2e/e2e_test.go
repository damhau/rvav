// Package e2e runs slice 1 end to end: the HTTP API on a real PostgreSQL,
// vcsim with the GVA layout, the fake EfficientIP and AWX, and the real
// Provision workflow and activities in Temporal's test environment.
//
// It starts its own PostgreSQL (see pgtest), or uses a database it may wipe:
//
//	RVAV_TEST_DATABASE_URL=postgres://rvav:rvav@127.0.0.1:5432/rvav_test?sslmode=disable go test ./internal/e2e/
//
// It is skipped with -short.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/httpapi"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/pgtest"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/store"
)

// databaseURL is set by TestMain.
var databaseURL string

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	url, stop, err := pgtest.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(1)
	}
	databaseURL = url
	// Ctrl-C would otherwise leave PostgreSQL running: pg_ctl detaches it.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		_ = stop()
		os.Exit(1)
	}()
	code := m.Run()
	if err := stop(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: stop PostgreSQL:", err)
	}
	os.Exit(code)
}

// recorder stands in for Temporal: the test runs each started workflow
// itself, and delivers approval signals with deliver.
type recorder struct {
	mu         sync.Mutex
	started    []string
	cancelled  []string
	signals    []provision.ApprovalSignal
	rolledBack []string
	startErr   error // returned by Start, to act as an unreachable Temporal
}

func (r *recorder) Start(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return r.startErr
	}
	r.started = append(r.started, id)
	return nil
}

func (r *recorder) Cancel(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelled = append(r.cancelled, id)
	return nil
}

func (r *recorder) Approval(_ context.Context, _, approvalID, outcome string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.signals = append(r.signals, provision.ApprovalSignal{ApprovalID: approvalID, Outcome: outcome})
	return nil
}

func (r *recorder) Rollback(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rolledBack = append(r.rolledBack, id)
	return nil
}

// deliver sends the approval signals sent so far to a test workflow.
func (r *recorder) deliver(env *testsuite.TestWorkflowEnvironment) {
	r.mu.Lock()
	sigs := r.signals
	r.signals = nil
	r.mu.Unlock()
	for _, sig := range sigs {
		env.SignalWorkflow(provision.SignalApproval, sig)
	}
}

type harness struct {
	t     *testing.T
	ctx   context.Context
	store *store.Store
	srv   *httptest.Server
	api   *httpapi.Server
	acts  *provision.Activities
	svc   *core.Service
	hub   *events.Hub
	vc    *vsphere.Client
	wf    *recorder
	awx   *awx.Fake
	ipam  *efficientip.FakeServer
}

func (h *harness) do(method, path, user string, body any, headers ...string) (int, []byte, http.Header) {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(h.ctx, method, h.srv.URL+path, rd)
	if user != "" {
		req.Header.Set(auth.DevUserHeader, user)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func (h *harness) json(method, path, user string, body any, want int, out any, headers ...string) http.Header {
	h.t.Helper()
	code, b, hdr := h.do(method, path, user, body, headers...)
	if code != want {
		h.t.Fatalf("%s %s = %d, want %d: %s", method, path, code, want, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			h.t.Fatalf("%s %s: decode: %v: %s", method, path, err, b)
		}
	}
	return hdr
}

// runWorkflow executes the request's workflow with the real activities.
func (h *harness) runWorkflow(requestID string, setup func(env *testsuite.TestWorkflowEnvironment)) {
	h.t.Helper()
	var s testsuite.WorkflowTestSuite
	s.SetLogger(nil)
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(h.acts)
	env.SetTestTimeout(2 * time.Minute)
	if setup != nil {
		setup(env)
	}
	env.ExecuteWorkflow(provision.Provision, requestID)
	if !env.IsWorkflowCompleted() {
		h.t.Fatalf("workflow for %s did not complete", requestID)
	}
	// A cancelled workflow reports a canceled error even after it cleaned up.
	if err := env.GetWorkflowError(); err != nil && !temporal.IsCanceledError(err) {
		h.t.Fatalf("workflow for %s: %v", requestID, err)
	}
}

func setup(t *testing.T, ctx context.Context, c *vim25.Client) *harness {
	url := databaseURL
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool.Exec(ctx, `drop schema public cascade; create schema public`); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	if err := seed.Run(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := vsphere.Bootstrap(ctx, c, vsphere.DefaultLayout()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("RVAV_TEST_VERBOSE") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	vc := vsphere.NewFromVim("vc01.corp.example", c)
	pub := &events.Publisher{Store: st, Logger: log}
	syncer := &inventory.Syncer{Store: st, VSphere: vc, Events: pub, Log: log}
	if err := syncer.SyncOnce(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	fakeIPAM, err := app.NewFakeEfficientIP(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	ipamSrv := httptest.NewServer(fakeIPAM)
	t.Cleanup(ipamSrv.Close)
	ipam, err := fakeIPAM.Client(ipamSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	fakeAWX := awx.NewFake(20*time.Millisecond, "")
	wf := &recorder{}
	svc := &core.Service{Store: st, IPAM: ipam, Workflows: wf, Events: pub, Log: log, AWX: fakeAWX}
	hub := events.NewHub(st, log)
	hctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go hub.Run(hctx)

	apiSrv := &httpapi.Server{Store: st, Core: svc, Hub: hub, Log: log, AuthMode: "dev",
		Auth: &auth.Dev{Store: st, DefaultUser: "lmartin"}, Heartbeat: time.Second}
	srv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(srv.Close)

	acts := &provision.Activities{Store: st, VSphere: vc, IPAM: ipam, AWX: fakeAWX, Events: pub, Log: log,
		PollInterval: 20 * time.Millisecond, IPTimeout: 20 * time.Second}
	return &harness{t: t, ctx: ctx, store: st, srv: srv, api: apiSrv, acts: acts, svc: svc, hub: hub, vc: vc, wf: wf, awx: fakeAWX, ipam: fakeIPAM}
}

func TestSlice1EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web = "prj_web"

		// Identity and catalog
		var me api.Me
		h.json("GET", "/api/v1/me", "lmartin", nil, 200, &me)
		if me.User.Name != "Léa Martin" || len(me.Projects) != 1 || me.Projects[0].Role != "project-admin" {
			t.Fatalf("me = %+v", me)
		}
		var items api.CatalogItemPage
		h.json("GET", "/api/v1/catalog/items?project_id="+web+"&limit=4", "lmartin", nil, 200, &items)
		if len(items.Items) != 4 || items.NextCursor == nil {
			t.Fatalf("catalog page 1 = %d items, next %v", len(items.Items), items.NextCursor)
		}
		var page2 api.CatalogItemPage
		h.json("GET", "/api/v1/catalog/items?project_id="+web+"&limit=4&cursor="+*items.NextCursor, "lmartin", nil, 200, &page2)
		if len(page2.Items) == 0 || page2.Items[0].Name <= items.Items[3].Name {
			t.Fatalf("catalog page 2 does not continue after page 1: %+v", page2.Items)
		}
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		if item.Approval != api.Conditional || item.Version.Number != "3.2" {
			t.Fatalf("rhel9 item = %+v", item)
		}
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		if form.UiSchema.Fields["datastore"].ValueSource == nil {
			t.Fatalf("form has no datastore value source")
		}

		// Dynamic values
		dev := "dev"
		values := func(field string, inputs map[string]any) api.FieldValues {
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", "lmartin", api.FieldValuesQuery{VersionId: form.VersionId,
				ProjectId: web, Environment: &dev, Field: field, Inputs: inputs}, 200, &out)
			return out
		}
		clusters := values("cluster", map[string]any{})
		if clusters.Options == nil || len(*clusters.Options) != 1 || (*clusters.Options)[0].Value != "GVA-DEV-CL01" {
			t.Fatalf("clusters = %+v", clusters.Options)
		}
		ds := values("datastore", map[string]any{"cluster": "GVA-DEV-CL01"})
		if ds.Options == nil || len(*ds.Options) != 2 {
			t.Fatalf("datastores = %+v", ds.Options)
		}
		nets := values("network", map[string]any{"cluster": "GVA-DEV-CL01"})
		if nets.Options == nil || len(*nets.Options) != 2 || (*nets.Options)[0].Value != "dvpg-app-dev-vl310" {
			t.Fatalf("networks = %+v", nets.Options)
		}
		ip := values("ip_address", map[string]any{"network": "dvpg-app-dev-vl310"})
		if ip.Value != "10.31.10.20" {
			t.Fatalf("next free ip = %v", ip.Value)
		}
		name := values("vm_name", map[string]any{"role": "app"})
		if name.Value != "web-dev-app001" {
			t.Fatalf("name preview = %v", name.Value)
		}

		inputs := map[string]any{"role": "app", "size": "M", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline", "idm-join", "monitoring-agent"}}
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: inputs, Justification: strPtr("Second API node for the checkout load test, WEB-2291.")}

		// Preview
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "lmartin", draft, 200, &pv)
		if !pv.Valid || pv.Approval.Required || pv.Names == nil || (*pv.Names)[0] != "web-dev-app001" {
			t.Fatalf("preview = %+v", pv)
		}
		if pv.Quota[0].Adding != 4 || pv.Lease == nil || pv.Lease.DefaultDays == nil || *pv.Lease.DefaultDays != 30 {
			t.Fatalf("preview quota/lease = %+v %+v", pv.Quota, pv.Lease)
		}
		bad := draft
		bad.Inputs = map[string]any{"role": "App!", "size": "XXL", "cluster": "GVA-PRD-CL01", "datastore": "x", "network": "y", "post_config": []string{"idm-join"}}
		h.json("POST", "/api/v1/requests/preview", "lmartin", bad, 200, &pv)
		if pv.Valid || len(pv.Errors) < 4 {
			t.Fatalf("bad preview should report errors: %+v", pv.Errors)
		}

		// Live events: subscribe before submitting.
		evc := make(chan string, 512)
		sctx, stopSSE := context.WithCancel(ctx)
		defer stopSSE()
		go readSSE(sctx, h.srv.URL+"/api/v1/events?topics=requests,deployments,notifications&project_id="+web, "lmartin", "", evc)
		time.Sleep(200 * time.Millisecond)

		// Submit, with idempotency
		key := uuid.NewString()
		var req api.Request
		hdr := h.json("POST", "/api/v1/requests", "lmartin", draft, 202, &req, "Idempotency-Key", key)
		if req.Status != api.RequestStatusQueued || req.Number == "" || hdr.Get("Location") != "/api/v1/requests/"+req.Id {
			t.Fatalf("created request = %+v, location %q", req, hdr.Get("Location"))
		}
		var replay api.Request
		_, b, rh := h.do("POST", "/api/v1/requests", "lmartin", draft, "Idempotency-Key", key)
		_ = json.Unmarshal(b, &replay)
		if replay.Id != req.Id || rh.Get("Idempotent-Replayed") != "true" {
			t.Fatalf("replay = %s", b)
		}
		other := draft
		other.Justification = strPtr("something else")
		if code, b, _ := h.do("POST", "/api/v1/requests", "lmartin", other, "Idempotency-Key", key); code != 409 {
			t.Fatalf("reused key with another body = %d %s", code, b)
		}
		if code, _, _ := h.do("POST", "/api/v1/requests", "lmartin", draft); code != 400 {
			t.Fatalf("missing Idempotency-Key = %d, want 400", code)
		}
		if len(h.wf.started) != 1 {
			t.Fatalf("workflows started = %v", h.wf.started)
		}

		// Run it
		h.runWorkflow(req.Id, nil)

		h.json("GET", "/api/v1/requests/"+req.Number, "lmartin", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded || req.Progress.Done != req.Progress.Total || req.DeploymentIds == nil || len(*req.DeploymentIds) != 1 {
			t.Fatalf("request after workflow = %+v", req)
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "lmartin", nil, 200, &steps)
		var job api.Step
		for _, s := range steps.Items {
			if s.Status != api.StepStatusDone {
				t.Fatalf("step %s is %s: %v", s.Key, s.Status, deref(s.Error))
			}
			if s.Key == "awx.job:idm-join" {
				job = s
			}
		}
		if len(job.Children) != 4 || job.Children[2].Title != "idm : join realm" || !job.HasLog || job.ExternalRef == nil {
			t.Fatalf("idm-join step = %+v", job)
		}
		var logc api.LogChunk
		h.json("GET", fmt.Sprintf("/api/v1/requests/%s/steps/%s/log?offset=0", req.Id, job.Id), "lmartin", nil, 200, &logc)
		if len(logc.Lines) < 8 || !logc.Complete || !strings.HasPrefix(logc.Lines[0].Text, "PLAY [Idm join]") {
			t.Fatalf("log = %+v", logc)
		}

		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+(*req.DeploymentIds)[0], "lmartin", nil, 200, &dep)
		if dep.Status != api.DeploymentStatusReady || dep.VmCount != 1 || dep.VmsPoweredOn != 1 || len(dep.Resources) != 4 {
			t.Fatalf("deployment = %+v", dep)
		}
		vmRes, err := dep.Resources[0].AsVmResource()
		if err != nil || vmRes.Vm.PowerState != "on" || vmRes.Vm.CpuCount != 4 || vmRes.Vm.MemoryMb != 16384 {
			t.Fatalf("vm resource = %+v %v", vmRes, err)
		}
		vm, err := h.vc.FindVM(ctx, "web-dev-app001")
		if err != nil || vm == nil || vm.PowerState != "on" || vm.IP != "10.31.10.20" || vm.Folder != "/GVA/vm/rvav/web/dev" {
			t.Fatalf("vcsim vm = %+v %v", vm, err)
		}
		var deps api.DeploymentPage
		h.json("GET", "/api/v1/deployments?project_id="+web+"&q=10.31.10.20", "lmartin", nil, 200, &deps)
		if len(deps.Items) != 1 || deps.Items[0].PrimaryAddress == nil || deps.Items[0].PrimaryAddress.Ip != "10.31.10.20" {
			t.Fatalf("deployment search by IP = %+v", deps.Items)
		}

		// Quota now counts the VM.
		var quota struct {
			Lines []struct {
				Resource string
				Used     int
				Limit    *int
			}
		}
		h.json("GET", "/api/v1/projects/prj_web/quota", "lmartin", nil, 200, &quota)
		if quota.Lines[0].Resource != "vcpu" || quota.Lines[0].Used != 4 {
			t.Fatalf("quota = %+v", quota.Lines)
		}

		// Events arrived live and in order.
		got := drain(evc, 500*time.Millisecond)
		if !containsAll(got, "request.updated", "request.step", "request.log", "deployment.updated", "resource.updated", "notification") {
			t.Fatalf("events seen: %v", uniq(got))
		}
		// Reconnect with Last-Event-ID replays what came after it.
		last := firstID(t, h, "lmartin")
		replayed := make(chan string, 512)
		rctx, stopReplay := context.WithTimeout(ctx, 2*time.Second)
		defer stopReplay()
		go readSSE(rctx, h.srv.URL+"/api/v1/events?topics=requests&project_id="+web, "lmartin", last, replayed)
		if r := drain(replayed, time.Second); len(r) == 0 {
			t.Fatalf("no events replayed after id %s", last)
		}

		// Production with 48 GB needs approval; approve through the signal.
		prd := draft
		prd.Environment = "prd"
		prd.Inputs = map[string]any{"role": "front", "size": "XL", "cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02",
			"network": "dvpg-web-prd-vl210", "post_config": []string{"linux-baseline"}}
		var prdPV api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", prd, 200, &prdPV)
		if !prdPV.Valid || !prdPV.Approval.Required || len(prdPV.Approval.Policies) != 2 || prdPV.Lease.ExpiresAt != nil {
			t.Fatalf("prd preview = %+v", prdPV)
		}
		var prdReq api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", prd, 202, &prdReq, "Idempotency-Key", uuid.NewString())
		if prdReq.Status != api.RequestStatusPendingApproval {
			t.Fatalf("prd request status = %s", prdReq.Status)
		}
		h.runWorkflow(prdReq.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				var inbox api.ApprovalPage
				h.json("GET", "/api/v1/approvals", "mkeller", nil, 200, &inbox)
				if inbox.Total != 1 || inbox.Items[0].Request.Id != prdReq.Id {
					t.Errorf("mkeller's inbox = %+v", inbox)
					return
				}
				h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", "mkeller",
					api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove, Comment: strPtr("OK for Black Friday")}, 200, nil)
				h.wf.deliver(env)
			}, time.Hour)
		})
		h.json("GET", "/api/v1/requests/"+prdReq.Id, "tnguyen", nil, 200, &prdReq)
		if prdReq.Status != api.RequestStatusSucceeded {
			t.Fatalf("approved request = %s", prdReq.Status)
		}
		h.json("GET", "/api/v1/requests/"+prdReq.Id+"/steps", "tnguyen", nil, 200, &steps)
		if steps.Items[1].Title != "Approved by Marc Keller" {
			t.Fatalf("policy step = %+v", steps.Items[1])
		}

		// A failing AWX task fails the request and keeps the VM.
		h.awx.FailTask = "idm : join realm"
		fail := draft
		fail.Inputs = map[string]any{"role": "worker", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline", "idm-join"}}
		var failReq api.Request
		h.json("POST", "/api/v1/requests", "lmartin", fail, 202, &failReq, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(failReq.Id, nil)
		h.awx.FailTask = ""
		h.json("GET", "/api/v1/requests/"+failReq.Id, "lmartin", nil, 200, &failReq)
		if failReq.Status != api.RequestStatusFailed || failReq.Error == nil || !strings.Contains(failReq.Error.Message, "idm : join realm") {
			t.Fatalf("failed request = %+v", failReq)
		}
		h.json("GET", "/api/v1/deployments/"+(*failReq.DeploymentIds)[0], "lmartin", nil, 200, &dep)
		if dep.Status != api.DeploymentStatusFailed || dep.VmsPoweredOn != 1 {
			t.Fatalf("failed deployment = %+v", dep)
		}

		// Cancel while waiting for approval.
		var waiting api.Request
		prd.Inputs["role"] = "edge"
		h.json("POST", "/api/v1/requests", "lmartin", prd, 202, &waiting, "Idempotency-Key", uuid.NewString())
		var cancelled api.Request
		h.json("POST", "/api/v1/requests/"+waiting.Number+"/cancel", "lmartin", nil, 202, &cancelled)
		if cancelled.Status != api.RequestStatusCancelled {
			t.Fatalf("cancelled request = %s", cancelled.Status)
		}
		if code, _, _ := h.do("POST", "/api/v1/requests/"+waiting.Id+"/cancel", "lmartin", nil); code != 409 {
			t.Fatalf("cancelling twice = %d, want 409", code)
		}

		// Cancel while running: once the VM exists, the API cancel marks the
		// request cancelling and the workflow rolls everything back.
		var running api.Request
		draft.Inputs = map[string]any{"role": "tmp", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline", "idm-join", "monitoring-agent"}}
		h.json("POST", "/api/v1/requests", "lmartin", draft, 202, &running, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(running.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.SetOnActivityCompletedListener(func(info *activity.Info, _ converter.EncodedValue, _ error) {
				if info.ActivityType.Name == "CloneVM" {
					var mid api.Request
					h.json("POST", "/api/v1/requests/"+running.Id+"/cancel", "lmartin", nil, 202, &mid)
					if mid.Status != api.RequestStatusCancelling {
						t.Errorf("status after cancelling a running request = %s", mid.Status)
					}
					env.CancelWorkflow()
				}
			})
		})
		h.json("GET", "/api/v1/requests/"+running.Id, "lmartin", nil, 200, &running)
		if running.Status != api.RequestStatusCancelled {
			t.Fatalf("cancelled running request = %s", running.Status)
		}
		h.json("GET", "/api/v1/requests/"+running.Id+"/steps", "lmartin", nil, 200, &steps)
		rolledBack := steps.Items[len(steps.Items)-1]
		if rolledBack.Key != "rollback" || !strings.Contains(deref(rolledBack.Detail), "deleted VM web-dev-tmp001") {
			t.Fatalf("last step = %+v", rolledBack)
		}
		if vm, err := h.vc.FindVM(ctx, "web-dev-tmp001"); err != nil || vm != nil {
			t.Fatalf("rolled back VM still in vCenter: %+v %v", vm, err)
		}
		h.json("GET", "/api/v1/deployments/"+(*running.DeploymentIds)[0], "lmartin", nil, 200, &dep)
		if dep.Status != api.DeploymentStatusDeleted || len(dep.Resources) != 0 {
			t.Fatalf("rolled back deployment = %+v", dep)
		}

		// Visibility: Sophie is not in Web Platform.
		if code, _, _ := h.do("GET", "/api/v1/requests/"+req.Id, "sbrunner", nil); code != 404 {
			t.Fatalf("other project's request = %d, want 404", code)
		}
		if code, _, _ := h.do("GET", "/api/v1/catalog/items?project_id="+web, "sbrunner", nil); code != 404 {
			t.Fatalf("other project's catalog = %d, want 404", code)
		}
		var problem api.Problem
		h.json("GET", "/api/v1/requests/REQ-0", "lmartin", nil, 404, &problem)
		if problem.RequestId == "" || problem.Type != "https://rvav.dev/problems/not-found" {
			t.Fatalf("problem = %+v", problem)
		}
		var reqs api.RequestPage
		h.json("GET", "/api/v1/requests?project_id="+web+"&status=succeeded", "lmartin", nil, 200, &reqs)
		if len(reqs.Items) != 2 {
			t.Fatalf("succeeded requests = %d, want 2", len(reqs.Items))
		}

		// Temporal unreachable: 502, nothing is kept, and the same key works on retry.
		count := func(table string) int {
			var n int
			if err := h.store.Pool.QueryRow(h.ctx, "select count(*) from "+table).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		beforeReqs, beforeDeps := count("requests"), count("deployments")
		h.wf.mu.Lock()
		h.wf.startErr = errors.New("connection refused")
		h.wf.mu.Unlock()
		retryKey := uuid.NewString()
		var upstream api.Problem
		h.json("POST", "/api/v1/requests", "lmartin", draft, 502, &upstream, "Idempotency-Key", retryKey)
		if upstream.Type != "https://rvav.dev/problems/upstream" {
			t.Fatalf("unreachable Temporal problem = %+v", upstream)
		}
		if r, d := count("requests"), count("deployments"); r != beforeReqs || d != beforeDeps {
			t.Fatalf("failed start kept rows: requests %d→%d, deployments %d→%d", beforeReqs, r, beforeDeps, d)
		}
		h.wf.mu.Lock()
		h.wf.startErr = nil
		h.wf.mu.Unlock()
		var retried api.Request
		h.json("POST", "/api/v1/requests", "lmartin", draft, 202, &retried, "Idempotency-Key", retryKey)
		if count("requests") != beforeReqs+1 {
			t.Fatalf("retry after a failed start did not create the request")
		}
	})
}

// readSSE sends "<id> <type>" for each event of the stream.
func readSSE(ctx context.Context, url, user, lastID string, out chan<- string) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set(auth.DevUserHeader, user)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	id, typ := "", ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			typ = strings.TrimPrefix(line, "event: ")
		case line == "":
			if typ != "" && typ != "heartbeat" {
				select {
				case out <- id + " " + typ:
				default:
				}
			}
			id, typ = "", ""
		}
	}
}

func drain(c <-chan string, quiet time.Duration) []string {
	var out []string
	for {
		select {
		case s := <-c:
			out = append(out, s)
		case <-time.After(quiet):
			return out
		}
	}
}

func containsAll(got []string, types ...string) bool {
	seen := map[string]bool{}
	for _, g := range got {
		if parts := strings.SplitN(g, " ", 2); len(parts) == 2 {
			seen[parts[1]] = true
		}
	}
	for _, t := range types {
		if !seen[t] {
			return false
		}
	}
	return true
}

func uniq(got []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range got {
		parts := strings.SplitN(g, " ", 2)
		if len(parts) == 2 && !seen[parts[1]] {
			seen[parts[1]] = true
			out = append(out, parts[1])
		}
	}
	return out
}

// firstID returns an early request event ID to replay from.
func firstID(t *testing.T, h *harness, _ string) string {
	var id int64
	if err := h.store.Pool.QueryRow(h.ctx, `select min(id) from events where 'requests' = any(topics)`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(id)
}

func strPtr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
