package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// ExpireLeases applies the lease policies (ADR-0014): in-app reminders 7
// days and 1 day before a lease ends, then its expiry as day-2 requests by
// rvav: lease_expire powers the VMs off, and delete runs once the grace
// period is over (or at once, for expiry_action delete). A deployment busy
// with another request waits for the next round; an expiry that failed is
// tried again after a day. It returns the requests started.
func (s *Service) ExpireLeases(ctx context.Context, now time.Time) ([]string, error) {
	deps, err := s.Store.LeasedDeployments(ctx)
	if err != nil {
		return nil, err
	}
	var started []string
	for _, d := range deps {
		left := d.LeaseExpiresAt.Sub(now)
		switch {
		case left > 0:
			s.remind(ctx, d, left)
			continue
		case d.ExpiryAction == "notify_only":
			if !slices.Contains(d.LeaseReminded, "ended") {
				s.notify(ctx, d.OwnerID, "The lease of "+d.Name+" ended", "Its policy keeps it running; delete it when you no longer need it.", "warning", d.ID)
				_ = s.Store.SetLeaseReminded(ctx, s.Store.Pool, d.ID, append(d.LeaseReminded, "ended"))
			}
			continue
		}
		action := "lease_expire"
		if d.ExpiryAction == "delete" {
			action = "delete"
		} else if d.LeaseExpiredAt != nil {
			grace := 0
			if d.LeaseGraceDays != nil {
				grace = *d.LeaseGraceDays
			}
			if now.Before(d.LeaseExpiredAt.Add(time.Duration(grace) * 24 * time.Hour)) {
				continue // in its grace period
			}
			action = "delete"
		}
		if d.Status != "ready" {
			continue
		}
		failed, err := s.Store.RecentlyFailedAction(ctx, SystemUserID, d.ID, "", action, "", "", now.Add(-24*time.Hour))
		if err != nil {
			return started, err
		}
		if failed {
			continue
		}
		why := "The lease ended on " + d.LeaseExpiresAt.UTC().Format("2 Jan 2006 15:04 UTC") + "."
		row, err := s.RunAction(ctx, system(), d.ID, api.Day2Draft{Action: action, Inputs: map[string]any{}, Justification: &why})
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			continue
		}
		if err != nil {
			s.Log.Warn("lease expiry", "deployment", d.ID, "action", action, "err", err)
			continue
		}
		started = append(started, row.ID)
	}
	return started, nil
}

// remind tells the owner once when a week, then a day, is left.
func (s *Service) remind(ctx context.Context, d store.DeploymentRow, left time.Duration) {
	var tag, when string
	switch {
	case left <= 24*time.Hour && !slices.Contains(d.LeaseReminded, "1d"):
		tag, when = "1d", "within a day"
	case left <= 7*24*time.Hour && !slices.Contains(d.LeaseReminded, "7d") && !slices.Contains(d.LeaseReminded, "1d"):
		tag, when = "7d", fmt.Sprintf("in %d days", int(left.Hours()/24)+1)
	default:
		return
	}
	then := "Extend it if you still need it."
	switch d.ExpiryAction {
	case "power_off_then_delete":
		then = "Its VMs are then powered off, and deleted after the grace period, unless you extend the lease."
	case "delete":
		then = "It is then deleted, unless you extend the lease."
	}
	s.notify(ctx, d.OwnerID, "The lease of "+d.Name+" ends "+when, then, "warning", d.ID)
	reminded := append(slices.Clone(d.LeaseReminded), tag)
	if tag == "1d" && !slices.Contains(reminded, "7d") {
		reminded = append(reminded, "7d")
	}
	if err := s.Store.SetLeaseReminded(ctx, s.Store.Pool, d.ID, reminded); err != nil {
		s.Log.Warn("lease reminder", "deployment", d.ID, "err", err)
	}
}

func (s *Service) notify(ctx context.Context, userID, title, body, level, deploymentID string) {
	if s.Events != nil {
		s.Events.Notify(ctx, userID, title, body, level, "/deployments/"+deploymentID)
	}
}
