package e2e

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/integrations/awx"
)

// TestCompositeDesigner: what the composite designer offers a step (D1.3,
// ADR-0032), and the path D1.5's acceptance takes through it: the seeded
// PostgreSQL cluster gets a step, its inputs bound to the form and to an
// earlier step's outputs and a condition, is published and requested, and
// the step runs with what it was bound to.
func TestCompositeDesigner(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log, Filter: awx.TemplateFilter{Labels: []string{"rvav"}}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", admin, nil, 200, nil)
		regProjects := func(key string, projects []string, usages ...api.WorkflowUsage) {
			h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new(key), Title: "Register " + key,
				WorkflowType: key + ".v1", TaskQueue: "platform-workflows", Usages: usages, ProjectIds: &projects,
				InputSchema: &map[string]any{"type": "object", "required": []string{"fqdn"}, "properties": map[string]any{
					"team": map[string]any{"type": "string"}, "fqdn": map[string]any{"type": "string", "title": "FQDN"}}}}, 201, nil)
		}
		reg := func(key string, usages ...api.WorkflowUsage) { regProjects(key, []string{}, usages...) }
		reg("monitoring", api.WorkflowUsageCompositeStep)
		reg("cmdb", api.WorkflowUsageCatalogItem)
		// Offered to the web project only, as a step and as an item.
		regProjects("monitoring-web", []string{"prj_web"}, api.WorkflowUsageCompositeStep, api.WorkflowUsageCatalogItem)
		// Disabled once its item is made: neither can run.
		reg("retired", api.WorkflowUsageCompositeStep, api.WorkflowUsageCatalogItem)
		workflowItem := func(slug, workflow string) {
			def := api.CatalogVersionDefinition{
				FormSchema: map[string]any{"type": "object", "required": []string{"fqdn"},
					"properties": map[string]any{"fqdn": map[string]any{"type": "string", "minLength": 1}}},
				UiSchema:      api.UiSchema{Fields: map[string]api.UiField{"fqdn": {Widget: api.UiWidgetText, Label: "FQDN"}}},
				InitialValues: map[string]any{},
				Blueprint:     map[string]any{"kind": "workflow", "workflow": workflow},
			}
			def.UiSchema.Layout.Kind = api.Page
			def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Host", Fields: []string{"fqdn"}}}
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: "Run " + workflow,
				Summary: "Runs " + workflow + ".", Category: "compute", Type: api.Workflow, Definition: &def}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		}
		workflowItem("web-monitoring", "monitoring-web")
		workflowItem("retired-item", "retired")
		if _, err := h.store.Pool.Exec(ctx, `update workflow_registrations set enabled = false where key = 'retired'`); err != nil {
			t.Fatal(err)
		}

		// Only those who build items see what steps can run: catalog admins
		// and administrators, the same list.
		h.json("GET", "/api/v1/admin/catalog/composite-steps", "tnguyen", nil, 403, nil)
		var targets, catalogAdmins api.CompositeStepTargetList
		h.json("GET", "/api/v1/admin/catalog/composite-steps", admin, nil, 200, &targets)
		h.json("GET", "/api/v1/admin/catalog/composite-steps", "lmartin", nil, 200, &catalogAdmins)
		if !slices.Equal(refsOf(targets), refsOf(catalogAdmins)) {
			t.Fatalf("a catalog admin gets %v, an administrator %v", refsOf(catalogAdmins), refsOf(targets))
		}
		find := func(kind api.CompositeStepTargetKind, ref string) *api.CompositeStepTarget {
			for i, tg := range targets.Items {
				if tg.Kind == kind && tg.Ref == ref {
					return &targets.Items[i]
				}
			}
			return nil
		}
		names := func(tg *api.CompositeStepTarget) (in, out []string) {
			for _, i := range tg.Inputs {
				in = append(in, i.Name)
			}
			for _, o := range tg.Outputs {
				out = append(out, o.Name)
			}
			return in, out
		}

		// The cluster's nodes: VMs planned with the request, inputs in their form's order.
		nodes := find(api.StepTargetItem, "pg-node")
		if nodes == nil || nodes.Runs != api.StepRunsVm || !nodes.Planned || nodes.OpenOutputs ||
			nodes.ItemType == nil || *nodes.ItemType != api.VmBlueprint {
			t.Fatalf("pg-node = %+v", nodes)
		}
		in, out := names(nodes)
		if want := []string{"name", "pg_version", "nodes", "size", "data_gb", "cluster", "datastore", "network"}; !slices.Equal(in, want) {
			t.Fatalf("pg-node's inputs = %v, want %v", in, want)
		}
		if want := []string{"name", "fqdn", "ip", "moref", "deployment_id", "vms"}; !slices.Equal(out, want) {
			t.Fatalf("pg-node's outputs = %v, want %v", out, want)
		}
		if nodes.Inputs[0].Title != "Database" || !nodes.Inputs[0].Required {
			t.Fatalf("pg-node's name = %+v", nodes.Inputs[0])
		}
		// An AWX item: its survey's inputs, its job's ID.
		user := find(api.StepTargetItem, "db-user")
		if user == nil || user.Runs != api.StepRunsAwx || user.Planned {
			t.Fatalf("db-user = %+v", user)
		}
		in, out = names(user)
		if !slices.Equal(in, []string{"database", "role_name", "privileges"}) || !slices.Equal(out, []string{"job_id"}) ||
			user.Inputs[2].Enum == nil || len(*user.Inputs[2].Enum) != 3 {
			t.Fatalf("db-user's inputs %v (%+v), outputs %v", in, user.Inputs, out)
		}
		// Composites don't nest; unpublished items have nothing to run.
		if find(api.StepTargetItem, "postgres-cluster") != nil {
			t.Fatal("a composite is offered as a step")
		}
		// AWX templates imported from AWX, with their surveys, and their ID
		// in AWX, which the designer writes beside the name.
		var imported api.AwxTemplateList
		h.json("GET", "/api/v1/admin/awx/templates", admin, nil, 200, &imported)
		awxID := 0
		for _, tpl := range imported.Items {
			if tpl.Name == "monitoring-agent" && !tpl.Workflow {
				awxID = tpl.TemplateId
			}
		}
		agent := find(api.StepTargetAwx, "monitoring-agent")
		if agent == nil || agent.Runs != api.StepRunsAwx || awxID == 0 || agent.AwxId == nil || *agent.AwxId != awxID ||
			agent.AwxWorkflow != nil {
			t.Fatalf("monitoring-agent = %+v, AWX ID %d (targets %v)", agent, awxID, refsOf(targets))
		}
		for _, tg := range targets.Items {
			if tg.Kind == api.StepTargetAwx && tg.AwxId == nil {
				t.Fatalf("the AWX template %s has no ID", tg.Ref)
			}
		}
		if in, out = names(agent); !slices.Equal(in, []string{"monitoring_agent", "listen_port"}) || !slices.Equal(out, []string{"job_id"}) {
			t.Fatalf("monitoring-agent's inputs %v, outputs %v", in, out)
		}
		// Custom workflows registered for composite steps only: whatever they return.
		monitoring := find(api.StepTargetWorkflow, "monitoring")
		if monitoring == nil || !monitoring.OpenOutputs || len(monitoring.Outputs) != 0 || monitoring.Inputs[0].Name != "fqdn" ||
			monitoring.ProjectIds != nil || find(api.StepTargetWorkflow, "cmdb") != nil {
			t.Fatalf("workflows: monitoring %+v, cmdb %v", monitoring, find(api.StepTargetWorkflow, "cmdb") != nil)
		}
		// A workflow offered to some projects says which, as a step and as
		// an item's (a request from another project is refused); one
		// disabled is offered neither way.
		web, webItem := find(api.StepTargetWorkflow, "monitoring-web"), find(api.StepTargetItem, "web-monitoring")
		if web == nil || web.ProjectIds == nil || !slices.Equal(*web.ProjectIds, []string{"prj_web"}) ||
			webItem == nil || webItem.ProjectIds == nil || !slices.Equal(*webItem.ProjectIds, []string{"prj_web"}) {
			t.Fatalf("offered to the web project: workflow %+v, item %+v", web, webItem)
		}
		if find(api.StepTargetWorkflow, "retired") != nil || find(api.StepTargetItem, "retired-item") != nil {
			t.Fatalf("a disabled workflow is offered: %v", refsOf(targets))
		}
		// Items, then templates, then workflows.
		last := map[api.CompositeStepTargetKind]int{}
		for i, tg := range targets.Items {
			last[tg.Kind] = i
		}
		if !(last[api.StepTargetItem] < last[api.StepTargetAwx] &&
			last[api.StepTargetAwx] < last[api.StepTargetWorkflow]) {
			t.Fatalf("the order of %v", refsOf(targets))
		}

		// The designer's edit, as the canvas saves it: a step after the
		// nodes, its inputs bound to the form and to the nodes' outputs, and a
		// condition.
		var draft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/postgres-cluster/versions", admin, api.CatalogItemVersionCreate{}, 201, &draft)
		def := draft.Definition
		steps := def.Blueprint["steps"].([]any)
		monitor := map[string]any{"key": "monitor", "title": "Monitoring", "awx": map[string]any{"job_template": "monitoring-agent"},
			"with": map[string]any{"listen_port": 9187, "target": "steps.nodes.fqdn", "database": "inputs.name"},
			"when": "inputs.data_gb >= 50"}
		def.Blueprint["steps"] = append([]any{steps[0], monitor}, steps[1:]...)
		// A binding to a later step is refused where it is.
		bad := def
		bad.Blueprint = map[string]any{"kind": "composite", "rollback_on_failure": true,
			"steps": append([]any{steps[0], map[string]any{"key": "monitor", "awx": map[string]any{"job_template": "monitoring-agent"},
				"with": map[string]any{"target": "steps.owner.job_id"}}}, steps[1:]...)}
		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/catalog/items/postgres-cluster/versions/"+draft.Id, admin,
			api.CatalogItemVersionUpdate{Definition: bad, BaseRevision: &draft.Revision}, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/definition/blueprint/steps/1/with/target") {
			t.Fatalf("a binding to a later step = %v", problem.Errors)
		}
		h.json("PUT", "/api/v1/admin/catalog/items/postgres-cluster/versions/"+draft.Id, admin,
			api.CatalogItemVersionUpdate{Definition: def, BaseRevision: &draft.Revision}, 200, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/postgres-cluster/versions/"+draft.Id+"/publish", admin, nil, 200, nil)

		// Requested: the step runs after the nodes, bound to the first node.
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/postgres-cluster?project_id=prj_data", "sbrunner", nil, 200, &item)
		if item.Version.Id != draft.Id {
			t.Fatalf("the catalog serves %s, not the version published (%s)", item.Version.Id, draft.Id)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", "sbrunner", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
			ProjectId: "prj_data", Environment: "dev", Inputs: map[string]any{"name": "ledger", "pg_version": "16.6", "size": "S",
				"data_gb": 50, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-db-dev-vl320"}},
			202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, "sbrunner", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("request = %s %s", req.Status, errorOf(req))
		}
		var timeline struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "sbrunner", nil, 200, &timeline)
		var keys []string
		all := map[string]api.Step{}
		for _, s := range timeline.Items {
			keys = append(keys, s.Key)
			all[s.Key] = s
		}
		if i, j, k := slices.Index(keys, "step:nodes"), slices.Index(keys, "step:monitor"), slices.Index(keys, "step:owner"); !(i >= 0 && i < j && j < k) {
			t.Fatalf("the timeline's steps = %v", keys)
		}
		if all["step:monitor"].Status != api.StepStatusDone || all["step:monitor"].ExternalRef == nil {
			t.Fatalf("step:monitor = %+v", all["step:monitor"])
		}
		job, _ := strconv.Atoi(all["step:monitor"].ExternalRef.Id)
		vars := h.awx.Launched(job).ExtraVars
		if !strings.HasPrefix(vars["target"].(string), "data-dev-pgledger01.") || vars["database"] != "ledger" || vars["listen_port"] != float64(9187) {
			t.Fatalf("the monitoring job got %v", vars)
		}
		// What the steps gave later ones is what the designer offered:
		// core's stepOutputs, as the workflow's vmOutputs and FinishStep make them.
		for key, tg := range map[string]*api.CompositeStepTarget{"step:nodes": nodes, "step:monitor": agent} {
			st, err := h.store.GetStepByKey(ctx, req.Id, key)
			if err != nil {
				t.Fatal(err)
			}
			var gave map[string]any
			if err := json.Unmarshal(st.Outputs, &gave); err != nil {
				t.Fatalf("%s's outputs %s: %v", key, st.Outputs, err)
			}
			_, offered := names(tg)
			if got := slices.Sorted(maps.Keys(gave)); !slices.Equal(got, slices.Sorted(slices.Values(offered))) {
				t.Fatalf("%s gave %v; the designer offers %v", key, got, offered)
			}
		}

		// Below its condition, the step is skipped and the owner's runs.
		h.json("POST", "/api/v1/requests", "sbrunner", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
			ProjectId: "prj_data", Environment: "dev", Inputs: map[string]any{"name": "audit", "pg_version": "16.6", "size": "S",
				"data_gb": 20, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-db-dev-vl320"}},
			202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, "sbrunner", nil, 200, &req)
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "sbrunner", nil, 200, &timeline)
		all = map[string]api.Step{}
		for _, s := range timeline.Items {
			all[s.Key] = s
		}
		if req.Status != api.RequestStatusSucceeded || all["step:monitor"].Status != api.StepStatusSkipped ||
			deref(all["step:monitor"].Detail) != "Its condition is false." || all["step:owner"].Status != api.StepStatusDone {
			t.Fatalf("below the condition: %s, monitor %+v, owner %s", req.Status, all["step:monitor"], all["step:owner"].Status)
		}
	})
}

func refsOf(l api.CompositeStepTargetList) []string {
	out := make([]string, len(l.Items))
	for i, tg := range l.Items {
		out[i] = string(tg.Kind) + ":" + tg.Ref
	}
	return out
}
