package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/integrations/awx"
)

// monitoringForm is a form designed for the monitoring agent's AWX action:
// its defaults and options read the VM, a field shows for one agent only.
const monitoringForm = `{
	"form_schema": {"type": "object", "required": ["monitoring_agent", "listen_port"], "properties": {
		"monitoring_agent": {"type": "string", "enum": ["node_exporter", "telegraf"]},
		"listen_port": {"type": "integer", "minimum": 1024, "maximum": 65535},
		"metrics_path": {"type": "string"}}},
	"ui_schema": {
		"layout": {"kind": "page", "sections": [{"key": "agent", "title": "Agent", "fields": ["about", "monitoring_agent", "listen_port", "metrics_path"]}]},
		"fields": {
			"about": {"widget": "info", "label": "About", "help": "The agent reports to the monitoring server."},
			"monitoring_agent": {"widget": "select", "label": "Agent",
				"value_source": {"kind": "cel", "params": {"options": "resource.os_family == 'linux' ? ['node_exporter', 'telegraf'] : ['telegraf']"}},
				"rules": {"default": "resource.os_family == 'linux' ? 'node_exporter' : 'telegraf'"}},
			"listen_port": {"widget": "number", "label": "Port", "rules": {"default": "inputs.monitoring_agent == 'telegraf' ? 8125 : 9100"}},
			"metrics_path": {"widget": "text", "label": "Metrics path",
				"rules": {"visible": "inputs.monitoring_agent == 'telegraf'", "default": "'/metrics/' + resource.name"}}}},
	"initial_values": {}}`

func designedDefinition(t *testing.T, raw string, change func(*api.Day2FormDefinition)) api.Day2FormDefinition {
	t.Helper()
	var def api.Day2FormDefinition
	if err := json.Unmarshal([]byte(raw), &def); err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(&def)
	}
	return def
}

