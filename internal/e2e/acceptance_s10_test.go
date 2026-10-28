package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
)

// lockedBuffer collects a subprocess's output.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestExtensibilityAcceptance is the acceptance scenario of slice 10, on a
// real Temporal server: a blocking subscription asks a CMDB webhook before
// provisioning and stops a request the CMDB doesn't know; a composite item
// builds a VM, then its database user and monitoring at the same time, in
// one timeline; the Python decommission workflow of examples/decommission,
// run by its own worker, is offered as a day-2 action on that deployment,
// asks for approval in rvav's inbox and shows its steps (S10.9, ADR-0021
// and ADR-0022). Its days of retention are skipped in its own tests, on
// Temporal's time-skipping server (make test-python); here it keeps none.
func TestExtensibilityAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	uv, err := exec.LookPath("uv")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("uv is needed to run the Python worker")
		}
		t.Skip("uv is not installed: the Python worker can't run")
	}
	ts := startTemporal(t)

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, taskQueue, pyQueue = "mkeller", "rvav-s10-acceptance", "platform-workflows"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		h.acts.TaskQueue = taskQueue
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		// The custom worker: the example, as its README runs it.
		py := exec.CommandContext(ctx, uv, "run", "--frozen", "--", "python", "-m", "decommission.worker")
		py.Dir = filepath.Join("..", "..", "examples", "decommission")
		py.Env = append(os.Environ(), "TEMPORAL_ADDRESS="+ts.FrontendHostPort(), "TEMPORAL_NAMESPACE=default", "TASK_QUEUE="+pyQueue)
		pyOut := &lockedBuffer{}
		py.Stdout, py.Stderr = pyOut, pyOut
		// uv starts Python as its child: both are stopped, as a group.
		py.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		py.Cancel = func() error { return syscall.Kill(-py.Process.Pid, syscall.SIGKILL) }
		py.WaitDelay = 5 * time.Second
		if err := py.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() {
			_ = py.Wait()
			close(exited)
		}()
		t.Cleanup(func() {
			_ = syscall.Kill(-py.Process.Pid, syscall.SIGKILL)
			<-exited
			if t.Failed() {
				t.Logf("the Python worker said:\n%s", pyOut.String())
			}
		})

		// A blocking subscription: the CMDB is asked before anything is built.
		cmdb := &fakeCMDB{}
		srv := httptest.NewServer(cmdb)
		defer srv.Close()
		h.allowWebhooks(srv.URL)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-hook", Kind: api.SecretWriteKindToken,
			Value: new("hook-secret")}, 201, nil)
		var target api.SubscriptionTarget
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+srv.URL+`/rvav", "secret": "cmdb-hook"}}`), &target)
		h.json("POST", "/api/v1/admin/subscriptions", admin, api.SubscriptionWrite{Name: "CMDB check", Topic: api.ProvisioningPre,
			Blocking: new(true), Condition: new(`environment == "dev"`), TimeoutSeconds: new(10), Target: target}, 201, nil)

		steps := func(id string) map[string]api.Step {
			t.Helper()
			var list struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+id+"/steps", "tnguyen", nil, 200, &list)
			out := map[string]api.Step{}
			var walk func([]api.Step)
			walk = func(ss []api.Step) {
				for _, s := range ss {
					out[s.Key] = s
					walk(s.Children)
				}
			}
			walk(list.Items)
			return out
		}

		// A VM for an application the CMDB doesn't know: stopped, nothing built.
		var rhel api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", "tnguyen", nil, 200, &rhel)
		var stopped api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: rhel.Id, VersionId: rhel.Version.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "unknown", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
			202, &stopped, "Idempotency-Key", uuid.NewString())
		stopped = h.waitStatus(stopped.Id, "tnguyen", api.RequestStatusFailed)
		if !strings.Contains(errorOf(stopped), "no application for the role unknown") {
			t.Fatalf("the stopped request's error = %q", errorOf(stopped))
		}
		for key, s := range steps(stopped.Id) {
			if strings.HasPrefix(key, "vsphere.") && s.Status == api.StepStatusDone {
				t.Fatalf("%s ran on a request the CMDB stopped", key)
			}
		}

		// A composite: the VM, then its database user and monitoring at once.
		def := vmWithDBUser()
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "vm-db-user", Name: "VM with a database user",
			Summary: "A VM, its database user and monitoring.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/vm-db-user/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/vm-db-user/entitlements", admin,
			api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		var built api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "db", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "database": "orders", "role_name": "orders_ro"}},
			202, &built, "Idempotency-Key", uuid.NewString())
		built = h.waitStatus(built.Id, "tnguyen", api.RequestStatusSucceeded)
		timeline := steps(built.Id)
		for _, key := range []string{"step:vm", "step:vm/vsphere.clone", "step:vm/awx.job:linux-baseline", "step:db_user",
			"step:monitoring"} {
			if timeline[key].Status != api.StepStatusDone {
				t.Fatalf("%s = %s (steps %v)", key, timeline[key].Status, keysOf(timeline))
			}
		}
		if timeline["step:dba_notice"].Status != api.StepStatusSkipped {
			t.Fatalf("dba_notice = %s", timeline["step:dba_notice"].Status)
		}
		// Both started once the VM was built, before either ended.
		vmDone, db, mon := timeline["step:vm"].FinishedAt, timeline["step:db_user"], timeline["step:monitoring"]
		if vmDone == nil || db.StartedAt == nil || mon.StartedAt == nil || db.StartedAt.Before(*vmDone) ||
			mon.StartedAt.Before(*vmDone) || db.FinishedAt == nil || mon.StartedAt.After(*db.FinishedAt) {
			t.Fatalf("vm done %v; db_user %v-%v; monitoring %v", vmDone, db.StartedAt, db.FinishedAt, mon.StartedAt)
		}
		if built.DeploymentIds == nil || len(*built.DeploymentIds) == 0 {
			t.Fatal("the composite made no deployment")
		}
		deploymentID := (*built.DeploymentIds)[0]
		var props []byte
		_ = h.store.Pool.QueryRow(ctx, `select properties from deployments where id = $1`, deploymentID).Scan(&props)
		if !strings.Contains(string(props), "CI0042") {
			t.Fatalf("the CMDB's properties on the deployment = %s", props)
		}

		// The Python decommission workflow, offered on deployments.
		pollers := func() int {
			resp, err := tc.WorkflowService().DescribeTaskQueue(ctx, &workflowservice.DescribeTaskQueueRequest{Namespace: "default",
				TaskQueue: &taskqueuepb.TaskQueue{Name: pyQueue}, TaskQueueType: enumspb.TASK_QUEUE_TYPE_WORKFLOW})
			if err != nil {
				return 0
			}
			return len(resp.Pollers)
		}
		for deadline := time.Now().Add(3 * time.Minute); pollers() == 0; time.Sleep(200 * time.Millisecond) {
			select {
			case <-exited:
				t.Fatalf("the Python worker exited:\n%s", pyOut.String())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("the Python worker isn't polling %s", pyQueue)
			}
		}
		h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new("decommission"),
			Title: "Decommission an application", WorkflowType: "decommission.v1", TaskQueue: pyQueue,
			InputSchema: &map[string]any{"type": "object", "required": []string{"application"}, "properties": map[string]any{
				"application":    map[string]any{"type": "string", "minLength": 1},
				"retention_days": map[string]any{"type": "integer", "minimum": 0, "default": 3},
			}},
			Usages: []api.WorkflowUsage{api.WorkflowUsageDay2Action},
			Day2: &api.WorkflowDay2{Name: "Decommission", Types: []api.WorkflowDay2Types{api.WorkflowDay2TypesDeployment},
				Offered: new(true)}}, 201, nil)
		var actions struct{ Items []api.Day2Action }
		h.json("GET", "/api/v1/deployments/"+deploymentID+"/actions", "tnguyen", nil, 200, &actions)
		if !slices.ContainsFunc(actions.Items, func(a api.Day2Action) bool { return a.Key == "wf.decommission" }) {
			t.Fatal("the decommission action isn't offered on the composite's deployment")
		}
		var decom api.Request
		h.json("POST", "/api/v1/deployments/"+deploymentID+"/actions", "tnguyen", api.Day2Draft{Action: "wf.decommission",
			Inputs: map[string]any{"application": "orders", "retention_days": 0}}, 202, &decom, "Idempotency-Key", uuid.NewString())

		// Its approval, in the inbox of the group it names.
		inbox := h.waitInbox(admin, 1)
		if !strings.Contains(fmt.Sprint(inbox.Items[0].Level.Name, inbox.Items[0].Reasons), "orders") {
			t.Fatalf("the approval = %+v", inbox.Items[0])
		}
		h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", admin,
			api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove, Comment: new("Orders moved to the new cluster")}, 200, nil)
		decom = h.waitStatus(decom.Id, "tnguyen", api.RequestStatusSucceeded)

		// Its own steps, under its step, with their logs.
		timeline = steps(decom.Id)
		const parent = provision.KeyWorkflow + "decommission"
		var children []string
		for _, ch := range timeline[parent].Children {
			children = append(children, fmt.Sprintf("%s=%s", strings.TrimPrefix(ch.Key, parent+"/"), ch.Status))
		}
		if timeline[parent].Status != api.StepStatusDone || fmt.Sprint(children) != "[freeze=done retention=done archive=done cmdb=done]" {
			t.Fatalf("the decommission's steps = %s %v", timeline[parent].Status, children)
		}
		var log api.LogChunk
		h.json("GET", "/api/v1/requests/"+decom.Id+"/steps/"+timeline[parent+"/archive"].Id+"/log", "tnguyen", nil, 200, &log)
		if len(log.Lines) == 0 || !strings.Contains(log.Lines[0].Text, "orders") {
			t.Fatalf("the archive step's log = %+v", log.Lines)
		}
		var result []byte
		if err := h.store.Pool.QueryRow(ctx, `select result from workflow_runs where request_id = $1`, decom.Id).Scan(&result); err != nil ||
			!strings.Contains(string(result), "Orders moved to the new cluster") {
			t.Fatalf("the run's result = %s (%v)", result, err)
		}
	})
}
