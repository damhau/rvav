package provision

import (
	"context"
	"errors"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Housekeeper applies rvav's own policies (core.Service): expired
// snapshots now, leases in S5.
type Housekeeper interface {
	ExpireSnapshots(ctx context.Context, now time.Time) ([]string, error)
}

// HousekeepingSchedule is the ID of the Temporal schedule that starts
// Housekeeping every HousekeepingEvery.
const (
	HousekeepingSchedule = "rvav-housekeeping"
	HousekeepingEvery    = 15 * time.Minute
)

// Housekeeping applies the periodic policies once. Each run is short: what
// it starts runs as ordinary requests, so no workflow waits for days.
func Housekeeping(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	var a *Activities
	return workflow.ExecuteActivity(ctx, a.ExpireSnapshots).Get(ctx, nil)
}

// ExpireSnapshots starts the deletion of snapshots past their retention.
func (a *Activities) ExpireSnapshots(ctx context.Context) (int, error) {
	if a.Housekeeper == nil {
		return 0, nil
	}
	started, err := a.Housekeeper.ExpireSnapshots(ctx, time.Now().UTC())
	if len(started) > 0 {
		a.Log.Info("expired snapshots", "requests", len(started))
	}
	return len(started), err
}

// EnsureHousekeeping creates the housekeeping schedule unless it exists.
func EnsureHousekeeping(ctx context.Context, c client.Client, taskQueue string) error {
	_, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:      HousekeepingSchedule,
		Spec:    client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: HousekeepingEvery}}},
		Action:  &client.ScheduleWorkflowAction{ID: "housekeeping", Workflow: Housekeeping, TaskQueue: taskQueue},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	if errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return nil
	}
	return err
}
