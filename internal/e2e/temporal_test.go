package e2e

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/temporalproto"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
)

// TestThroughTemporal runs requests through a real Temporal server, the CLI
// dev server (downloaded once into the user cache directory), and the real
// worker: starting the workflow, the approval signal and cancelling work as
// they do in production.
func TestThroughTemporal(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-provision-e2e"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		// The housekeeping schedule is created once, whoever starts first.
		for range 2 {
			if err := provision.EnsureHousekeeping(ctx, tc, taskQueue); err != nil {
				t.Fatal(err)
			}
		}
		sched, err := tc.ScheduleClient().GetHandle(ctx, provision.HousekeepingSchedule).Describe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if every := sched.Schedule.Spec.Intervals[0].Every; every != provision.HousekeepingEvery {
			t.Fatalf("housekeeping every %s", every)
		}

		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		draft := func(env, role string, inputs map[string]any) api.RequestDraft {
			inputs["role"] = role
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web,
				Environment: api.EnvironmentKey(env), Inputs: inputs}
		}
		devInputs := func() map[string]any {
			return map[string]any{"size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}
		}
		prdInputs := func() map[string]any {
			return map[string]any{"size": "XL", "cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02",
				"network": "dvpg-web-prd-vl210", "post_config": []string{"linux-baseline"}}
		}
		submit := func(user string, d api.RequestDraft) api.Request {
			var r api.Request
			h.json("POST", "/api/v1/requests", user, d, 202, &r, "Idempotency-Key", uuid.NewString())
			return r
		}

		// A dev request runs straight through, and Temporal keeps its history.
		req := submit("lmartin", draft("dev", "app", devInputs()))
		h.waitStatus(req.Id, "lmartin", api.RequestStatusSucceeded)
		// The last activity marks the request succeeded just before the
		// workflow itself completes.
		wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := tc.GetWorkflow(wctx, provision.WorkflowID(req.Id), "").Get(wctx, nil); err != nil {
			t.Fatal(err)
		}
		desc, err := tc.DescribeWorkflowExecution(ctx, provision.WorkflowID(req.Id), "")
		if err != nil {
			t.Fatal(err)
		}
		if st := desc.WorkflowExecutionInfo.Status; st != enums.WORKFLOW_EXECUTION_STATUS_COMPLETED {
			t.Fatalf("workflow status = %s", st)
		}
		if n := desc.WorkflowExecutionInfo.HistoryLength; n < 10 {
			t.Fatalf("workflow history has %d events", n)
		}

		// Production waits for approval; the decision arrives as a signal.
		prd := submit("tnguyen", draft("prd", "front", prdInputs()))
		h.waitStatus(prd.Id, "tnguyen", api.RequestStatusPendingApproval)
		inbox := h.waitInbox("mkeller", 1)
		h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", "mkeller",
			api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove, Comment: strPtr("OK for Black Friday")}, 200, nil)
		h.waitStatus(prd.Id, "tnguyen", api.RequestStatusSucceeded)

		// Cancelling a request that waits for approval ends its workflow.
		waiting := submit("lmartin", draft("prd", "edge", prdInputs()))
		h.waitStatus(waiting.Id, "lmartin", api.RequestStatusPendingApproval)
		h.json("POST", "/api/v1/requests/"+waiting.Id+"/cancel", "lmartin", nil, 202, nil)
		h.waitStatus(waiting.Id, "lmartin", api.RequestStatusCancelled)
		cctx, cancelWait := context.WithTimeout(ctx, 30*time.Second)
		defer cancelWait()
		// It records the cancellation and completes; a timeout here means it
		// is still waiting.
		if err := tc.GetWorkflow(cctx, provision.WorkflowID(waiting.Id), "").Get(cctx, nil); err != nil && !temporal.IsCanceledError(err) {
			t.Fatalf("cancelled request's workflow: %v", err)
		}
		desc, err = tc.DescribeWorkflowExecution(ctx, provision.WorkflowID(waiting.Id), "")
		if err != nil {
			t.Fatal(err)
		}
		if st := desc.WorkflowExecutionInfo.Status; st == enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			t.Fatalf("cancelled request's workflow is still running")
		}
	})
}

