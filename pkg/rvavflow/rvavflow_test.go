package rvavflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func sample(ctx workflow.Context, rc Context) (map[string]any, error) {
	if err := Step(ctx, rc, "one", "Step one", func() error { return AppendLog(ctx, rc, "one", "a", "b") }); err != nil {
		return nil, err
	}
	if err := Step(ctx, rc, "two", "Step two", func() error { return errors.New("no disk") }); err == nil {
		return nil, errors.New("step two should have failed")
	}
	d, err := RequestApproval(ctx, rc, Approval{Title: "Go on?", Group: "leads", Wait: 48 * time.Hour})
	if err != nil {
		return nil, err
	}
	return map[string]any{"approved": d.Approved, "timed_out": d.TimedOut, "comment": d.Comment, "app": rc.Inputs["app"]}, nil
}

func run(t *testing.T, signal *map[string]any) (map[string]any, []string) {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	var calls []string
	env.RegisterActivityWithOptions(func(_ context.Context, in map[string]any) error {
		calls = append(calls, in["key"].(string)+"="+in["status"].(string))
		return nil
	}, activity.RegisterOptions{Name: "rvav.ReportStep"})
	env.RegisterActivityWithOptions(func(_ context.Context, in map[string]any) error {
		calls = append(calls, "log")
		return nil
	}, activity.RegisterOptions{Name: "rvav.AppendLog"})
	env.RegisterActivityWithOptions(func(_ context.Context, in map[string]any) (map[string]string, error) {
		calls = append(calls, "approval "+in["group"].(string))
		return map[string]string{"approval_id": "apr_1"}, nil
	}, activity.RegisterOptions{Name: "rvav.RequestApproval"})
	if signal != nil {
		env.RegisterDelayedCallback(func() { env.SignalWorkflow(Signal, *signal) }, time.Hour)
	}
	env.ExecuteWorkflow(sample, Context{Contract: Contract, TaskQueue: "rvav", Inputs: map[string]any{"app": "billing"}})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := env.GetWorkflowResult(&out); err != nil {
		t.Fatal(err)
	}
	return out, calls
}

func TestStepsLogsAndApproval(t *testing.T) {
	out, calls := run(t, &map[string]any{"approval_id": "apr_1", "outcome": "approved", "comment": "Fine"})
	if out["approved"] != true || out["comment"] != "Fine" || out["app"] != "billing" {
		t.Fatalf("result = %v", out)
	}
	want := []string{"one=running", "log", "one=done", "two=running", "two=failed", "approval leads"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v", calls)
		}
	}
}

func TestApprovalWaitTimesOut(t *testing.T) {
	out, _ := run(t, nil)
	if out["timed_out"] != true || out["approved"] != false {
		t.Fatalf("result = %v", out)
	}
}
