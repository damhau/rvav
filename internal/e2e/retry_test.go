package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/provision"
)

// TestRetryAndRollback retries a request that failed in its last AWX job
// (what was built stays), and rolls back one that failed, then retries it
// from the start.
func TestRetryAndRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		submit := func() api.Request {
			var r api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId,
				ProjectId: web, Environment: "dev", Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline", "idm-join"}}},
				202, &r, "Idempotency-Key", uuid.NewString())
			return r
		}
		steps := func(id string) map[string]api.Step {
			var out struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+id+"/steps", "tnguyen", nil, 200, &out)
			m := map[string]api.Step{}
			for _, s := range out.Items {
				m[s.Key] = s
			}
			return m
		}
		deployment := func(id string) api.Deployment {
			var d api.Deployment
			h.json("GET", "/api/v1/deployments/"+id, "tnguyen", nil, 200, &d)
			return d
		}
		rollback := func(id string) {
			var s testsuite.WorkflowTestSuite
			s.SetLogger(nil)
			env := s.NewTestWorkflowEnvironment()
			env.RegisterActivity(h.acts)
			env.SetTestTimeout(time.Minute)
			env.ExecuteWorkflow(provision.RollbackWorkflow, id)
			if err := env.GetWorkflowError(); err != nil {
				t.Fatalf("rollback workflow: %v", err)
			}
		}

		// The IdM join fails; everything before it is done.
		h.awx.FailTask = "idm : join realm"
		r := submit()
		h.runWorkflow(r.Id, nil)
		failed := h.waitStatus(r.Id, "tnguyen", api.RequestStatusFailed)
		dep := (*failed.DeploymentIds)[0]
		before := steps(r.Id)
		if before["awx.job:idm-join"].Status != api.StepStatusFailed || before["vsphere.clone"].Status != api.StepStatusDone {
			t.Fatalf("steps after the failure = %+v", before)
		}
		if d := deployment(dep); d.Status != api.DeploymentStatusFailed {
			t.Fatalf("deployment after the failure = %s", d.Status)
		}

		// Retry: only the requester, the owner or a project admin.
		h.json("POST", "/api/v1/requests/"+r.Id+"/retry", "afavre", nil, 404, nil)
		h.awx.FailTask = ""
		var queued api.Request
		h.json("POST", "/api/v1/requests/"+r.Id+"/retry", "tnguyen", nil, 202, &queued)
		if queued.Status != api.RequestStatusQueued || queued.Error != nil {
			t.Fatalf("retried request = %+v", queued)
		}
		if s := steps(r.Id); s["awx.job:idm-join"].Status != api.StepStatusPending || s["vsphere.clone"].Status != api.StepStatusDone {
			t.Fatalf("steps after retry = %+v", s)
		}
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, "tnguyen", api.RequestStatusSucceeded)
		after := steps(r.Id)
		for _, key := range []string{"ipam.reserve", "vsphere.clone", "vsphere.power_on", "awx.job:linux-baseline"} {
			if !after[key].StartedAt.Equal(*before[key].StartedAt) {
				t.Errorf("%s ran again on retry", key)
			}
		}
		if after["awx.job:idm-join"].Status != api.StepStatusDone || deployment(dep).Status != api.DeploymentStatusReady {
			t.Fatalf("after the retry: %s, deployment %s", after["awx.job:idm-join"].Status, deployment(dep).Status)
		}
		h.json("POST", "/api/v1/requests/"+r.Id+"/retry", "tnguyen", nil, 409, nil)
		h.json("POST", "/api/v1/requests/"+r.Id+"/rollback", "tnguyen", nil, 409, nil)

		// Roll back a failed request: what it built is deleted.
		h.awx.FailTask = "idm : join realm"
		r2 := submit()
		h.runWorkflow(r2.Id, nil)
		failed = h.waitStatus(r2.Id, "tnguyen", api.RequestStatusFailed)
		dep2 := (*failed.DeploymentIds)[0]
		var vmMoref string
		var d2 struct {
			Resources []struct {
				Type string
				VM   api.VmProperties `json:"vm"`
			}
		}
		h.json("GET", "/api/v1/deployments/"+dep2, "tnguyen", nil, 200, &d2)
		for _, res := range d2.Resources {
			if res.Type == "vsphere.vm" {
				vmMoref = res.VM.Moref
			}
		}
		h.json("POST", "/api/v1/requests/"+r2.Id+"/rollback", "tnguyen", nil, 202, nil)
		if d := deployment(dep2); d.Status != api.DeploymentStatusDeleting {
			t.Fatalf("deployment while rolling back = %s", d.Status)
		}
		// Not while the rollback runs.
		h.json("POST", "/api/v1/requests/"+r2.Id+"/retry", "tnguyen", nil, 409, nil)
		h.json("POST", "/api/v1/requests/"+r2.Id+"/rollback", "tnguyen", nil, 409, nil)
		rollback(r2.Id)
		if d := deployment(dep2); d.Status != api.DeploymentStatusDeleted {
			t.Fatalf("deployment after rollback = %s", d.Status)
		}
		if _, err := h.vc.GetVM(ctx, vmMoref); err == nil {
			t.Fatal("the VM is still in vCenter")
		}
		// The rollback is the failed request's: its VM's removal events
		// (ADR-0049) are that request's.
		for _, topic := range []string{outbox.ComputeRemovalPre, outbox.ComputeRemovalPost} {
			var moref, deploymentID string
			if err := h.store.Pool.QueryRow(ctx, `select payload->'data'->'machine'->>'moref', deployment_id from integration_events
				where request_id = $1 and topic = $2`, r2.Id, topic).Scan(&moref, &deploymentID); err != nil || moref != vmMoref || deploymentID != dep2 {
				t.Fatalf("%s: %q %q %v", topic, moref, deploymentID, err)
			}
		}
		if s := steps(r2.Id); s["rollback"].Status != api.StepStatusDone {
			t.Fatalf("rollback step = %+v", s["rollback"])
		}
		h.json("POST", "/api/v1/requests/"+r2.Id+"/rollback", "tnguyen", nil, 409, nil)

		// Retried after the rollback, it builds everything again.
		h.awx.FailTask = ""
		h.json("POST", "/api/v1/requests/"+r2.Id+"/retry", "tnguyen", nil, 202, nil)
		s := steps(r2.Id)
		if _, ok := s["rollback"]; ok || s["vsphere.clone"].Status != api.StepStatusPending {
			t.Fatalf("steps after retrying a rolled-back request = %+v", s)
		}
		h.runWorkflow(r2.Id, nil)
		h.waitStatus(r2.Id, "tnguyen", api.RequestStatusSucceeded)
		if d := deployment(dep2); d.Status != api.DeploymentStatusReady {
			t.Fatalf("rebuilt deployment = %s", d.Status)
		}

		// A failed request whose deployment was deleted since (a day-2
		// delete is allowed on failed deployments) is not brought back.
		h.awx.FailTask = "idm : join realm"
		r3 := submit()
		h.runWorkflow(r3.Id, nil)
		failed = h.waitStatus(r3.Id, "tnguyen", api.RequestStatusFailed)
		h.awx.FailTask = ""
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+(*failed.DeploymentIds)[0]+"/actions", "tnguyen",
			api.Day2Draft{Action: "delete", Inputs: map[string]any{}}, 202, &del, "Idempotency-Key", uuid.NewString())
		h.json("POST", "/api/v1/requests/"+r3.Id+"/retry", "tnguyen", nil, 409, nil) // the delete runs
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		h.json("POST", "/api/v1/requests/"+r3.Id+"/retry", "tnguyen", nil, 409, nil)
		h.json("POST", "/api/v1/requests/"+r3.Id+"/rollback", "tnguyen", nil, 409, nil)
	})
}

