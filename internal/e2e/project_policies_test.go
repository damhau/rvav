package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestProjectPolicies: Web Platform's project admin writes its day-2,
// approval and lease policies, and is refused global ones, another
// project's, and a lease beyond the environment's; a project's policy
// can't loosen a hard global one (G1.3, ADR-0045).
func TestProjectPolicies(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, projectAdmin, otherAdmin, member, colleague = "mkeller", "lmartin", "sbrunner", "tnguyen", "afavre"
		web, data := "prj_web", "prj_data"
		if err := h.store.SetMemberRoles(ctx, web, seed.UserID(colleague), []string{"member"}); err != nil {
			t.Fatal(err)
		}
		day2 := func(user, id string, w api.Day2PolicyWrite, want int) api.Day2Policy {
			t.Helper()
			var out api.Day2Policy
			method, path := "POST", "/api/v1/admin/day2-policies"
			if id != "" {
				method, path = "PUT", path+"/"+id
			}
			h.json(method, path, user, w, want, &out)
			return out
		}
		deny := func(project *string, priority int) api.Day2PolicyWrite {
			return api.Day2PolicyWrite{Name: "No power-off for members", Reason: strPtr("Ask the project admins."), Priority: &priority,
				Actions: &[]string{"power_off"}, Roles: &[]string{"member"}, Effect: api.Day2PolicyWriteEffectDeny, ProjectId: project}
		}

		dataPolicy := day2(admin, "", deny(&data, 5), 201)
		day2(member, "", deny(&web, 5), 403)
		day2(projectAdmin, "", deny(nil, 5), 403)
		day2(projectAdmin, "", deny(&data, 5), 403)
		day2(otherAdmin, "", deny(&web, 5), 403)
		day2(projectAdmin, dataPolicy.Id, deny(&web, 5), 404)
		day2(projectAdmin, "d2p_members", deny(&web, 5), 403)
		h.json("DELETE", "/api/v1/admin/day2-policies/d2p_members", projectAdmin, nil, 403, nil)
		webPolicy := day2(projectAdmin, "", deny(&web, 5), 201)
		if webPolicy.ProjectId == nil || *webPolicy.ProjectId != web {
			t.Fatalf("saved %+v", webPolicy)
		}

		// The global policies and the project's, not another project's.
		var list api.Day2PolicyList
		h.json("GET", "/api/v1/admin/day2-policies", projectAdmin, nil, 200, &list)
		var listed []string
		for _, p := range list.Items {
			listed = append(listed, p.Id)
		}
		if !slices.Contains(listed, "d2p_members") || !slices.Contains(listed, webPolicy.Id) || slices.Contains(listed, dataPolicy.Id) ||
			len(list.LevelRoles) == 0 {
			t.Fatalf("a project admin lists %v", listed)
		}
		h.json("GET", "/api/v1/admin/day2-policies", member, nil, 403, nil)

		// Stricter than the global allow: it decides.
		d := buildVM(h, nil)
		if a := d.actions(member)["power_off"]; a.Enabled || deref(a.DisabledReason) != "Ask the project admins." {
			t.Fatalf("power_off for a member = %+v", a)
		}
		// Looser than the global deny of handing over, and than the approval
		// of deletes in production: the global ones win.
		allow := 15
		day2(projectAdmin, "", api.Day2PolicyWrite{Name: "Members delete", Priority: &allow, Actions: &[]string{"delete"},
			Roles: &[]string{"member"}, Effect: api.Day2PolicyWriteEffectAllow, ProjectId: &web}, 201)
		if a := d.actions(colleague)["delete"]; a.Enabled || !strings.Contains(deref(a.DisabledReason), "Only the owner") {
			t.Fatalf("delete for a colleague = %+v", a)
		}
		var why api.PolicyExplanation
		h.json("POST", "/api/v1/admin/policies/explain", projectAdmin, api.PolicyExplainQuery{ProjectId: web, Environment: "prd",
			Action: strPtr("delete"), AsUserId: strPtr(seed.UserID(colleague))}, 200, &why)
		loosens := func(c api.PolicyConsidered) bool {
			return c.Name == "Members delete" && strings.Contains(c.Why, "can't loosen")
		}
		if why.Day2 == nil || deref(why.Day2.DecidedBy) != "Deleting in production" || !slices.ContainsFunc(why.Day2.Considered, loosens) {
			t.Fatalf("explained %+v", why.Day2)
		}

		// Approval policies.
		approval := func(project *string) api.ApprovalPolicyWrite {
			return api.ApprovalPolicyWrite{Name: "Web production", Condition: `environment == "prd"`, ProjectId: project,
				Levels: []api.ApprovalLevel{{Name: "Web leads", Group: strPtr("rvav-web-admins")}}}
		}
		h.json("POST", "/api/v1/admin/approval-policies", projectAdmin, approval(nil), 403, nil)
		h.json("POST", "/api/v1/admin/approval-policies", projectAdmin, approval(&data), 403, nil)
		var webApproval api.ApprovalPolicy
		h.json("POST", "/api/v1/admin/approval-policies", projectAdmin, approval(&web), 201, &webApproval)
		h.json("DELETE", "/api/v1/admin/approval-policies/"+webApproval.Id, projectAdmin, nil, 204, nil)

		// Leases: no more than the environment's (dev: 30 days, 90 at most, 2 extensions).
		lease := func(user string, project *string, maxDays *int, ext int, action api.LeaseExpiryAction, want int) []string {
			t.Helper()
			var problem struct{ Errors []api.FieldError }
			h.json("PUT", "/api/v1/admin/lease-policies", user, api.LeasePolicy{Environment: "dev", ProjectId: project,
				DefaultDays: new(14), MaxDays: maxDays, MaxExtensions: ext, ExpiryAction: action}, want, &problem)
			var fields []string
			for _, e := range problem.Errors {
				fields = append(fields, e.Field+":"+deref(e.Code))
			}
			return fields
		}
		lease(projectAdmin, nil, new(30), 1, api.PowerOffThenDelete, 403)
		lease(projectAdmin, &data, new(30), 1, api.PowerOffThenDelete, 403)
		if got := lease(projectAdmin, &web, nil, 5, api.NotifyOnly, 422); !slices.Equal(got,
			[]string{"/max_days:looser", "/max_extensions:looser", "/expiry_action:looser"}) {
			t.Fatalf("a looser lease is refused with %v", got)
		}
		lease(projectAdmin, &web, new(60), 1, api.Delete, 200)
		lease(admin, &data, nil, 5, api.NotifyOnly, 200)
		h.json("DELETE", "/api/v1/admin/lease-policies?environment=dev&project_id="+data, projectAdmin, nil, 403, nil)
		h.json("DELETE", "/api/v1/admin/lease-policies?environment=dev&project_id="+web, projectAdmin, nil, 204, nil)

		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?action=policy.&limit=200", admin, nil, 200, &audit)
		byProjectAdmin := map[string]int{}
		for _, e := range audit.Items {
			if e.Actor != nil && e.Actor.Id == seed.UserID(projectAdmin) {
				byProjectAdmin[e.Action]++
			}
		}
		for _, a := range []string{"policy.day2.create", "policy.approval.create", "policy.approval.delete", "policy.lease.set", "policy.lease.delete"} {
			if byProjectAdmin[a] == 0 {
				t.Errorf("not audited: %s (%v)", a, byProjectAdmin)
			}
		}
	})
}
