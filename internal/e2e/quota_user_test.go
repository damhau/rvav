package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestUserAndGlobalQuotas checks each member's quota in a project and rvav's
// own (ADR-0047): every level holds on submit, on behalf of someone and on a
// handover; who may set which; and What applies here.
func TestUserAndGlobalQuotas(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		d := buildVM(h, simulator.VPX())
		vcpu := d.vm().CpuCount
		const web, admin, padmin = "prj_web", "mkeller", "lmartin"
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, padmin, nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: d.built.CatalogItem.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		submit := func(user string, d api.RequestDraft) (int, string) {
			code, body, _ := h.do("POST", "/api/v1/requests", user, d, "Idempotency-Key", uuid.NewString())
			return code, string(body)
		}
		userQuotas := func(user string, items []api.UserQuotaWrite, want int) []api.UserQuota {
			var out api.UserQuotaList
			h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", user, api.UserQuotaWriteList{Items: items}, want, &out)
			return out.Items
		}

		// Each member of Web Platform: one vCPU more than Thomas's VM.
		each := api.UserQuotaWrite{Resource: api.Vcpu, Limit: new(vcpu + 1), Enforcement: api.UserQuotaWriteEnforcementHard}
		if got := userQuotas(admin, []api.UserQuotaWrite{each}, 200); len(got) != 1 || !got[0].SetByAdmin {
			t.Fatalf("an administrator's line = %+v", got)
		}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		var mine *api.QuotaImpact
		for i, l := range pv.Quota {
			if l.Level == api.QuotaLevelProjectUser {
				mine = &pv.Quota[i]
			}
		}
		if pv.Valid || mine == nil || !mine.Exceeds || mine.Used != vcpu || *mine.Limit != vcpu+1 {
			t.Fatalf("preview over each member's quota: valid %v, line %+v", pv.Valid, mine)
		}
		if code, body := submit("tnguyen", draft); code != 422 || !strings.Contains(body, "but your quota in Web Platform has 1 left") {
			t.Fatalf("submit over your quota = %d %s", code, body)
		}
		forThomas := draft
		forThomas.OnBehalfOf = new(seed.UserID("tnguyen"))
		if code, body := submit(padmin, forThomas); code != 422 || !strings.Contains(body, "but Thomas Nguyen's quota in Web Platform has 1 left") {
			t.Fatalf("submit for Thomas over his quota = %d %s", code, body)
		}
		// Léa owns nothing yet: her own request fits, and reserves its vCPU.
		if code, body := submit(padmin, draft); code != 202 {
			t.Fatalf("Léa's request = %d %s", code, body)
		}
		_, body, _ := h.do("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "change_owner",
			Inputs: map[string]any{"owner": seed.UserID(padmin)}}, "Idempotency-Key", uuid.NewString())
		if !strings.Contains(string(body), "Handing it over needs") || !strings.Contains(string(body), "but Léa Martin's quota in Web Platform has 1 left") {
			t.Fatalf("handover over the new owner's quota = %s", body)
		}

		// Project admins write within the project's quota, and leave an administrator's lines alone.
		mem := api.UserQuotaWrite{Resource: api.MemoryGb, Limit: new(1000), Enforcement: api.UserQuotaWriteEnforcementSoft}
		var bad struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/projects/"+web+"/user-quotas", padmin, api.UserQuotaWriteList{Items: []api.UserQuotaWrite{each, mem}}, 422, &bad)
		if len(bad.Errors) != 1 || deref(bad.Errors[0].Code) != "looser" || bad.Errors[0].Field != "/items/1/limit" {
			t.Fatalf("a project admin's limit above the project's = %+v", bad.Errors)
		}
		mem.Limit = new(64)
		got := userQuotas(padmin, []api.UserQuotaWrite{each, mem}, 200)
		if len(got) != 2 || !got[1].SetByAdmin || got[0].SetByAdmin || got[0].Resource != api.MemoryGb {
			t.Fatalf("a project admin's line beside an administrator's = %+v", got)
		}
		userQuotas(padmin, []api.UserQuotaWrite{mem}, 403)
		raised := each
		raised.Limit = new(vcpu + 8)
		userQuotas(padmin, []api.UserQuotaWrite{raised, mem}, 403)
		h.json("GET", "/api/v1/admin/projects/"+web+"/user-quotas", "tnguyen", nil, 403, nil)
		userQuotas(admin, nil, 200)

		// rvav's own quota is administrators' only.
		global := func(user string, items []api.GlobalQuota, want int) {
			h.json("PUT", "/api/v1/admin/quotas", user, api.GlobalQuotaList{Items: items}, want, nil)
		}
		h.json("GET", "/api/v1/admin/quotas", padmin, nil, 403, nil)
		global(padmin, nil, 403)
		global(admin, []api.GlobalQuota{{Resource: api.Vcpu, Limit: new(1), Enforcement: api.GlobalQuotaEnforcementHard}}, 200)
		if code, body := submit("tnguyen", draft); code != 422 || !strings.Contains(body, "but rvav has 0 left. Choose a smaller size or ask an administrator") {
			t.Fatalf("submit over rvav's quota = %d %s", code, body)
		}
		// Each user across rvav, soft: approval.
		global(admin, []api.GlobalQuota{{Resource: api.Vcpu, PerUser: true, Limit: new(vcpu + 1),
			Enforcement: api.GlobalQuotaEnforcementSoft}}, 200)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		if req.Status != api.RequestStatusPendingApproval {
			t.Fatalf("request over each user's soft quota across rvav = %s", req.Status)
		}
		var list api.GlobalQuotaList
		h.json("GET", "/api/v1/admin/quotas", admin, nil, 200, &list)
		if len(list.Items) != 1 || !list.Items[0].PerUser {
			t.Fatalf("rvav's quota = %+v", list.Items)
		}

		// What applies here counts the explained user's.
		var ex api.PolicyExplanation
		h.json("POST", "/api/v1/admin/policies/explain", admin, api.PolicyExplainQuery{ProjectId: web, Environment: "dev",
			AsUserId: new(seed.UserID("tnguyen"))}, 200, &ex)
		found := false
		for _, l := range ex.Quota {
			if l.Level == api.QuotaLevelGlobalUser && l.Resource == api.Vcpu {
				found = l.Used == vcpu && l.Reserved == vcpu && *l.Limit == vcpu+1
			}
		}
		if !found {
			t.Fatalf("explained quota = %+v", ex.Quota)
		}
		var audit struct{ Items []api.AuditEvent }
		h.json("GET", "/api/v1/audit?action=policy.quota.user.set", admin, nil, 200, &audit)
		if len(audit.Items) != 3 {
			t.Fatalf("each member's quota audited %d times, want 3", len(audit.Items))
		}
	})
}
