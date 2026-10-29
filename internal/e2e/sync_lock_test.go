package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/inventory"
)

// TestSyncLockLost: the active sync whose lock's connection is lost (a
// database failover, a dropped connection) stops, instead of syncing on
// beside the standby that takes the lock over (S14 review).
func TestSyncLockLost(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log, Interval: time.Hour}
		done := make(chan error, 1)
		runCtx, stop := context.WithCancel(ctx)
		defer stop()
		go func() { done <- sync.Run(runCtx) }()
		var pid int
		for deadline := time.Now().Add(10 * time.Second); pid == 0; time.Sleep(50 * time.Millisecond) {
			_ = h.store.Pool.QueryRow(ctx, `select pid from pg_locks where locktype = 'advisory' and granted
				and pid <> pg_backend_pid() limit 1`).Scan(&pid)
			if time.Now().After(deadline) {
				t.Fatal("the sync never took its lock")
			}
		}
		if _, err := h.store.Pool.Exec(ctx, `select pg_terminate_backend($1)`, pid); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "lost the connection") {
				t.Fatalf("Run = %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the sync went on after losing its lock")
		}
	})
}
