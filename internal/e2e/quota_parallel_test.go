package e2e

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestQuotasHoldTogether checks that requests asked for at the same time
// can't all take the last of a quota or a deployment limit, and that a
// handover not done yet counts for its new owner (ADR-0047).
func TestQuotasHoldTogether(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin, padmin, limits = "prj_web", "mkeller", "lmartin", "/api/v1/admin/deployment-limit-policies"
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, padmin, nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		together := func(user string, n int) (accepted int, refused []string) {
			var mu sync.Mutex
			var wg sync.WaitGroup
			for range n {
				wg.Go(func() {
					code, body, _ := h.do("POST", "/api/v1/requests", user, draft, "Idempotency-Key", uuid.NewString())
					mu.Lock()
					defer mu.Unlock()
					if code == 202 {
						accepted++
					} else {
						refused = append(refused, string(body))
					}
				})
			}
			wg.Wait()
			return accepted, refused
		}
		userQuota := func(vcpu int) {
			h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", admin, api.UserQuotaWriteList{Items: []api.UserQuotaWrite{
				{Resource: api.Vcpu, Limit: new(vcpu), Enforcement: api.UserQuotaWriteEnforcementHard}}}, 200, nil)
		}

		// One deployment each: of six requests Thomas sends at once, one passes.
		var one api.DeploymentLimitPolicy
		h.json("POST", limits, admin, api.DeploymentLimitPolicyWrite{Name: "One each", DeploymentsPerUser: new(1)}, 201, &one)
		if n, refused := together("tnguyen", 6); n != 1 || len(refused) != 5 || !strings.Contains(refused[0], "limit_exceeded") {
			t.Fatalf("six requests at once under one deployment each: %d accepted, refused %v", n, refused)
		}
		h.json("DELETE", limits+"/"+one.Id, admin, nil, 204, nil)

		// 2 vCPU each: of six 2-vCPU requests Léa sends at once, one passes.
		userQuota(2)
		if n, refused := together(padmin, 6); n != 1 || len(refused) != 5 || !strings.Contains(refused[0], "quota_exceeded") {
			t.Fatalf("six requests at once under 2 vCPU each: %d accepted, refused %v", n, refused)
		}
		h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", admin, api.UserQuotaWriteList{}, 200, nil)

		// Thomas hands a VM over to Léa: until it is done, it counts for her.
		d := buildVM(h, simulator.VPX())
		vcpu := d.vm().CpuCount
		userQuota(2 + vcpu + 1)
		var handover api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "change_owner",
			Inputs: map[string]any{"owner": seed.UserID(padmin)}}, 202, &handover, "Idempotency-Key", uuid.NewString())
		code, body, _ := h.do("POST", "/api/v1/requests", padmin, draft, "Idempotency-Key", uuid.NewString())
		if code != 422 || !strings.Contains(string(body), "but your quota in Web Platform has 1 left") {
			t.Fatalf("Léa's request while a handover to her waits = %d %s", code, body)
		}
		h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", admin, api.UserQuotaWriteList{}, 200, nil)
		h.json("POST", limits, admin, api.DeploymentLimitPolicyWrite{Name: "Two each", DeploymentsPerUser: new(2)}, 201, nil)
		const overTwo = "Each user may have at most 2 deployments in Web Platform under Two each, and you would have 3."
		if code, body, _ := h.do("POST", "/api/v1/requests", padmin, draft, "Idempotency-Key", uuid.NewString()); code != 422 ||
			!strings.Contains(string(body), overTwo) {
			t.Fatalf("Léa's third deployment while a handover to her waits = %d %s", code, body)
		}

		// Once it is done, it counts once, as hers.
		h.runWorkflow(handover.Id, nil)
		h.waitStatus(handover.Id, "tnguyen", api.RequestStatusSucceeded)
		var ex api.PolicyExplanation
		h.json("POST", "/api/v1/admin/policies/explain", admin, api.PolicyExplainQuery{ProjectId: web, Environment: "dev",
			AsUserId: new(seed.UserID(padmin))}, 200, &ex)
		if len(ex.Limits.Lines) != 1 || valueOf(ex.Limits.Lines[0].Used) != 2 {
			t.Fatalf("Léa's deployments once the handover is done = %+v", ex.Limits.Lines)
		}
	})
}
