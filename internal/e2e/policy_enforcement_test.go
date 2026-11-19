package e2e

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestPolicyEnforcement: a hard lease or day-2 policy refuses or asks for
// approval; a soft one warns the requester, who goes on by accepting the
// warnings, kept on the request and audited (G1.3, ADR-0045).
func TestPolicyEnforcement(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, projectAdmin, member, web = "mkeller", "lmartin", "tnguyen", "prj_web"
		lease := func(user string, project *string, enf api.PolicyEnforcement, want int) {
			t.Helper()
			h.json("PUT", "/api/v1/admin/lease-policies", user, api.LeasePolicy{Environment: "dev", ProjectId: project,
				DefaultDays: new(30), MaxDays: new(90), MaxExtensions: 2, GraceDays: 7, ExpiryAction: api.PowerOffThenDelete,
				Enforcement: &enf}, want, nil)
		}
		codes := func(errs []api.FieldError) map[string]string {
			out := map[string]string{}
			for _, e := range errs {
				out[e.Field] = deref(e.Code)
			}
			return out
		}
		var problem struct{ Errors []api.FieldError }

		// A project admin can't soften the environment's hard lease.
		h.json("PUT", "/api/v1/admin/lease-policies", projectAdmin, api.LeasePolicy{Environment: "dev", ProjectId: new(web),
			DefaultDays: new(30), MaxDays: new(90), MaxExtensions: 2, GraceDays: 7, ExpiryAction: api.PowerOffThenDelete,
			Enforcement: new(api.PolicyEnforcementSoft)}, 422, &problem)
		if codes(problem.Errors)["/enforcement"] != "looser" {
			t.Fatalf("a soft override of a hard lease: %+v", problem.Errors)
		}

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, member, nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, member, nil, 200, &form)
		draft := func(days int, accept bool) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
				LeaseDays: &days, AcceptWarnings: &accept, Inputs: map[string]any{"role": "app", "size": "S",
					"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310",
					"post_config": []string{"linux-baseline"}}}
		}

		// Hard: a 120-day lease in dev, 90 at most, is refused.
		var preview api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", member, draft(120, false), 200, &preview)
		if preview.Valid || preview.Warnings != nil || codes(preview.Errors)["/lease_days"] != "too_long" ||
			preview.Lease == nil || valueOf(preview.Lease.Enforcement) != api.PolicyEnforcementHard {
			t.Fatalf("a hard lease's preview = %+v", preview)
		}
		// Soft: it warns, and goes on once the warning is accepted.
		lease(admin, nil, api.PolicyEnforcementSoft, 200)
		h.json("POST", "/api/v1/requests/preview", member, draft(120, false), 200, &preview)
		if !preview.Valid || preview.Warnings == nil || len(*preview.Warnings) != 1 || valueOf(preview.Lease.Enforcement) != api.PolicyEnforcementSoft ||
			(*preview.Warnings)[0].Message != "Leases in Development should be at most 90 days." {
			t.Fatalf("a soft lease's preview = %+v", preview)
		}
		h.json("POST", "/api/v1/requests", member, draft(120, false), 422, &problem, "Idempotency-Key", uuid.NewString())
		if codes(problem.Errors)["/accept_warnings"] != "warning" {
			t.Fatalf("not accepted: %+v", problem.Errors)
		}
		var built api.Request
		h.json("POST", "/api/v1/requests", member, draft(120, true), 202, &built, "Idempotency-Key", uuid.NewString())
		if built.Warnings == nil || (*built.Warnings)[0].Policy != "Lease in Development" {
			t.Fatalf("the request keeps no warning: %+v", built.Warnings)
		}
		h.runWorkflow(built.Id, nil)
		d := &vmDeployment{h: h, id: (*built.DeploymentIds)[0], built: built, viewer: member}
		d.resource = *d.actions(member)["power_off"].ResourceId
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+d.id, member, nil, 200, &dep)
		if valueOf(dep.Lease.Enforcement) != api.PolicyEnforcementSoft {
			t.Fatalf("a deployment under a soft lease says %+v", dep.Lease)
		}

		// A soft lease's extension beyond it warns instead of asking the project's approvers.
		extend := func(days int, accept bool, want int) api.Request {
			var r api.Request
			var out any = &problem
			if want == 202 {
				out = &r
			}
			h.json("POST", "/api/v1/deployments/"+d.id+"/actions", member, api.Day2Draft{Action: "extend_lease",
				Inputs: map[string]any{"days": days}, AcceptWarnings: &accept}, want, out, "Idempotency-Key", uuid.NewString())
			return r
		}
		extend(120, false, 422)
		if codes(problem.Errors)["/accept_warnings"] != "warning" || problem.Errors[0].Message != "Longer than the 90 days the lease policy allows." {
			t.Fatalf("a soft lease's extension: %+v", problem.Errors)
		}
		if r := extend(120, true, 202); r.Status == api.RequestStatusPendingApproval || r.Warnings == nil {
			t.Fatalf("an accepted extension = %s, warnings %+v", r.Status, r.Warnings)
		} else {
			h.runWorkflow(r.Id, nil)
			h.waitStatus(r.Id, member, api.RequestStatusSucceeded)
		}
		// Day-2: soft only on a deny or an approval policy.
		pol := api.Day2PolicyWrite{Name: "Power-off in office hours", Reason: new("Tell the team before you power it off."),
			Priority: new(5), Actions: &[]string{"power_off"}, Roles: &[]string{"owner"}, Effect: api.Day2PolicyWriteEffectAllow,
			ProjectId: new(web), Enforcement: new(api.PolicyEnforcementSoft)}
		h.json("POST", "/api/v1/admin/day2-policies", projectAdmin, pol, 422, &problem)
		if codes(problem.Errors)["/enforcement"] != "invalid" {
			t.Fatalf("a soft allow: %+v", problem.Errors)
		}
		pol.Effect, pol.Enforcement = api.Day2PolicyWriteEffectDeny, new(api.PolicyEnforcementHard)
		var saved api.Day2Policy
		h.json("POST", "/api/v1/admin/day2-policies", projectAdmin, pol, 201, &saved)
		if a := d.actions(member)["power_off"]; a.Enabled || a.Warnings != nil {
			t.Fatalf("power_off under a hard deny = %+v", a)
		}
		d.run(member, "power_off", nil, 403)

		// Soft: it warns, and runs once the warning is accepted.
		pol.Enforcement = new(api.PolicyEnforcementSoft)
		h.json("PUT", "/api/v1/admin/day2-policies/"+saved.Id, projectAdmin, pol, 200, nil)
		a := d.actions(member)["power_off"]
		if !a.Enabled || a.Warnings == nil || (*a.Warnings)[0].Message != "Tell the team before you power it off." {
			t.Fatalf("power_off under a soft deny = %+v", a)
		}
		d.run(member, "power_off", nil, 422)
		r := runAccepted(d, member, "power_off")
		if r.Warnings == nil || (*r.Warnings)[0].Policy != "Power-off in office hours" {
			t.Fatalf("the request keeps no warning: %+v", r)
		}
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, member, api.RequestStatusSucceeded)
		var why api.PolicyExplanation
		h.json("POST", "/api/v1/admin/policies/explain", projectAdmin, api.PolicyExplainQuery{ProjectId: web, Environment: "dev",
			Action: new("power_off"), AsUserId: new(d.built.Requester.Id)}, 200, &why)
		if why.Day2 == nil || deref(why.Day2.DecidedBy) != pol.Name || deref(why.Day2.Outcome) !=
			"Power off runs once the requester accepts a warning: Tell the team before you power it off." {
			t.Fatalf("explained %+v", why.Day2)
		}

		// Hard again: the project's approvers are asked.
		lease(admin, nil, api.PolicyEnforcementHard, 200)
		dep = api.DeploymentDetail{}
		h.json("GET", "/api/v1/deployments/"+d.id, member, nil, 200, &dep)
		if valueOf(dep.Lease.Enforcement) != api.PolicyEnforcementHard {
			t.Fatalf("a deployment under a hard lease says %+v", dep.Lease)
		}
		if r := extend(120, false, 202); r.Status != api.RequestStatusPendingApproval {
			t.Fatalf("a hard lease's extension = %s", r.Status)
		}

		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?action=request.warnings.accept&limit=50", admin, nil, 200, &audit)
		if len(audit.Items) != 3 {
			t.Fatalf("accepted warnings audited %d times, want 3", len(audit.Items))
		}
	})
}

func runAccepted(d *vmDeployment, user, action string) api.Request {
	d.h.t.Helper()
	var r api.Request
	d.h.json("POST", "/api/v1/deployments/"+d.id+"/actions", user, api.Day2Draft{Action: action, ResourceId: &d.resource,
		Inputs: map[string]any{}, AcceptWarnings: new(true)}, 202, &r, "Idempotency-Key", uuid.NewString())
	return r
}