// TestRetryAsTheRunCloses: a request reads failed a moment before its run
// closes, longer on a worker under load. A Retry or a rollback in that
// moment replaces the run; it used to return the old one, which ran
// nothing, and left the request queued (#180).
func TestRetryAsTheRunCloses(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	hold := &holdFailed{held: make(chan chan struct{})}
	tc, err := client.Dial(client.Options{HostPort: ts.FrontendHostPort(), Interceptors: []interceptor.ClientInterceptor{hold},
		Logger: sdklog.NewStructuredLogger(slog.New(slog.DiscardHandler))})
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const queue, user = "rvav-closing-e2e", "tnguyen"
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		const web = "prj_web"
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		// It fails in its last job, the IdM join, with its VM built, and its
		// run stays open until let go.
		failing := func() (api.Request, string, chan struct{}) {
			t.Helper()
			h.awx.FailTask = "idm : join realm"
			var r api.Request
			h.json("POST", "/api/v1/requests", user, api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId,
				ProjectId: web, Environment: "dev", Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline", "idm-join"}}},
				202, &r, "Idempotency-Key", uuid.NewString())
			var letGo chan struct{}
			select {
			case letGo = <-hold.held:
			case <-time.After(time.Minute):
				t.Fatalf("request %s didn't fail", r.Number)
			}
			h.awx.FailTask = ""
			failed := h.waitStatus(r.Id, user, api.RequestStatusFailed)
			desc, err := tc.DescribeWorkflowExecution(ctx, provision.WorkflowID(r.Id), "")
			if err != nil {
				t.Fatal(err)
			}
			if st := desc.WorkflowExecutionInfo.Status; st != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
				t.Fatalf("%s's failed run is %s, want still running", r.Number, st)
			}
			return failed, desc.WorkflowExecutionInfo.Execution.RunId, letGo
		}
		ended := func(r api.Request, run string, letGo chan struct{}) {
			t.Helper()
			close(letGo)
			desc, err := tc.DescribeWorkflowExecution(ctx, provision.WorkflowID(r.Id), run)
			if err != nil {
				t.Fatal(err)
			}
			if st := desc.WorkflowExecutionInfo.Status; st != enums.WORKFLOW_EXECUTION_STATUS_TERMINATED {
				t.Fatalf("%s's failed run is %s, want terminated", r.Number, st)
			}
		}

		r, run, letGo := failing()
		h.json("POST", "/api/v1/requests/"+r.Id+"/retry", user, nil, 202, nil)
		h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
		ended(r, run, letGo)

		r, run, letGo = failing()
		dep := (*r.DeploymentIds)[0]
		h.json("POST", "/api/v1/requests/"+r.Id+"/rollback", user, nil, 202, nil)
		deadline := time.Now().Add(time.Minute)
		for {
			var d api.Deployment
			h.json("GET", "/api/v1/deployments/"+dep, user, nil, 200, &d)
			if d.Status == api.DeploymentStatusDeleted {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s's deployment is %s, want deleted", r.Number, d.Status)
			}
			time.Sleep(50 * time.Millisecond)
		}
		ended(r, run, letGo)
	})
}

