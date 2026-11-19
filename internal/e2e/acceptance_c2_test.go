package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
)

// TestC2Acceptance: C2's acceptance, on the Temporal dev server against
// vcsim. The administrator offers a new version of RHEL 9 server beside
// the old one, and gives RHEL 9 servers in Development a lease rule.
// Thomas requests the older version and names its deployment Payroll: it
// is built on that version under the rule's lease, a longer lease is
// refused, an extension past the rule's maximum asks for approval, and no
// other request takes the name. Once a member quota leaves him no vCPU,
// his next request is refused (ADR-0046, ADR-0047, ADR-0048).
func TestC2Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-c2-acceptance"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		const admin, projectAdmin, requester, web = "mkeller", "lmartin", "tnguyen", "prj_web"

		// A new version, offered beside the one before.
		var item api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &item)
		older := item.PublishedVersion.Id
		var newer api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions", admin,
			api.CatalogItemVersionCreate{Changelog: new("Newer.")}, 201, &newer)
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions/"+newer.Id+"/publish", admin,
			api.CatalogItemVersionPublish{KeepPublished: new(true)}, 200, nil)
		var rhel api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, requester, nil, 200, &rhel)
		if rhel.Version.Id != newer.Id || rhel.Versions == nil || len(*rhel.Versions) != 2 || (*rhel.Versions)[1].Id != older {
			t.Fatalf("the catalog offers %s, versions %+v", rhel.Version.Id, rhel.Versions)
		}

		// RHEL 9 servers in Development: 7 days, 14 at most, one extension, 30 days in all.
		rule := api.LeaseRuleWrite{Name: "RHEL 9 in Development", Condition: `item.slug == "rhel9-server"`, Environment: new("dev"),
			Priority: new(20), DefaultDays: new(7), MaxDays: new(14), MaxExtensions: 1, GraceDays: 3, ExpiryAction: api.Delete,
			MaxLifetimeDays: new(30)}
		h.json("POST", "/api/v1/admin/lease-rules", admin, rule, 201, nil)

		request := func(name string, days *int) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: rhel.Id, VersionId: older, ProjectId: web, Environment: "dev",
				DeploymentName: &name, LeaseDays: days, Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", requester, request("Payroll", new(20)), 200, &pv)
		if pv.Valid || len(pv.Errors) != 1 || pv.Errors[0].Message != "Leases under RHEL 9 in Development can be at most 14 days." {
			t.Fatalf("a 20-day lease: %+v", pv.Errors)
		}

		// Thomas requests the older version and names its deployment.
		var r api.Request
		h.json("POST", "/api/v1/requests", requester, request("Payroll", nil), 202, &r, "Idempotency-Key", uuid.NewString())
		built := h.waitStatus(r.Id, requester, api.RequestStatusSucceeded)
		dep := (*built.DeploymentIds)[0]
		var d api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+dep, requester, nil, 200, &d)
		l := d.Lease
		if d.Name != "Payroll" || d.Version.Id != older || deref(l.Policy) != rule.Name || valueOf(l.MaxDays) != 14 ||
			valueOf(l.ExtensionsMax) != 1 || valueOf(l.MaxLifetimeDays) != 30 || l.LifetimeEndsAt == nil || l.ExpiresAt == nil {
			t.Fatalf("the deployment %q on %s, lease %+v", d.Name, d.Version.Id, l)
		}
		if left := time.Until(*l.ExpiresAt); left < 6*24*time.Hour || left > 7*24*time.Hour+time.Hour {
			t.Fatalf("the lease ends in %s, want 7 days", left)
		}

		// An extension past the rule's 14 days, within the lifetime: an approver decides.
		var ext api.Request
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", requester, api.Day2Draft{Action: "extend_lease",
			Inputs: map[string]any{"days": 20}}, 202, &ext, "Idempotency-Key", uuid.NewString())
		if ext.Status != api.RequestStatusPendingApproval {
			t.Fatalf("a 20-day extension = %s", ext.Status)
		}
		h.json("POST", "/api/v1/requests/"+ext.Id+"/cancel", requester, nil, 202, nil)

		// The name is the deployment's: another request can't take it, whatever its case.
		h.json("POST", "/api/v1/requests/preview", requester, request("PAYROLL", nil), 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool {
			return e.Field == "/deployment_name" && deref(e.Code) == "taken"
		}) {
			t.Fatalf("a second Payroll: %+v", pv.Errors)
		}

		// A member quota of what Thomas holds: his next request is refused.
		var held int
		if err := h.store.Pool.QueryRow(ctx, `select coalesce(sum((r.properties->'vm'->>'cpu_count')::int), 0)
			from resources r join deployments d on d.id = r.deployment_id
			where r.type = 'vsphere.vm' and d.project_id = $1 and d.owner_id = $2 and d.status not in ('provisioning', 'deleted')`,
			web, seed.UserID(requester)).Scan(&held); err != nil || held == 0 {
			t.Fatalf("Thomas holds %d vCPU: %v", held, err)
		}
		h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", projectAdmin, api.UserQuotaWriteList{Items: []api.UserQuotaWrite{
			{Resource: api.Vcpu, Limit: &held, Enforcement: api.UserQuotaWriteEnforcementHard}}}, 200, nil)
		status, body, _ := h.do("POST", "/api/v1/requests", requester, request("Billing", nil), "Idempotency-Key", uuid.NewString())
		var problem struct{ Errors []api.FieldError }
		_ = json.Unmarshal(body, &problem)
		if status != 422 || !slices.ContainsFunc(problem.Errors, func(e api.FieldError) bool {
			return deref(e.Code) == "quota_exceeded" && strings.Contains(e.Message, "but your quota in Web Platform has 0 left")
		}) {
			t.Fatalf("a request past the member quota = %d %s", status, body)
		}

		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?limit=200", admin, nil, 200, &audit)
		for _, a := range []string{"catalog.version.publish", "policy.lease_rule.create", "policy.quota.user.set"} {
			if !slices.ContainsFunc(audit.Items, func(e api.AuditEvent) bool { return e.Action == a }) {
				t.Errorf("not audited: %s", a)
			}
		}
	})
}
