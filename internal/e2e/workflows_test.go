package e2e

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/pkg/rvavflow"
)

// decommission is a custom workflow written with rvavflow: it reports its
// steps and a log, asks the Infrastructure leads, then waits three days
// before archiving.
func decommission(ctx workflow.Context, rc rvavflow.Context) (map[string]any, error) {
	app, _ := rc.Inputs["application"].(string)
	if err := rvavflow.Step(ctx, rc, "inventory", "List what "+app+" uses", func() error {
		return rvavflow.AppendLog(ctx, rc, "inventory", "2 VMs", "1 database")
	}); err != nil {
		return nil, err
	}
	d, err := rvavflow.RequestApproval(ctx, rc, rvavflow.Approval{Title: "Decommission " + app,
		Reason: "Its data is deleted after three days.", Group: "rvav-infra-leads"})
	if err != nil {
		return nil, err
	}
	if !d.Approved {
		_ = rvavflow.ReportStep(ctx, rc, "archive", "Archive "+app, rvavflow.Skipped, "")
		return nil, errors.New("the Infrastructure leads said no: " + d.Comment)
	}
	if err := workflow.Sleep(ctx, 72*time.Hour); err != nil {
		return nil, err
	}
	if err := rvavflow.Step(ctx, rc, "archive", "Archive "+app, func() error { return nil }); err != nil {
		return nil, err
	}
	out := map[string]any{"archived": app, "requested_by": rc.Requester.Name, "contract": rc.Contract}
	if rc.Deployment != nil {
		out["deployment"] = rc.Deployment.Name
	}
	return out, nil
}