// TestRetryOrRollbackWhenTheStartErs: a rollback whose start errs leaves
// its deployment failed, to try again. A retry or a rollback whose start
// errs once Temporal took it, such as one that timed out, is under way: it
// used to fail, and set its deployment failed while the run deleted it.
func TestRetryOrRollbackWhenTheStartErs(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	starts := &erringStarts{}
	tc, err := client.Dial(client.Options{HostPort: ts.FrontendHostPort(), Interceptors: []interceptor.ClientInterceptor{starts},
		Logger: sdklog.NewStructuredLogger(slog.New(slog.DiscardHandler))})
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const queue, user, web = "rvav-rollback-start-e2e", "tnguyen", "prj_web"
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		h.awx.FailTask = "idm : join realm"
		var r api.Request
		h.json("POST", "/api/v1/requests", user, api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId,
			ProjectId: web, Environment: "dev", Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline", "idm-join"}}},
			202, &r, "Idempotency-Key", uuid.NewString())
		r = h.waitStatus(r.Id, user, api.RequestStatusFailed)
		dep := (*r.DeploymentIds)[0]
		status := func() api.DeploymentStatus {
			var d api.Deployment
			h.json("GET", "/api/v1/deployments/"+dep, user, nil, 200, &d)
			return d.Status
		}

		starts.mode.Store(errAfterStart)
		h.json("POST", "/api/v1/requests/"+r.Id+"/retry", user, nil, 202, nil)
		starts.mode.Store(0)
		r = h.waitStatus(r.Id, user, api.RequestStatusFailed)

		starts.mode.Store(refuseStart)
		h.json("POST", "/api/v1/requests/"+r.Id+"/rollback", user, nil, 502, nil)
		if st := status(); st != api.DeploymentStatusFailed {
			t.Fatalf("after a rollback Temporal refused, the deployment is %s, want failed", st)
		}

		starts.mode.Store(errAfterStart)
		h.json("POST", "/api/v1/requests/"+r.Id+"/rollback", user, nil, 202, nil)
		deadline := time.Now().Add(time.Minute)
		for st := status(); st != api.DeploymentStatusDeleted; st = status() {
			if st != api.DeploymentStatusDeleting {
				t.Fatalf("while its rollback runs, the deployment is %s, want deleting", st)
			}
			if time.Now().After(deadline) {
				t.Fatal("the deployment wasn't deleted")
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

const (
	refuseStart int32 = iota + 1
	errAfterStart
)

// erringStarts makes starts err: refused, or once Temporal started the
// run.
type erringStarts struct {
	interceptor.InterceptorBase
	mode atomic.Int32
}

func (e *erringStarts) InterceptClient(next interceptor.ClientOutboundInterceptor) interceptor.ClientOutboundInterceptor {
	return &erringStart{ClientOutboundInterceptorBase: interceptor.ClientOutboundInterceptorBase{Next: next}, mode: &e.mode}
}

type erringStart struct {
	interceptor.ClientOutboundInterceptorBase
	mode *atomic.Int32
}

func (e *erringStart) ExecuteWorkflow(ctx context.Context, in *interceptor.ClientExecuteWorkflowInput) (client.WorkflowRun, error) {
	switch e.mode.Load() {
	case refuseStart:
		return nil, errors.New("connection refused")
	case errAfterStart:
		if _, err := e.Next.ExecuteWorkflow(ctx, in); err != nil {
			return nil, err
		}
		return nil, context.DeadlineExceeded
	}
	return e.Next.ExecuteWorkflow(ctx, in)
}

// holdFailed holds a failed request's Finish once it has recorded the
// outcome, until the test lets it go: the request reads failed while its
// run is still open.
type holdFailed struct {
	interceptor.InterceptorBase
	held chan chan struct{}
}

func (h *holdFailed) InterceptActivity(_ context.Context, next interceptor.ActivityInboundInterceptor) interceptor.ActivityInboundInterceptor {
	return &heldFinish{ActivityInboundInterceptorBase: interceptor.ActivityInboundInterceptorBase{Next: next}, held: h.held}
}

type heldFinish struct {
	interceptor.ActivityInboundInterceptorBase
	held chan chan struct{}
}

func (a *heldFinish) ExecuteActivity(ctx context.Context, in *interceptor.ExecuteActivityInput) (any, error) {
	out, err := a.Next.ExecuteActivity(ctx, in)
	var f provision.FinishInput
	if len(in.Args) == 1 {
		f, _ = in.Args[0].(provision.FinishInput)
	}
	if f.Outcome == "failed" && err == nil {
		letGo := make(chan struct{})
		select {
		case a.held <- letGo:
			select {
			case <-letGo:
			case <-ctx.Done():
			}
		case <-ctx.Done():
		}
	}
	return out, err
}

// startTemporal starts a Temporal dev server for the test.
func startTemporal(t *testing.T, extraArgs ...string) *testsuite.DevServer {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cache, "rvav", "temporal-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var out io.Writer = io.Discard
	if os.Getenv("RVAV_TEST_VERBOSE") != "" {
		out = os.Stderr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ts, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		CachedDownload: testsuite.CachedDownload{DestDir: dir},
		ClientOptions:  &client.Options{Logger: sdklog.NewStructuredLogger(slog.New(slog.NewTextHandler(out, nil)))},
		LogLevel:       "error",
		ExtraArgs:      extraArgs,
		Stdout:         out,
		Stderr:         out,
	})
	if err != nil {
		t.Fatalf("start Temporal dev server: %v", err)
	}
	t.Cleanup(func() { _ = ts.Stop() })
	return ts
}

// settle waits until a request's workflow has no task pending, only a timer
// or a signal to wait for, so that a worker can stop. A worker stopping as the
// server hands it a task drops that task, which Temporal gives again only at
// its timeout: an activity's is its 2-minute heartbeat timeout (#125).
func settle(t *testing.T, ctx context.Context, tc client.Client, requestID string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for {
		desc, err := tc.DescribeWorkflowExecution(ctx, provision.WorkflowID(requestID), "")
		if err != nil {
			t.Fatal(err)
		}
		if len(desc.PendingActivities) == 0 && desc.PendingWorkflowTask == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("request %s's workflow has a task pending: %v, %v", requestID, desc.PendingActivities, desc.PendingWorkflowTask)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// recordHistory writes a finished request's workflow history to
// $RVAV_RECORD_HISTORIES/<name>.json, as TestReplayHistories reads it, when
// that names a directory (docs/operations/upgrades.md).
func recordHistory(t *testing.T, ctx context.Context, tc client.Client, requestID, name string) {
	t.Helper()
	recordWorkflow(t, ctx, tc, provision.WorkflowID(requestID), name)
}

// recordWorkflow is recordHistory for any workflow, by its ID.
func recordWorkflow(t *testing.T, ctx context.Context, tc client.Client, workflowID, name string) {
	t.Helper()
	recordRun(t, ctx, tc, workflowID, "", name)
}

// recordRun is recordWorkflow for one of its runs: a run that continued as
// new, the last when runID is empty.
func recordRun(t *testing.T, ctx context.Context, tc client.Client, workflowID, runID, name string) {
	t.Helper()
	dir := os.Getenv("RVAV_RECORD_HISTORIES")
	if dir == "" {
		return
	}
	var h historypb.History
	iter := tc.GetWorkflowHistory(ctx, workflowID, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		ev, err := iter.Next()
		if err != nil {
			t.Fatal(err)
		}
		h.Events = append(h.Events, ev)
	}
	b, err := temporalproto.CustomJSONMarshalOptions{Indent: "  "}.Marshal(&h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitInbox polls a user's approvals until n are pending: the workflow opens
// a level shortly after the request is submitted.
func (h *harness) waitInbox(user string, n int) api.ApprovalPage {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var inbox api.ApprovalPage
		h.json("GET", "/api/v1/approvals", user, nil, 200, &inbox)
		if inbox.Total == n {
			return inbox
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("%s has %d approvals waiting, want %d", user, inbox.Total, n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitStatus polls the request until it has the wanted status.
func (h *harness) waitStatus(id, user string, want api.RequestStatus) api.Request {
	h.t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		var r api.Request
		h.json("GET", "/api/v1/requests/"+id, user, nil, 200, &r)
		if r.Status == want {
			return r
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("request %s is %s, want %s", r.Number, r.Status, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