// TestRetryAfterRollbackCountsAgain retries a request whose rollback freed
// its deployment: it takes its name, its quota and its place in the
// deployment limits again, and is refused when another took them since.
func TestRetryAfterRollbackCountsAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin, limits = "prj_web", "mkeller", "/api/v1/admin/deployment-limit-policies"
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		named := func(name string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
				DeploymentName: &name, Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline", "idm-join"}}}
		}
		submit := func(name string) api.Request {
			var r api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", named(name), 202, &r, "Idempotency-Key", uuid.NewString())
			return r
		}
		retry := func(id string, want int) string {
			t.Helper()
			code, body, _ := h.do("POST", "/api/v1/requests/"+id+"/retry", "tnguyen", nil)
			if code != want {
				t.Fatalf("retry = %d %s, want %d", code, body, want)
			}
			return string(body)
		}
		cancel := func(id string) {
			h.json("POST", "/api/v1/requests/"+id+"/cancel", "tnguyen", nil, 202, nil)
		}

		// shop fails in its IdM join and is rolled back: its name and quota are free.
		h.awx.FailTask = "idm : join realm"
		first := submit("shop")
		h.runWorkflow(first.Id, nil)
		h.waitStatus(first.Id, "tnguyen", api.RequestStatusFailed)
		h.json("POST", "/api/v1/requests/"+first.Id+"/rollback", "tnguyen", nil, 202, nil)
		var s testsuite.WorkflowTestSuite
		s.SetLogger(nil)
		env := s.NewTestWorkflowEnvironment()
		env.RegisterActivity(h.acts)
		env.SetTestTimeout(time.Minute)
		env.ExecuteWorkflow(provision.RollbackWorkflow, first.Id)
		if err := env.GetWorkflowError(); err != nil {
			t.Fatalf("rollback workflow: %v", err)
		}
		h.awx.FailTask = ""

		// One deployment each, and Thomas has another one now.
		var one api.DeploymentLimitPolicy
		h.json("POST", limits, admin, api.DeploymentLimitPolicyWrite{Name: "One each", DeploymentsPerUser: new(1)}, 201, &one)
		other := submit("other")
		if body := retry(first.Id, 409); !strings.Contains(body,
			"Each user may have at most 1 deployment in Web Platform under One each, and you would have 2.") {
			t.Fatalf("retry past one deployment each = %s", body)
		}
		cancel(other.Id)
		h.json("DELETE", limits+"/"+one.Id, admin, nil, 204, nil)

		// A vCPU each.
		h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", admin, api.UserQuotaWriteList{Items: []api.UserQuotaWrite{
			{Resource: api.Vcpu, Limit: new(1), Enforcement: api.UserQuotaWriteEnforcementHard}}}, 200, nil)
		if body := retry(first.Id, 409); !strings.Contains(body, "but your quota in Web Platform has 1 left") {
			t.Fatalf("retry past a vCPU each = %s", body)
		}
		h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", admin, api.UserQuotaWriteList{}, 200, nil)

		// Another deployment took the name shop.
		again := submit("shop")
		if body := retry(first.Id, 409); !strings.Contains(body,
			"Another deployment of Web Platform is named shop since the rollback. Submit a new request instead.") {
			t.Fatalf("retry with its name taken = %s", body)
		}
		cancel(again.Id)

		// Free again: it builds shop.
		retry(first.Id, 202)
		h.runWorkflow(first.Id, nil)
		h.waitStatus(first.Id, "tnguyen", api.RequestStatusSucceeded)
		var live int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from deployments
			where project_id = $1 and lower(name) = 'shop' and status <> 'deleted'`, web).Scan(&live); err != nil {
			t.Fatal(err)
		}
		if live != 1 {
			t.Fatalf("%d live deployments named shop", live)
		}
	})
}
