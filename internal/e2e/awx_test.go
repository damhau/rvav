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

// TestAWXWorkflowItem requests an item backed by an AWX workflow: the step
// shows one task per workflow node.
func TestAWXWorkflowItem(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		// Firewall flows are reviewed by network security; not what this is about.
		if _, err := h.store.Pool.Exec(ctx, `delete from approval_policies where id = 'pol_fw'`); err != nil {
			t.Fatal(err)
		}
		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/firewall-flow?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/firewall-flow/form?project_id="+web, "lmartin", nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"source": "web-dev-app001", "destination": "10.22.30.10", "port": 5432, "protocol": "tcp"}}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("workflow item = %s", req.Status)
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &steps)
		var nodes []string
		for _, s := range steps.Items {
			for _, c := range s.Children {
				if c.Status == api.StepStatusDone {
					nodes = append(nodes, c.Title)
				}
			}
		}
		if strings.Join(nodes, ",") != "fw-check-flow,fw-open-change,fw-push-rules" {
			t.Fatalf("workflow nodes = %v", nodes)
		}
	})
}