// TestDay2DesignedForm: an administrator designs the form of an AWX
// day-2 action; while it is inactive the action keeps its survey's form,
// once active its rules and sources work on the target and a run gets
// what they work out; dropped, the survey's form is back.
func TestDay2DesignedForm(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log,
			Filter: awx.TemplateFilter{Labels: []string{"catalog"}}, ActionLabels: []string{"day2"}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", "mkeller", nil, 200, nil)
		const key, admin = "awx.monitoring-agent", "mkeller"
		path := "/api/v1/admin/day2-forms/" + key
		var problem struct{ Errors []api.FieldError }

		// Before the first save, the survey's form to start from.
		var design api.Day2FormDesign
		h.json("GET", path, admin, nil, 200, &design)
		props, _ := design.Definition.FormSchema["properties"].(map[string]any)
		if design.Revision != 0 || design.Active || design.Kind != api.Day2FormAwx || len(props) != 2 ||
			!slices.Equal(design.Types, []api.Day2FormDesignTypes{api.Day2FormDesignTypesVsphereVm}) {
			t.Fatalf("the form to start from = %+v", design)
		}
		h.json("GET", path, "tnguyen", nil, 403, nil)
		h.json("GET", "/api/v1/admin/day2-forms/power_off", admin, nil, 404, nil)
		h.json("GET", "/api/v1/admin/day2-forms/awx.nothing", admin, nil, 404, nil)

		// What an action's form can't do, and a rule that isn't CEL, are refused.
		save := func(def api.Day2FormDefinition, base int, active *bool, want int, out any) {
			t.Helper()
			h.json("PUT", path, admin, api.Day2FormWrite{Definition: def, BaseRevision: &base, Active: active}, want, out)
		}
		refused := func(field string) {
			t.Helper()
			if !slices.ContainsFunc(problem.Errors, func(e api.FieldError) bool { return e.Field == field }) {
				t.Fatalf("errors = %+v, want one on %s", problem.Errors, field)
			}
		}
		save(designedDefinition(t, monitoringForm, func(d *api.Day2FormDefinition) { d.UiSchema.Layout.Kind = api.Wizard }), 0, nil, 422, &problem)
		refused("/definition/ui_schema/layout/kind")
		save(designedDefinition(t, monitoringForm, func(d *api.Day2FormDefinition) {
			f := d.UiSchema.Fields["listen_port"]
			f.Rules.Default = new("((")
			d.UiSchema.Fields["listen_port"] = f
		}), 0, nil, 422, &problem)
		refused("/definition/ui_schema/fields/listen_port/rules/default")
		save(designedDefinition(t, monitoringForm, func(d *api.Day2FormDefinition) {
			f := d.UiSchema.Fields["metrics_path"]
			f.Secret = new(true)
			d.UiSchema.Fields["metrics_path"] = f
		}), 0, nil, 422, &problem)
		refused("/definition/ui_schema/fields/metrics_path/secret")

		// Saved, it is a draft: the action still asks for the survey's form.
		def := designedDefinition(t, monitoringForm, nil)
		save(def, 0, nil, 200, &design)
		if design.Revision != 1 || design.Active || len(design.Warnings) != 1 || !strings.Contains(design.Warnings[0], "metrics_path") {
			t.Fatalf("the saved draft = %+v", design)
		}
		d := buildVM(h, model)
		if a := d.actions("tnguyen")[key]; a.Form == nil || a.Form.Designed != nil {
			t.Fatalf("the action asks for %+v while its form is a draft", a.Form)
		}

		// The designer previews it on the VM.
		var preview api.Day2FormPreview
		h.json("POST", path+"/preview", admin, api.Day2FormDesignerQuery{DeploymentId: d.id, ResourceId: &d.resource,
			Inputs: map[string]any{}}, 200, &preview)
		if f := preview.Fields; f["monitoring_agent"].Default != "node_exporter" || f["listen_port"].Default != float64(9100) ||
			f["metrics_path"].Visible || len(preview.Errors) != 0 {
			t.Fatalf("the designer's preview = %+v, errors %+v", preview.Fields, preview.Errors)
		}
		h.json("POST", path+"/preview", admin, api.Day2FormDesignerQuery{DeploymentId: d.id, Inputs: map[string]any{}}, 422, &problem)
		refused("/resource_id")
		h.json("POST", path+"/preview", "tnguyen", api.Day2FormDesignerQuery{DeploymentId: d.id, ResourceId: &d.resource,
			Inputs: map[string]any{}}, 403, nil)

		// Turned on from a revision someone saved over, it is refused; from the latest, the action asks for it.
		on := true
		save(def, 0, &on, 409, nil)
		save(def, 1, &on, 200, &design)
		var list api.Day2FormList
		h.json("GET", "/api/v1/admin/day2-forms", admin, nil, 200, &list)
		if design.Revision != 2 || !design.Active || len(list.Items) != 1 || !list.Items[0].Active {
			t.Fatalf("the form turned on = %+v, listed %+v", design, list.Items)
		}
		a := d.actions("tnguyen")[key]
		if a.Form == nil || a.Form.Designed == nil || !*a.Form.Designed || a.Form.UiSchema.Fields["metrics_path"].Label != "Metrics path" {
			t.Fatalf("the action asks for %+v", a.Form)
		}

		// Its rules and sources work on the VM, for whoever runs it.
		actionPath := "/api/v1/deployments/" + d.id + "/actions"
		h.json("POST", actionPath+"/preview", "tnguyen", api.Day2FormQuery{Action: key, ResourceId: &d.resource,
			Inputs: map[string]any{"monitoring_agent": "telegraf"}}, 200, &preview)
		vmName := d.actions("tnguyen")["power_off"].ResourceName
		if f := preview.Fields; f["listen_port"].Default != float64(8125) || !f["metrics_path"].Visible ||
			f["metrics_path"].Default != "/metrics/"+deref(vmName) {
			t.Fatalf("the action's preview = %+v", preview.Fields)
		}
		var values api.FieldValues
		h.json("POST", actionPath+"/values", "tnguyen", api.Day2FieldValuesQuery{Action: key, ResourceId: &d.resource,
			Field: "monitoring_agent", Inputs: map[string]any{}}, 200, &values)
		if values.Options == nil || len(*values.Options) != 2 || (*values.Options)[1].Value != "telegraf" {
			t.Fatalf("the agent's options = %+v", values)
		}
		h.json("POST", actionPath+"/values", "tnguyen", api.Day2FieldValuesQuery{Action: key, ResourceId: &d.resource,
			Field: "nothing", Inputs: map[string]any{}}, 400, nil)
		h.json("POST", actionPath+"/values", "sbrunner", api.Day2FieldValuesQuery{Action: key, ResourceId: &d.resource,
			Field: "monitoring_agent", Inputs: map[string]any{}}, 404, nil)

		// A run gets what the form works out: defaults, and no hidden field.
		h.acts.AWXInventorySource = 1
		launched := func(r api.Request) map[string]any {
			t.Helper()
			step, err := h.store.GetStepByKey(ctx, r.Id, "awx.job:monitoring-agent")
			if err != nil || step.ExternalID == nil {
				t.Fatalf("job step = %+v, %v", step, err)
			}
			var jobID int
			fmt.Sscan(*step.ExternalID, &jobID)
			return h.awx.Launched(jobID).ExtraVars
		}
		d.run("tnguyen", key, map[string]any{"listen_port": 80}, 422)
		vars := launched(d.succeed("tnguyen", key, map[string]any{"metrics_path": "/kept-out"}))
		if vars["monitoring_agent"] != "node_exporter" || vars["listen_port"] != float64(9100) && vars["listen_port"] != int64(9100) {
			t.Fatalf("extra_vars of a run with the defaults = %v", vars)
		}
		if _, ok := vars["metrics_path"]; ok {
			t.Fatalf("a hidden field reached AWX: %v", vars)
		}
		vars = launched(d.succeed("tnguyen", key, map[string]any{"monitoring_agent": "telegraf"}))
		if fmt.Sprint(vars["listen_port"]) != "8125" || vars["metrics_path"] != "/metrics/"+deref(vmName) {
			t.Fatalf("extra_vars of a run with telegraf = %v", vars)
		}

		// Dropped, the survey's form is back.
		h.json("DELETE", path, admin, nil, 204, nil)
		h.json("GET", path, admin, nil, 200, &design)
		if a := d.actions("tnguyen")[key]; design.Revision != 0 || a.Form == nil || a.Form.Designed != nil {
			t.Fatalf("after the form is dropped: %+v, the action asks for %+v", design, a.Form)
		}
	})
}

