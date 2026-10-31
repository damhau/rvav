package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/store"
)

// TestProcessDrain: when a process drains to apply newer connection
// settings (ADR-0030), beside TestApplyConnections. It doesn't before it
// has read what is saved: the database not answering at its first look
// left it comparing the environment alone with the settings it started
// with, which differ once connections are saved in Administration, and it
// drained with nothing changed. A drain cut by RVAV_DRAIN_TIMEOUT is a
// drain: the process exits to be started again, and says so.
func TestProcessDrain(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		env := config.Config{DrainTimeout: time.Second}
		// Started with a vCenter saved in Administration.
		started := env
		started.VCenter.URL, started.VCenter.Username = "https://vc01.corp.example/sdk", "svc-rvav"
		process := func(live *store.Store) *app.App {
			return &app.App{Cfg: started, Log: discardLog(), Store: h.store, SettingsRevision: 1,
				Live:  &core.LiveSettings{Store: live, Base: env, Every: time.Millisecond, Log: discardLog()},
				Watch: app.Watch{Every: 10 * time.Millisecond}}
		}

		// The database doesn't answer the settings' reads: no drain.
		down, err := store.Open(ctx, databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		down.Close()
		rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		var cause error
		err = process(down).Run(rctx, "worker", func(c context.Context) error {
			<-c.Done()
			cause = context.Cause(c)
			return c.Err()
		})
		cancel()
		if errors.Is(err, app.ErrRestart) || errors.Is(cause, app.ErrRestart) {
			t.Fatalf("with the saved settings not read, the process drained: %v (%v)", err, cause)
		}

		// Read, they differ (nothing is saved here): it drains, and the work
		// still running at RVAV_DRAIN_TIMEOUT is cut; it exits to start again.
		err = process(h.store).Run(ctx, "worker", func(c context.Context) error {
			<-c.Done()
			if !app.Draining(c) {
				return c.Err()
			}
			return context.DeadlineExceeded // as the API's Shutdown answers
		})
		if !errors.Is(err, app.ErrRestart) {
			t.Fatalf("a drain cut by its timeout: Run = %v, want ErrRestart", err)
		}
		var timedOut bool
		if err := h.store.Pool.QueryRow(ctx, `select coalesce((detail->>'timed_out')::bool, false) from audit_events
			where action = 'settings.process_restart' order by id desc limit 1`).Scan(&timedOut); err != nil || !timedOut {
			t.Fatalf("the restart audited with timed_out=%v: %v", timedOut, err)
		}
	})
}
