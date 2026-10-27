package e2e

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
)

// leasedVM builds a dev VM with a lease of the given days, requested by Thomas.
func leasedVM(h *harness, days int) *vmDeployment {
	h.t.Helper()
	const web = "prj_web"
	var item api.CatalogItem
	h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
	var form api.RequestForm
	h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
	var built api.Request
	h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId,
		ProjectId: web, Environment: "dev", LeaseDays: &days, Inputs: map[string]any{"role": "app", "size": "S",
			"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310",
			"post_config": []string{"linux-baseline"}}},
		202, &built, "Idempotency-Key", uuid.NewString())
	h.runWorkflow(built.Id, nil)
	d := &vmDeployment{h: h, id: (*built.DeploymentIds)[0], built: built, viewer: "tnguyen"}
	d.resource = *d.actions("tnguyen")["power_off"].ResourceId
	return d
}

func (d *vmDeployment) lease() api.Lease {
	var dep api.Deployment
	d.h.json("GET", "/api/v1/deployments/"+d.id, d.viewer, nil, 200, &dep)
	return dep.Lease
}

// TestLeaseExtension extends a lease within the policy at once, and beyond
// it through the project's admins.
func TestLeaseExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		d := leasedVM(h, 1)
		l := d.lease()
		if l.ExpiresAt == nil || time.Until(*l.ExpiresAt) > 25*time.Hour || l.MaxDays == nil || *l.MaxDays != 90 ||
			l.ExtensionsMax == nil || *l.ExtensionsMax != 2 || l.GraceDays == nil || *l.GraceDays != 7 || *l.ExpiryAction != api.PowerOffThenDelete {
			t.Fatalf("lease = %+v", l)
		}
		extend := func(user string, days int) api.Request {
			var r api.Request
			h.json("POST", "/api/v1/deployments/"+d.id+"/actions", user, api.Day2Draft{Action: "extend_lease",
				Inputs: map[string]any{"days": days}}, 202, &r, "Idempotency-Key", uuid.NewString())
			return r
		}

		a := d.actions("tnguyen")["extend_lease"]
		if !a.Enabled || a.Approval.Required || a.Form == nil || a.Form.InitialValues["days"] != float64(30) {
			t.Fatalf("extend_lease = %+v", a)
		}
		r := extend("tnguyen", 10)
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, "tnguyen", api.RequestStatusSucceeded)
		if l := d.lease(); time.Until(*l.ExpiresAt) < 9*24*time.Hour || *l.ExtensionsUsed != 1 {
			t.Fatalf("after extending by 10 days: %+v", l)
		}

		// Beyond the 90 days of the dev policy: Léa, the project admin, approves.
		r = extend("tnguyen", 120)
		if r.Status != api.RequestStatusPendingApproval {
			t.Fatalf("a 120-day extension = %s", r.Status)
		}
		h.runWorkflow(r.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				inbox := h.waitInbox("lmartin", 1)
				if inbox.Items[0].Reasons[0] != "Longer than the 90 days allowed without approval" {
					t.Errorf("approval reasons = %v", inbox.Items[0].Reasons)
				}
				h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", "lmartin",
					api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove}, 200, nil)
				h.wf.deliver(env)
			}, time.Hour)
		})
		h.waitStatus(r.Id, "tnguyen", api.RequestStatusSucceeded)
		if l := d.lease(); time.Until(*l.ExpiresAt) < 119*24*time.Hour || *l.ExtensionsUsed != 2 {
			t.Fatalf("after the approved extension: %+v", l)
		}
		// Both extensions are used: the next one needs approval whatever its length.
		if a := d.actions("tnguyen")["extend_lease"]; !a.Approval.Required || a.Approval.Reasons[0] != "All 2 extensions without approval are used" {
			t.Fatalf("extend_lease after two = %+v", a)
		}
		// Léa is Web Platform's only admin: nobody else could approve her own long extension.
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "lmartin", api.Day2Draft{Action: "extend_lease",
			Inputs: map[string]any{"days": 120}}, 409, nil, "Idempotency-Key", uuid.NewString())

		// A project's own policy wins over the environment's.
		if _, err := h.store.Pool.Exec(ctx, `insert into lease_policies (environment, project_id, default_days, max_days,
			max_extensions, expiry_action, grace_days) values ('dev', 'prj_web', 7, 14, 5, 'delete', 2)`); err != nil {
			t.Fatal(err)
		}
		a = d.actions("tnguyen")["extend_lease"]
		if a.Approval.Required || deref(a.Form.UiSchema.Fields["days"].Help) != "Up to 14 days without approval. 2 of 5 extensions used." {
			t.Fatalf("extend_lease under the project's policy = %+v", a)
		}
		if l := d.lease(); *l.MaxDays != 14 || *l.GraceDays != 2 {
			t.Fatalf("lease under the project's policy = %+v", l)
		}
		// Only rvav ends leases.
		if _, ok := d.actions("tnguyen")["lease_expire"]; ok {
			t.Fatal("lease_expire is offered")
		}
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "lmartin", api.Day2Draft{Action: "lease_expire",
			Inputs: map[string]any{}}, 422, nil, "Idempotency-Key", uuid.NewString())
	})
}