// TestCustomWorkflows: administrators register custom workflows; a
// workflow item starts one as a child of the request's workflow; it reports
// steps and logs into the timeline and asks for approval in rvav's inbox
// (S10.4, ADR-0022).
func TestCustomWorkflows(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }

		// Registering: the type carries its major version.
		reg := api.WorkflowRegistrationWrite{Key: new("decommission"), Title: "Decommission an application",
			WorkflowType: "decommission.v1", TaskQueue: "platform-workflows",
			InputSchema: &map[string]any{"type": "object", "required": []string{"application"},
				"properties": map[string]any{"application": map[string]any{"type": "string"}}},
			Usages: []api.WorkflowUsage{api.WorkflowUsageCatalogItem}}
		h.json("POST", "/api/v1/admin/workflows", "lmartin", reg, 403, nil)
		bad := reg
		bad.Key, bad.WorkflowType, bad.Usages = new("Bad Key"), "decommission", []api.WorkflowUsage{"everywhere", api.WorkflowUsageDay2Action}
		bad.InputSchema = &map[string]any{"type": "objekt"}
		h.json("POST", "/api/v1/admin/workflows", admin, bad, 422, &problem)
		for _, want := range []string{"/key", "/workflow_type", "/usages/0", "/day2", "/input_schema"} {
			if !slices.Contains(fieldsOf(problem.Errors), want) {
				t.Fatalf("a bad registration = %v, no %s", fieldsOf(problem.Errors), want)
			}
		}
		var registered api.WorkflowRegistration
		h.json("POST", "/api/v1/admin/workflows", admin, reg, 201, &registered)
		h.json("POST", "/api/v1/admin/workflows", admin, reg, 409, nil)
		// A breaking change is another type, registered beside the first.
		v2 := reg
		v2.Key, v2.WorkflowType = new("decommission-v2"), "decommission.v2"
		h.json("POST", "/api/v1/admin/workflows", admin, v2, 201, nil)

		// A catalog item that starts it.
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []string{"application"},
				"properties": map[string]any{"application": map[string]any{"type": "string", "minLength": 1}}},
			UiSchema:      api.UiSchema{Fields: map[string]api.UiField{"application": {Widget: api.UiWidgetText, Label: "Application"}}},
			InitialValues: map[string]any{},
			Blueprint:     map[string]any{"kind": "workflow", "workflow": "decommission"},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Application", Fields: []string{"application"}}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "decommission", Name: "Decommission an application",
			Summary: "Retire an application over three days.", Category: "compute", Type: api.Workflow, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/decommission/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/decommission/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		h.json("DELETE", "/api/v1/admin/workflows/decommission", admin, nil, 409, nil) // the item uses it

		request := func(app string, decide func(env *testsuite.TestWorkflowEnvironment)) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"application": app}},
				202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
				env.RegisterWorkflowWithOptions(decommission, workflow.RegisterOptions{Name: "decommission.v1"})
				env.RegisterDelayedCallback(func() { decide(env) }, time.Hour)
			})
			h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
			return req
		}
		decide := func(outcome api.ApprovalDecisionDecision, comment string) func(env *testsuite.TestWorkflowEnvironment) {
			return func(env *testsuite.TestWorkflowEnvironment) {
				var inbox api.ApprovalPage
				h.json("GET", "/api/v1/approvals", admin, nil, 200, &inbox)
				if inbox.Total != 1 || !strings.HasPrefix(inbox.Items[0].Level.Name, "Decommission ") {
					t.Errorf("the leads' inbox = %+v", inbox.Items)
					return
				}
				// The workflow runs already: its inputs can't change now.
				h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", admin,
					api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove, Comment: &comment,
						Inputs: &map[string]any{"application": "other"}}, 422, nil)
				h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", admin,
					api.ApprovalDecision{Decision: outcome, Comment: &comment}, 200, nil)
				h.wf.deliver(env)
			}
		}

		// Approved: it waits its three days, archives and ends.
		done := request("billing", decide(api.ApprovalDecisionDecisionApprove, "Fine"))
		if done.Status != api.RequestStatusSucceeded {
			t.Fatalf("an approved decommission = %s %s", done.Status, errorOf(done))
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+done.Id+"/steps", "tnguyen", nil, 200, &steps)
		i := slices.IndexFunc(steps.Items, func(s api.Step) bool { return s.Key == provision.KeyWorkflow+"decommission" })
		if i < 0 || steps.Items[i].Status != api.StepStatusDone {
			t.Fatalf("steps = %+v", steps.Items)
		}
		var children []string
		for _, ch := range steps.Items[i].Children {
			children = append(children, fmt.Sprintf("%s=%s", strings.TrimPrefix(ch.Key, provision.KeyWorkflow+"decommission/"), ch.Status))
		}
		if fmt.Sprint(children) != "[inventory=done archive=done]" {
			t.Fatalf("the workflow's own steps = %v", children)
		}
		inventory := steps.Items[i].Children[0]
		var log api.LogChunk
		h.json("GET", "/api/v1/requests/"+done.Id+"/steps/"+inventory.Id+"/log", "tnguyen", nil, 200, &log)
		if len(log.Lines) != 2 || log.Lines[0].Text != "2 VMs" {
			t.Fatalf("its log = %+v", log.Lines)
		}
		var approvals struct{ Items []api.Approval }
		h.json("GET", "/api/v1/requests/"+done.Id+"/approvals", "tnguyen", nil, 200, &approvals)
		if len(approvals.Items) != 1 || approvals.Items[0].Status != api.ApprovalStatusApproved {
			t.Fatalf("the request's approvals = %+v", approvals.Items)
		}
		var result []byte
		if err := h.store.Pool.QueryRow(ctx, `select result from workflow_runs where request_id = $1 and status = 'succeeded'`,
			done.Id).Scan(&result); err != nil || !strings.Contains(string(result), `"archived": "billing"`) ||
			!strings.Contains(string(result), `"contract": "rvav.v1"`) {
			t.Fatalf("the run's result = %s (%v)", result, err)
		}

		// Rejected: the request fails with what the workflow said.
		no := request("payroll", decide(api.ApprovalDecisionDecisionReject, "Payroll still uses it"))
		if no.Status != api.RequestStatusFailed || !strings.Contains(errorOf(no), "Payroll still uses it") {
			t.Fatalf("a rejected decommission = %s %s", no.Status, errorOf(no))
		}

		// A run that ends cancels the approvals it left open: nothing waits for them.
		const leftOpen = "apr_left_open"
		if err := h.store.InsertWorkflowApproval(ctx, store.Approval{ID: leftOpen, RequestID: done.Id, Name: "Left open",
			Mode: "any", WorkflowID: new("wf-left-open")}); err != nil {
			t.Fatal(err)
		}
		if err := h.store.StartWorkflowRun(ctx, "wf-left-open", "decommission", &done.Id, nil); err != nil {
			t.Fatal(err)
		}
		if err := h.store.FinishWorkflowRun(ctx, "wf-left-open", "failed", nil, new("No decision in time.")); err != nil {
			t.Fatal(err)
		}
		var openStatus string
		_ = h.store.Pool.QueryRow(ctx, `select status from approvals where id = $1`, leftOpen).Scan(&openStatus)
		if openStatus != "cancelled" {
			t.Fatalf("an approval its ended run left open = %s", openStatus)
		}

		// A workflow rvav didn't start can't report into a request.
		var s testsuite.WorkflowTestSuite
		env := s.NewTestWorkflowEnvironment()
		provision.RegisterContract(env, h.acts)
		env.RegisterWorkflowWithOptions(decommission, workflow.RegisterOptions{Name: "decommission.v1"})
		env.ExecuteWorkflow("decommission.v1", rvavflow.Context{TaskQueue: "rvav-test", Inputs: map[string]any{"application": "x"}})
		if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "rvav didn't start") {
			t.Fatalf("an outside workflow = %v", err)
		}

		// Disabled: no longer requestable; its registration lists what uses it.
		reg.Enabled = new(false)
		h.json("PUT", "/api/v1/admin/workflows/decommission", admin, reg, 200, &registered)
		if registered.Running != 0 || !slices.Contains(registered.UsedBy, "item decommission") {
			t.Fatalf("registration = %+v", registered)
		}
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"application": "x"}},
			422, &problem, "Idempotency-Key", uuid.NewString())
		var list api.WorkflowRegistrationList
		h.json("GET", "/api/v1/admin/workflows", "svc-awx", nil, 200, &list) // auditors read them
		if len(list.Items) != 2 {
			t.Fatalf("registrations = %+v", list.Items)
		}
		h.json("DELETE", "/api/v1/admin/workflows/decommission-v2", admin, nil, 204, nil)
	})
}

