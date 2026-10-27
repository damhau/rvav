package provision

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
)

func vmPlan(approval bool, levels ...string) Info {
	info := Info{RequestID: "req_1", Number: "REQ-1", ProjectID: "prj_web", Status: map[bool]string{true: "pending_approval", false: "queued"}[approval],
		Plan: Plan{Kind: blueprint.KindVM, ApprovalRequired: approval,
			VMs:        []VMUnit{{DeploymentID: "dep_1", Name: "web-dev-app001", Subnet: "10.31.10.0/24", MemoryGB: 48}},
			PostConfig: []Job{{Key: "linux-baseline", Template: "linux-baseline"}}}}
	if approval && len(levels) == 0 {
		levels = []string{"apr_1"}
	}
	info.Approvals = levels
	return info
}

// approved is the plan after the approval, as LoadPlan returns it again.
func approved(memoryGB int) Info {
	info := vmPlan(false)
	info.Plan.VMs[0].MemoryGB = memoryGB
	return info
}

func signal(env *testsuite.TestWorkflowEnvironment, id, outcome string, after time.Duration) {
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApproval, ApprovalSignal{ApprovalID: id, Outcome: outcome})
	}, after)
}

func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})
	t.Cleanup(func() { env.AssertExpectations(t) })
	return env
}

