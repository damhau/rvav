package e2e

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/client"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
)

// TestLeasesAndQuotasAcceptance is the acceptance scenario of slice 5, run
// through a real Temporal server and worker, with the housekeeping clock
// moved on: a dev VM with a 1-day lease gets a reminder, is powered off at
// expiry and deleted after the grace period; an extension beyond the
// maximum asks for approval; a request over a hard quota is refused with a
// clear message.
func TestLeasesAndQuotasAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-s5-acceptance"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		var shift atomic.Int64
		h.acts.Housekeeper = h.svc
		h.acts.Now = func() time.Time { return time.Now().Add(time.Duration(shift.Load())) }
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		housekeeping := func(after time.Duration) {
			t.Helper()
			shift.Store(int64(after))
			run, err := tc.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "housekeeping-" + uuid.NewString(),
				TaskQueue: taskQueue}, provision.Housekeeping)
			if err != nil {
				t.Fatal(err)
			}
			if err := run.Get(ctx, nil); err != nil {
				t.Fatalf("housekeeping: %v", err)
			}
		}
		lastRun := func(dep, action string) api.Request {
			t.Helper()
			var hist api.RequestPage
			h.json("GET", "/api/v1/deployments/"+dep+"/history", "tnguyen", nil, 200, &hist)
			for _, r := range hist.Items {
				if r.Action != nil && r.Action.Key == action {
					return h.waitStatus(r.Id, "tnguyen", api.RequestStatusSucceeded)
				}
			}
			t.Fatalf("no %s on %s", action, dep)
			return api.Request{}
		}

		// A dev VM with a 1-day lease.
		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		one := 1
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			LeaseDays: &one, Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		var built api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &built, "Idempotency-Key", uuid.NewString())
		built = h.waitStatus(built.Id, "tnguyen", api.RequestStatusSucceeded)
		dep := (*built.DeploymentIds)[0]
		deployment := func() api.Deployment {
			var d api.Deployment
			h.json("GET", "/api/v1/deployments/"+dep, "tnguyen", nil, 200, &d)
			return d
		}

		// Over a hard quota: refused, saying what is left.
		if _, err := h.store.Pool.Exec(ctx, `insert into quotas (project_id, environment, resource, limit_value, enforcement)
			values ('prj_web', 'dev', 'vm_count', 1, 'hard')`); err != nil {
			t.Fatal(err)
		}
		status, body, _ := h.do("POST", "/api/v1/requests", "tnguyen", draft, "Idempotency-Key", uuid.NewString())
		if status != 422 || !strings.Contains(string(body), "This request needs 1 VM but Web Platform in Development has 0 left") {
			t.Fatalf("over a hard quota: %d %s", status, body)
		}

		// An extension beyond the 90-day maximum waits for the project's admins.
		var ext api.Request
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", "tnguyen", api.Day2Draft{Action: "extend_lease",
			Inputs: map[string]any{"days": 120}}, 202, &ext, "Idempotency-Key", uuid.NewString())
		if ext.Status != api.RequestStatusPendingApproval {
			t.Fatalf("a 120-day extension = %s", ext.Status)
		}
		inbox := h.waitInbox("lmartin", 1)
		if inbox.Items[0].Request.Id != ext.Id {
			t.Fatalf("lmartin's inbox = %+v", inbox.Items)
		}
		h.json("POST", "/api/v1/requests/"+ext.Id+"/cancel", "tnguyen", nil, 202, nil)
		h.waitStatus(ext.Id, "tnguyen", api.RequestStatusCancelled)

		// A reminder, once less than a day is left.
		housekeeping(time.Hour)
		var n int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from events where type = 'notification'
			and 'user:usr_tnguyen' = any(topics) and payload->'data'->>'title' like '% ends within a day'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%d reminders (%v)", n, err)
		}

		// At expiry, the VM is powered off; the deployment waits out its grace period.
		housekeeping(25 * time.Hour)
		lastRun(dep, "lease_expire")
		d := deployment()
		if d.VmsPoweredOn != 0 || d.Lease.ExpiredAt == nil || d.Status != api.DeploymentStatusReady {
			t.Fatalf("after expiry: %d on, lease %+v, %s", d.VmsPoweredOn, d.Lease, d.Status)
		}
		housekeeping(3 * 24 * time.Hour)
		if d := deployment(); d.Status != api.DeploymentStatusReady {
			t.Fatalf("in the grace period: %s", d.Status)
		}

		// After the 7-day grace period, it is deleted, with its IP and DNS records.
		housekeeping(9 * 24 * time.Hour)
		lastRun(dep, "delete")
		var after struct {
			Status    api.DeploymentStatus
			Resources []any
		}
		h.json("GET", "/api/v1/deployments/"+dep, "tnguyen", nil, 200, &after)
		if after.Status != api.DeploymentStatusDeleted || len(after.Resources) != 0 {
			t.Fatalf("after the grace period: %s, %d resources", after.Status, len(after.Resources))
		}
	})
}
