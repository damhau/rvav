package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/damhau/rvav/internal/buildinfo"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/store"
)

// How a process applies newer connection settings (ADR-0030): it records
// itself every few seconds with the settings revision it runs with; when
// the connections saved differ from those it started with, and have been
// left alone for a moment, it drains (takes no new work, lets what runs
// finish, within RVAV_DRAIN_TIMEOUT) and exits with ExitRestart, for its
// supervisor (Compose, Kubernetes, systemd) to start it again with them.

// ErrRestart: the process drained to apply newer connection settings, and
// is to be started again.
var ErrRestart = errors.New("drained to apply newer connection settings")

// ExitRestart is the status a process exits with to be started again.
const ExitRestart = 3

// Watch is how often a process records itself and looks for newer
// settings; Settle is how long saved connections stay unchanged before it
// drains, so that settings saved together apply at once. Open sets them;
// tests make them shorter.
type Watch struct {
	Every  time.Duration
	Settle time.Duration
	// Grace is how long an API answers /readyz with 503 before it stops
	// taking requests, for the load balancer to send them elsewhere.
	Grace time.Duration
}

// forgetAfter is how long a process that stopped recording itself (it
// crashed) is kept, before any other forgets it.
const forgetAfter = 10 * time.Minute

// Run runs a process (role: api, worker, sync, dev) until ctx is done. It
// records the process, and drains it once newer connection settings are
// saved: run's context ends with ErrRestart as its cause, and when run
// returns, so does Run, with ErrRestart.
func (a *App) Run(ctx context.Context, role string, run func(context.Context) error) error {
	host, _ := os.Hostname()
	p := store.Process{ID: processID(host, role), Role: role, Host: host, Version: buildinfo.Version(),
		Revision: a.SettingsRevision, State: "running"}
	if err := a.Store.SeeProcess(ctx, p, time.Now().Add(-forgetAfter)); err != nil {
		return fmt.Errorf("record the process: %w", err)
	}
	defer func() {
		gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := a.Store.ProcessGone(gctx, p.ID); err != nil {
			a.Log.Warn("forget the process", "err", err)
		}
	}()

	rctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan error, 1)
	go func() { done <- run(rctx) }()

	tick := time.NewTicker(a.Watch.Every)
	defer tick.Stop()
	var (
		pending  int64 // the revision of connections not applied yet
		since    time.Time
		draining time.Time
	)
	for {
		select {
		case err := <-done:
			if !draining.IsZero() && (err == nil || errors.Is(err, context.Canceled)) {
				a.Log.Info("drained; exiting to apply newer connection settings", "role", role,
					"drained_in", time.Since(draining).Round(time.Millisecond).String())
				a.auditProcess(ctx, "settings.process_restart", p, pending, map[string]any{"drained_ms": time.Since(draining).Milliseconds()})
				return ErrRestart
			}
			return err
		case <-tick.C:
		}
		if draining.IsZero() {
			cfg, rev := a.Live.Snapshot(ctx)
			switch {
			case reflect.DeepEqual(core.ConnectionsOf(cfg), core.ConnectionsOf(a.Cfg)):
				// Nothing to restart for: what changed is read on next use.
				p.Revision, pending = max(p.Revision, rev), 0
			case rev != pending:
				pending, since = rev, time.Now()
			case time.Since(since) >= a.Watch.Settle:
				// One API at a time, so the others keep answering.
				ok := true
				if role == "api" {
					var err error
					if ok, err = a.Store.ClaimDrain(ctx, p.ID, role, time.Now().Add(-core.ProcessesSeen)); err != nil {
						a.Log.Warn("claim the drain", "err", err)
						ok = false
					}
				}
				if ok {
					draining, p.State = time.Now(), "draining"
					a.Log.Info("newer connection settings saved: draining, then exiting to be started again with them",
						"role", role, "revision", p.Revision, "saved", pending, "timeout", a.Cfg.DrainTimeout.String())
					a.auditProcess(ctx, "settings.process_drain", p, pending, nil)
					cancel(ErrRestart)
				}
			}
		}
		if err := a.Store.SeeProcess(ctx, p, time.Now().Add(-forgetAfter)); err != nil && ctx.Err() == nil {
			a.Log.Warn("record the process", "err", err)
		}
	}
}

// Draining says whether a process's context ended for it to apply newer
// settings, rather than to stop.
func Draining(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), ErrRestart)
}

func (a *App) auditProcess(ctx context.Context, action string, p store.Process, saved int64, extra map[string]any) {
	detail := map[string]any{"role": p.Role, "host": p.Host, "revision": p.Revision, "applies": saved}
	for k, v := range extra {
		detail[k] = v
	}
	actor := core.SystemUserID
	if err := a.Store.Audit(context.WithoutCancel(ctx), a.Store.Pool, store.AuditEvent{ActorID: &actor, Action: action,
		TargetType: "process", TargetID: p.ID, Detail: detail}); err != nil {
		a.Log.Warn("audit", "action", action, "err", err)
	}
}

func processID(host, role string) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s/%s/%d-%s", host, role, os.Getpid(), hex.EncodeToString(b))
}