func TestProvisionRunsStepsInOrder(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	var order []string
	track := func(name string) func(args mock.Arguments) {
		return func(mock.Arguments) { order = append(order, name) }
	}
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(false), nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.ReserveIP, mock.Anything, mock.Anything).Run(track("ip")).Return("10.31.10.20", nil)
	env.OnActivity(a.CreateDNS, mock.Anything, mock.MatchedBy(func(in VMInput) bool { return in.IP == "10.31.10.20" })).Run(track("dns")).Return(nil)
	env.OnActivity(a.CloneVM, mock.Anything, mock.Anything).Run(track("clone")).Return("vm-42", nil)
	env.OnActivity(a.Customize, mock.Anything, mock.MatchedBy(func(in VMInput) bool { return in.Moref == "vm-42" })).Run(track("customize")).Return(nil)
	env.OnActivity(a.PowerOn, mock.Anything, mock.Anything).Run(track("power")).Return(nil)
	env.OnActivity(a.RefreshInventory, mock.Anything, "req_1", "prj_web").Run(track("inventory")).Return(nil)
	env.OnActivity(a.RunJob, mock.Anything, mock.MatchedBy(func(in JobInput) bool {
		rvav, _ := in.ExtraVars["rvav"].(map[string]any)
		return in.StepKey == "awx.job:linux-baseline" && in.Limit == "web-dev-app001" && rvav != nil
	})).Run(track("job")).Return(nil)
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_1", ProjectID: "prj_web", Outcome: "succeeded"}).Return(nil)

	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	want := []string{"ip", "dns", "clone", "customize", "power", "inventory", "job"}
	if len(order) != len(want) {
		t.Fatalf("order = %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestProvisionRejected(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(true), nil)
	env.OnActivity(a.OpenApproval, mock.Anything, "apr_1").Return(LevelState{Status: "pending"}, nil)
	env.OnActivity(a.ApprovalState, mock.Anything, "apr_1").Return(LevelState{Status: "pending"}, nil).Maybe()
	env.OnActivity(a.FinishApprovals, mock.Anything, "req_1").Return(false, nil)
	signal(env, "apr_1", "rejected", 2*time.Hour)
	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

// After approval the workflow loads the plan again: an approver may have
// lowered the RAM.
func TestProvisionApprovedUsesTheNewPlan(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(true), nil).Once()
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(approved(32), nil).Once()
	env.OnActivity(a.OpenApproval, mock.Anything, "apr_1").Return(LevelState{Status: "pending"}, nil)
	env.OnActivity(a.ApprovalState, mock.Anything, "apr_1").Return(LevelState{Status: "pending"}, nil).Maybe()
	env.OnActivity(a.FinishApprovals, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.ReserveIP, mock.Anything, mock.Anything).Return("10.31.10.20", nil)
	env.OnActivity(a.CreateDNS, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.CloneVM, mock.Anything, mock.MatchedBy(func(in VMInput) bool { return in.VM.MemoryGB == 32 })).Return("vm-42", nil)
	env.OnActivity(a.Customize, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.PowerOn, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RefreshInventory, mock.Anything, "req_1", "prj_web").Return(nil)
	env.OnActivity(a.RunJob, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_1", ProjectID: "prj_web", Outcome: "succeeded"}).Return(nil)
	signal(env, "apr_1", "approved", time.Hour)
	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

// Levels run one after the other; a signal for another level is ignored.
func TestProvisionTwoLevels(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	var opened []string
	openedAt := map[string]time.Time{}
	track := func(args mock.Arguments) {
		opened = append(opened, args.String(1))
		openedAt[args.String(1)] = env.Now()
	}
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(true, "apr_1", "apr_2"), nil).Once()
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(approved(48), nil).Once()
	env.OnActivity(a.OpenApproval, mock.Anything, mock.Anything).Run(track).Return(LevelState{Status: "pending"}, nil)
	env.OnActivity(a.ApprovalState, mock.Anything, mock.Anything).Return(LevelState{Status: "pending"}, nil).Maybe()
	env.OnActivity(a.FinishApprovals, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_1").Return(false, nil)
	signal(env, "apr_2", "approved", 30*time.Minute) // not open yet: ignored
	signal(env, "apr_1", "approved", time.Hour)
	signal(env, "apr_2", "approved", 3*time.Hour)
	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 || opened[0] != "apr_1" || opened[1] != "apr_2" {
		t.Fatalf("opened = %v", opened)
	}
	// The signal for level 2 at 30 minutes did not decide level 1.
	if d := openedAt["apr_2"].Sub(openedAt["apr_1"]); d < time.Hour {
		t.Fatalf("level 2 opened %s after level 1, before level 1 was approved", d)
	}
}

// No answer: the level escalates once, then the request is rejected.
func TestProvisionTimeoutEscalatesThenRejects(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	start := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	env.SetStartTime(start)
	due1, due2 := start.Add(48*time.Hour), start.Add(96*time.Hour)
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(true), nil)
	// The level as the database holds it.
	level := LevelState{Status: "pending", DueAt: &due1}
	env.OnActivity(a.OpenApproval, mock.Anything, "apr_1").Return(level, nil)
	env.OnActivity(a.ApprovalState, mock.Anything, "apr_1").Return(func(context.Context, string) (LevelState, error) {
		return level, nil
	}).Maybe()
	env.OnActivity(a.ApprovalTimedOut, mock.Anything, "apr_1").Return(func(context.Context, string) (LevelState, error) {
		if *level.DueAt == due1 {
			level.DueAt = &due2 // escalated
		} else {
			level = LevelState{Status: "rejected"}
		}
		return level, nil
	}).Times(2)
	env.OnActivity(a.FinishApprovals, mock.Anything, "req_1").Return(false, nil)
	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if now := env.Now(); now.Before(due2) {
		t.Fatalf("rejected at %s, before the escalation timed out (%s)", now, due2)
	}
}

// A lost signal is caught when the level is read again.
func TestProvisionFindsADecisionWithoutItsSignal(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(true), nil).Once()
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(approved(48), nil).Once()
	env.OnActivity(a.OpenApproval, mock.Anything, "apr_1").Return(LevelState{Status: "pending"}, nil)
	env.OnActivity(a.ApprovalState, mock.Anything, "apr_1").Return(LevelState{Status: "approved"}, nil).Once()
	env.OnActivity(a.FinishApprovals, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_1").Return(false, nil)
	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionFailureRecordsStep(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(false), nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.ReserveIP, mock.Anything, mock.Anything).Return("", temporal.NewNonRetryableApplicationError("Subnet 10.31.10.0/24 has no free address", "Permanent", nil))
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_1", ProjectID: "prj_web", Outcome: "failed",
		StepKey: KeyIPAM, Message: "Subnet 10.31.10.0/24 has no free address"}).Return(nil)
	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

// A transient error (vCenter or EfficientIP briefly unreachable) is retried,
// and the request carries on.
func TestProvisionRetriesTransientErrors(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(false), nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.ReserveIP, mock.Anything, mock.Anything).Return("", errors.New("dial tcp: connection refused")).Times(2)
	env.OnActivity(a.ReserveIP, mock.Anything, mock.Anything).Return("10.31.10.20", nil).Once()
	env.OnActivity(a.CreateDNS, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.CloneVM, mock.Anything, mock.Anything).Return("vm-42", nil)
	env.OnActivity(a.Customize, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.PowerOn, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RefreshInventory, mock.Anything, "req_1", "prj_web").Return(nil)
	env.OnActivity(a.RunJob, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_1", ProjectID: "prj_web", Outcome: "succeeded"}).Return(nil)
	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

// After the last attempt, a transient error fails the request on its step.
func TestProvisionGivesUpAfterRetries(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(false), nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.ReserveIP, mock.Anything, mock.Anything).Return("", errors.New("dial tcp: connection refused")).Times(int(activityOptions.RetryPolicy.MaximumAttempts))
	env.OnActivity(a.Finish, mock.Anything, mock.MatchedBy(func(in FinishInput) bool {
		return in.Outcome == "failed" && in.StepKey == KeyIPAM
	})).Return(nil)
	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionCancelledWhileWaitingForApproval(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(true), nil)
	env.OnActivity(a.OpenApproval, mock.Anything, "apr_1").Return(LevelState{Status: "pending"}, nil)
	env.OnActivity(a.ApprovalState, mock.Anything, "apr_1").Return(LevelState{Status: "pending"}, nil).Maybe()
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_1", ProjectID: "prj_web", Outcome: "cancelled"}).Return(nil)
	env.RegisterDelayedCallback(env.CancelWorkflow, time.Hour)
	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil && !temporal.IsCanceledError(err) {
		t.Fatal(err)
	}
}

func TestProvisionAWXItem(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	info := Info{RequestID: "req_2", ProjectID: "prj_data", Status: "queued", Plan: Plan{Kind: blueprint.KindAWX,
		AWX: &Job{Key: "pg-create-role", Template: "pg-create-role"}, ExtraVars: map[string]any{"role_name": "app_reporting"},
		Context: map[string]any{"request_number": "REQ-2"}}}
	env.OnActivity(a.LoadPlan, mock.Anything, "req_2").Return(info, nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_2").Return(true, nil)
	env.OnActivity(a.RunJob, mock.Anything, mock.MatchedBy(func(in JobInput) bool {
		return in.ExtraVars["role_name"] == "app_reporting" && in.ExtraVars["rvav"] != nil && in.Limit == ""
	})).Return(nil)
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_2", ProjectID: "prj_data", Outcome: "succeeded"}).Return(nil)
	env.ExecuteWorkflow(Provision, "req_2")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func TestPlanSteps(t *testing.T) {
	p := vmPlan(true).Plan
	steps := p.Steps("Production")
	keys := []string{KeySubmit, KeyPolicy, KeyIPAM, KeyDNS, KeyClone, KeyCustomize, KeyPowerOn, KeyInventory, KeyJob + "linux-baseline", KeyReady}
	if len(steps) != len(keys) {
		t.Fatalf("steps = %+v", steps)
	}
	for i, k := range keys {
		if steps[i].Key != k {
			t.Fatalf("step %d = %s, want %s", i, steps[i].Key, k)
		}
	}
	if steps[1].Status != "running" || steps[1].Title != "Waiting for approval" {
		t.Fatalf("policy step = %+v", steps[1])
	}
	p.VMs = append(p.VMs, VMUnit{Name: "web-dev-app002"})
	if got := p.Steps("Development")[2].Key; got != KeyIPAM+"@web-dev-app001" {
		t.Fatalf("multi-VM key = %s", got)
	}
	if p.Limit() != "web-dev-app001,web-dev-app002" {
		t.Fatalf("limit = %s", p.Limit())
	}
}

func TestErrMessage(t *testing.T) {
	if got := errMessage(temporal.NewNonRetryableApplicationError("boom", "Permanent", nil)); got != "boom" {
		t.Fatalf("errMessage = %q", got)
	}
	if got := errMessage(errors.New("plain")); got != "plain" {
		t.Fatalf("errMessage = %q", got)
	}
}

func TestDay2RunsOpsThenFinishes(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	info := Info{RequestID: "req_2", ProjectID: "prj_web", Status: "queued", Plan: Plan{Kind: "day2", Day2: &Day2{
		Action: "resize", DeploymentID: "dep_1", ResourceID: "res_1", Moref: "vm-42", After: "ready",
		Ops: []day2.Op{{Key: "vsphere.shutdown_guest", Op: "shutdown_guest"}, {Key: "vsphere.reconfigure", Op: "reconfigure"}, {Key: "vsphere.power_on", Op: "power_on"}},
	}}}
	var ran []string
	env.OnActivity(a.LoadPlan, mock.Anything, "req_2").Return(info, nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_2").Return(true, nil)
	env.OnActivity(a.RunOp, mock.Anything, mock.MatchedBy(func(in OpInput) bool { return in.Moref == "vm-42" && in.ResourceID == "res_1" })).
		Run(func(args mock.Arguments) { ran = append(ran, args.Get(1).(OpInput).Op.Op) }).Return(nil)
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_2", ProjectID: "prj_web", Outcome: "succeeded"}).Return(nil)
	env.ExecuteWorkflow(Provision, "req_2")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 3 || ran[0] != "shutdown_guest" || ran[2] != "power_on" {
		t.Fatalf("ops = %v", ran)
	}
	if steps := info.Plan.Steps("Development"); len(steps) != 6 || steps[5].Title != "Done" {
		t.Fatalf("steps = %+v", steps)
	}
}

type fakeHousekeeper struct{ calls int }

func (f *fakeHousekeeper) ExpireSnapshots(context.Context, time.Time) ([]string, error) {
	f.calls++
	return []string{"req_9"}, nil
}

func (f *fakeHousekeeper) ExpireLeases(context.Context, time.Time) ([]string, error) {
	f.calls++
	return nil, errors.New("database unavailable")
}

func TestHousekeepingExpiresSnapshots(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	fake := &fakeHousekeeper{}
	env.RegisterActivity(&Activities{Housekeeper: fake, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	env.ExecuteWorkflow(Housekeeping)
	// Leases failing (three attempts) does not stop snapshots.
	if err := env.GetWorkflowError(); err == nil || fake.calls != 4 {
		t.Fatalf("housekeeping: %v, %d calls", err, fake.calls)
	}
}
