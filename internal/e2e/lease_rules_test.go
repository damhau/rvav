package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestLeaseRules: lease rules decide a request's lease by priority before
// the environment's; a project admin's can't give more than what
// administrators set, at save or at request; a condition that can't be
// evaluated applies on submit only when it gives no more; a lifetime caps
// a request and the extensions past it (C2.2, ADR-0048).
func TestLeaseRules(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, projectAdmin, member, web = "mkeller", "lmartin", "tnguyen", "prj_web"
		var problem struct{ Errors []api.FieldError }
		codes := func() map[string]string {
			out := map[string]string{}
			for _, e := range problem.Errors {
				out[e.Field] = deref(e.Code)
			}
			return out
		}
		rule := func(user, id string, w api.LeaseRuleWrite, want int) api.LeaseRule {
			t.Helper()
			var out api.LeaseRule
			method, path := "POST", "/api/v1/admin/lease-rules"
			if id != "" {
				method, path = "PUT", path+"/"+id
			}
			problem.Errors = nil
			var body any = &out
			if want >= 400 {
				body = &problem
			}
			h.json(method, path, user, w, want, body)
			return out
		}

		h.json("POST", "/api/v1/admin/lease-rules", admin, api.LeaseRuleWrite{Name: " ", Condition: "inputs.size ==",
			Environment: new("nowhere"), MaxDays: new(40), MaxLifetimeDays: new(30), ExpiryAction: api.Delete}, 422, &problem)
		if got := codes(); got["/name"] != "required" || got["/condition"] != "invalid" || got["/environment"] != "unknown" ||
			got["/max_days"] != "maximum" {
			t.Fatalf("an invalid rule: %v", got)
		}
		small := api.LeaseRuleWrite{Name: "Small VMs", Condition: `inputs.size == "S"`, Environment: new("dev"), Priority: new(50),
			DefaultDays: new(7), MaxDays: new(14), MaxExtensions: 1, GraceDays: 3, ExpiryAction: api.Delete, MaxLifetimeDays: new(30)}
		rule(member, "", small, 403)
		saved := rule(admin, "", small, 201)
		if !saved.SetByAdmin || saved.Priority != 50 || saved.Enforcement != api.PolicyEnforcementHard {
			t.Fatalf("saved %+v", saved)
		}

		// A project admin's: for their project, and no more than dev's 90 days and 2 extensions.
		mine := api.LeaseRuleWrite{Name: "Web Platform's VMs", Condition: "true", Environment: new("dev"), Priority: new(10), DefaultDays: new(20),
			MaxDays: new(120), MaxExtensions: 2, GraceDays: 7, ExpiryAction: api.PowerOffThenDelete}
		rule(projectAdmin, "", mine, 403)
		mine.ProjectId = new(web)
		rule(projectAdmin, "", mine, 422)
		if codes()["/max_days"] != "looser" || problem.Errors[0].Message != "Development allows 90 days at most." {
			t.Fatalf("a looser rule: %+v", problem.Errors)
		}
		mine.MaxDays = new(60)
		ours := rule(projectAdmin, "", mine, 201)
		if ours.SetByAdmin {
			t.Fatal("a project admin's rule is marked as an administrator's")
		}

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, member, nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, member, nil, 200, &form)
		draft := func(size string, days *int) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
				LeaseDays: days, Inputs: map[string]any{"role": "app", "size": size, "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		}
		preview := func(size string, days *int) api.RequestPreview {
			t.Helper()
			var out api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", member, draft(size, days), 200, &out)
			if out.Lease == nil {
				t.Fatalf("no lease in %+v", out)
			}
			return out
		}
		// Medium VMs: the project's rule, first by priority.
		if l := preview("M", nil).Lease; deref(l.Policy) != mine.Name || *l.DefaultDays != 20 || *l.MaxDays != 60 || l.MaxLifetimeDays != nil {
			t.Fatalf("a medium VM's lease = %+v", l)
		}
		// Small VMs: the project's rule would give more than the administrators' rule, which decides.
		if l := preview("S", nil).Lease; deref(l.Policy) != small.Name || *l.DefaultDays != 7 || *l.MaxDays != 14 ||
			*l.MaxExtensions != 1 || valueOf(l.MaxLifetimeDays) != 30 {
			t.Fatalf("a small VM's lease = %+v", l)
		}
		if p := preview("S", new(20)); p.Valid || len(p.Errors) != 1 || p.Errors[0].Message != "Leases under Small VMs can be at most 14 days." {
			t.Fatalf("a 20-day lease of a small VM: %+v", p.Errors)
		}
		var why api.PolicyExplanation
		h.json("POST", "/api/v1/admin/policies/explain", projectAdmin, api.PolicyExplainQuery{ProjectId: web, Environment: "dev",
			ItemId: &item.Id, Inputs: &map[string]any{"size": "S"}}, 200, &why)
		if deref(why.Lease.DecidedBy) != small.Name || len(why.Lease.Considered) < 3 || why.Lease.Considered[0].Applies ||
			!strings.HasPrefix(why.Lease.Considered[0].Why, "It would decide, but a project admin's rule can't give more than the rule Small VMs") ||
			!why.Lease.Considered[1].Applies {
			t.Fatalf("what applies here: %+v", why.Lease)
		}

		// An administrator's change makes the rule theirs.
		rule(admin, ours.Id, mine, 200)
		rule(projectAdmin, ours.Id, mine, 403)
		h.json("DELETE", "/api/v1/admin/lease-rules/"+ours.Id, projectAdmin, nil, 403, nil)
		h.json("DELETE", "/api/v1/admin/lease-rules/"+ours.Id, admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/lease-rules/"+ours.Id, admin, nil, 404, nil)

		// A condition on an input the item doesn't have: skipped in the
		// preview; on submit, applied when it gives no more than dev's lease.
		tagged := api.LeaseRuleWrite{Name: "Ticketed", Condition: `inputs.ticket != ""`, Priority: new(5), DefaultDays: new(10),
			MaxDays: new(30), GraceDays: 7, ExpiryAction: api.PowerOffThenDelete}
		taggedRule := rule(admin, "", tagged, 201)
		if l := preview("M", nil).Lease; deref(l.Policy) != "Development" {
			t.Fatalf("a failing condition in the preview: %+v", l)
		}
		submitted := func() api.Lease {
			t.Helper()
			var built api.Request
			h.json("POST", "/api/v1/requests", member, draft("M", nil), 202, &built, "Idempotency-Key", uuid.NewString())
			var dep api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+(*built.DeploymentIds)[0], member, nil, 200, &dep)
			return dep.Lease
		}
		if l := submitted(); deref(l.Policy) != tagged.Name || valueOf(l.MaxDays) != 30 || time.Until(*l.ExpiresAt) > 10*24*time.Hour {
			t.Fatalf("a failing condition that gives less, on submit: %+v", l)
		}
		tagged.MaxDays = new(120)
		rule(admin, taggedRule.Id, tagged, 200)
		if l := submitted(); deref(l.Policy) != "Development" {
			t.Fatalf("a failing condition that gives more, on submit: %+v", l)
		}
		h.json("DELETE", "/api/v1/admin/lease-rules/"+taggedRule.Id, admin, nil, 204, nil)

		// A lifetime on a lease policy: a project's override keeps one, no longer.
		dev := api.LeasePolicy{Environment: "dev", DefaultDays: new(30), MaxDays: new(90), MaxExtensions: 2, GraceDays: 7,
			ExpiryAction: api.PowerOffThenDelete, MaxLifetimeDays: new(60)}
		h.json("PUT", "/api/v1/admin/lease-policies", admin, dev, 422, &problem)
		if codes()["/max_days"] != "maximum" {
			t.Fatalf("a lifetime shorter than the maximum: %+v", problem.Errors)
		}
		dev.MaxLifetimeDays = new(180)
		h.json("PUT", "/api/v1/admin/lease-policies", admin, dev, 200, nil)
		override := dev
		override.ProjectId, override.MaxLifetimeDays = new(web), nil
		h.json("PUT", "/api/v1/admin/lease-policies", projectAdmin, override, 422, &problem)
		if codes()["/max_lifetime_days"] != "looser" {
			t.Fatalf("an override without the lifetime: %+v", problem.Errors)
		}
		override.MaxLifetimeDays = new(120)
		h.json("PUT", "/api/v1/admin/lease-policies", projectAdmin, override, 200, nil)
		h.json("DELETE", "/api/v1/admin/lease-policies?environment=dev&project_id="+web, projectAdmin, nil, 204, nil)

		// A small VM's deployment keeps the rule's lease, and its lifetime counts from its creation.
		d := leasedVM(h, 1)
		l := d.lease()
		if deref(l.Policy) != small.Name || valueOf(l.MaxLifetimeDays) != 30 || l.LifetimeEndsAt == nil ||
			time.Until(*l.LifetimeEndsAt) < 29*24*time.Hour || valueOf(l.MaxDays) != 14 || valueOf(l.ExtensionsMax) != 1 {
			t.Fatalf("a small VM's deployment lease = %+v", l)
		}
		if _, err := h.store.Pool.Exec(ctx, `update deployments set created_at = now() - interval '25 days' + interval '1 hour'
			where id = $1`, d.id); err != nil {
			t.Fatal(err)
		}
		a := d.actions(member)["extend_lease"]
		if a.Approval.Required || a.Form.InitialValues["days"] != float64(5) || deref(a.Form.UiSchema.Fields["days"].Help) !=
			"Up to 14 days without approval. 0 of 1 extensions used. 5 days of its 30-day lifetime left." {
			t.Fatalf("extend_lease 25 days in = %+v", a)
		}
		extend := func(days, want int) api.Request {
			t.Helper()
			var r api.Request
			var out any = &r
			if want >= 400 {
				out = &problem
			}
			h.json("POST", "/api/v1/deployments/"+d.id+"/actions", member, api.Day2Draft{Action: "extend_lease",
				Inputs: map[string]any{"days": days}}, want, out, "Idempotency-Key", uuid.NewString())
			return r
		}
		// Soft: past the lifetime warns.
		small.Enforcement = new(api.PolicyEnforcementSoft)
		rule(admin, saved.Id, small, 200)
		extend(10, 422)
		if codes()["/accept_warnings"] != "warning" || problem.Errors[0].Message != "Past the 30-day lifetime the lease policy allows." {
			t.Fatalf("a soft lifetime: %+v", problem.Errors)
		}
		// Hard: past the lifetime asks the project's approvers, though an extension is left.
		small.Enforcement = new(api.PolicyEnforcementHard)
		rule(admin, saved.Id, small, 200)
		if r := extend(10, 202); r.Status != api.RequestStatusPendingApproval {
			t.Fatalf("an extension past the lifetime = %s", r.Status)
		}

		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?action=policy.lease_rule.&limit=200", admin, nil, 200, &audit)
		seen := map[string]int{}
		for _, e := range audit.Items {
			seen[e.Action]++
		}
		if seen["policy.lease_rule.create"] != 3 || seen["policy.lease_rule.update"] != 4 || seen["policy.lease_rule.delete"] != 2 {
			t.Fatalf("audited %v", seen)
		}
	})
}