// TestLeaseExpiry follows a 1-day dev lease: a reminder, the VMs powered off
// at expiry, the deployment deleted after the grace period; another one,
// extended during its grace period, is kept.
func TestLeaseExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		// The activities' clock follows the sweeps', as on the worker.
		var clock atomic.Int64
		h.acts.Now = func() time.Time { return time.Unix(0, clock.Load()) }
		sweep := func(at time.Time, want int) []string {
			t.Helper()
			clock.Store(at.UnixNano())
			started, err := h.svc.ExpireLeases(ctx, at)
			if err != nil || len(started) != want {
				t.Fatalf("expire leases at %s: %v, %v", at.Format(time.DateTime), started, err)
			}
			for _, id := range started {
				h.runWorkflow(id, nil)
				h.waitStatus(id, "tnguyen", api.RequestStatusSucceeded)
			}
			return started
		}
		reminders := func() int {
			var n int
			if err := h.store.Pool.QueryRow(ctx, `select count(*) from events where type = 'notification'
				and 'user:usr_tnguyen' = any(topics) and payload->'data'->>'title' like 'The lease of %'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}

		d1 := leasedVM(h, 1)
		now := time.Now()
		// Less than a day left: one reminder, not repeated.
		sweep(now.Add(time.Hour), 0)
		sweep(now.Add(2*time.Hour), 0)
		if n := reminders(); n != 1 {
			t.Fatalf("%d reminders", n)
		}

		// An extension waiting for approval doesn't hold the expiry up.
		var waiting api.Request
		h.json("POST", "/api/v1/deployments/"+d1.id+"/actions", "tnguyen", api.Day2Draft{Action: "extend_lease",
			Inputs: map[string]any{"days": 120}}, 202, &waiting, "Idempotency-Key", uuid.NewString())
		if waiting.Status != api.RequestStatusPendingApproval {
			t.Fatalf("a 120-day extension = %s", waiting.Status)
		}

		// Expired: rvav powers the VM off; the grace period starts.
		started := sweep(now.Add(25*time.Hour), 1)
		var r api.Request
		h.json("GET", "/api/v1/requests/"+started[0], "tnguyen", nil, 200, &r)
		if r.Requester.Name != "rvav" || r.Action.Key != "lease_expire" {
			t.Fatalf("expiry request = %+v", r)
		}
		if p := d1.vm().PowerState; p != api.Off {
			t.Fatalf("after expiry the VM is %s", p)
		}
		l := d1.lease()
		if l.ExpiredAt == nil || l.DeletesAt == nil || l.DeletesAt.Sub(*l.ExpiredAt) != 7*24*time.Hour {
			t.Fatalf("lease after expiry = %+v", l)
		}

		// Another one expires too (after its reminder), and its owner extends
		// it during the grace period.
		d2 := leasedVM(h, 1)
		sweep(now.Add(time.Hour), 0)    // d2's reminder; d1 is in its grace period
		sweep(now.Add(25*time.Hour), 1) // d2's expiry
		var ext api.Request
		h.json("POST", "/api/v1/deployments/"+d2.id+"/actions", "tnguyen", api.Day2Draft{Action: "extend_lease",
			Inputs: map[string]any{"days": 30}}, 202, &ext, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(ext.Id, nil)
		h.waitStatus(ext.Id, "tnguyen", api.RequestStatusSucceeded)
		if l := d2.lease(); l.ExpiredAt != nil || l.DeletesAt != nil {
			t.Fatalf("lease extended in its grace period = %+v", l)
		}

		// After the grace period (7 days from the power off), only the first is deleted.
		sweep(now.Add(8*24*time.Hour), 0)
		sweep(now.Add(9*24*time.Hour), 1)
		var dep1, dep2 api.Deployment
		h.json("GET", "/api/v1/deployments/"+d1.id, "tnguyen", nil, 200, &dep1)
		h.json("GET", "/api/v1/deployments/"+d2.id, "tnguyen", nil, 200, &dep2)
		if dep1.Status != api.DeploymentStatusDeleted || dep2.Status != api.DeploymentStatusReady {
			t.Fatalf("after the grace period: %s and %s", dep1.Status, dep2.Status)
		}
		// The extension that still waited was cancelled before the deletion.
		h.json("GET", "/api/v1/requests/"+waiting.Id, "tnguyen", nil, 200, &waiting)
		if waiting.Status != api.RequestStatusCancelled {
			t.Fatalf("the waiting extension is %s", waiting.Status)
		}
		sweep(now.Add(10*24*time.Hour), 0)

		// A lease that ended without its reminder (housekeeping was down) is
		// moved on a day, so its owner hears first.
		d4 := leasedVM(h, 1)
		late := time.Now().Add(30 * time.Hour)
		sweep(late, 0)
		if l := d4.lease(); l.ExpiresAt.Sub(late) < 23*time.Hour || l.ExpiredAt != nil || d4.vm().PowerState != api.On {
			t.Fatalf("lease that ended unannounced = %+v", l)
		}
		sweep(late.Add(time.Hour), 0) // now the reminder

		// A week or less left: the 7-day reminder.
		d3 := leasedVM(h, 5)
		sweep(time.Now(), 0)
		h.acts.Now = nil
		var n int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from events where type = 'notification'
			and payload->'data'->>'title' like '% ends in 5 days'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%d week reminders (%v)", n, err)
		}
		if dep, _ := h.store.GetDeployment(ctx, d3.id); len(dep.LeaseReminded) != 1 || dep.LeaseReminded[0] != "7d" {
			t.Fatalf("reminded = %v", dep.LeaseReminded)
		}
	})
}