// TestDay2DesignedWorkflowForm: a workflow's day-2 action on a whole
// deployment gets its defaults from the deployment.
func TestDay2DesignedWorkflowForm(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		reg := api.WorkflowRegistrationWrite{Key: new("decommission"), Title: "Decommission an application",
			WorkflowType: "decommission.v1", TaskQueue: "platform-workflows",
			InputSchema: &map[string]any{"type": "object", "required": []string{"application"}, "additionalProperties": false,
				"properties": map[string]any{"application": map[string]any{"type": "string", "minLength": 1}}},
			Usages: []api.WorkflowUsage{api.WorkflowUsageDay2Action},
			Day2: &api.WorkflowDay2{Name: "Decommission", Types: []api.WorkflowDay2Types{api.WorkflowDay2TypesDeployment},
				Offered: new(true)}}
		h.json("POST", "/api/v1/admin/workflows", admin, reg, 201, nil)
		const key = "wf.decommission"
		path := "/api/v1/admin/day2-forms/" + key
		var design api.Day2FormDesign
		h.json("GET", path, admin, nil, 200, &design)
		if design.Kind != api.Day2FormWorkflow || design.Name != "Decommission" || design.Definition.UiSchema.Fields["application"].Label == "" {
			t.Fatalf("the workflow's form to start from = %+v", design)
		}
		def := design.Definition
		f := def.UiSchema.Fields["application"]
		f.Rules = &api.UiRules{Default: new("deployment.name + '-' + resource.os_family")}
		def.UiSchema.Fields["application"] = f
		def.FormSchema["properties"].(map[string]any)["ticket"] = map[string]any{"type": "string"}
		def.UiSchema.Fields["ticket"] = api.UiField{Widget: api.UiWidgetText, Label: "Ticket"}
		def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "ticket")
		h.json("PUT", path, admin, api.Day2FormWrite{Definition: def, BaseRevision: new(0), Active: new(true)}, 200, &design)
		if len(design.Warnings) != 1 || !strings.Contains(design.Warnings[0], "ticket") {
			t.Fatalf("warnings = %v", design.Warnings)
		}

		d := buildVM(h, nil)
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+d.id, "tnguyen", nil, 200, &dep)
		var preview api.Day2FormPreview
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions/preview", "tnguyen", api.Day2FormQuery{Action: key, Inputs: map[string]any{}},
			200, &preview)
		if got := preview.Fields["application"].Default; got != dep.Name+"-linux" || len(preview.Errors) != 0 {
			t.Fatalf("the workflow action's preview = %+v, errors %+v", preview.Fields, preview.Errors)
		}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions/preview", "tnguyen", api.Day2FormQuery{Action: key, ResourceId: &d.resource,
			Inputs: map[string]any{}}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/resource_id" {
			t.Fatalf("a preview on a VM of a deployment's action = %+v", problem.Errors)
		}
		var req api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: key, Inputs: map[string]any{}},
			202, &req, "Idempotency-Key", uuid.NewString())
		var inputs map[string]any
		if err := h.store.Pool.QueryRow(ctx, `select inputs from requests where id = $1`, req.Id).Scan(&inputs); err != nil ||
			inputs["application"] != dep.Name+"-linux" {
			t.Fatalf("the run's inputs = %v (%v)", inputs, err)
		}
	})
}
