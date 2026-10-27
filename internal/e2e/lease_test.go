package e2e

import (
	"context"
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
