package e2e

import (
	"context"
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

// TestApproversByRole: the supervisor sees and approves, and approval levels
// that name project roles ask their holders in the request's project when
// they open, given the role themselves or through a group, in mode any and
// all, for catalog requests, day-2 policies and lease extensions; a level
// nobody may decide is rejected at once, saying why (ADR-0041).
func TestApproversByRole(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-approvers-e2e"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		const admin, web = "mkeller", "prj_web"

		// Claire is Web Platform's supervisor; Alain is one through a group.
		h.json("PUT", "/api/v1/projects/"+web+"/members/"+seed.UserID("cdubois"), admin,
			api.ProjectMemberWrite{Roles: &[]string{"supervisor"}}, 200, nil)
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "rvav-web-supervisors",
			ProjectId: new(web), Role: "supervisor"}, 201, nil)
		if err := h.store.SetUserGroups(ctx, seed.UserID("afavre"), []string{"rvav-core", "rvav-network", "rvav-web-supervisors"}); err != nil {
			t.Fatal(err)
		}

		// She sees the project and its deployments, and neither requests nor runs day-2 actions.
		var project api.Project
		h.json("GET", "/api/v1/projects/"+web, "cdubois", nil, 200, &project)
		if mine := *project.MyPermissions; !slices.Equal(mine, []string{"approval.project", "project.see"}) {
			t.Fatalf("a supervisor's permissions = %v", mine)
		}
		h.json("GET", "/api/v1/deployments?project_id="+web, "cdubois", nil, 200, nil)

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		draft := func(role string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
				Inputs: map[string]any{"role": role, "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
					"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		}
		submit := func(user string, d api.RequestDraft) api.Request {
			var r api.Request
			h.json("POST", "/api/v1/requests", user, d, 202, &r, "Idempotency-Key", uuid.NewString())
			return r
		}
		status, _, _ := h.do("POST", "/api/v1/requests", "cdubois", draft("sup"), "Idempotency-Key", uuid.NewString())
		if status != 403 {
			t.Fatalf("a supervisor requests: %d", status)
		}
		approve := func(user, id string) api.Approval {
			var a api.Approval
			h.json("POST", "/api/v1/approvals/"+id+"/decision", user, api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove}, 200, &a)
			return a
		}
		policyStep := func(user, requestID string) api.Step {
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+requestID+"/steps", user, nil, 200, &steps)
			return steps.Items[1]
		}
		policy := func(role string, l api.ApprovalLevel) {
			h.json("POST", "/api/v1/admin/approval-policies", admin, api.ApprovalPolicyWrite{Name: "By role " + role,
				Condition: `environment == "dev" && inputs.role == "` + role + `"`, Levels: []api.ApprovalLevel{l}, Priority: new(5)}, 201, nil)
		}

		// A level names roles the projects give, and nothing else.
		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/approval-policies", admin, api.ApprovalPolicyWrite{Name: "Odd", Condition: "true",
			Levels: []api.ApprovalLevel{{Name: "Odd", Roles: &[]string{"supervisor", "auditor", "nobody"}}}}, 422, &refused)
		if len(refused.Errors) != 2 || refused.Errors[0].Field != "/levels/0/roles/1" || refused.Errors[1].Field != "/levels/0/roles/2" {
			t.Fatalf("refused = %+v", refused.Errors)
		}
		var list api.ApprovalPolicyList
		h.json("GET", "/api/v1/admin/approval-policies", admin, nil, 200, &list)
		if !slices.ContainsFunc(list.LevelRoles, func(r api.RoleRef) bool { return r.Key == "supervisor" && r.Name == "Supervisor" }) ||
			slices.ContainsFunc(list.LevelRoles, func(r api.RoleRef) bool { return r.Key == "admin" }) {
			t.Fatalf("the roles a level may name = %+v", list.LevelRoles)
		}

		// ── Supervisors, any of them: named and through a group ──────────────
		policy("sup", api.ApprovalLevel{Name: "Supervisors", Roles: &[]string{"supervisor"}})
		sup := submit("tnguyen", draft("sup"))
		if sup.Status != api.RequestStatusPendingApproval {
			t.Fatalf("a request supervisors approve = %s", sup.Status)
		}
		lvl := h.waitInbox("cdubois", 1).Items[0]
		h.waitInbox("afavre", 1)
		h.waitInbox("lmartin", 0)
		var detail api.Approval
		h.json("GET", "/api/v1/approvals/"+lvl.Id, "cdubois", nil, 200, &detail)
		if detail.Roles == nil || len(*detail.Roles) != 1 || (*detail.Roles)[0].Name != "Supervisor" || !detail.CanDecide ||
			!slices.ContainsFunc(detail.Approvers, func(u api.UserRef) bool { return u.Id == seed.UserID("cdubois") }) ||
			!slices.ContainsFunc(detail.Approvers, func(u api.UserRef) bool { return u.Id == seed.UserID("afavre") }) {
			t.Fatalf("the level = %+v", detail)
		}
		h.waitNotification("cdubois", "Approval needed: ")
		h.waitNotification("afavre", "Approval needed: ")
		if a := approve("afavre", lvl.Id); a.Status != api.ApprovalStatusApproved || *a.Outcome != "Approved by Alain Favre" {
			t.Fatalf("approved through the group = %s %v", a.Status, a.Outcome)
		}
		built := h.waitStatus(sup.Id, "tnguyen", api.RequestStatusSucceeded)
		dep := (*built.DeploymentIds)[0]

		// ── All of them: every user given the role, not a group's members ────
		h.json("PUT", "/api/v1/projects/"+web+"/members/"+seed.UserID("sbrunner"), admin,
			api.ProjectMemberWrite{Roles: &[]string{"supervisor"}}, 200, nil)
		policy("supall", api.ApprovalLevel{Name: "Every supervisor", Roles: &[]string{"supervisor"}, Mode: new(api.ApprovalLevelModeAll)})
		all := submit("tnguyen", draft("supall"))
		al := h.waitInbox("sbrunner", 1).Items[0].Id
		if a := approve("cdubois", al); a.Status != api.ApprovalStatusPending {
			t.Fatalf("mode all after one supervisor = %s", a.Status)
		}
		if a := approve("sbrunner", al); a.Status != api.ApprovalStatusApproved || *a.Outcome != "Approved by Claire Dubois and Sophie Brunner" {
			t.Fatalf("mode all after both = %s %v", a.Status, a.Outcome)
		}
		h.waitStatus(all.Id, "tnguyen", api.RequestStatusSucceeded)

		// ── Nobody holds the role: rejected at once, even when time-outs approve
		policy("view", api.ApprovalLevel{Name: "Viewers", Roles: &[]string{"viewer"}, Timeout: new("1h"),
			OnTimeout: new(api.ApprovalLevelOnTimeoutApprove)})
		none := submit("tnguyen", draft("view"))
		h.waitStatus(none.Id, "tnguyen", api.RequestStatusRejected)
		const nobody = "Rejected automatically: nobody can approve it, as nobody holds Viewer in Web Platform"
		if s := policyStep("tnguyen", none.Id); s.Title != nobody {
			t.Fatalf("no viewer: %q", s.Title)
		}
		if n := h.waitNotification("tnguyen", "Rejected: "); n.Body == nil || *n.Body != nobody {
			t.Fatalf("the requester is told %+v", n)
		}

		// ── Only the requester holds it: the same ────────────────────────────
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "change-board", Name: "Change board",
			Permissions: []string{"project.see"}}, 201, nil)
		h.json("PUT", "/api/v1/projects/"+web+"/members/"+seed.UserID("tnguyen"), admin,
			api.ProjectMemberWrite{Roles: &[]string{"member", "change-board"}}, 200, nil)
		policy("board", api.ApprovalLevel{Name: "Change board", Roles: &[]string{"change-board"}})
		mine := submit("tnguyen", draft("board"))
		h.waitStatus(mine.Id, "tnguyen", api.RequestStatusRejected)
		if s := policyStep("tnguyen", mine.Id); s.Title != "Rejected automatically: nobody can approve it, as only the requester holds Change board in Web Platform" {
			t.Fatalf("only the requester: %q", s.Title)
		}
		h.json("DELETE", "/api/v1/admin/roles/change-board", admin, nil, 409, nil)

		// ── A day-2 policy's level, and a lease extension ────────────────────
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "Snapshots by supervisors",
			Actions: &[]string{"snapshot_create"}, Effect: api.Day2PolicyWriteEffectApproval, ProjectId: new(web), Priority: new(1),
			Levels: &[]api.ApprovalLevel{{Name: "Supervisors", Roles: &[]string{"supervisor"}}}}, 201, nil)
		var actions struct{ Items []api.Day2Action }
		h.json("GET", "/api/v1/deployments/"+dep+"/actions", "cdubois", nil, 200, &actions)
		if i := slices.IndexFunc(actions.Items, func(a api.Day2Action) bool { return a.Key == "power_off" }); i < 0 ||
			actions.Items[i].Enabled || !strings.Contains(deref(actions.Items[i].DisabledReason), "does not allow") {
			t.Fatalf("a supervisor's power off = %+v", actions.Items)
		}
		var vm string
		if err := h.store.Pool.QueryRow(ctx, `select id from resources where deployment_id = $1 and type = 'vsphere.vm'`, dep).Scan(&vm); err != nil {
			t.Fatal(err)
		}
		var snap api.Request
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", "tnguyen", api.Day2Draft{Action: "snapshot_create", ResourceId: &vm,
			Inputs: map[string]any{"name": "before-upgrade"}}, 202, &snap, "Idempotency-Key", uuid.NewString())
		if snap.Status != api.RequestStatusPendingApproval {
			t.Fatalf("a snapshot supervisors approve = %s", snap.Status)
		}
		approve("sbrunner", h.waitInbox("sbrunner", 1).Items[0].Id)
		h.waitStatus(snap.Id, "tnguyen", api.RequestStatusSucceeded)

		var ext api.Request
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", "tnguyen", api.Day2Draft{Action: "extend_lease",
			Inputs: map[string]any{"days": 120}}, 202, &ext, "Idempotency-Key", uuid.NewString())
		lease := h.waitInbox("cdubois", 1).Items[0]
		if lease.Request.Id != ext.Id || lease.Level.Name != "Project approvers" {
			t.Fatalf("the extension = %+v", lease)
		}
		h.waitInbox("lmartin", 1)
		approve("cdubois", lease.Id)
		h.waitStatus(ext.Id, "tnguyen", api.RequestStatusSucceeded)
	})
}

// waitNotification waits for the user's newest notification whose title
// starts so.
func (h *harness) waitNotification(user, title string) api.Notification {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var page api.NotificationPage
		h.json("GET", "/api/v1/me/notifications", user, nil, 200, &page)
		if i := slices.IndexFunc(page.Items, func(n api.Notification) bool { return strings.HasPrefix(n.Title, title) }); i >= 0 {
			return page.Items[i]
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("%s has no notification %q: %+v", user, title, page.Items)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
