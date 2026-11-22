package provision

import (
	"context"
	"errors"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Scheduler submits the runs of recurring schedules (core.Service, ADR-0057).
type Scheduler interface {
	RunSchedule(ctx context.Context, f ScheduleFiring) error
	ScheduleFailed(ctx context.Context, f ScheduleFiring) error
}

// ScheduleFiring is one firing of a schedule: FireID, the workflow's ID,
// is unique to it, so a firing tried again submits once.
type ScheduleFiring struct {
	ScheduleID string    `json:"schedule_id"`
	FireID     string    `json:"fire_id"`
	At         time.Time `json:"at"`
	Reason     string    `json:"reason,omitempty"`
}

// ScheduleSpec is when a schedule runs: a cron expression in Timezone, or
// every so many minutes.
type ScheduleSpec struct {
	Cron         string
	EveryMinutes int
	Timezone     string
	Paused       bool
	Note         string
}

// InvalidScheduleError is a spec Temporal refuses.
type InvalidScheduleError struct{ Msg string }

func (e *InvalidScheduleError) Error() string { return e.Msg }

// ScheduleTemporalID is the Temporal schedule of a schedule.
func ScheduleTemporalID(id string) string { return scheduleIDPrefix + id }

const scheduleIDPrefix = "rvav-schedule-"

// ScheduledRun submits one run of a schedule as its owner's request; what
// the request runs is the request's own workflow.
func ScheduledRun(ctx workflow.Context, scheduleID string) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	})
	var a *Activities
	f := ScheduleFiring{ScheduleID: scheduleID, FireID: workflow.GetInfo(ctx).WorkflowExecution.ID, At: workflow.Now(ctx).UTC()}
	err := workflow.ExecuteActivity(ctx, a.RunSchedule, f).Get(ctx, nil)
	if err != nil {
		f.Reason = errMessage(err)
		_ = workflow.ExecuteActivity(ctx, a.ScheduleFailed, f).Get(ctx, nil)
	}
	return err
}

// RunSchedule submits a firing's request, or records why not.
func (a *Activities) RunSchedule(ctx context.Context, f ScheduleFiring) error {
	if a.Scheduler == nil {
		return errors.New("this worker runs no schedules")
	}
	return a.Scheduler.RunSchedule(ctx, f)
}

// ScheduleFailed records a firing rvav couldn't submit.
func (a *Activities) ScheduleFailed(ctx context.Context, f ScheduleFiring) error {
	if a.Scheduler == nil {
		return nil
	}
	return a.Scheduler.ScheduleFailed(ctx, f)
}

func temporalSpec(s ScheduleSpec) client.ScheduleSpec {
	if s.Cron != "" {
		return client.ScheduleSpec{CronExpressions: []string{s.Cron}, TimeZoneName: s.Timezone}
	}
	return client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: time.Duration(s.EveryMinutes) * time.Minute}}}
}

// CreateSchedule makes the Temporal schedule that fires a schedule's runs.
// Firings it missed while rvav or Temporal was down are dropped but the
// last of the 15 minutes before.
func (t *Temporal) CreateSchedule(ctx context.Context, id string, s ScheduleSpec) error {
	_, err := t.Client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   ScheduleTemporalID(id),
		Spec: temporalSpec(s),
		Action: &client.ScheduleWorkflowAction{ID: "schedule-" + id, Workflow: ScheduledRun, Args: []any{id},
			TaskQueue: t.TaskQueue, WorkflowRunTimeout: 15 * time.Minute},
		Overlap:       enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		CatchupWindow: 15 * time.Minute,
		Paused:        s.Paused,
		Note:          s.Note,
	})
	var invalid *serviceerror.InvalidArgument
	switch {
	case errors.As(err, &invalid):
		return &InvalidScheduleError{Msg: invalid.Message}
	case errors.Is(err, temporal.ErrScheduleAlreadyRunning):
		// Made by a worker's reconcile while this was being saved.
		return nil
	}
	return err
}

func (t *Temporal) PauseSchedule(ctx context.Context, id, note string) error {
	return t.Client.ScheduleClient().GetHandle(ctx, ScheduleTemporalID(id)).Pause(ctx, client.SchedulePauseOptions{Note: note})
}

func (t *Temporal) ResumeSchedule(ctx context.Context, id string) error {
	return t.Client.ScheduleClient().GetHandle(ctx, ScheduleTemporalID(id)).Unpause(ctx, client.ScheduleUnpauseOptions{})
}

// DeleteSchedule deletes the Temporal schedule; one gone already is fine.
func (t *Temporal) DeleteSchedule(ctx context.Context, id string) error {
	err := t.Client.ScheduleClient().GetHandle(ctx, ScheduleTemporalID(id)).Delete(ctx)
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return nil
	}
	return err
}

// ListSchedules gives rvav's schedules Temporal has, by rvav's id, and
// whether each is paused.
func (t *Temporal) ListSchedules(ctx context.Context) (map[string]bool, error) {
	it, err := t.Client.ScheduleClient().List(ctx, client.ScheduleListOptions{})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for it.HasNext() {
		e, err := it.Next()
		if err != nil {
			return nil, err
		}
		if id, ok := strings.CutPrefix(e.ID, scheduleIDPrefix); ok {
			out[id] = e.Paused
		}
	}
	return out, nil
}

// NextRuns are the next times Temporal fires a schedule.
func (t *Temporal) NextRuns(ctx context.Context, id string) ([]time.Time, error) {
	d, err := t.Client.ScheduleClient().GetHandle(ctx, ScheduleTemporalID(id)).Describe(ctx)
	if err != nil {
		return nil, err
	}
	if d.Schedule.State != nil && d.Schedule.State.Paused {
		return nil, nil
	}
	return d.Info.NextActionTimes, nil
}
