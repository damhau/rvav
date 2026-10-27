package e2e

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
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

// startTemporal starts a Temporal dev server for the test.
func startTemporal(t *testing.T) *testsuite.DevServer {
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
		Stdout:         out,
		Stderr:         out,
	})
	if err != nil {
		t.Fatalf("start Temporal dev server: %v", err)
	}
	t.Cleanup(func() { _ = ts.Stop() })
	return ts
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