// TestWorkflowDay2Actions: a registered workflow offered on deployments is
// the action wf.<key>, with the input schema as its form, decided by the
// day-2 policies, offered to its projects only (S10.7, ADR-0022).
func TestWorkflowDay2Actions(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }
		reg := api.WorkflowRegistrationWrite{Key: new("decommission"), Title: "Decommission an application",
			WorkflowType: "decommission.v1", TaskQueue: "platform-workflows",
			InputSchema: &map[string]any{"type": "object", "required": []string{"application"},
				"properties": map[string]any{"application": map[string]any{"type": "string", "minLength": 1},
					"retention_days": map[string]any{"type": "integer", "default": 3}}},
			Usages:     []api.WorkflowUsage{api.WorkflowUsageDay2Action},
			ProjectIds: &[]string{"prj_web"},
			Day2: &api.WorkflowDay2{Name: "Decommission", Types: []api.WorkflowDay2Types{api.WorkflowDay2TypesDeployment},
				Condition: new(`environment.key == "dev"`), Offered: new(true)}}
		h.json("POST", "/api/v1/admin/workflows", admin, reg, 201, nil)

		d := buildVM(h, nil)
		actions := d.actions("tnguyen")
		action, ok := actions["wf.decommission"]
		if !ok || action.Form == nil || action.Form.InitialValues["retention_days"] != float64(3) ||
			action.Form.UiSchema.Fields["application"].Widget != api.UiWidgetText {
			t.Fatalf("the workflow action = %+v (offered %v)", action, ok)
		}

		// Its form is checked like any action's.
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "wf.decommission",
			Inputs: map[string]any{}}, 422, &problem, "Idempotency-Key", uuid.NewString())
		var req api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "wf.decommission",
			Inputs: map[string]any{"application": "web"}}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterWorkflowWithOptions(decommission, workflow.RegisterOptions{Name: "decommission.v1"})
			env.RegisterDelayedCallback(func() {
				var inbox api.ApprovalPage
				h.json("GET", "/api/v1/approvals", admin, nil, 200, &inbox)
				if inbox.Total != 1 {
					t.Errorf("inbox = %+v", inbox.Items)
					return
				}
				h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", admin,
					api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove, Comment: new("Go")}, 200, nil)
				h.wf.deliver(env)
			}, time.Hour)
		})
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded || req.Action == nil || req.Action.Key != "wf.decommission" {
			t.Fatalf("the action's request = %s %s", req.Status, errorOf(req))
		}
		var result map[string]any
		if err := h.store.Pool.QueryRow(ctx, `select result from workflow_runs where request_id = $1`, req.Id).Scan(&result); err != nil ||
			result["archived"] != "web" {
			t.Fatalf("the run = %v (%v)", result, err)
		}
		var ctxDeployment string
		_ = h.store.Pool.QueryRow(ctx, `select coalesce((select name from deployments where id = $1), '')`, d.id).Scan(&ctxDeployment)
		if result["deployment"] != ctxDeployment {
			t.Fatalf("the workflow saw deployment %v, not %s", result["deployment"], ctxDeployment)
		}

		// Not offered outside its projects, nor once switched off.
		reg.ProjectIds = &[]string{"prj_data"}
		h.json("PUT", "/api/v1/admin/workflows/decommission", admin, reg, 200, nil)
		if _, ok := d.actions("tnguyen")["wf.decommission"]; ok {
			t.Fatal("offered outside its projects")
		}
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "wf.decommission",
			Inputs: map[string]any{"application": "web"}}, 422, nil, "Idempotency-Key", uuid.NewString())
		reg.ProjectIds, reg.Day2.Offered = &[]string{}, new(false)
		h.json("PUT", "/api/v1/admin/workflows/decommission", admin, reg, 200, nil)
		if _, ok := d.actions("tnguyen")["wf.decommission"]; ok {
			t.Fatal("offered while switched off")
		}
	})
}
