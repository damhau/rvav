package e2e

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// TestApprovalSignalResent: a custom workflow's approval decided while its
// signal failed is sent again by the worker, once (#81).
func TestApprovalSignalResent(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", "tnguyen", nil, 200, &form)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "sg", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
			202, &req, "Idempotency-Key", uuid.NewString())
		// Decided a minute ago; its signal never went.
		if err := h.store.InsertWorkflowApproval(ctx, store.Approval{ID: "apr_lost", RequestID: req.Id, Name: "Lost",
			Mode: "any", WorkflowID: new("wf-lost")}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Pool.Exec(ctx, `update approvals set status = 'approved', decided_at = now() - interval '1 minute'
			where id = 'apr_lost'`); err != nil {
			t.Fatal(err)
		}
		_, _ = h.store.Pool.Exec(ctx, `insert into approval_votes (approval_id, user_id, decision, comment, changes, decided_at)
			values ('apr_lost', 'usr_mkeller', 'approve', 'Go ahead', '[]', now() - interval '1 minute')`)
		n, err := h.svc.ResendApprovalSignals(ctx)
		if err != nil || n != 1 {
			t.Fatalf("resent %d, %v", n, err)
		}
		h.wf.mu.Lock()
		sent := h.wf.byID
		h.wf.mu.Unlock()
		if len(sent) != 1 || sent[0].id != "wf-lost" || sent[0].name != provision.SignalWorkflowApproval {
			t.Fatalf("signals = %+v", sent)
		}
		if s, ok := sent[0].arg.(provision.WorkflowApprovalSignal); !ok || s.ApprovalID != "apr_lost" || s.Outcome != "approved" ||
			s.Comment != "Go ahead" {
			t.Fatalf("signal = %+v", sent[0].arg)
		}
		if n, _ := h.svc.ResendApprovalSignals(ctx); n != 0 {
			t.Fatalf("sent again %d times", n)
		}
	})
}
