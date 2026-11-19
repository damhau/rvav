package e2e

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/pkg/rvavflow"
)

// scheduleFake stands in for Temporal's schedules.
type scheduleFake struct {
	mu     sync.Mutex
	specs  map[string]provision.ScheduleSpec
	paused map[string]string
}

func (f *scheduleFake) CreateSchedule(_ context.Context, id string, s provision.ScheduleSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs[id] = s
	return nil
}

func (f *scheduleFake) PauseSchedule(_ context.Context, id, note string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused[id] = note
	return nil
}

func (f *scheduleFake) ResumeSchedule(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.paused, id)
	return nil
}

func (f *scheduleFake) DeleteSchedule(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.specs, id)
	return nil
}

func (f *scheduleFake) NextRuns(_ context.Context, id string) ([]time.Time, error) {
	return []time.Time{time.Date(2030, 1, 1, 2, 0, 0, 0, time.UTC)}, nil
}

// cleanup is the custom workflow a scheduled item runs.
func cleanup(_ workflow.Context, rc rvavflow.Context) (map[string]any, error) {
	return map[string]any{"cleaned": rc.Inputs["target"]}, nil
}

// cleanupItem is a workflow item with a target and a secret token.
func cleanupItem(h *harness) api.AdminCatalogItem {
	h.t.Helper()
	const admin = "mkeller"
	h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new("cleanup"), Title: "Clean up",
		WorkflowType: "cleanup.v1", TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageCatalogItem}}, 201, nil)
	def := api.CatalogVersionDefinition{
		FormSchema: map[string]any{"type": "object", "required": []string{"target"}, "properties": map[string]any{
			"target": map[string]any{"type": "string", "minLength": 1}, "token": map[string]any{"type": "string"}}},
		UiSchema: api.UiSchema{Fields: map[string]api.UiField{"target": {Widget: api.UiWidgetText, Label: "Target"},
			"token": {Widget: api.UiWidgetSecret, Label: "Token"}}},
		InitialValues: map[string]any{},
		Blueprint:     map[string]any{"kind": "workflow", "workflow": "cleanup"},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Clean up", Fields: []string{"target", "token"}}}
	var item api.AdminCatalogItem
	h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "cleanup", Name: "Clean up",
		Summary: "Removes what is old.", Category: "compute", Type: api.Workflow, Definition: &def}, 201, &item)
	h.json("POST", "/api/v1/admin/catalog/items/cleanup/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
	h.json("PUT", "/api/v1/admin/catalog/items/cleanup/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
	return item
}

func hourly(item api.AdminCatalogItem, inputs map[string]any) api.ScheduleWrite {
	return api.ScheduleWrite{Name: "Hourly clean-up", CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
		Environment: "dev", Inputs: inputs, EveryMinutes: new(60)}
}

// TestSchedules: a recurring schedule of a workflow item runs as its
// owner's request, once per firing; a run is skipped while the one before
// hasn't finished, refused as a request would be, and kept with why. Its
// owner or a project admin pauses, resumes and deletes it; the requests it
// started still name it (E1.5, ADR-0057).
func TestSchedules(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		fake := &scheduleFake{specs: map[string]provision.ScheduleSpec{}, paused: map[string]string{}}
		h.svc.ScheduleControl = fake
		const owner, admin, outsider = "tnguyen", "lmartin", "sbrunner"
		item := cleanupItem(h)
		var problem struct{ Errors []api.FieldError }

		refused := func(w api.ScheduleWrite, field, code string) {
			t.Helper()
			h.json("POST", "/api/v1/schedules", owner, w, 422, &problem)
			for _, e := range problem.Errors {
				if e.Field == field && deref(e.Code) == code {
					return
				}
			}
			t.Fatalf("%+v refused with %+v, not %s %s", w, problem.Errors, field, code)
		}
		w := hourly(item, map[string]any{"target": "/tmp"})
		w.EveryMinutes = nil
		refused(w, "/cron", "required")
		w.Cron = new("0 2 * *")
		refused(w, "/cron", "pattern")
		w.Cron, w.Timezone = new("0 2 * * 1-5"), new("Mars/Olympus")
		refused(w, "/timezone", "unknown")
		refused(hourly(item, map[string]any{"target": "/tmp", "token": "s3cret"}), "/inputs/token", "secret_input")
		refused(hourly(item, map[string]any{}), "/inputs/target", "required")
		short := hourly(item, map[string]any{"target": "/tmp"})
		short.EveryMinutes = new(2)
		refused(short, "/every_minutes", "out_of_range")
		var vm api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", owner, nil, 200, &vm)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", owner, nil, 200, &form)
		refused(api.ScheduleWrite{Name: "Nightly VM", CatalogItemId: vm.Id, VersionId: form.VersionId, ProjectId: "prj_web",
			Environment: "dev", EveryMinutes: new(60), Inputs: map[string]any{"size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "role": "app", "post_config": []string{"linux-baseline"}}},
			"/catalog_item_id", "makes_deployments")

		var sc api.Schedule
		h.json("POST", "/api/v1/schedules", owner, hourly(item, map[string]any{"target": "/tmp"}), 201, &sc)
		if spec := fake.specs[sc.Id]; spec.EveryMinutes != 60 || spec.Cron != "" || sc.Owner.Id != seed.UserID(owner) ||
			len(sc.NextRuns) != 1 || sc.Paused || sc.LastRun != nil || sc.Timezone != "UTC" {
			t.Fatalf("the schedule = %+v, its spec %+v", sc, spec)
		}
		var list api.ScheduleList
		h.json("GET", "/api/v1/schedules", admin, nil, 200, &list)
		if len(list.Items) != 1 {
			t.Fatalf("the project admin's schedules = %+v", list.Items)
		}
		h.json("GET", "/api/v1/schedules", outsider, nil, 200, &list)
		if len(list.Items) != 0 {
			t.Fatalf("an outsider's schedules = %+v", list.Items)
		}
		h.json("GET", "/api/v1/schedules/"+sc.Id, outsider, nil, 404, nil)
		h.json("POST", "/api/v1/schedules/"+sc.Id+"/pause", outsider, nil, 404, nil)

		fire := func(id string) {
			t.Helper()
			if err := h.svc.RunSchedule(ctx, provision.ScheduleFiring{ScheduleID: sc.Id, FireID: id, At: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
		}
		runs := func() []api.ScheduleRun {
			t.Helper()
			var got api.Schedule
			h.json("GET", "/api/v1/schedules/"+sc.Id, owner, nil, 200, &got)
			return *got.Runs
		}

		// A firing runs once, tried again or not, as its owner's request.
		fire("f1")
		fire("f1")
		first := runs()
		if len(first) != 1 || first[0].Outcome != api.ScheduleRunOutcomeStarted || first[0].Request == nil {
			t.Fatalf("the runs = %+v", first)
		}
		var req api.Request
		h.json("GET", "/api/v1/requests/"+first[0].Request.Id, owner, nil, 200, &req)
		if req.Schedule == nil || req.Schedule.Id != sc.Id || req.Schedule.Name != "Hourly clean-up" || req.Requester.Id != seed.UserID(owner) ||
			req.Status != api.RequestStatusQueued {
			t.Fatalf("the scheduled request = %+v", req)
		}

		// The next is skipped until it finishes.
		fire("f2")
		if got := runs(); got[0].Outcome != api.ScheduleRunOutcomeSkipped || deref(got[0].Reason) != req.Number+" hadn't finished." {
			t.Fatalf("a firing while it runs = %+v", got[0])
		}
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterWorkflowWithOptions(cleanup, workflow.RegisterOptions{Name: "cleanup.v1"})
		})
		h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
		fire("f3")
		if got := runs(); len(got) != 3 || got[0].Outcome != api.ScheduleRunOutcomeStarted || got[0].Request.Id == req.Id {
			t.Fatalf("the runs = %+v", got)
		}
		second := runs()[0].Request
		h.runWorkflow(second.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterWorkflowWithOptions(cleanup, workflow.RegisterOptions{Name: "cleanup.v1"})
		})
		h.waitStatus(second.Id, owner, api.RequestStatusSucceeded)

		// What would refuse its owner's request refuses the run.
		h.json("PUT", "/api/v1/admin/catalog/items/cleanup/entitlements", "mkeller", api.CatalogEntitlements{ProjectIds: []string{"prj_data"}}, 200, nil)
		fire("f4")
		got := runs()
		if got[0].Outcome != api.ScheduleRunOutcomeRefused || deref(got[0].Reason) == "" || got[0].Request != nil {
			t.Fatalf("a refused run = %+v", got[0])
		}
		h.json("GET", "/api/v1/schedules/"+sc.Id, owner, nil, 200, &sc)
		if sc.LastRun == nil || sc.LastRun.Outcome != api.ScheduleRunOutcomeRefused {
			t.Fatalf("its last run = %+v", sc.LastRun)
		}
		h.json("PUT", "/api/v1/admin/catalog/items/cleanup/entitlements", "mkeller", api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		// Paused, it skips; its project admin resumes it.
		h.json("POST", "/api/v1/schedules/"+sc.Id+"/pause", owner, api.SchedulePause{Note: new("Until the migration is done")}, 200, &sc)
		if !sc.Paused || deref(sc.PausedNote) != "Until the migration is done" || len(sc.NextRuns) != 0 ||
			fake.paused[sc.Id] != "Until the migration is done" {
			t.Fatalf("paused = %+v", sc)
		}
		fire("f5")
		if got := runs(); got[0].Outcome != api.ScheduleRunOutcomeSkipped || deref(got[0].Reason) != "It was paused." {
			t.Fatalf("a firing while paused = %+v", got[0])
		}
		h.json("POST", "/api/v1/schedules/"+sc.Id+"/resume", admin, nil, 200, &sc)
		if _, still := fake.paused[sc.Id]; sc.Paused || still {
			t.Fatalf("resumed = %+v", sc)
		}

		// Tries run out: the firing is kept as failed.
		if err := h.svc.ScheduleFailed(ctx, provision.ScheduleFiring{ScheduleID: sc.Id, FireID: "f6", At: time.Now().UTC(),
			Reason: "Temporal: unavailable"}); err != nil {
			t.Fatal(err)
		}
		if got := runs(); got[0].Outcome != api.ScheduleRunOutcomeFailed || deref(got[0].Reason) != "Temporal: unavailable" {
			t.Fatalf("a failed firing = %+v", got[0])
		}

		// Deleted, it runs no more; its requests still name it.
		h.json("DELETE", "/api/v1/schedules/"+sc.Id, outsider, nil, 404, nil)
		h.json("DELETE", "/api/v1/schedules/"+sc.Id, owner, nil, 204, nil)
		h.json("GET", "/api/v1/schedules/"+sc.Id, owner, nil, 404, nil)
		if _, still := fake.specs[sc.Id]; still {
			t.Fatal("its Temporal schedule stays")
		}
		fire("f7")
		h.json("GET", "/api/v1/requests/"+req.Id, owner, nil, 200, &req)
		if req.Schedule == nil || req.Schedule.Name != "Hourly clean-up" {
			t.Fatalf("a request of a deleted schedule = %+v", req.Schedule)
		}
		var n int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from requests where schedule_id = $1`, sc.Id).Scan(&n); err != nil || n != 2 {
			t.Fatalf("%d requests (%v)", n, err)
		}

		rows, err := h.store.Pool.Query(ctx, `select action from audit_events where target_type = 'schedule' order by id`)
		if err != nil {
			t.Fatal(err)
		}
		var audit []string
		for rows.Next() {
			var action string
			if err := rows.Scan(&action); err != nil {
				t.Fatal(err)
			}
			audit = append(audit, action)
		}
		if !slices.Equal(audit, []string{"schedule.create", "schedule.pause", "schedule.resume", "schedule.delete"}) {
			t.Fatalf("the audit = %v", audit)
		}
	})
}

// TestSchedulesThroughTemporal: a schedule is a Temporal schedule; its
// firing submits the owner's request, which runs to its end; pausing,
// resuming and deleting it reach Temporal, and a cron Temporal refuses is
// refused (E1.5, ADR-0057).
func TestSchedulesThroughTemporal(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const queue, owner = "rvav-schedules-e2e", "tnguyen"
		tc := ts.Client()
		temporal := &provision.Temporal{Client: tc, TaskQueue: queue}
		h.svc.Workflows, h.svc.ScheduleControl, h.acts.Scheduler = temporal, temporal, h.svc
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		platform := worker.New(tc, "platform-workflows", worker.Options{})
		platform.RegisterWorkflowWithOptions(cleanup, workflow.RegisterOptions{Name: "cleanup.v1"})
		if err := platform.Start(); err != nil {
			t.Fatal(err)
		}
		defer platform.Stop()
		item := cleanupItem(h)

		bad := hourly(item, map[string]any{"target": "/var/tmp"})
		bad.EveryMinutes, bad.Cron = nil, new("61 * * * *")
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/schedules", owner, bad, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/cron" || deref(problem.Errors[0].Code) != "invalid" {
			t.Fatalf("a cron Temporal refuses = %+v", problem.Errors)
		}
		var list api.ScheduleList
		h.json("GET", "/api/v1/schedules", owner, nil, 200, &list)
		if len(list.Items) != 0 {
			t.Fatalf("a refused schedule stays: %+v", list.Items)
		}

		nightly := hourly(item, map[string]any{"target": "/var/tmp"})
		nightly.EveryMinutes, nightly.Cron, nightly.Timezone = nil, new("0 2 * * *"), new("Europe/Zurich")
		var sc api.Schedule
		h.json("POST", "/api/v1/schedules", owner, nightly, 201, &sc)
		handle := tc.ScheduleClient().GetHandle(ctx, provision.ScheduleTemporalID(sc.Id))
		d, err := handle.Describe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		zurich, _ := time.LoadLocation("Europe/Zurich")
		if len(sc.NextRuns) == 0 || sc.NextRuns[0].In(zurich).Hour() != 2 || d.Schedule.Spec.TimeZoneName != "Europe/Zurich" {
			t.Fatalf("next runs %v, the spec %+v", sc.NextRuns, d.Schedule.Spec)
		}

		// Fired now: its request runs to its end.
		if err := handle.Trigger(ctx, client.ScheduleTriggerOptions{}); err != nil {
			t.Fatal(err)
		}
		var req api.Request
		deadline := time.Now().Add(time.Minute)
		for {
			h.json("GET", "/api/v1/schedules/"+sc.Id, owner, nil, 200, &sc)
			if sc.LastRun != nil && sc.LastRun.Request != nil {
				req = h.waitStatus(sc.LastRun.Request.Id, owner, api.RequestStatusSucceeded)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no run: %+v", sc)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if req.Schedule == nil || req.Schedule.Id != sc.Id || sc.LastRun.Outcome != api.ScheduleRunOutcomeStarted {
			t.Fatalf("the run = %+v, its request %+v", sc.LastRun, req.Schedule)
		}

		h.json("POST", "/api/v1/schedules/"+sc.Id+"/pause", owner, api.SchedulePause{Note: new("Freeze")}, 200, &sc)
		if d, err = handle.Describe(ctx); err != nil || !d.Schedule.State.Paused || d.Schedule.State.Note != "Freeze" || len(sc.NextRuns) != 0 {
			t.Fatalf("paused in Temporal = %+v (%v)", d.Schedule.State, err)
		}
		h.json("POST", "/api/v1/schedules/"+sc.Id+"/resume", owner, nil, 200, &sc)
		if d, err = handle.Describe(ctx); err != nil || d.Schedule.State.Paused || len(sc.NextRuns) == 0 {
			t.Fatalf("resumed in Temporal = %+v (%v)", d.Schedule.State, err)
		}
		h.json("DELETE", "/api/v1/schedules/"+sc.Id, owner, nil, 204, nil)
		var nf *serviceerror.NotFound
		if _, err := handle.Describe(ctx); !errors.As(err, &nf) && !strings.Contains(lowerErr(err), "not found") {
			t.Fatalf("deleted in Temporal: %v", err)
		}
	})
}

func lowerErr(err error) string {
	if err == nil {
		return ""
	}
	return strings.ToLower(err.Error())
}
