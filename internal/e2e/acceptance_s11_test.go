package e2e

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/client"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/provision"
)

// TestNotificationsAcceptance is the acceptance scenario of slice 11, on a
// real Temporal server with the notifier running as on the worker, against
// fake Teams and SMTP endpoints: an approver approves a production VM from
// the card Teams shows them; the requester gets an email when it is ready,
// and, for a VM with a lease, another seven days before the lease ends,
// once however often housekeeping runs (S11.7, ADR-0023).
func TestNotificationsAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-s11-acceptance"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		var shift atomic.Int64
		h.acts.Housekeeper = h.svc
		h.acts.Now = func() time.Time { return time.Now().Add(time.Duration(shift.Load())) }
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		housekeeping := func(after time.Duration) {
			t.Helper()
			shift.Store(int64(after))
			run, err := tc.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "housekeeping-" + uuid.NewString(),
				TaskQueue: taskQueue}, provision.Housekeeping)
			if err != nil {
				t.Fatal(err)
			}
			if err := run.Get(ctx, nil); err != nil {
				t.Fatalf("housekeeping: %v", err)
			}
		}

		// Email and Teams, and the notifier running as on the worker.
		mail := startSMTP(t)
		teams := startTeams(t)
		h.svc.Events.Channels = []string{"email", "teams"}
		h.api.Teams = &notify.TeamsVerifier{AppID: "app-1", OpenIDURL: teams.URL + "/openid", TenantID: "tenant-1"}
		n := &notify.Notifier{Store: h.store, Log: h.svc.Log, PublicURL: "https://rvav.corp.example", Interval: 100 * time.Millisecond,
			Senders: map[string]notify.Sender{
				"email": &notify.Email{Config: config.SMTPConfig{Host: "127.0.0.1", Port: mail.addr.Port, TLS: "none", From: "rvav <rvav@corp.example>"}},
				"teams": &notify.Teams{Store: h.store, Config: config.TeamsConfig{AppID: "app-1", TenantID: "tenant-1",
					AppPassword: "app-secret", ServiceURL: teams.URL + "/smba", LoginURL: teams.URL, GraphURL: teams.URL}},
			}}
		nctx, stop := context.WithCancel(ctx)
		defer stop()
		go n.Run(nctx)
		eventually := func(what string, ok func() bool) {
			t.Helper()
			for deadline := time.Now().Add(30 * time.Second); !ok(); time.Sleep(50 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatalf("waited 30 s for %s", what)
				}
			}
		}
		const thomas, marc = "thomas.nguyen@corp.example", "marc.keller@corp.example"
		mailTo := func(addr, prefix string) []sentMail {
			var out []sentMail
			for _, m := range mail.to(addr) {
				if strings.HasPrefix(m.Subject, prefix) {
					out = append(out, m)
				}
			}
			return out
		}

		// A production VM waits for the infrastructure leads; Marc approves it from Teams.
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", "tnguyen", nil, 200, &item)
		var prd api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
			ProjectId: "prj_web", Environment: "prd", Inputs: map[string]any{"role": "front", "size": "XL",
				"cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02", "network": "dvpg-web-prd-vl210",
				"post_config": []string{"linux-baseline"}}}, 202, &prd, "Idempotency-Key", uuid.NewString())
		h.waitStatus(prd.Id, "tnguyen", api.RequestStatusPendingApproval)
		eventually("Marc's Teams card", func() bool { return len(teams.cardsFor("oid-mkeller")) == 1 })
		eventually("Marc's email", func() bool { return len(mailTo(marc, "Approval needed: ")) == 1 })
		card := teams.cardsFor("oid-mkeller")[0]
		data := map[string]any{}
		for k, v := range card["actions"].([]any)[0].(map[string]any)["data"].(map[string]any) {
			data[k] = v
		}
		data["comment"] = "Fine for Black Friday"
		_, out := teams.press(t, h, "oid-mkeller", "approve", data, teams.token(t, teams.URL+"/smba", nil))
		if v, _ := out["value"].(map[string]any); !strings.Contains(cardText(v), "Approved by Marc Keller") {
			t.Fatalf("approved from Teams = %v", out)
		}

		// Built; Thomas hears it is ready, by email.
		h.waitStatus(prd.Id, "tnguyen", api.RequestStatusSucceeded)
		eventually("Thomas's ready email", func() bool { return len(mailTo(thomas, "Ready: ")) == 1 })
		ready := mailTo(thomas, "Ready: ")[0]
		if !strings.Contains(ready.Text, "https://rvav.corp.example/requests/"+prd.Number) || !strings.Contains(ready.Subject, prd.Number) {
			t.Fatalf("the ready email = %s\n%s", ready.Subject, ready.Text)
		}

		// A dev VM with a 30-day lease: ready, then a week before its end a reminder.
		days := 30
		var dev api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
			ProjectId: "prj_web", Environment: "dev", LeaseDays: &days, Inputs: map[string]any{"role": "app", "size": "S",
				"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310",
				"post_config": []string{"linux-baseline"}}}, 202, &dev, "Idempotency-Key", uuid.NewString())
		dev = h.waitStatus(dev.Id, "tnguyen", api.RequestStatusSucceeded)
		eventually("the second ready email", func() bool { return len(mailTo(thomas, "Ready: ")) == 2 })
		var dep api.Deployment
		h.json("GET", "/api/v1/deployments/"+(*dev.DeploymentIds)[0], "tnguyen", nil, 200, &dep)

		housekeeping(23*24*time.Hour + time.Hour)
		housekeeping(23*24*time.Hour + 2*time.Hour)
		want := "The lease of " + dep.Name + " ends in 7 days"
		eventually("the lease reminder", func() bool { return len(mailTo(thomas, want)) == 1 })
		reminder := mailTo(thomas, want)[0]
		if !strings.Contains(reminder.Text, "https://rvav.corp.example/deployments/"+dep.Id) {
			t.Fatalf("the reminder = %s", reminder.Text)
		}
		time.Sleep(500 * time.Millisecond) // a second reminder would have gone by now
		if got := len(mailTo(thomas, "The lease of ")); got != 1 {
			t.Fatalf("%d lease emails after housekeeping ran twice", got)
		}

		// All of it is in Thomas's notification centre.
		var centre api.NotificationPage
		h.json("GET", "/api/v1/me/notifications", "tnguyen", nil, 200, &centre)
		kinds := map[api.NotificationKind]int{}
		for _, it := range centre.Items {
			kinds[it.Kind]++
		}
		if kinds[api.KindRequestApproved] != 1 || kinds[api.KindRequestSucceeded] != 2 || kinds[api.KindLeaseExpiring] != 1 {
			t.Fatalf("Thomas's centre = %v", kinds)
		}
	})
}
