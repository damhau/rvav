package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
)

// TestG1Acceptance: G1's acceptance, on the Temporal dev server against
// vcsim. Web Platform shows its deployments to their owners only. An item
// shared with one group is listed and requested by its members alone; a
// request for it is approved by the project's supervisor, then by the
// requester's manager, and built. Its deployment is seen by its owner and
// those who see all of the project's, not by another member. Last, the
// review's two must-fix cases: a project admin's day-2 approval adds to a
// hard global one, and a project's level with a global level's approvers
// stays apart from it (ADR-0041, ADR-0045).
func TestG1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-g1-acceptance"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		const admin, projectAdmin, requester, colleague, supervisor = "mkeller", "lmartin", "tnguyen", "afavre", "cdubois"
		const web = "prj_web"

		// Web Platform: Claire supervises it, Alain is a member like Thomas,
		// and each member sees only the deployments they own.
		h.json("PUT", "/api/v1/projects/"+web+"/members/"+seed.UserID(supervisor), admin,
			api.ProjectMemberWrite{Roles: &[]string{"supervisor"}}, 200, nil)
		if err := h.store.SetMemberRoles(ctx, web, seed.UserID(colleague), []string{"member"}); err != nil {
			t.Fatal(err)
		}
		h.json("PUT", "/api/v1/projects/"+web+"/deployment-sharing", projectAdmin,
			api.DeploymentSharingWrite{DeploymentSharing: api.OwnerOnly}, 200, nil)

		// An item for the rvav-web group of Web Platform only: Thomas is in it, Alain isn't.
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		var published api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, admin, nil, 200, &published)
		var created api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-linux", Name: "Web Linux VM",
			Summary: "A VM for the web team.", Category: "compute", Type: api.VmBlueprint, Definition: &published.Definition}, 201, &created)
		h.json("POST", "/api/v1/admin/catalog/items/web-linux/versions/"+created.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-linux/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{web},
			Audiences: &[]api.CatalogProjectAudience{{ProjectId: web, UserIds: []string{}, Groups: []string{"rvav-web"},
				Roles: []string{}}}}, 200, nil)
		listed := func(user string) bool {
			t.Helper()
			var page api.CatalogItemPage
			h.json("GET", "/api/v1/catalog/items?limit=200&q=web&project_id="+web, user, nil, 200, &page)
			return slices.ContainsFunc(page.Items, func(i api.CatalogItem) bool { return i.Slug == "web-linux" })
		}
		if !listed(requester) || listed(colleague) {
			t.Fatalf("the group's item is listed for Thomas %v, for Alain %v", listed(requester), listed(colleague))
		}
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/web-linux?project_id="+web, requester, nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/web-linux/form?project_id="+web, requester, nil, 200, &form)
		draft := func(role string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
				Inputs: map[string]any{"role": role, "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
					"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		}
		h.json("POST", "/api/v1/requests", colleague, draft("app"), 403, nil, "Idempotency-Key", uuid.NewString())

		// Its requests are approved by the project's supervisor, then by the requester's manager.
		h.json("POST", "/api/v1/admin/approval-policies", admin, api.ApprovalPolicyWrite{Name: "Web Linux sign-off",
			Reason: strPtr("Web Linux VMs are signed off"), Condition: `item.slug == "web-linux"`, Priority: new(5),
			Levels: []api.ApprovalLevel{{Name: "Supervisor", Roles: &[]string{"supervisor"}}, {Name: "Manager", Manager: new(true)}}},
			201, nil)
		var r api.Request
		h.json("POST", "/api/v1/requests", requester, draft("app"), 202, &r, "Idempotency-Key", uuid.NewString())
		if r.Status != api.RequestStatusPendingApproval {
			t.Fatalf("the request is %s", r.Status)
		}
		approve := func(user string, l api.ApprovalSummary, want string) {
			t.Helper()
			if l.Request.Id != r.Id {
				t.Fatalf("%s's inbox holds %s, want %s", user, l.Request.Id, r.Id)
			}
			var a api.Approval
			h.json("POST", "/api/v1/approvals/"+l.Id+"/decision", user, api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove},
				200, &a)
			if a.Status != api.ApprovalStatusApproved || deref(a.Outcome) != want {
				t.Fatalf("%s's decision = %s %v", user, a.Status, a.Outcome)
			}
		}
		first := h.waitInbox(supervisor, 1).Items[0]
		h.waitInbox(projectAdmin, 0)
		approve(supervisor, first, "Approved by Claire Dubois")
		second := h.waitInbox(projectAdmin, 1).Items[0]
		var level api.Approval
		h.json("GET", "/api/v1/approvals/"+second.Id, projectAdmin, nil, 200, &level)
		if level.Manager == nil || !*level.Manager || len(level.Approvers) != 1 || level.Approvers[0].Id != seed.UserID(projectAdmin) {
			t.Fatalf("the second level = %+v", level)
		}
		approve(projectAdmin, second, "Approved by Léa Martin")
		built := h.waitStatus(r.Id, requester, api.RequestStatusSucceeded)
		dep := (*built.DeploymentIds)[0]

		// Its owner, its project's admin and supervisor see the deployment; Alain doesn't.
		sees := func(user string) bool {
			t.Helper()
			var page api.DeploymentPage
			h.json("GET", "/api/v1/deployments?limit=200&project_id="+web, user, nil, 200, &page)
			return slices.ContainsFunc(page.Items, func(d api.Deployment) bool { return d.Id == dep })
		}
		for _, u := range []string{requester, projectAdmin, supervisor} {
			if !sees(u) {
				t.Errorf("%s doesn't list the deployment", u)
			}
		}
		if sees(colleague) {
			t.Error("Alain lists Thomas's deployment")
		}
		h.json("GET", "/api/v1/deployments/"+dep, colleague, nil, 404, nil)
		h.json("GET", "/api/v1/requests/"+r.Id, colleague, nil, 404, nil)
		h.json("GET", "/api/v1/deployments/"+dep, supervisor, nil, 200, nil)

		// A project admin's day-2 approval asks after a hard global one, not instead.
		var vm string
		if err := h.store.Pool.QueryRow(ctx, `select id from resources where deployment_id = $1 and type = 'vsphere.vm'`, dep).Scan(&vm); err != nil {
			t.Fatal(err)
		}
		var global, project api.Day2Policy
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "Leads approve power-off",
			Reason: strPtr("Infra leads sign off power-offs"), Priority: new(20), Actions: &[]string{"power_off"},
			Roles: &[]string{"owner"}, Effect: api.Day2PolicyWriteEffectApproval,
			Levels: &[]api.ApprovalLevel{{Name: "Infra leads", Group: strPtr("rvav-infra-leads")}}}, 201, &global)
		h.json("POST", "/api/v1/admin/day2-policies", projectAdmin, api.Day2PolicyWrite{Name: "Léa approves power-off",
			Reason: strPtr("Léa signs off"), Priority: new(1), Actions: &[]string{"power_off"}, Roles: &[]string{"owner"},
			Effect: api.Day2PolicyWriteEffectApproval, ProjectId: new(web),
			Levels: &[]api.ApprovalLevel{{Name: "Léa", Approvers: &[]string{seed.UserID(projectAdmin)}, Timeout: strPtr("1s"),
				OnTimeout: new(api.ApprovalLevelOnTimeoutApprove)}}}, 201, &project)
		var off api.Request
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", requester, api.Day2Draft{Action: "power_off", ResourceId: &vm,
			Inputs: map[string]any{}}, 202, &off, "Idempotency-Key", uuid.NewString())
		chain := chainOf(t, h, ctx, off.Id)
		if len(chain) != 2 || chain[0].Group != "rvav-infra-leads" || !slices.Equal(chain[0].Policies, []string{global.Id}) ||
			!slices.Equal(chain[1].Approvers, []string{seed.UserID(projectAdmin)}) || !slices.Equal(chain[1].Policies, []string{project.Id}) {
			t.Fatalf("power_off asks %+v, want the leads, then Léa", chain)
		}
		h.json("POST", "/api/v1/requests/"+off.Id+"/cancel", requester, nil, 202, nil)

		// A project's level with a global level's approvers keeps its timeout and escalation to itself.
		cond := `item.slug == "web-linux" && inputs.role == "cab"`
		var cab, cabToo api.ApprovalPolicy
		h.json("POST", "/api/v1/admin/approval-policies", admin, api.ApprovalPolicyWrite{Name: "CAB", Condition: cond, Priority: new(6),
			Levels: []api.ApprovalLevel{{Name: "CAB", Group: strPtr("rvav-cab")}}}, 201, &cab)
		h.json("POST", "/api/v1/admin/approval-policies", projectAdmin, api.ApprovalPolicyWrite{Name: "CAB too", Condition: cond,
			Priority: new(1), ProjectId: new(web), Levels: []api.ApprovalLevel{{Name: "CAB", Group: strPtr("rvav-cab"),
				Timeout: strPtr("1h"), OnTimeout: new(api.ApprovalLevelOnTimeoutEscalate),
				EscalateTo: &struct {
					Approvers *[]string `json:"approvers,omitempty"`
					Group     *string   `json:"group,omitempty"`
				}{Approvers: &[]string{seed.UserID(projectAdmin)}}}}}, 201, &cabToo)
		var asked api.Request
		h.json("POST", "/api/v1/requests", requester, draft("cab"), 202, &asked, "Idempotency-Key", uuid.NewString())
		chain = chainOf(t, h, ctx, asked.Id)
		i := slices.IndexFunc(chain, func(l chainLevel) bool { return slices.Equal(l.Policies, []string{cab.Id}) })
		j := slices.IndexFunc(chain, func(l chainLevel) bool { return slices.Equal(l.Policies, []string{cabToo.Id}) })
		if i < 0 || j < 0 || chain[i].Group != "rvav-cab" || chain[i].Timeout != nil || len(chain[i].Escalate) != 0 ||
			chain[j].Timeout == nil || *chain[j].Timeout != 3600 || !slices.Equal(chain[j].Escalate, []string{seed.UserID(projectAdmin)}) {
			t.Fatalf("levels %+v, want the global CAB level apart from the project's", chain)
		}
		h.json("POST", "/api/v1/requests/"+asked.Id+"/cancel", requester, nil, 202, nil)

		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?limit=200", admin, nil, 200, &audit)
		for _, a := range []string{"project.sharing.set", "catalog.item.entitlements", "policy.day2.create", "policy.approval.create"} {
			if !slices.ContainsFunc(audit.Items, func(e api.AuditEvent) bool { return strings.HasPrefix(e.Action, a) }) {
				t.Errorf("not audited: %s", a)
			}
		}
	})
}
