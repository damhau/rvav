package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestQuotas checks an environment's quota on submit, resize and move: a
// hard limit refuses with what is left, a soft one asks for approval.
func TestQuotas(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		d := buildVM(h, simulator.VPX())
		vcpu := d.vm().CpuCount
		limit := func(project, enforcement string, n int) {
			if _, err := h.store.Pool.Exec(ctx, `insert into quotas (project_id, environment, resource, limit_value, enforcement)
				values ($1, 'dev', 'vcpu', $2, $3) on conflict (project_id, environment, resource)
				do update set limit_value = excluded.limit_value, enforcement = excluded.enforcement`, project, n, enforcement); err != nil {
				t.Fatal(err)
			}
		}
		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}

		// A hard limit in Development, one vCPU above what is used.
		limit(web, "hard", vcpu+1)
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		var dev *api.QuotaImpact
		for i := range pv.Quota {
			if pv.Quota[i].Environment != nil && *pv.Quota[i].Environment == "dev" {
				dev = &pv.Quota[i]
			}
		}
		if pv.Valid || dev == nil || !dev.Exceeds || *dev.Limit != vcpu+1 || *dev.Enforcement != "hard" {
			t.Fatalf("preview over a hard limit: valid %v, dev line %+v", pv.Valid, dev)
		}
		_, body, _ := h.do("POST", "/api/v1/requests", "tnguyen", draft, "Idempotency-Key", uuid.NewString())
		if !strings.Contains(string(body), "but Web Platform in Development has 1 left") {
			t.Fatalf("submit over a hard limit = %s", body)
		}
		resize := map[string]any{"cpu_count": vcpu + 2, "memory_gb": d.vm().MemoryMb / 1024, "restart": true}
		_, body, _ = h.do("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen",
			api.Day2Draft{Action: "resize", ResourceId: &d.resource, Inputs: resize}, "Idempotency-Key", uuid.NewString())
		if !strings.Contains(string(body), "The new size needs 2 vCPU but Web Platform in Development has 1 left") {
			t.Fatalf("resize over a hard limit = %s", body)
		}

		// Soft: the same asks the Infrastructure leads.
		limit(web, "soft", vcpu+1)
		r := d.run("tnguyen", "resize", resize, 202)
		if r.Status != api.RequestStatusPendingApproval {
			t.Fatalf("resize over a soft limit = %s", r.Status)
		}
		h.json("POST", "/api/v1/requests/"+r.Id+"/cancel", "tnguyen", nil, 202, nil)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		if req.Status != api.RequestStatusPendingApproval {
			t.Fatalf("request over a soft limit = %s", req.Status)
		}
		var levels struct{ Items []api.Approval }
		h.json("GET", "/api/v1/requests/"+req.Id+"/approvals", "tnguyen", nil, 200, &levels)
		if len(levels.Items) != 1 || !contains(levels.Items[0].Reasons, "Over the project's soft quota") {
			t.Fatalf("approval of a request over a soft limit = %+v", levels.Items)
		}

		// Moving to a project without room is not offered.
		limit("prj_data", "hard", 1)
		move := d.actions("mkeller")["change_project"]
		var data *api.Option
		for _, o := range *move.Form.UiSchema.Fields["project"].Options {
			if o.Value == "prj_data" {
				data = &o
			}
		}
		if data == nil || data.Disabled == nil || !*data.Disabled || !strings.Contains(deref(data.DisabledReason), "Data Engineering in Development has 1 left") {
			t.Fatalf("data option = %+v", data)
		}
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "mkeller", api.Day2Draft{Action: "change_project",
			Inputs: map[string]any{"project": "prj_data"}}, 422, nil, "Idempotency-Key", uuid.NewString())
	})
}
