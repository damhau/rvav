package provision

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

// TestReplayHistories replays recorded workflow histories against the
// workflow code as it is now (S14.4, docs/operations/upgrades.md): a change
// that would make a workflow in flight take another path than the one it
// took fails here, instead of failing requests after an upgrade. Each
// history in testdata/histories is a request rvav ran at 1.0: provisioning
// (one VM, approved, cancelled while waiting, failed), the PostgreSQL
// composite, day-2 actions, housekeeping; then NSX's: a VM on its own
// segment in security groups, and a composite whose VM step is (N1, N2). A change to workflow code goes
// behind workflow.GetVersion until no workflow in flight can predate it.
func TestReplayHistories(t *testing.T) {
	files, err := filepath.Glob("testdata/histories/*.json")
	if err != nil || len(files) < 10 {
		t.Fatalf("histories: %v %v", files, err)
	}
	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".json"), func(t *testing.T) {
			// As the worker runs them: with the tracing interceptor (S14.5).
			ti, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			r, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
				Interceptors: []interceptor.WorkerInterceptor{ti}})
			if err != nil {
				t.Fatal(err)
			}
			r.RegisterWorkflow(Provision)
			r.RegisterWorkflow(Housekeeping)
			r.RegisterWorkflow(RollbackWorkflow)
			logger := sdklog.NewStructuredLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err := r.ReplayWorkflowHistoryFromJSONFile(logger, f); err != nil {
				t.Fatal(err)
			}
		})
	}
}
