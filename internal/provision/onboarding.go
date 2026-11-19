package provision

import (
	"context"
	"errors"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Onboarder runs onboarding plans (core.Service, X1.5).
type Onboarder interface {
	RunOnboarding(ctx context.Context, runID string, beat func()) error
	FailOnboardingRun(ctx context.Context, runID, msg string) error
	StartDueOnboardingPlans(ctx context.Context, now time.Time) ([]string, error)
}

// OnboardingWorkflowID is the workflow ID of a plan's run.
func OnboardingWorkflowID(runID string) string { return "onboarding-" + runID }

// OnboardingRun runs a plan's run: one activity adopts what the plan
// matches, VM by VM, going on where it was when retried. When it can't,
// the run ends failed with why; what it adopted stays.
func OnboardingRun(ctx workflow.Context, runID string) error {
	var a *Activities
	run := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Hour,
		HeartbeatTimeout:    5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	err := workflow.ExecuteActivity(run, a.RunOnboardingPlan, runID).Get(run, nil)
	if err == nil {
		return nil
	}
	msg := err.Error()
	var ae *temporal.ApplicationError
	if errors.As(err, &ae) {
		msg = ae.Error()
	}
	fail := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 10},
	})
	return workflow.ExecuteActivity(fail, a.FailOnboardingRun, runID, msg).Get(fail, nil)
}

// RunOnboardingPlan runs a plan's run.
func (a *Activities) RunOnboardingPlan(ctx context.Context, runID string) error {
	if a.Onboarder == nil {
		return temporal.NewNonRetryableApplicationError("this worker runs no onboarding plans", "setup", nil)
	}
	return a.Onboarder.RunOnboarding(ctx, runID, func() { activity.RecordHeartbeat(ctx) })
}

// FailOnboardingRun ends a run that couldn't go on.
func (a *Activities) FailOnboardingRun(ctx context.Context, runID, msg string) error {
	if a.Onboarder == nil {
		return nil
	}
	return a.Onboarder.FailOnboardingRun(ctx, runID, msg)
}

// StartOnboardingPlans starts the runs of the plans whose schedule is due.
func (a *Activities) StartOnboardingPlans(ctx context.Context) (int, error) {
	if a.Onboarder == nil {
		return 0, nil
	}
	started, err := a.Onboarder.StartDueOnboardingPlans(ctx, a.now())
	if len(started) > 0 {
		a.Log.Info("onboarding plans started", "runs", len(started))
	}
	return len(started), err
}
