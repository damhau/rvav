package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/store"
)

// SystemUserID is rvav itself, the requester of what it starts on its own.
const SystemUserID = "usr_rvav"

// system acts for rvav's own policies; it may run any action.
func system() *auth.Principal {
	return &auth.Principal{User: store.User{ID: SystemUserID, Name: "rvav"}, GlobalRoles: []string{"admin"}}
}

// ExpireSnapshots starts the deletion of snapshots older than their
// environment allows: the oldest one per VM each time, as an ordinary
// snapshot_delete request requested by rvav. A VM busy with another request
// waits for the next round; a snapshot whose deletion failed is left for a
// day, so a failure is not repeated every round. It returns the requests
// started.
func (s *Service) ExpireSnapshots(ctx context.Context, now time.Time) ([]string, error) {
	envs, err := s.Store.ListEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	maxAge, why := map[string]time.Duration{}, map[string]string{}
	for _, e := range envs {
		if e.SnapshotMaxAgeDays != nil {
			maxAge[e.Key] = time.Duration(*e.SnapshotMaxAgeDays) * 24 * time.Hour
			why[e.Key] = fmt.Sprintf("Snapshots are kept %d days in %s.", *e.SnapshotMaxAgeDays, e.Name)
		}
	}
	resources, err := s.Store.VMResources(ctx)
	if err != nil {
		return nil, err
	}
	var started []string
	for _, r := range resources {
		vm := day2.VM(r.Properties)
		if vm.Snapshots == nil || len(*vm.Snapshots) == 0 {
			continue
		}
		d, err := s.Store.GetDeployment(ctx, r.DeploymentID)
		if err != nil {
			return started, err
		}
		age, ok := maxAge[d.Environment]
		if !ok || d.Status != "ready" {
			continue
		}
		var oldest *time.Time
		name := ""
		for _, snap := range *vm.Snapshots {
			if now.Sub(snap.CreatedAt) <= age || (oldest != nil && !snap.CreatedAt.Before(*oldest)) {
				continue
			}
			failed, err := s.Store.RecentlyFailedAction(ctx, SystemUserID, r.ID, "snapshot_delete", "snapshot", snap.Name, now.Add(-24*time.Hour))
			if err != nil {
				return started, err
			}
			if !failed {
				t := snap.CreatedAt
				oldest, name = &t, snap.Name
			}
		}
		if name == "" {
			continue
		}
		reason := why[d.Environment]
		row, err := s.RunAction(ctx, system(), d.ID, api.Day2Draft{Action: "snapshot_delete", ResourceId: &r.ID,
			Inputs: map[string]any{"snapshot": name}, Justification: &reason})
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			continue // busy: next round
		}
		if err != nil {
			s.Log.Warn("expire snapshot", "deployment", d.ID, "snapshot", name, "err", err)
			continue
		}
		started = append(started, row.ID)
	}
	return started, nil
}
