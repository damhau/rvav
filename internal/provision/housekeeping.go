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

// Housekeeper applies rvav's own policies (core.Service): snapshot
// retention and leases.
type Housekeeper interface {
	ExpireSnapshots(ctx context.Context, now time.Time) ([]string, error)
	ExpireLeases(ctx context.Context, now time.Time) ([]string, error)
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
	// One failing does not keep the other from running.
	snapshots := workflow.ExecuteActivity(ctx, a.ExpireSnapshots)
	leases := workflow.ExecuteActivity(ctx, a.ExpireLeases)
	return errors.Join(snapshots.Get(ctx, nil), leases.Get(ctx, nil))
}

// ExpireSnapshots starts the deletion of snapshots past their retention.
func (a *Activities) ExpireSnapshots(ctx context.Context) (int, error) {
	if a.Housekeeper == nil {
		return 0, nil
	}
	started, err := a.Housekeeper.ExpireSnapshots(ctx, a.now())
	if len(started) > 0 {
		a.Log.Info("expired snapshots", "requests", len(started))
	}
	return len(started), err
}

// ExpireLeases sends lease reminders and starts the expiry of ended leases.
func (a *Activities) ExpireLeases(ctx context.Context) (int, error) {
	if a.Housekeeper == nil {
		return 0, nil
	}
	started, err := a.Housekeeper.ExpireLeases(ctx, a.now())
	if len(started) > 0 {
		a.Log.Info("lease expiries started", "requests", len(started))
	}
	return len(started), err
}

func (a *Activities) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

// EnsureHousekeeping creates the housekeeping schedule, or brings an
// existing one up to date. A run ends after 10 minutes at most, so one
// stuck run (overlapping runs are skipped) can't stop the rounds after it.
func EnsureHousekeeping(ctx context.Context, c client.Client, taskQueue string) error {
	spec := client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: HousekeepingEvery}}}
	action := &client.ScheduleWorkflowAction{ID: "housekeeping", Workflow: Housekeeping, TaskQueue: taskQueue,
		WorkflowRunTimeout: 10 * time.Minute}
	_, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: HousekeepingSchedule, Spec: spec, Action: action, Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	if !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return err
	}
	return c.ScheduleClient().GetHandle(ctx, HousekeepingSchedule).Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			s := in.Description.Schedule
			s.Spec, s.Action = &spec, action
			if s.Policy != nil {
				s.Policy.Overlap = enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
			}
			return &client.ScheduleUpdate{Schedule: &s}, nil
		},
	})
}
