package e2e

import (
	"context"
	"errors"
	"slices"
	"strconv"
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

func (f *scheduleFake) ListSchedules(context.Context) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for id := range f.specs {
		_, paused := f.paused[id]
		out[id] = paused
	}
	return out, nil
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

// TestScheduleGuards: a schedule's runs never overlap, whether the firing
// before was skipped or fires at the same time; an owner not seen for
// RVAV_SCHEDULE_OWNER_IDLE_DAYS has their runs skipped until they sign in;
// a worker's start makes Temporal's schedules agree with rvav's (E1.6,
// ADR-0057).
func TestScheduleGuards(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		fake := &scheduleFake{specs: map[string]provision.ScheduleSpec{}, paused: map[string]string{}}
		h.svc.ScheduleControl = fake
		h.svc.Config.ScheduleOwnerIdleDays = 90
		const owner = "tnguyen"
		item := cleanupItem(h)
		long := hourly(item, map[string]any{"target": "/tmp"})
		long.Name = strings.Repeat("n", 121)
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/schedules", owner, long, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/name" || deref(problem.Errors[0].Code) != "too_long" {
			t.Fatalf("a long name = %+v", problem.Errors)
		}
		var sc api.Schedule
		h.json("POST", "/api/v1/schedules", owner, hourly(item, map[string]any{"target": "/tmp"}), 201, &sc)
		if sc.OwnerSeenAt != nil {
			t.Fatalf("an owner never signed in was seen at %v", sc.OwnerSeenAt)
		}
		fire := func(id string) error {
			return h.svc.RunSchedule(ctx, provision.ScheduleFiring{ScheduleID: sc.Id, FireID: id, At: time.Now().UTC()})
		}
		runs := func() []api.ScheduleRun {
			t.Helper()
			var got api.Schedule
			h.json("GET", "/api/v1/schedules/"+sc.Id, owner, nil, 200, &got)
			return *got.Runs
		}
		unfinished := func() int {
			t.Helper()
			var n int
			if err := h.store.Pool.QueryRow(ctx, `select count(*) from requests where schedule_id = $1
				and status not in ('succeeded', 'failed', 'rejected', 'cancelled')`, sc.Id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}

		// Firings at the same time start one request.
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = fire("f1-" + strconv.Itoa(i))
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		got := runs()
		started := slices.IndexFunc(got, func(r api.ScheduleRun) bool { return r.Outcome == api.ScheduleRunOutcomeStarted })
		if len(got) != 4 || unfinished() != 1 || started < 0 {
			t.Fatalf("firings at once = %+v", got)
		}
		number := got[started].Request.Number
		// A skipped run is no reason to start the next: the request before
		// still runs.
		for _, id := range []string{"f2", "f3"} {
			if err := fire(id); err != nil {
				t.Fatal(err)
			}
			if r := runs()[0]; r.Outcome != api.ScheduleRunOutcomeSkipped || deref(r.Reason) != number+" hadn't finished." {
				t.Fatalf("%s while %s runs = %+v", id, number, r)
			}
		}
		if unfinished() != 1 {
			t.Fatal("a skip let a second request start")
		}
		if _, err := h.store.Pool.Exec(ctx, `update requests set status = 'succeeded' where schedule_id = $1`, sc.Id); err != nil {
			t.Fatal(err)
		}

		// An owner unseen for longer than the bound: skipped until they sign in.
		if _, err := h.store.Pool.Exec(ctx, `update schedules set created_at = now() - interval '200 days' where id = $1`, sc.Id); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Pool.Exec(ctx, `update users set last_seen_at = now() - interval '91 days' where id = $1`, seed.UserID(owner)); err != nil {
			t.Fatal(err)
		}
		if err := fire("f4"); err != nil {
			t.Fatal(err)
		}
		if r := runs()[0]; r.Outcome != api.ScheduleRunOutcomeSkipped || !strings.Contains(deref(r.Reason), "hasn't signed in for 90 days") {
			t.Fatalf("a run of an owner gone = %+v", r)
		}
		h.json("GET", "/api/v1/schedules/"+sc.Id, owner, nil, 200, &sc)
		if sc.OwnerSeenAt == nil || time.Since(*sc.OwnerSeenAt) < 90*24*time.Hour {
			t.Fatalf("the owner was seen at %v", sc.OwnerSeenAt)
		}
		// As a sign-in does.
		if _, err := h.store.Pool.Exec(ctx, `update users set last_seen_at = now() where id = $1`, seed.UserID(owner)); err != nil {
			t.Fatal(err)
		}
		if err := fire("f5"); err != nil {
			t.Fatal(err)
		}
		if r := runs()[0]; r.Outcome != api.ScheduleRunOutcomeStarted {
			t.Fatalf("a run once the owner signed in = %+v", r)
		}

		// A worker's start: a row without its Temporal schedule gets one, a
		// pause only Temporal took is undone, a deleted schedule's goes; one
		// rvav doesn't know is another's.
		var other api.Schedule
		h.json("POST", "/api/v1/schedules", owner, hourly(item, map[string]any{"target": "/srv"}), 201, &other)
		var gone api.Schedule
		h.json("POST", "/api/v1/schedules", owner, hourly(item, map[string]any{"target": "/opt"}), 201, &gone)
		fake.mu.Lock()
		delete(fake.specs, sc.Id)
		fake.paused[other.Id] = "half paused"
		fake.specs["sch_someone_else"] = provision.ScheduleSpec{EveryMinutes: 5}
		fake.mu.Unlock()
		if err := h.store.DeleteSchedule(ctx, gone.Id); err != nil {
			t.Fatal(err)
		}
		if err := h.svc.ReconcileSchedules(ctx); err != nil {
			t.Fatal(err)
		}
		fake.mu.Lock()
		spec, made := fake.specs[sc.Id]
		_, paused := fake.paused[other.Id]
		_, kept := fake.specs[gone.Id]
		_, foreign := fake.specs["sch_someone_else"]
		fake.mu.Unlock()
		if !made || spec.EveryMinutes != 60 || spec.Timezone != "UTC" || paused || kept || !foreign {
			t.Fatalf("reconciled: made %v (%+v), paused %v, deleted kept %v, another's kept %v", made, spec, paused, kept, foreign)
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

		// Deleted in rvav only (the process stopped before Temporal): a
		// worker's start deletes it in Temporal.
		var orphan api.Schedule
		h.json("POST", "/api/v1/schedules", owner, nightly, 201, &orphan)
		if err := h.store.DeleteSchedule(ctx, orphan.Id); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			listed, err := temporal.ListSchedules(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := listed[orphan.Id]; ok {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Temporal never listed %s: %v", orphan.Id, listed)
			}
		}
		if err := h.svc.ReconcileSchedules(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := tc.ScheduleClient().GetHandle(ctx, provision.ScheduleTemporalID(orphan.Id)).Describe(ctx); !errors.As(err, &nf) &&
			!strings.Contains(lowerErr(err), "not found") {
			t.Fatalf("a schedule deleted in rvav stays in Temporal: %v", err)
		}
	})
}

func lowerErr(err error) string {
	if err == nil {
		return ""
	}
	return strings.ToLower(err.Error())
}
