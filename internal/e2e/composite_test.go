package e2e

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
)

// TestCompositeItems: a composite item builds a VM, then creates a database
// user on it and registers it in monitoring at the same time, in one
// timeline; the database user's inputs come from the VM step; a failure
// undoes what was done and deletes the VM (S10.6, ADR-0022).
func TestCompositeItems(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }
		fields := []string{"role", "size", "cluster", "datastore", "network", "database", "role_name"}
		props := map[string]any{}
		ui := map[string]api.UiField{}
		for _, f := range fields {
			props[f] = map[string]any{"type": "string", "minLength": 1}
			ui[f] = api.UiField{Widget: api.UiWidgetText, Label: f}
		}
		def := api.CatalogVersionDefinition{
			FormSchema:    map[string]any{"type": "object", "required": fields, "properties": props},
			UiSchema:      api.UiSchema{Fields: ui},
			InitialValues: map[string]any{},
			Blueprint: map[string]any{"kind": "composite", "rollback_on_failure": true, "steps": []any{
				map[string]any{"key": "vm", "title": "The VM", "item": "rhel9-server", "with": map[string]any{
					"role": "inputs.role", "size": "inputs.size", "cluster": "inputs.cluster", "datastore": "inputs.datastore",
					"network": "inputs.network", "post_config": []any{"linux-baseline"}}},
				map[string]any{"parallel": []any{
					map[string]any{"key": "db_user", "title": "Database user", "item": "db-user",
						"with": map[string]any{"database": "inputs.database", "role_name": "inputs.role_name",
							"privileges": `"read"`, "host": "steps.vm.fqdn"},
						"undo": map[string]any{"job_template": "idm-hbac-group", "with": map[string]any{"role_name": "inputs.role_name"}}},
					map[string]any{"key": "monitoring", "awx": map[string]any{"job_template": "monitoring-agent", "title": "Monitoring"},
						"with": map[string]any{"target": "steps.vm.fqdn"}},
				}},
				map[string]any{"key": "dba_notice", "awx": map[string]any{"job_template": "role-nginx", "title": "Tell the DBAs"},
					"when": `inputs.database == "ledger"`},
			}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "VM and database user", Fields: fields}}

		// Checked when saved: keys, references to earlier steps, CEL.
		bad := def
		bad.Blueprint = map[string]any{"kind": "composite", "steps": []any{
			map[string]any{"key": "a", "awx": map[string]any{"job_template": "x"}, "with": map[string]any{"x": "steps.b.fqdn"}},
			map[string]any{"key": "a", "item": "rhel9-server", "awx": map[string]any{"job_template": "x"}},
			map[string]any{"key": "c", "item": "rhel9-server", "undo": map[string]any{"job_template": "x"}, "when": "inputs.x =="},
		}}
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-composite", Name: "Bad",
			Summary: "Bad.", Category: "compute", Type: api.Composite, Definition: &bad}, 422, &problem)
		for _, want := range []string{"/definition/blueprint/steps/0/with/x", "/definition/blueprint/steps/1/key",
			"/definition/blueprint/steps/1", "/definition/blueprint/steps/2/when"} {
			if !slices.Contains(fieldsOf(problem.Errors), want) {
				t.Fatalf("a bad composite = %v, no %s", fieldsOf(problem.Errors), want)
			}
		}

		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "vm-db-user", Name: "VM with a database user",
			Summary: "A VM, its database user and monitoring.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/vm-db-user/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/vm-db-user/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		request := func(role string) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": role, "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "database": "orders", "role_name": "orders_ro"}},
				202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
			return req
		}
		timeline := func(id string) map[string]api.Step {
			t.Helper()
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+id+"/steps", "tnguyen", nil, 200, &steps)
			out := map[string]api.Step{}
			for _, s := range steps.Items {
				out[s.Key] = s
				for _, ch := range s.Children {
					out[ch.Key] = ch
				}
			}
			return out
		}

		// Built: the VM, then both at once, in one timeline.
		ok := request("db")
		if ok.Status != api.RequestStatusSucceeded {
			t.Fatalf("composite request = %s %s", ok.Status, errorOf(ok))
		}
		steps := timeline(ok.Id)
		for _, key := range []string{"step:vm", "step:vm/vsphere.clone", "step:vm/awx.job:linux-baseline", "step:db_user", "step:monitoring"} {
			if steps[key].Status != api.StepStatusDone {
				t.Fatalf("%s = %s (steps %v)", key, steps[key].Status, keysOf(steps))
			}
		}
		if steps["step:dba_notice"].Status != api.StepStatusSkipped {
			t.Fatalf("a step whose condition is false = %s", steps["step:dba_notice"].Status)
		}
		var vmName string
		_ = h.store.Pool.QueryRow(ctx, `select name from deployments where created_by_request_id = $1`, ok.Id).Scan(&vmName)
		dbJob, _ := strconv.Atoi(steps["step:db_user"].ExternalRef.Id)
		vars := h.awx.Launched(dbJob).ExtraVars
		if !strings.HasPrefix(vars["host"].(string), vmName+".") || vars["role_name"] != "orders_ro" || vars["privileges"] != "read" {
			t.Fatalf("the database user's job got %v", vars)
		}
		var outputs []byte
		_ = h.store.Pool.QueryRow(ctx, `select outputs from request_steps where request_id = $1 and key = 'step:db_user'`, ok.Id).Scan(&outputs)
		if !strings.Contains(string(outputs), `"job_id"`) {
			t.Fatalf("db_user's outputs = %s", outputs)
		}

		// A step that fails: the database user is undone, the VM deleted.
		h.awx.FailTask = "monitoring : install node_exporter"
		failed := request("db")
		h.awx.FailTask = ""
		if failed.Status != api.RequestStatusFailed {
			t.Fatalf("a composite whose step failed = %s", failed.Status)
		}
		steps = timeline(failed.Id)
		if steps["step:monitoring"].Status != api.StepStatusFailed || steps["step:db_user/undo"].Status != api.StepStatusDone {
			t.Fatalf("monitoring %s, undo %s (steps %v)", steps["step:monitoring"].Status, steps["step:db_user/undo"].Status, keysOf(steps))
		}
		_ = h.store.Pool.QueryRow(ctx, `select name from deployments where created_by_request_id = $1`, failed.Id).Scan(&vmName)
		if _, err := find.NewFinder(c).VirtualMachine(ctx, vmName); err == nil {
			t.Fatalf("%s is still there after the rollback", vmName)
		}

		// The seeded composite without steps can't be requested.
		h.json("GET", "/api/v1/catalog/items/postgres-cluster?project_id=prj_data", "sbrunner", nil, 200, nil)
		var pg api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/postgres-cluster?project_id=prj_data", "sbrunner", nil, 200, &pg)
		h.json("POST", "/api/v1/requests", "sbrunner", api.RequestDraft{CatalogItemId: pg.Id, VersionId: pg.Version.Id,
			ProjectId: "prj_data", Environment: "dev", Inputs: map[string]any{"name": "ledger"}}, 422, &problem,
			"Idempotency-Key", uuid.NewString())
		_ = provision.KeyStep
	})
}

func keysOf(m map[string]api.Step) []string {
	out := make([]string, 0, len(m))
	for k, s := range m {
		out = append(out, k+"="+string(s.Status))
	}
	slices.Sort(out)
	return out
}
