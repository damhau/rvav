package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/snowsync"
	"github.com/damhau/rvav/internal/trust"
)

// TestServiceNowRejected: a request rejected in rvav closes its requested
// item incomplete, saying who rejected it once; a work note ServiceNow
// keeps refusing is given up after a few tries rather than hold back the
// ones after it for good.
func TestServiceNowRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-snow-rejected"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		snow, srv := snowInstance(t)
		h.svc.Config.ServiceNow = config.ServiceNowConfig{URL: srv.URL, Auth: servicenow.AuthBasic, Username: snow.Username,
			Secret: snow.Password, CertSHA256: trust.Fingerprint(srv.Certificate()), RequestsPerMinute: 30}
		var made api.ServiceNowTokenMade
		h.json("POST", "/api/v1/admin/integrations/servicenow/token", "mkeller", nil, 201, &made)

		ritm := snow.Insert(servicenow.TableRequestedItem, servicenow.Record{"number": "RITM0010050", "state": "1"})
		inputs := map[string]any{"role": "front", "size": "S", "cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02",
			"network": "dvpg-web-prd-vl210"}
		var got api.ServiceNowRequest
		h.json("POST", "/api/v1/servicenow/requests", "", api.ServiceNowRequestDraft{RitmSysId: ritm, RitmNumber: "RITM0010050",
			User: "thomas.nguyen@corp.example", CatalogItemId: "rhel9-server", ProjectId: "web",
			Environment: "prd", Inputs: &inputs}, 202, &got, "Authorization", "Bearer "+made.Secret)
		if got.Status != api.RequestStatusPendingApproval {
			t.Fatalf("the request = %+v", got)
		}
		inbox := h.waitInbox("mkeller", 1)
		h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", "mkeller",
			api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionReject, Comment: strPtr("Not this quarter")}, 200, nil)
		h.waitStatus(got.RequestId, "tnguyen", api.RequestStatusRejected)

		// A data policy refuses the first note, every time.
		snow.RefuseUpdates("submitted", 403)
		var ahead time.Duration
		sync := &snowsync.Syncer{Store: h.store, PublicURL: "https://rvav.corp.example",
			Config: func(context.Context) config.ServiceNowConfig { return h.svc.Config.ServiceNow },
			Now:    func() time.Time { return time.Now().Add(ahead) }}
		for range 6 {
			if _, err := h.svc.Dispatcher.Once(ctx); err != nil {
				t.Fatal(err)
			}
			if err := sync.Pass(ctx); err != nil {
				t.Fatal(err)
			}
			ahead += 20 * time.Minute
		}

		notes, item := snow.WorkNotes(ritm), snow.Row(servicenow.TableRequestedItem, ritm)
		if item["state"] != servicenow.ItemClosedIncomplete || len(notes) != 2 || !strings.Contains(notes[0], "waits for approval") ||
			!strings.Contains(notes[1], got.RequestNumber+", ") || !strings.Contains(notes[1], ": Rejected by Marc Keller. https://") ||
			strings.Contains(notes[1], "Keller by") {
			t.Fatalf("the requested item: state %v, notes %q", item["state"], notes)
		}
		var attempts int
		var why string
		if err := h.store.Pool.QueryRow(ctx, `select n.attempts, n.last_error from servicenow_ritm_notes n
			join integration_events e on e.id = n.event_id where n.request_id = $1 and e.topic = 'request.submitted' and n.sent_at is not null`,
			got.RequestId).Scan(&attempts, &why); err != nil || attempts != 5 || !strings.Contains(why, "403") {
			t.Fatalf("the refused note: %d attempts, %q, %v", attempts, why, err)
		}
	})
}
