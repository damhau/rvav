package provision

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/blueprint"
)

func vmPlan(approval bool) Info {
	return Info{RequestID: "req_1", Number: "REQ-1", ProjectID: "prj_web", Status: map[bool]string{true: "pending_approval", false: "queued"}[approval],
		Plan: Plan{Kind: blueprint.KindVM, ApprovalRequired: approval,
			VMs:        []VMUnit{{DeploymentID: "dep_1", Name: "web-dev-app001", Subnet: "10.31.10.0/24"}},
			PostConfig: []Job{{Key: "linux-baseline", Template: "linux-baseline"}}}}
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
	env.OnActivity(a.RecordDecision, mock.Anything, "req_1", Decision{Approved: false, By: "usr_mkeller", Comment: "Use dev"}).Return(nil)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalDecision, Decision{Approved: false, By: "usr_mkeller", Comment: "Use dev"})
	}, 2*time.Hour)
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

func TestProvisionCancelledWhileWaitingForApproval(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(vmPlan(true), nil)
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
