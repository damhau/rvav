package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestApproverEditsCountForTheOwner checks an approver's edit against the
// quotas and deployment limits of the request's owner, for what it adds to
// what the request holds already (ADR-0047).
func TestApproverEditsCountForTheOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin, padmin, approver = "prj_web", "mkeller", "lmartin", "mkeller"
		vcpu := buildVM(h, simulator.VPX()).vm().CpuCount
		if _, err := h.store.Pool.Exec(ctx, `insert into approval_policies (id, name, reason, condition, item_type, levels, priority)
			values ('pol_edit', 'Edits', 'Test', 'inputs.role == "edit"', 'vm_blueprint',
			'[{"name": "Infra", "approvers": ["usr_mkeller"], "mode": "any"}]', 10)`); err != nil {
			t.Fatal(err)
		}
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, padmin, nil, 200, &form)
		forThomas := func(size string) api.Approval {
			t.Helper()
			d := api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
				OnBehalfOf: new(seed.UserID("tnguyen")), Inputs: map[string]any{"role": "edit", "size": size, "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
			var r api.Request
			h.json("POST", "/api/v1/requests", padmin, d, 202, &r, "Idempotency-Key", uuid.NewString())
			// Stands for the workflow's policy step, which opens the level.
			if _, err := h.store.Pool.Exec(ctx, `update approvals set status = 'pending', opened_at = now() where request_id = $1`, r.Id); err != nil {
				t.Fatal(err)
			}
			var lvl api.Approval
			h.json("GET", "/api/v1/approvals/"+h.waitInbox(approver, 1).Items[0].Id, approver, nil, 200, &lvl)
			return lvl
		}
		resize := func(lvl api.Approval, size string) (int, string) {
			code, body, _ := h.do("POST", "/api/v1/approvals/"+lvl.Id+"/decision", approver, api.ApprovalDecision{
				Decision: api.ApprovalDecisionDecisionApprove, Comment: strPtr("resized"), Inputs: &map[string]any{"size": size}})
			return code, string(body)
		}
		memberQuota := func(n int) {
			h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", admin, api.UserQuotaWriteList{Items: []api.UserQuotaWrite{
				{Resource: api.Vcpu, Limit: new(n), Enforcement: api.UserQuotaWriteEnforcementHard}}}, 200, nil)
		}

		// Léa asks for 2 vCPU for Thomas, which fills his quota: the
		// approver sees it, and can't make it 4.
		memberQuota(vcpu + 2)
		lvl := forThomas("S")
		want := fmt.Sprintf("%d / %d vCPU (Thomas Nguyen's quota in Web Platform)", vcpu+2, vcpu+2)
		var after string
		for _, f := range lvl.Facts {
			if f.Label == "Quota after" {
				after = f.Value
			}
		}
		if !strings.HasPrefix(after, want) {
			t.Fatalf("quota after = %q, want %q first", after, want)
		}
		if code, body := resize(lvl, "M"); code != 422 ||
			!strings.Contains(body, "This change needs 2 vCPU but Thomas Nguyen's quota in Web Platform has 0 left.") {
			t.Fatalf("an edit past Thomas's quota = %d %s", code, body)
		}
		// With 2 more, it fits: the request's own 2 count once.
		memberQuota(vcpu + 4)
		if code, body := resize(lvl, "M"); code != 200 {
			t.Fatalf("an edit within Thomas's quota = %d %s", code, body)
		}
		h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", admin, api.UserQuotaWriteList{}, 200, nil)

		// A limit set since: an edit may make the request smaller, not larger.
		lvl = forThomas("M")
		h.json("POST", "/api/v1/admin/deployment-limit-policies", admin, api.DeploymentLimitPolicyWrite{Name: "One vCPU",
			VcpuPerDeployment: new(1)}, 201, nil)
		if code, body := resize(lvl, "L"); code != 422 || !strings.Contains(body,
			"Deployments in Web Platform may have at most 1 vCPU under One vCPU, and this one would have 8.") {
			t.Fatalf("an edit past a vCPU limit = %d %s", code, body)
		}
		if code, body := resize(lvl, "S"); code != 200 {
			t.Fatalf("an edit that makes it smaller = %d %s", code, body)
		}
	})
}
