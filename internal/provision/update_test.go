package provision

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
)

func updatePlan() Info {
	return Info{RequestID: "req_4", ProjectID: "prj_web", Status: "queued", Plan: Plan{Kind: blueprint.KindVM, Update: &Update{
		DeploymentID: "dep_1", Before: "ready",
		Ops:    []day2.Op{{Key: "vsphere.reconfigure@app01", Title: "Resize · app01", Op: "reconfigure", Args: map[string]any{"moref": "vm-1"}}},
		Remove: []day2.Op{{Key: "vsphere.destroy@app02", Title: "Delete VM app02", Op: "destroy_vm"}},
		Kept:   []KeptVM{{Component: blueprint.SingleKey, Name: "app01", IP: "10.0.0.11", Moref: "vm-1"}},
		Jobs:   []UpdateJobs{{Component: blueprint.SingleKey, Jobs: []Job{{Key: "monitoring", Template: "monitoring-agent"}}}},
	}}}
}

// An update changes the VMs it keeps, removes those it scales in, then
// runs the jobs the new version adds on those it keeps.
func TestUpdateRunsInOrder(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	info := updatePlan()
	var ran []string
	env.OnActivity(a.LoadPlan, mock.Anything, "req_4").Return(info, nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_4").Return(true, nil)
	env.OnActivity(a.RunOp, mock.Anything, mock.MatchedBy(func(in OpInput) bool { return in.DeploymentID == "dep_1" })).
		Return(func(_ context.Context, in OpInput) error { ran = append(ran, in.Op.Key); return nil })
	env.OnActivity(a.RefreshInventoryStep, mock.Anything, "req_4", "prj_web", "awx.inventory@vm").
		Run(func(mock.Arguments) { ran = append(ran, "inventory") }).Return(nil)
	env.OnActivity(a.RunJob, mock.Anything, mock.MatchedBy(func(in JobInput) bool {
		return in.StepKey == "awx.job:monitoring@vm" && in.Limit == "app01"
	})).Run(func(mock.Arguments) { ran = append(ran, "job") }).Return(nil)
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_4", ProjectID: "prj_web", Outcome: "succeeded"}).Return(nil)
	env.ExecuteWorkflow(Provision, "req_4")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"vsphere.reconfigure@app01", "vsphere.destroy@app02", "inventory", "job"}; !slices.Equal(ran, want) {
		t.Fatalf("ran %v, want %v", ran, want)
	}
	var keys []string
	for _, s := range info.Plan.Steps("Development") {
		keys = append(keys, s.Key)
	}
	want := []string{KeySubmit, KeyPolicy, "vsphere.reconfigure@app01", "vsphere.destroy@app02", "awx.inventory@vm", "awx.job:monitoring@vm", KeyReady}
	if !slices.Equal(keys, want) {
		t.Fatalf("steps %v, want %v", keys, want)
	}
}

// A failed deployment stays failed when its update fails too; it is ready
// once one succeeds.
func TestUpdateStatus(t *testing.T) {
	p := updatePlan().Plan
	p.Update.Before = "failed"
	if got := day2Status(p, "failed", true); got != "failed" {
		t.Fatalf("failed update of a failed deployment: %s", got)
	}
	if got := day2Status(p, "succeeded", true); got != "ready" {
		t.Fatalf("update succeeded: %s", got)
	}
	p.Update.Before = "ready"
	if got := day2Status(p, "failed", true); got != "ready" {
		t.Fatalf("failed update of a ready deployment: %s", got)
	}
}
