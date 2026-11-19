package e2e

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/provision"
)

// TestCompositeSignalThroughTemporal: a composite step's wait for a signal
// on the Temporal dev server (ADR-0057). It continues as new while it
// waits, and a signal sent then, to a worker started again, is taken; a wait for an event takes the one
// its condition picks, through the dispatcher; a loop and a catch path run
// as on the mocked clock.
func TestCompositeSignalThroughTemporal(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t, "--dynamic-config-value", "limit.historyCount.suggestContinueAsNew=5")
	recheck := provision.WaitRecheck
	provision.WaitRecheck = time.Second
	t.Cleanup(func() { provision.WaitRecheck = recheck })

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester, queue = "mkeller", "tnguyen", "rvav-signal-e2e"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		change := newChangeAPI(t)
		h.svc.REST.Hosts = []string{change.URL}
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { w.Stop() }()

		runOf := func(id string) string {
			t.Helper()
			desc, err := tc.DescribeWorkflowExecution(ctx, provision.WorkflowID(id), "")
			if err != nil {
				t.Fatal(err)
			}
			return desc.WorkflowExecutionInfo.Execution.RunId
		}
		until := func(what string, ok func() bool) {
			t.Helper()
			deadline := time.Now().Add(45 * time.Second)
			for !ok() {
				if time.Now().After(deadline) {
					t.Fatal(what)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}

		// By its name, after the wait has continued as new and the worker
		// restarted; a loop and a caught failure before it.
		change.script(changeAnswer{500, map[string]any{}})
		named := rig.publishDefinition("signal-named", askDefinition(false,
			loopOf(posting(change, "each", "{'host': each}"), "['a', 'b']", 2),
			caught(failing(change, "check"), messageOf("told", map[string]any{"requester": true, "message": "${error.message}"})),
			// Due after WaitRecheck's hour, as TestReplayHistories reads it.
			signalOf("go", map[string]any{"name": "go", "timeout": "2h"}),
			posting(change, "after", "{'by': steps.go.by, 'caught': steps.check.caught}"),
		))
		req := rig.submit(requester, named)
		until("the wait didn't begin", func() bool { return rig.steps(req.Id, requester)["step:go"].DueAt != nil })
		first := runOf(req.Id)
		until("the wait didn't continue as new", func() bool { return runOf(req.Id) != first })
		w.Stop()
		w = provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		var receipt api.SignalReceipt
		h.json("POST", "/api/v1/requests/"+req.Id+"/signal/go", admin, map[string]any{"by": "the board"}, 202, &receipt)
		h.waitStatus(req.Id, requester, api.RequestStatusSucceeded)
		got := change.got()
		if len(got) != 3 || got[2]["by"] != "the board" || got[2]["caught"] != true {
			t.Fatalf("what ran = %v", got)
		}
		if s := rig.steps(req.Id, requester)["step:check"]; s.Status != api.StepStatusFailed {
			t.Fatalf("the caught step = %+v", s)
		}
		recordRun(t, ctx, tc, provision.WorkflowID(req.Id), first, "composite-flow")
		recordHistory(t, ctx, tc, req.Id, "composite-signal")

		// By an event: another request of an item, picked by its condition,
		// in its project only.
		change.script()
		target := rig.publishDefinition("signal-target", askDefinition(false, posting(change, "target", "{'target': true}")))
		waiter := rig.publishDefinition("signal-event", askDefinition(false,
			signalOf("ev", map[string]any{"event": "request.submitted", "timeout": "10m",
				"when": "item.slug == 'signal-target' && request.id != waiting.request.id"}),
			posting(change, "after", "{'number': steps.ev.request.number}"),
		))
		req = rig.submit(requester, waiter)
		until("the event wait didn't begin", func() bool { return rig.steps(req.Id, requester)["step:ev"].DueAt != nil })
		disp := &dispatch.Dispatcher{Store: h.store, Log: slog.New(slog.DiscardHandler),
			Signal: func(ctx context.Context, workflowID, stepKey string) error {
				return tc.SignalWorkflow(ctx, workflowID, "", provision.SignalStep(stepKey), nil)
			}}
		elsewhere := target
		elsewhere.ProjectId = "prj_data"
		away := rig.submit("sbrunner", elsewhere)
		h.waitStatus(away.Id, "sbrunner", api.RequestStatusSucceeded)
		if _, err := disp.Once(ctx); err != nil {
			t.Fatal(err)
		}
		if n := rig.count(`select count(*) from signal_waits where request_id = $1 and received_at is not null`, req.Id); n != 0 {
			t.Fatal("another project's event was taken")
		}
		other := rig.submit(requester, target)
		h.waitStatus(other.Id, requester, api.RequestStatusSucceeded)
		until("the event wasn't dispatched", func() bool {
			if _, err := disp.Once(ctx); err != nil {
				t.Fatal(err)
			}
			return rig.count(`select count(*) from signal_waits where request_id = $1 and received_at is not null`, req.Id) == 1
		})
		h.waitStatus(req.Id, requester, api.RequestStatusSucceeded)
		got = change.got()
		if len(got) != 3 || got[2]["number"] != other.Number {
			t.Fatalf("after the event = %v (the other is %s)", got, other.Number)
		}
	})
}
