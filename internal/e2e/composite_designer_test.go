package e2e

import (
	"context"
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
		reg := func(key string, usages ...api.WorkflowUsage) {
			h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new(key), Title: "Register " + key,
				WorkflowType: key + ".v1", TaskQueue: "platform-workflows", Usages: usages,
				InputSchema: &map[string]any{"type": "object", "required": []string{"fqdn"}, "properties": map[string]any{
					"team": map[string]any{"type": "string"}, "fqdn": map[string]any{"type": "string", "title": "FQDN"}}}}, 201, nil)
		}
		reg("monitoring", api.WorkflowUsageCompositeStep)
		reg("cmdb", api.WorkflowUsageCatalogItem)

		// Only those who build items see what steps can run.
		h.json("GET", "/api/v1/admin/catalog/composite-steps", "tnguyen", nil, 403, nil)
		var targets api.CompositeStepTargetList
		h.json("GET", "/api/v1/admin/catalog/composite-steps", admin, nil, 200, &targets)
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
		// AWX templates imported from AWX, with their surveys.
		agent := find(api.StepTargetAwx, "monitoring-agent")
		if agent == nil || agent.Runs != api.StepRunsAwx {
			t.Fatalf("monitoring-agent = %+v (targets %v)", agent, refsOf(targets))
		}
		if in, out = names(agent); !slices.Equal(in, []string{"monitoring_agent", "listen_port"}) || !slices.Equal(out, []string{"job_id"}) {
			t.Fatalf("monitoring-agent's inputs %v, outputs %v", in, out)
		}
		// Custom workflows registered for composite steps only: whatever they return.
		monitoring := find(api.StepTargetWorkflow, "monitoring")
		if monitoring == nil || !monitoring.OpenOutputs || len(monitoring.Outputs) != 0 || monitoring.Inputs[0].Name != "fqdn" ||
			find(api.StepTargetWorkflow, "cmdb") != nil {
			t.Fatalf("workflows: monitoring %+v, cmdb %v", monitoring, find(api.StepTargetWorkflow, "cmdb") != nil)
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
	})
}

func refsOf(l api.CompositeStepTargetList) []string {
	out := make([]string, len(l.Items))
	for i, tg := range l.Items {
		out[i] = string(tg.Kind) + ":" + tg.Ref
	}
	return out
}
