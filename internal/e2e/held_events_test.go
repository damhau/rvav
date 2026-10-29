package e2e

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// startCheck runs check while a request's workflow starts.
type startCheck struct {
	*recorder
	check func(requestID string)
}

func (s startCheck) Start(ctx context.Context, id string) error {
	s.check(id)
	return s.recorder.Start(ctx, id)
}

// TestHeldEvents: a request's request.submitted event is held while its
// workflow starts, goes out once it has, and never when Temporal refuses
// the start (#81).
func TestHeldEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		held := func(id string) (exists, isHeld, dispatchable bool) {
			err := h.store.Pool.QueryRow(ctx, `select true, held from integration_events
				where request_id = $1 and topic = 'request.submitted'`, id).Scan(&exists, &isHeld)
			if err != nil {
				return false, false, false
			}
			tx, err := h.store.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			evs, err := h.store.UndispatchedEvents(ctx, tx, 1000)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range evs {
				if e.RequestID != nil && *e.RequestID == id && e.Topic == "request.submitted" {
					dispatchable = true
				}
			}
			return exists, isHeld, dispatchable
		}
		var during [3]bool
		h.svc.Workflows = startCheck{recorder: h.wf, check: func(id string) {
			during[0], during[1], during[2] = held(id)
		}}
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", "tnguyen", nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "ev", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		if during != [3]bool{true, true, false} {
			t.Fatalf("while its workflow started: exists, held, dispatchable = %v", during)
		}
		if exists, isHeld, dispatchable := held(req.Id); !exists || isHeld || !dispatchable {
			t.Fatalf("once started: exists %v, held %v, dispatchable %v", exists, isHeld, dispatchable)
		}

		// The API couldn't let it go after starting the workflow: the
		// workflow's first activity does.
		if _, err := h.store.Pool.Exec(ctx, `update integration_events set held = true where request_id = $1`, req.Id); err != nil {
			t.Fatal(err)
		}
		if _, err := h.acts.LoadPlan(ctx, req.Id); err != nil {
			t.Fatal(err)
		}
		if _, isHeld, _ := held(req.Id); isHeld {
			t.Fatal("still held once the workflow ran")
		}

		// Temporal refuses: the request and its event are gone, never sent.
		h.wf.startErr = context.DeadlineExceeded
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 502, nil, "Idempotency-Key", uuid.NewString())
		var left int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from integration_events e where topic = 'request.submitted'
			and not exists (select 1 from requests r where r.id = e.request_id)`).Scan(&left)
		if left != 0 || !during[1] {
			t.Fatalf("%d events of a request that never was; held while starting %v", left, during[1])
		}
	})
}
