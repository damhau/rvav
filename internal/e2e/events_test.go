package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/store"
)

// hook is a webhook receiver that fails its first answers.
type hook struct {
	mu       sync.Mutex
	failures int
	got      []*http.Request
	bodies   [][]byte
}

func (k *hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	k.mu.Lock()
	defer k.mu.Unlock()
	k.got, k.bodies = append(k.got, r), append(k.bodies, body)
	if k.failures > 0 {
		k.failures--
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestEventOutbox: request, provisioning, day-2, delete and lease events
// are written with the change they describe; each matching subscription
// gets a run, delivered at least once with back-off and kept with its
// attempts (S10.2, ADR-0021).
func TestEventOutbox(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		receiver := &hook{failures: 2}
		srv := httptest.NewServer(receiver)
		defer srv.Close()
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-hook", Kind: api.SecretWriteKindToken,
			Value: new("hook-secret")}, 201, nil)
		sub := func(id, topic, condition string) {
			t.Helper()
			target, _ := json.Marshal(map[string]any{"webhook": map[string]any{"url": srv.URL + "/rvav", "secret": "cmdb-hook"}})
			if err := h.store.SaveSubscription(ctx, store.Subscription{ID: id, Name: id, Topic: topic, Condition: condition,
				OnFailure: "stop_request", Priority: 100, TimeoutSeconds: 5, Enabled: true, Target: target}); err != nil {
				t.Fatal(err)
			}
		}
		sub("sub_ready_dev", outbox.ProvisioningPost, `environment == "dev" && request.outcome == "succeeded"`)
		sub("sub_ready_prd", outbox.ProvisioningPost, `environment == "prd"`)
		sub("sub_broken", outbox.ProvisioningPost, `vm.cpus > 1`)
		sub("sub_deleted", outbox.DeletePost, "")

		// A VM's life: each event once, with what it is about.
		d := buildVM(h, nil)
		topics := func(requestID string) []string {
			t.Helper()
			rows, _ := h.store.Pool.Query(ctx, `select topic from integration_events where request_id = $1 order by seq`, requestID)
			defer rows.Close()
			var out []string
			for rows.Next() {
				var s string
				_ = rows.Scan(&s)
				out = append(out, s)
			}
			return out
		}
		if got := topics(d.built.Id); !slices.Equal(got, []string{outbox.RequestSubmitted, outbox.ProvisioningPre, outbox.ProvisioningPost}) {
			t.Fatalf("a VM's events = %v", got)
		}
		var payload []byte
		if err := h.store.Pool.QueryRow(ctx, `select payload from integration_events where request_id = $1 and topic = $2`,
			d.built.Id, outbox.ProvisioningPost).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var ev outbox.Event
		_ = json.Unmarshal(payload, &ev)
		req := ev.Data["request"].(map[string]any)
		resources := ev.Data["resources"].([]any)
		vm := resources[slices.IndexFunc(resources, func(r any) bool { return r.(map[string]any)["type"] == "vsphere.vm" })].(map[string]any)
		if ev.Contract != outbox.Contract || req["outcome"] != "succeeded" || ev.Data["environment"] != "dev" ||
			vm["moref"] == nil || vm["ip"] == nil || vm["fqdn"] == nil || ev.Data["deployment"] == nil {
			t.Fatalf("provisioning.post = %s", payload)
		}
		// Written again by a retried activity: still one.
		for range 2 {
			if err := outbox.ForRequest(ctx, h.store.Pool, outbox.ProvisioningPost, d.built.Id, "same-key", "succeeded"); err != nil {
				t.Fatal(err)
			}
		}
		var copies int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from integration_events where dedupe_key = 'same-key'`).Scan(&copies)
		if copies != 1 {
			t.Fatalf("%d copies of one event", copies)
		}
		if _, err := h.store.Pool.Exec(ctx, `delete from integration_events where dedupe_key = 'same-key'`); err != nil {
			t.Fatal(err)
		}

		off := d.succeed("tnguyen", "power_off", nil)
		if got := topics(off.Id); !slices.Equal(got, []string{outbox.RequestSubmitted, outbox.Day2Pre, outbox.Day2Post}) {
			t.Fatalf("power off's events = %v", got)
		}
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		if got := topics(del.Id); !slices.Equal(got, []string{outbox.RequestSubmitted, outbox.DeletePre, outbox.DeletePost}) {
			t.Fatalf("delete's events = %v", got)
		}

		// Lease: a reminder a day before, then the expiry.
		leased := leasedVM(h, 1)
		now := time.Now()
		if _, err := h.svc.ExpireLeases(ctx, now); err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.ExpireLeases(ctx, now.Add(49*time.Hour)); err != nil {
			t.Fatal(err)
		}
		var leaseTopics []string
		rows, _ := h.store.Pool.Query(ctx, `select topic from integration_events where deployment_id = $1 and request_id is null order by seq`, leased.id)
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			leaseTopics = append(leaseTopics, s)
		}
		rows.Close()
		if !slices.Equal(leaseTopics, []string{outbox.LeaseExpiring, outbox.LeaseExpired}) {
			t.Fatalf("lease events = %v", leaseTopics)
		}

		// Delivery: one run per matching subscription, retried with back-off.
		clock := time.Now()
		disp := &dispatch.Dispatcher{Store: h.store, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return clock },
			Targets: dispatch.Targets{Webhook: &dispatch.Webhook{Hosts: []string{"http://" + strings.TrimPrefix(srv.URL, "http://")},
				Secret: dispatch.StoredSecret(h.store, h.svc.Secrets)}}}
		if _, err := disp.Once(ctx); err != nil {
			t.Fatal(err)
		}
		runs := func(subID string) []store.SubscriptionRun {
			t.Helper()
			list, err := h.store.SubscriptionRuns(ctx, store.RunFilter{SubscriptionID: subID, Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			return list
		}
		// Two VMs built in dev (the first and the leased one), none in production.
		ready := runs("sub_ready_dev")
		if len(ready) != 2 || len(runs("sub_ready_prd")) != 0 || len(runs("sub_deleted")) != 1 {
			t.Fatalf("runs: dev %d, prd %d, deleted %d", len(ready), len(runs("sub_ready_prd")), len(runs("sub_deleted")))
		}
		broken := runs("sub_broken")
		if len(broken) != 2 || broken[0].Status != "failed" || !strings.Contains(*broken[0].Reason, "condition") {
			t.Fatalf("a broken condition's runs = %+v", broken)
		}
		// The receiver failed twice: two runs retry in 10 s; nothing before.
		retrying := 0
		for _, r := range append(runs("sub_ready_dev"), runs("sub_deleted")...) {
			if r.Status == "queued" {
				retrying++
				if !r.NextAttemptAt.After(clock.Add(9 * time.Second)) {
					t.Fatalf("next attempt at %v", r.NextAttemptAt)
				}
			}
		}
		if retrying != 2 {
			for _, r := range append(runs("sub_ready_dev"), runs("sub_deleted")...) {
				t.Logf("run %s %s attempts %d reason %s", r.ID, r.Status, r.Attempts, deref(r.Reason))
			}
			t.Fatalf("%d runs retrying", retrying)
		}
		if n, _ := disp.Once(ctx); n != 0 {
			t.Fatalf("%d attempts before their time", n)
		}
		clock = clock.Add(11 * time.Second)
		if n, _ := disp.Once(ctx); n != 2 {
			t.Fatalf("%d attempts after 11 s", n)
		}
		for _, r := range append(runs("sub_ready_dev"), runs("sub_deleted")...) {
			if r.Status != "delivered" {
				t.Fatalf("run %s is %s", r.ID, r.Status)
			}
		}
		_, detail, err := h.store.GetSubscriptionRun(ctx, ready[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		attempts, _ := h.store.RunAttempts(ctx, ready[0].ID)
		if len(attempts) == 0 || !attempts[len(attempts)-1].OK || len(detail) == 0 {
			t.Fatalf("attempts %+v", attempts)
		}

		// Signed, the same event ID on every try.
		receiver.mu.Lock()
		first, body := receiver.got[0], receiver.bodies[0]
		ids := map[string]int{}
		for _, r := range receiver.got {
			ids[r.Header.Get("X-Rvav-Event-Id")]++
		}
		receiver.mu.Unlock()
		sig := first.Header.Get("X-Rvav-Signature")
		ts, _ := strings.CutPrefix(strings.Split(sig, ",")[0], "t=")
		at, _ := time.Parse(time.RFC3339, time.Unix(mustInt(t, ts), 0).UTC().Format(time.RFC3339))
		if sig != dispatch.Sign("hook-secret", at, body) || first.Header.Get("X-Rvav-Topic") == "" {
			t.Fatalf("signature %q", sig)
		}
		if len(ids) != 3 || slices.Max(mapValues(ids)) != 2 {
			t.Fatalf("event IDs per try: %v", ids)
		}

		// A receiver that never answers: retried for a day, then failed.
		receiver.mu.Lock()
		receiver.failures = 1000
		receiver.mu.Unlock()
		h.json("POST", "/api/v1/requests/"+off.Id+"/retry", "tnguyen", nil, 409, nil) // nothing to retry: fine, no new events
		if err := outbox.ForDeployment(ctx, h.store.Pool, outbox.DeletePost, d.id, "a-late-delete"); err != nil {
			t.Fatal(err)
		}
		for range 12 {
			if _, err := disp.Once(ctx); err != nil {
				t.Fatal(err)
			}
			clock = clock.Add(3 * time.Hour)
		}
		late := runs("sub_deleted")[0]
		if late.Status != "failed" || late.Attempts < 5 {
			t.Fatalf("a run that never got through = %+v", late)
		}

		// Kept 30 days.
		if n, err := h.store.PruneRuns(ctx, time.Now().Add(31*24*time.Hour)); err != nil || n == 0 {
			t.Fatalf("pruned %d, %v", n, err)
		}
		if len(runs("sub_ready_dev")) != 0 {
			t.Fatal("old runs kept")
		}
	})
}

func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func mapValues(m map[string]int) []int {
	out := make([]int, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
