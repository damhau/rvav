package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestAcceptedWarnings: a requester goes on despite the warnings they saw,
// listed by ID; a warning that appears between the preview and the submit
// refuses the request until they see it too (ADR-0045).
func TestAcceptedWarnings(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, member, web = "mkeller", "tnguyen", "prj_web"
		h.json("PUT", "/api/v1/admin/lease-policies", admin, api.LeasePolicy{Environment: "dev", DefaultDays: new(30),
			MaxDays: new(90), MaxExtensions: 2, GraceDays: 7, ExpiryAction: api.PowerOffThenDelete,
			Enforcement: new(api.PolicyEnforcementSoft)}, 200, nil)
		refused := func(problem struct{ Errors []api.FieldError }) []string {
			t.Helper()
			var ids []string
			for _, e := range problem.Errors {
				if e.Field != "/accept_warnings" || deref(e.Code) != "warning" || e.Warning == nil || e.Warning.Id == "" ||
					e.Warning.Message != e.Message {
					t.Fatalf("a refusal's error = %+v", e)
				}
				ids = append(ids, e.Warning.Id)
			}
			return ids
		}

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, member, nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, member, nil, 200, &form)
		draft := func(days int, ids ...string) api.RequestDraft {
			ids = append([]string{}, ids...)
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
				LeaseDays: &days, AcceptedWarnings: &ids, Inputs: map[string]any{"role": "app", "size": "S",
					"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310",
					"post_config": []string{"linux-baseline"}}}
		}
		var preview api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", member, draft(120), 200, &preview)
		if preview.Warnings == nil || len(*preview.Warnings) != 1 || (*preview.Warnings)[0].Id == "" {
			t.Fatalf("a soft lease's preview warns %+v", preview.Warnings)
		}
		seen := (*preview.Warnings)[0]

		// The warning of a shorter lease isn't the one of a longer one.
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/requests", member, draft(120), 422, &problem, "Idempotency-Key", uuid.NewString())
		if ids := refused(problem); !slices.Equal(ids, []string{seen.Id}) {
			t.Fatalf("an empty list is refused with %v, want %s", ids, seen.Id)
		}
		h.json("POST", "/api/v1/requests", member, draft(120, "w0000"), 422, nil, "Idempotency-Key", uuid.NewString())
		all := draft(120)
		all.AcceptWarnings = new(true)
		h.json("POST", "/api/v1/requests", member, all, 422, nil, "Idempotency-Key", uuid.NewString())
		var built api.Request
		h.json("POST", "/api/v1/requests", member, draft(120, seen.Id), 202, &built, "Idempotency-Key", uuid.NewString())
		if built.Warnings == nil || len(*built.Warnings) != 1 || (*built.Warnings)[0] != seen {
			t.Fatalf("the request keeps %+v, want %+v", built.Warnings, seen)
		}
		h.json("GET", "/api/v1/requests/"+built.Id, member, nil, 200, &built)
		if (*built.Warnings)[0] != seen {
			t.Fatalf("the request reads back %+v", built.Warnings)
		}
		h.runWorkflow(built.Id, nil)
		d := &vmDeployment{h: h, id: (*built.DeploymentIds)[0], built: built, viewer: member}
		d.resource = *d.actions(member)["power_off"].ResourceId

		// A soft deny added after the menu was listed: the action is refused
		// until the requester sees its warning.
		listed := d.actions(member)["power_off"]
		if listed.Warnings != nil {
			t.Fatalf("power_off warns before any policy: %+v", listed.Warnings)
		}
		var pol api.Day2Policy
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "Tell the team", Priority: new(5),
			Reason: new("Tell the team before you power it off."), Actions: &[]string{"power_off", "extend_lease"},
			Effect: api.Day2PolicyWriteEffectDeny, Enforcement: new(api.PolicyEnforcementSoft)}, 201, &pol)
		run := func(action string, inputs map[string]any, ids []string, want int) api.Request {
			t.Helper()
			var r api.Request
			var out any = &problem
			if want == 202 {
				out = &r
			}
			body := api.Day2Draft{Action: action, Inputs: inputs, AcceptedWarnings: &ids}
			if action == "power_off" {
				body.ResourceId = &d.resource
			}
			problem.Errors = nil
			h.json("POST", "/api/v1/deployments/"+d.id+"/actions", member, body, want, out, "Idempotency-Key", uuid.NewString())
			return r
		}
		run("power_off", map[string]any{}, []string{}, 422)
		policyWarning := refused(problem)
		if len(policyWarning) != 1 || problem.Errors[0].Warning.Policy != "Tell the team" {
			t.Fatalf("power_off after a soft deny is refused with %+v", problem.Errors)
		}
		if a := d.actions(member)["power_off"]; a.Warnings == nil || (*a.Warnings)[0].Id != policyWarning[0] {
			t.Fatalf("the menu warns %+v, the refusal %v", a.Warnings, policyWarning)
		}
		r := run("power_off", map[string]any{}, policyWarning, 202)
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, member, api.RequestStatusSucceeded)

		// The menu's warning accepted, an extension past the soft lease adds
		// one: both are needed.
		extend := d.actions(member)["extend_lease"]
		if extend.Warnings == nil || len(*extend.Warnings) != 1 {
			t.Fatalf("extend_lease's menu warns %+v", extend.Warnings)
		}
		fromMenu := []string{(*extend.Warnings)[0].Id}
		run("extend_lease", map[string]any{"days": 120}, fromMenu, 422)
		both := refused(problem)
		if len(both) != 2 || !slices.Contains(both, fromMenu[0]) {
			t.Fatalf("an extension past the lease is refused with %+v", problem.Errors)
		}
		r = run("extend_lease", map[string]any{"days": 120}, both, 202)
		if r.Warnings == nil || len(*r.Warnings) != 2 {
			t.Fatalf("the extension keeps %+v", r.Warnings)
		}
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, member, api.RequestStatusSucceeded)

		// Scripts may still accept whatever warns.
		var scripted api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", member, api.Day2Draft{Action: "extend_lease",
			Inputs: map[string]any{"days": 120}, AcceptWarnings: new(true)}, 202, &scripted, "Idempotency-Key", uuid.NewString())
		if scripted.Warnings == nil || len(*scripted.Warnings) != 2 {
			t.Fatalf("a script's extension keeps %+v", scripted.Warnings)
		}
	})
}
