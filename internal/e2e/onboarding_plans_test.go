package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
)

// TestOnboardingPlans: administrators save onboarding plans, a filter of
// the inventory's VMs and where they go, and run them by hand or on their
// schedule, as Temporal workflows (X1.5). A run adopts what no deployment
// has and skips the rest, saying why; VMs join an existing deployment as
// VMs of a component when they fit it, and are refused with why when they
// don't. A VM unregistered from rvav is adopted again.
func TestOnboardingPlans(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		h.acts.Onboarder = h.svc
		const admin = "mkeller"
		existingVM(t, ctx, h, c, model, "legacy-app01", "10.31.10.211")
		existingVM(t, ctx, h, c, model, "legacy-app02", "10.31.10.212")
		web01 := existingVM(t, ctx, h, c, model, "erp-web01", "10.31.10.221")
		existingVM(t, ctx, h, c, model, "erp-web02", "10.31.10.222")
		erpApp01 := existingVM(t, ctx, h, c, model, "erp-app01", "10.31.10.223")
		erpApp02 := existingVM(t, ctx, h, c, model, "erp-app02", "10.31.10.224")
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var rhel, threeTier api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", "lmartin", nil, 200, &rhel)
		h.json("GET", "/api/v1/catalog/items/three-tier?project_id=prj_web", "lmartin", nil, 200, &threeTier)

		// The inventory knows each VM's folder: VMs in a folder and below.
		var page api.OnboardingVmPage
		h.json("GET", "/api/v1/admin/onboarding/vms?folder=/GVA/vm/rvav&q=legacy-", admin, nil, 200, &page)
		if page.Total != 2 || deref(page.Items[0].Folder) != "/GVA/vm/rvav/web/dev" {
			t.Fatalf("by folder = %d %+v", page.Total, page.Items)
		}
		h.json("GET", "/api/v1/admin/onboarding/vms?folder=/GVA/vm/rv", admin, nil, 200, &page)
		if page.Total != 0 {
			t.Fatalf("a folder's name is matched whole: %d", page.Total)
		}

		runWorkflow := func(runID string) api.OnboardingRun {
			t.Helper()
			var s testsuite.WorkflowTestSuite
			s.SetLogger(nil)
			env := s.NewTestWorkflowEnvironment()
			env.RegisterActivity(h.acts)
			env.ExecuteWorkflow(provision.OnboardingRun, runID)
			if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
				t.Fatalf("run %s: %v", runID, env.GetWorkflowError())
			}
			var run api.OnboardingRun
			h.json("GET", "/api/v1/admin/onboarding/runs/"+runID, admin, nil, 200, &run)
			return run
		}
		runPlan := func(id string) api.OnboardingRun {
			t.Helper()
			var started api.OnboardingRun
			h.json("POST", "/api/v1/admin/onboarding/plans/"+id+"/runs", admin, nil, 202, &started)
			if started.Status != api.OnboardingRunStatusRunning || started.Trigger != api.OnboardingRunTriggerManual ||
				started.StartedBy == nil || started.StartedBy.Name == "" {
				t.Fatalf("started = %+v", started)
			}
			return runWorkflow(started.Id)
		}
		outcomes := func(run api.OnboardingRun) []string {
			var out []string
			for _, v := range *run.Vms {
				s := v.Name + " " + string(v.Outcome)
				if v.Code != "" {
					s += " " + v.Code
				}
				out = append(out, s)
			}
			return out
		}
		vmsOf := func(id string) []string {
			t.Helper()
			var d struct {
				Resources []struct{ Type, Name string }
			}
			h.json("GET", "/api/v1/deployments/"+id, admin, nil, 200, &d)
			var out []string
			for _, r := range d.Resources {
				if r.Type == "vsphere.vm" {
					out = append(out, r.Name)
				}
			}
			return out
		}
		inputsOf := func(id string) string {
			t.Helper()
			var raw []byte
			if err := h.store.Pool.QueryRow(ctx, `select inputs from deployments where id = $1`, id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			return string(raw)
		}

		// What a plan can't be is said field by field.
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/onboarding/plans", admin, api.OnboardingPlanWrite{Name: " ", EveryHours: present.Ptr(200),
			Target: api.OnboardingTarget{ProjectId: "prj_web", Environment: present.Ptr("dev"), OwnerId: present.Ptr("usr_tnguyen"),
				CatalogItemId: present.Ptr(threeTier.Id), Component: present.Ptr("cache"),
				Inputs: &map[string]any{"web_nodes": 9, "secret_sauce": "x"}}}, 422, &problem)
		var fields []string
		for _, e := range problem.Errors {
			fields = append(fields, e.Field+" "+deref(e.Code))
		}
		if want := []string{"/name invalid", "/filter required", "/every_hours out_of_range", "/target/component invalid",
			"/target/inputs/secret_sauce unknown_field", "/target/inputs/web_nodes maximum"}; !slices.Equal(fields, want) {
			t.Fatalf("errors = %v, want %v", fields, want)
		}
		h.json("POST", "/api/v1/admin/onboarding/plans", admin, api.OnboardingPlanWrite{Name: "No environment",
			Filter: api.OnboardingFilter{Tag: present.Ptr("app/erp")}, Target: api.OnboardingTarget{ProjectId: "prj_web",
				OwnerId: present.Ptr("usr_tnguyen"), CatalogItemId: present.Ptr(rhel.Id)}}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/target/environment" || deref(problem.Errors[0].Code) != "required" {
			t.Fatalf("without an environment = %+v", problem.Errors)
		}
		h.json("GET", "/api/v1/admin/onboarding/plans", "tnguyen", nil, 403, nil)

		// A plan adopts the two VMs it matches into one deployment it
		// makes, with the inputs it gives.
		legacy := api.OnboardingPlanWrite{Name: "Legacy apps", Description: present.Ptr("The apps the old portal built"),
			Filter: api.OnboardingFilter{Q: present.Ptr("legacy-app"), Folder: present.Ptr("/GVA/vm/rvav")},
			Target: api.OnboardingTarget{ProjectId: "prj_web", Environment: present.Ptr("dev"), OwnerId: present.Ptr("usr_tnguyen"),
				CatalogItemId: present.Ptr(rhel.Id), DeploymentName: present.Ptr("legacy-dev"), LeaseDays: present.Ptr(30),
				Inputs: &map[string]any{"role": "app", "size": "M"}},
			EveryHours: present.Ptr(24)}
		var plan api.OnboardingPlan
		h.json("POST", "/api/v1/admin/onboarding/plans", admin, legacy, 201, &plan)
		if plan.Matching != 2 || plan.Running || plan.LastRun != nil || plan.NextRunAt == nil || plan.Deployment != nil {
			t.Fatalf("plan = %+v", plan)
		}
		h.json("POST", "/api/v1/admin/onboarding/plans", admin, legacy, 422, &problem)
		if problem.Errors[0].Field != "/name" || deref(problem.Errors[0].Code) != "taken" {
			t.Fatalf("same name = %+v", problem.Errors)
		}
		first := runPlan(plan.Id)
		if first.Status != api.OnboardingRunStatusSucceeded || first.Adopted != 2 || first.Skipped != 0 || first.FinishedAt == nil {
			t.Fatalf("first run = %+v", first)
		}
		h.json("GET", "/api/v1/admin/onboarding/plans/"+plan.Id, admin, nil, 200, &plan)
		if plan.Deployment == nil || plan.Deployment.Name != "legacy-dev" || plan.Matching != 0 || plan.LastRun == nil ||
			plan.LastRun.Id != first.Id {
			t.Fatalf("plan after its run = %+v", plan)
		}
		dep := plan.Deployment.Id
		if got := vmsOf(dep); !slices.Equal(got, []string{"legacy-app01", "legacy-app02"}) {
			t.Fatalf("legacy-dev = %v", got)
		}
		if got := inputsOf(dep); got != `{"role": "app", "size": "M"}` {
			t.Fatalf("inputs = %s", got)
		}

		// A VM that matches later is adopted by the next run, which skips
		// the two the plan adopted.
		existingVM(t, ctx, h, c, model, "legacy-app03", "10.31.10.213")
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		second := runPlan(plan.Id)
		if got := outcomes(second); !slices.Equal(got, []string{"legacy-app01 skipped managed", "legacy-app02 skipped managed",
			"legacy-app03 adopted"}) || second.Status != api.OnboardingRunStatusSucceeded {
			t.Fatalf("second run = %v (%s)", got, second.Status)
		}
		if v := (*second.Vms)[0]; v.Reason != "legacy-dev has it." || v.Deployment == nil || v.Deployment.Id != dep {
			t.Fatalf("skipped = %+v", v)
		}
		if got := vmsOf(dep); len(got) != 3 {
			t.Fatalf("legacy-dev = %v", got)
		}
		// An update keeps the VMs of an item without components.
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", "lmartin", nil, 200, &form)
		var preview api.UpdatePreview
		h.json("POST", "/api/v1/deployments/"+dep+"/update/preview", admin, api.UpdateDraft{VersionId: form.VersionId,
			Inputs: map[string]any{"role": "app", "size": "M", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}, 200, &preview)
		for _, ch := range preview.Changes {
			if ch.Kind == "vm_remove" || ch.Kind == "vm_add" {
				t.Fatalf("an update of legacy-dev would %s", ch.Title)
			}
		}

		// Unregistered from rvav, a VM is adopted again by the next run.
		var actions struct{ Items []api.Day2Action }
		h.json("GET", "/api/v1/deployments/"+dep+"/actions", admin, nil, 200, &actions)
		i := slices.IndexFunc(actions.Items, func(a api.Day2Action) bool {
			return a.Key == "vm_unregister" && deref(a.ResourceName) == "legacy-app01"
		})
		if i < 0 {
			t.Fatal("no vm_unregister for legacy-app01")
		}
		var unregister api.Request
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", admin, api.Day2Draft{Action: "vm_unregister",
			ResourceId: actions.Items[i].ResourceId, Inputs: map[string]any{}}, 202, &unregister, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(unregister.Id, nil)
		h.waitStatus(unregister.Id, admin, api.RequestStatusSucceeded)
		if got := vmsOf(dep); slices.Contains(got, "legacy-app01") {
			t.Fatalf("legacy-dev after unregistering = %v", got)
		}
		third := runPlan(plan.Id)
		if got := outcomes(third); !slices.Equal(got, []string{"legacy-app01 adopted", "legacy-app02 skipped managed",
			"legacy-app03 skipped managed"}) {
			t.Fatalf("third run = %v", got)
		}
		if got := vmsOf(dep); len(got) != 3 || !slices.Contains(got, "legacy-app01") {
			t.Fatalf("legacy-dev = %v", got)
		}

		// Its schedule starts it when due, once.
		started, err := h.svc.StartDueOnboardingPlans(ctx, time.Now().Add(25*time.Hour))
		if err != nil || len(started) != 1 || !slices.Contains(h.wf.onboarding, started[0]) {
			t.Fatalf("scheduled = %v %v (%v)", started, err, h.wf.onboarding)
		}
		h.json("POST", "/api/v1/admin/onboarding/plans/"+plan.Id+"/runs", admin, nil, 409, nil)
		h.json("DELETE", "/api/v1/admin/onboarding/plans/"+plan.Id, admin, nil, 409, nil)
		if again, err := h.svc.StartDueOnboardingPlans(ctx, time.Now().Add(25*time.Hour)); err != nil || len(again) != 0 {
			t.Fatalf("started again = %v %v", again, err)
		}
		scheduled := runWorkflow(started[0])
		if scheduled.Trigger != api.OnboardingRunTriggerSchedule || scheduled.StartedBy != nil || scheduled.Adopted != 0 ||
			scheduled.Skipped != 3 {
			t.Fatalf("scheduled run = %+v", scheduled)
		}
		var runs api.OnboardingRunList
		h.json("GET", "/api/v1/admin/onboarding/plans/"+plan.Id+"/runs", admin, nil, 200, &runs)
		if len(runs.Items) != 4 || runs.Items[0].Id != scheduled.Id || runs.Items[3].Id != first.Id || runs.Items[3].Adopted != 2 ||
			runs.Items[0].Vms != nil {
			t.Fatalf("runs = %+v", runs.Items)
		}

		// VMs join an existing deployment of a blueprint with components,
		// as VMs of one: the input its count is rises with them.
		var res api.OnboardingResult
		h.json("POST", "/api/v1/admin/onboarding", admin, api.OnboardingDraft{ProjectId: "prj_web", Environment: "dev",
			OwnerId: "usr_tnguyen", CatalogItemId: threeTier.Id, DeploymentName: present.Ptr("erp-dev"),
			Vms: []vmRefT{{Moref: web01, Vcenter: "vc01.corp.example"}}}, 422, &problem)
		if problem.Errors[0].Field != "/component" || deref(problem.Errors[0].Code) != "required" {
			t.Fatalf("without a component = %+v", problem.Errors)
		}
		h.json("POST", "/api/v1/admin/onboarding", admin, api.OnboardingDraft{ProjectId: "prj_web", Environment: "dev",
			OwnerId: "usr_tnguyen", CatalogItemId: threeTier.Id, DeploymentName: present.Ptr("erp-dev"), Component: present.Ptr("web"),
			Vms: []vmRefT{{Moref: web01, Vcenter: "vc01.corp.example"}}}, 200, &res)
		erp := res.Deployments[0].Id
		if got := inputsOf(erp); got != `{"web_nodes": 1}` {
			t.Fatalf("erp-dev's inputs = %s", got)
		}
		into := api.OnboardingPlanWrite{Name: "ERP web", Filter: api.OnboardingFilter{Q: present.Ptr("erp-web")},
			Target: api.OnboardingTarget{ProjectId: "prj_web", DeploymentId: &erp, Component: present.Ptr("web"),
				Environment: present.Ptr("prd")}}
		h.json("POST", "/api/v1/admin/onboarding/plans", admin, into, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/target/environment" {
			t.Fatalf("an existing deployment's environment = %+v", problem.Errors)
		}
		into.Target.Environment = nil
		var webPlan api.OnboardingPlan
		h.json("POST", "/api/v1/admin/onboarding/plans", admin, into, 201, &webPlan)
		if webPlan.Deployment == nil || webPlan.Deployment.Name != "erp-dev" || webPlan.Matching != 1 || webPlan.NextRunAt != nil {
			t.Fatalf("plan into erp-dev = %+v", webPlan)
		}
		if got := outcomes(runPlan(webPlan.Id)); !slices.Equal(got, []string{"erp-web01 skipped managed", "erp-web02 adopted"}) {
			t.Fatalf("into erp-dev = %v", got)
		}
		if got := inputsOf(erp); got != `{"web_nodes": 2}` {
			t.Fatalf("erp-dev's inputs = %s", got)
		}

		// What doesn't fit is refused, with why: Application has one VM.
		h.json("POST", "/api/v1/admin/onboarding/into", admin, api.OnboardingIntoDraft{DeploymentId: erp, Component: present.Ptr("app"),
			Vms: []vmRefT{{Moref: erpApp01, Vcenter: "vc01.corp.example"}, {Moref: erpApp02, Vcenter: "vc01.corp.example"}}}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/vms/1" ||
			problem.Errors[0].Message != "Application has 1 VM (its count), and has 0 already." {
			t.Fatalf("too many = %+v", problem.Errors)
		}
		h.json("POST", "/api/v1/admin/onboarding/into", admin, api.OnboardingIntoDraft{DeploymentId: erp, Component: present.Ptr("app"),
			Vms: []vmRefT{{Moref: erpApp01, Vcenter: "vc01.corp.example"}}}, 200, &res)
		if got := vmsOf(erp); !slices.Equal(got, []string{"erp-app01", "erp-web01", "erp-web02"}) {
			t.Fatalf("erp-dev = %v", got)
		}
		var appPlan api.OnboardingPlan
		h.json("POST", "/api/v1/admin/onboarding/plans", admin, api.OnboardingPlanWrite{Name: "ERP app",
			Filter: api.OnboardingFilter{Q: present.Ptr("erp-app")}, Target: api.OnboardingTarget{ProjectId: "prj_web",
				DeploymentId: &erp, Component: present.Ptr("app")}}, 201, &appPlan)
		refused := runPlan(appPlan.Id)
		if refused.Status != api.OnboardingRunStatusPartial || refused.Failed != 1 {
			t.Fatalf("refused run = %+v", refused)
		}
		if v := (*refused.Vms)[1]; v.Name != "erp-app02" || v.Code != "no_fit" ||
			v.Reason != "Application has 1 VM (its count), and has 1 already." {
			t.Fatalf("refused = %+v", v)
		}

		// Every plan, run and adoption is audited.
		var audit struct{ Items []api.AuditEvent }
		h.json("GET", "/api/v1/audit?action=onboarding_plan.&limit=200", admin, nil, 200, &audit)
		count := map[string]int{}
		for _, e := range audit.Items {
			count[e.Action]++
		}
		if count["onboarding_plan.created"] != 3 || count["onboarding_plan.run_started"] != 6 ||
			count["onboarding_plan.run_finished"] != 6 {
			t.Fatalf("audited = %v", count)
		}
		h.json("GET", "/api/v1/audit?action=deployment.&limit=200", admin, nil, 200, &audit)
		var adopted []string
		for _, e := range audit.Items {
			if e.Action == "deployment.adopted" || e.Action == "deployment.vm_adopted" {
				b, _ := json.Marshal(e.Detail["vms"])
				adopted = append(adopted, e.Action+" "+strings.Trim(string(b), "[]")+" "+deref(asString(e.Detail["plan_name"])))
			}
		}
		slices.Sort(adopted)
		// The first VM of a run makes its plan's deployment; the next ones
		// join it.
		want := []string{`deployment.adopted "erp-web01" `, `deployment.adopted "legacy-app01" Legacy apps`,
			`deployment.vm_adopted "erp-app01" `, `deployment.vm_adopted "erp-web02" ERP web`,
			`deployment.vm_adopted "legacy-app01" Legacy apps`, `deployment.vm_adopted "legacy-app02" Legacy apps`,
			`deployment.vm_adopted "legacy-app03" Legacy apps`}
		if !slices.Equal(adopted, want) {
			t.Fatalf("adoptions audited = %q, want %q", adopted, want)
		}

		// A plan goes; what it adopted stays.
		h.json("DELETE", "/api/v1/admin/onboarding/plans/"+plan.Id, admin, nil, 204, nil)
		h.json("GET", "/api/v1/admin/onboarding/plans/"+plan.Id, admin, nil, 404, nil)
		h.json("GET", "/api/v1/admin/onboarding/runs/"+first.Id, admin, nil, 404, nil)
		if got := vmsOf(dep); len(got) != 3 {
			t.Fatalf("legacy-dev after its plan = %v", got)
		}
	}, model)
}

type vmRefT = api.OnboardingVmRef

func asString(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}