// TestLeaseRuleCap: a deployment whose lease a project admin's rule
// decided keeps the administrators' rule that held it, so editing the
// project admin's rule later never loosens its lease, and deleting it
// falls back to the administrators' rule, not to the environment's lease
// (ADR-0048).
func TestLeaseRuleCap(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const path = "/api/v1/admin/lease-rules"
		small := api.LeaseRuleWrite{Name: "Small VMs", Condition: `inputs.size == "S"`, Environment: new("dev"), Priority: new(50),
			DefaultDays: new(7), MaxDays: new(14), MaxExtensions: 1, GraceDays: 3, ExpiryAction: api.Delete, MaxLifetimeDays: new(30)}
		h.json("POST", path, "mkeller", small, 201, nil)
		mine := small
		mine.Name, mine.ProjectId, mine.Priority = "Web small VMs", new("prj_web"), new(10)
		var ours api.LeaseRule
		h.json("POST", path, "lmartin", mine, 201, &ours)

		d := leasedVM(h, 7) // size S
		if l := d.lease(); deref(l.Policy) != "Web small VMs" || valueOf(l.MaxDays) != 14 {
			t.Fatalf("lease at first = %+v", l)
		}

		// Dev's lease allows this edit, but the deployment keeps Small VMs' terms.
		loose := mine
		loose.MaxDays, loose.MaxExtensions, loose.GraceDays, loose.MaxLifetimeDays = new(90), 2, 7, nil
		h.json("PUT", path+"/"+ours.Id, "lmartin", loose, 200, nil)
		l := d.lease()
		if deref(l.Policy) != "Web small VMs" || valueOf(l.MaxDays) != 14 || valueOf(l.ExtensionsMax) != 1 || valueOf(l.GraceDays) != 3 ||
			valueOf(l.MaxLifetimeDays) != 30 {
			t.Fatalf("lease after the project admin loosened the rule = %+v", l)
		}
		var r api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "extend_lease",
			Inputs: map[string]any{"days": 60}}, 202, &r, "Idempotency-Key", uuid.NewString())
		if r.Status != api.RequestStatusPendingApproval {
			t.Fatalf("a 60-day extension = %s", r.Status)
		}

		// Her rule gone, Small VMs decides: not Development's 90 days.
		h.json("DELETE", path+"/"+ours.Id, "lmartin", nil, 204, nil)
		if l := d.lease(); deref(l.Policy) != "Small VMs" || valueOf(l.MaxDays) != 14 || valueOf(l.MaxLifetimeDays) != 30 {
			t.Fatalf("lease once the project admin's rule is deleted = %+v", l)
		}
	})
}
