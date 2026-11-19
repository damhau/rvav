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

// TestDeploymentLimits checks deployment limit policies (ADR-0047): who
// writes which, how a project's and a hard global one combine, and that
// they hold on submit, soft or hard, on a handover, a resize and an update.
func TestDeploymentLimits(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		d := buildVM(h, simulator.VPX())
		const web, admin, padmin, path = "prj_web", "mkeller", "lmartin", "/api/v1/admin/deployment-limit-policies"
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, padmin, nil, 200, &form)
		small := map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}
		draft := api.RequestDraft{CatalogItemId: d.built.CatalogItem.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: small}
		submit := func(user string, d api.RequestDraft) (int, string) {
			code, body, _ := h.do("POST", "/api/v1/requests", user, d, "Idempotency-Key", uuid.NewString())
			return code, string(body)
		}
		save := func(user, id string, w api.DeploymentLimitPolicyWrite, want int) api.DeploymentLimitPolicy {
			t.Helper()
			var out api.DeploymentLimitPolicy
			if id == "" {
				h.json("POST", path, user, w, want, &out)
			} else {
				h.json("PUT", path+"/"+id, user, w, want, &out)
			}
			return out
		}
		limitLines := func(lines []api.DeploymentLimitLine) map[api.DeploymentLimitKind]api.DeploymentLimitLine {
			m := map[api.DeploymentLimitKind]api.DeploymentLimitLine{}
			for _, l := range lines {
				m[l.Kind] = l
			}
			return m
		}

		// A project admin writes their project's policies, never a global one.
		save(padmin, "", api.DeploymentLimitPolicyWrite{Name: "Everywhere", VcpuPerDeployment: new(2)}, 403)
		var bad struct{ Errors []api.FieldError }
		h.json("POST", path, admin, api.DeploymentLimitPolicyWrite{Name: "Nothing"}, 422, &bad)
		if len(bad.Errors) != 1 || deref(bad.Errors[0].Code) != "required" {
			t.Fatalf("a policy with no limit = %+v", bad.Errors)
		}
		save(admin, "", api.DeploymentLimitPolicyWrite{Name: "rvav limits", VcpuPerDeployment: new(2), DeploymentsPerProject: new(100)}, 201)
		webw := api.DeploymentLimitPolicyWrite{Name: "Web limits", ProjectId: new(web), Priority: new(10), DeploymentsPerUser: new(1),
			VcpuPerDeployment: new(8)}
		webp := save(padmin, "", webw, 201)
		if webp.SetByAdmin || webp.Priority != 10 || webp.Enforcement != api.PolicyEnforcementHard {
			t.Fatalf("a project admin's policy = %+v", webp)
		}

		// Web limits, tried first, decides deployments per user; the hard global one, stricter, vCPU.
		var ex api.PolicyExplanation
		h.json("POST", "/api/v1/admin/policies/explain", padmin, api.PolicyExplainQuery{ProjectId: web, Environment: "dev",
			AsUserId: new(seed.UserID("tnguyen"))}, 200, &ex)
		explained := map[api.DeploymentLimitKind]api.PolicyLimitLine{}
		for _, l := range ex.Limits.Lines {
			explained[l.Kind] = l
		}
		perUser, cpu, perProject := explained[api.DeploymentsPerUser], explained[api.VcpuPerDeployment], explained[api.DeploymentsPerProject]
		if perUser.Policy != "Web limits" || valueOf(perUser.Used) != 1 || cpu.Policy != "rvav limits" || cpu.Limit != 2 || perProject.Used == nil {
			t.Fatalf("explained limits = %+v", ex.Limits.Lines)
		}
		held := *perProject.Used
		var webWhy string
		for _, c := range ex.Limits.Considered {
			if c.Name == "Web limits" && c.Applies {
				webWhy = c.Why
			}
		}
		if webWhy != "Decides deployments per user. Its vCPU per deployment gives way to the global rvav limits, hard and stricter." {
			t.Fatalf("Web limits considered: %+v", ex.Limits.Considered)
		}

		// Thomas has one deployment in Web Platform: a second is refused.
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		got := limitLines(valueOf(pv.Limits))
		if pv.Valid || !got[api.DeploymentsPerUser].Exceeds || got[api.DeploymentsPerUser].Value != 2 ||
			got[api.DeploymentsPerProject].Value != held+1 || got[api.VcpuPerDeployment].Exceeds || got[api.VcpuPerDeployment].Value != 2 {
			t.Fatalf("preview over each user's deployments: valid %v, limits %+v", pv.Valid, got)
		}
		const thomasOver = "Each user may have at most 1 deployment in Web Platform under Web limits, and you would have 2. " +
			"Delete one, or ask a project admin to raise the limit."
		if code, body := submit("tnguyen", draft); code != 422 || !strings.Contains(body, thomasOver) {
			t.Fatalf("submit over each user's deployments = %d %s", code, body)
		}

		// Soft, it warns: Thomas goes on once he accepts.
		soft := api.PolicyEnforcementSoft
		webw.Enforcement = &soft
		save(padmin, webp.Id, webw, 200)
		if code, body := submit("tnguyen", draft); code != 422 || !strings.Contains(body, `"code":"warning"`) ||
			!strings.Contains(body, "Each user should have at most 1 deployment in Web Platform under Web limits, and you would have 2.") {
			t.Fatalf("submit over a soft limit = %d %s", code, body)
		}
		accepting := draft
		accepting.AcceptWarnings = new(true)
		if code, body := submit("tnguyen", accepting); code != 202 {
			t.Fatalf("submit accepting the warning = %d %s", code, body)
		}
		// Léa has none: hers fits.
		if code, body := submit(padmin, draft); code != 202 {
			t.Fatalf("Léa's request = %d %s", code, body)
		}

		// An administrator takes Web limits over, hard: its project admins leave it alone.
		webw.Enforcement = nil
		if pol := save(admin, webp.Id, webw, 200); !pol.SetByAdmin {
			t.Fatalf("an administrator's policy = %+v", pol)
		}
		save(padmin, webp.Id, webw, 403)
		h.json("DELETE", path+"/"+webp.Id, padmin, nil, 403, nil)

		// Handing over counts for the new owner; a resize, against each deployment's size.
		_, body, _ := h.do("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "change_owner",
			Inputs: map[string]any{"owner": seed.UserID(padmin)}}, "Idempotency-Key", uuid.NewString())
		if !strings.Contains(string(body), "/inputs/owner") || !strings.Contains(string(body),
			"Each user may have at most 1 deployment in Web Platform under Web limits, and Léa Martin would have 2. Delete one, or ask an administrator") {
			t.Fatalf("handover over the new owner's deployments = %s", body)
		}
		_, body, _ = h.do("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "resize", ResourceId: &d.resource,
			Inputs: map[string]any{"cpu_count": 4, "memory_gb": 4, "restart": true}}, "Idempotency-Key", uuid.NewString())
		if !strings.Contains(string(body), "/inputs/cpu_count") || !strings.Contains(string(body),
			"Deployments in Web Platform may have at most 2 vCPU under rvav limits, and this one would have 4. Choose a smaller size, or ask an administrator") {
			t.Fatalf("resize over each deployment's vCPU = %s", body)
		}

		// An update that grows the deployment is held to it; one that doesn't, isn't, though Thomas is over his count.
		medium := map[string]any{}
		for k, v := range small {
			medium[k] = v
		}
		medium["size"] = "M"
		var up api.UpdatePreview
		h.json("POST", "/api/v1/deployments/"+d.id+"/update/preview", "tnguyen", api.UpdateDraft{VersionId: form.VersionId, Inputs: medium}, 200, &up)
		if l := limitLines(valueOf(up.Limits))[api.VcpuPerDeployment]; !l.Exceeds || l.Value != 4 || len(up.Errors) != 1 ||
			deref(up.Errors[0].Code) != "limit_exceeded" {
			t.Fatalf("update growing the deployment: limits %+v, errors %+v", valueOf(up.Limits), up.Errors)
		}
		h.json("POST", "/api/v1/deployments/"+d.id+"/update/preview", "tnguyen", api.UpdateDraft{VersionId: form.VersionId, Inputs: small}, 200, &up)
		if len(up.Errors) != 0 {
			t.Fatalf("update keeping the size: errors %+v", up.Errors)
		}

		// Removing it lifts each user's count.
		h.json("DELETE", path+"/"+webp.Id, admin, nil, 204, nil)
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if _, ok := limitLines(valueOf(pv.Limits))[api.DeploymentsPerUser]; ok {
			t.Fatalf("limits once Web limits is gone = %+v", valueOf(pv.Limits))
		}
		var list api.DeploymentLimitPolicyList
		h.json("GET", path, padmin, nil, 200, &list)
		if len(list.Items) != 1 || list.Items[0].Name != "rvav limits" || !list.Items[0].SetByAdmin {
			t.Fatalf("policies = %+v", list.Items)
		}
		h.json("GET", path, "tnguyen", nil, 403, nil)
		for action, want := range map[string]int{"policy.limit.create": 2, "policy.limit.update": 2, "policy.limit.delete": 1} {
			var audit struct{ Items []api.AuditEvent }
			h.json("GET", "/api/v1/audit?action="+action, admin, nil, 200, &audit)
			if len(audit.Items) != want {
				t.Fatalf("%s audited %d times, want %d", action, len(audit.Items), want)
			}
		}
	})
}
