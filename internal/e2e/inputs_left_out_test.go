package e2e

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/integrations/awx"
)

// agentForm is a form designed for the monitoring agent's AWX action
// whose inputs all have a default but the ticket: a rule's, an initial
// value, the schema's.
const agentForm = `{
	"form_schema": {"type": "object", "required": ["monitoring_agent", "listen_port", "ticket"], "properties": {
		"monitoring_agent": {"type": "string", "enum": ["node_exporter", "telegraf"]},
		"listen_port": {"type": "integer", "minimum": 1024, "maximum": 65535},
		"metrics_path": {"type": "string", "default": "/metrics"},
		"ticket": {"type": "string", "minLength": 1}}},
	"ui_schema": {
		"layout": {"kind": "page", "sections": [{"key": "agent", "title": "Agent",
			"fields": ["monitoring_agent", "listen_port", "metrics_path", "ticket"]}]},
		"fields": {
			"monitoring_agent": {"widget": "select", "label": "Agent",
				"options": [{"value": "node_exporter", "label": "node_exporter"}, {"value": "telegraf", "label": "telegraf"}],
				"rules": {"default": "resource.os_family == 'linux' ? 'telegraf' : 'node_exporter'"}},
			"listen_port": {"widget": "number", "label": "Port"},
			"metrics_path": {"widget": "text", "label": "Metrics path"},
			"ticket": {"widget": "text", "label": "Ticket"}}},
	"initial_values": {"listen_port": 9200}}`

// TestInputsLeftOut: a request and a designed AWX action given only the
// inputs without a default get the others from their forms, as the web
// app shows them; an input sent as null stays empty (ADR-0055, #194).
func TestInputsLeftOut(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, dev, user = "prj_web", "dev", "tnguyen"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, user, nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, user, nil, 200, &form)

		// A source reads the initial value of an input left out.
		var name api.FieldValues
		h.json("POST", "/api/v1/forms/values", user, api.FieldValuesQuery{VersionId: form.VersionId, ProjectId: web,
			Environment: new(dev), Field: "vm_name", Inputs: map[string]any{}}, 200, &name)
		if name.Value != "web-dev-app001" {
			t.Fatalf("the name with the initial role = %v", name.Value)
		}

		// A dry run and a request with the placement alone get the role, size and configuration the form starts with.
		placement := map[string]any{"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: dev,
			Inputs: placement, Justification: new("Inputs left out, #194.")}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", user, draft, 200, &pv)
		if !pv.Valid || len(pv.Errors) != 0 || pv.Names == nil || !slices.Contains(*pv.Names, "web-dev-app001") {
			t.Fatalf("the dry run = valid %v, errors %+v, names %v", pv.Valid, pv.Errors, pv.Names)
		}
		emptied := draft
		emptied.Inputs = maps.Clone(placement)
		emptied.Inputs["size"] = nil
		h.json("POST", "/api/v1/requests/preview", user, emptied, 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return e.Field == "/inputs/size" }) {
			t.Fatalf("a size sent as null took the initial one: valid %v, errors %+v", pv.Valid, pv.Errors)
		}
		var r api.Request
		h.json("POST", "/api/v1/requests", user, draft, 202, &r, "Idempotency-Key", uuid.NewString())
		if r.Inputs == nil || (*r.Inputs)["role"] != "app" || (*r.Inputs)["size"] != "M" ||
			fmt.Sprint((*r.Inputs)["post_config"]) != "[linux-baseline idm-join monitoring-agent]" {
			t.Fatalf("the request's inputs = %v", r.Inputs)
		}
		h.runWorkflow(r.Id, nil)
		r = h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
		d := &vmDeployment{h: h, model: model, id: (*r.DeploymentIds)[0], built: r, viewer: user}
		if vm := d.vm(); vm.CpuCount != 4 || vm.MemoryMb != 16*1024 {
			t.Fatalf("the VM of size M has %d vCPU and %d MB", vm.CpuCount, vm.MemoryMb)
		}
		d.resource = *d.actions(user)["power_off"].ResourceId

		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log,
			Filter: awx.TemplateFilter{Labels: []string{"catalog"}}, ActionLabels: []string{"day2"}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", "mkeller", nil, 200, nil)
		h.acts.AWXInventorySource = 1
		const key = "awx.monitoring-agent"
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

		// The survey's form: a run without inputs gets its defaults.
		vars := launched(d.succeed(user, key, nil))
		if vars["monitoring_agent"] != "node_exporter" || fmt.Sprint(vars["listen_port"]) != "9100" {
			t.Fatalf("extra_vars of a run with the survey's defaults = %v", vars)
		}

		// A designed form: the rule's default, the initial value and the schema's default.
		h.json("PUT", "/api/v1/admin/day2-forms/"+key, "mkeller", api.Day2FormWrite{Definition: designedDefinition(t, agentForm, nil),
			BaseRevision: new(0), Active: new(true)}, 200, nil)
		a := d.actions(user)[key]
		if a.Form == nil || fmt.Sprint(a.Form.InitialValues) != "map[listen_port:9200 metrics_path:/metrics]" {
			t.Fatalf("the form the action asks for starts with %v", a.Form)
		}
		var preview api.Day2FormPreview
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions/preview", user, api.Day2FormQuery{Action: key, ResourceId: &d.resource,
			Inputs: map[string]any{}}, 200, &preview)
		if len(preview.Errors) != 1 || preview.Errors[0].Field != "/inputs/ticket" {
			t.Fatalf("the preview without inputs = %+v", preview.Errors)
		}
		vars = launched(d.succeed(user, key, map[string]any{"ticket": "CHG-1"}))
		if vars["monitoring_agent"] != "telegraf" || fmt.Sprint(vars["listen_port"]) != "9200" || vars["metrics_path"] != "/metrics" ||
			vars["ticket"] != "CHG-1" {
			t.Fatalf("extra_vars of a run with the ticket alone = %v", vars)
		}
		vars = launched(d.succeed(user, key, map[string]any{"ticket": "CHG-2", "metrics_path": nil}))
		if _, ok := vars["metrics_path"]; ok || vars["ticket"] != "CHG-2" {
			t.Fatalf("a metrics path sent as null took the schema's default: %v", vars)
		}
	})
}
