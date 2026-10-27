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
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/integrations/awx"
)

// TestAWXWorkflowItem requests an item backed by an AWX workflow: the step
// shows one task per workflow node.
func TestAWXWorkflowItem(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		// Firewall flows are reviewed by network security; not what this is about.
		if _, err := h.store.Pool.Exec(ctx, `delete from approval_policies where id = 'pol_fw'`); err != nil {
			t.Fatal(err)
		}
		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/firewall-flow?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/firewall-flow/form?project_id="+web, "lmartin", nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"source": "web-dev-app001", "destination": "10.22.30.10", "port": 5432, "protocol": "tcp"}}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("workflow item = %s", req.Status)
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &steps)
		var nodes []string
		for _, s := range steps.Items {
			for _, c := range s.Children {
				if c.Status == api.StepStatusDone {
					nodes = append(nodes, c.Title)
				}
			}
		}
		if strings.Join(nodes, ",") != "fw-check-flow,fw-open-change,fw-push-rules" {
			t.Fatalf("workflow nodes = %v", nodes)
		}
	})
}

// TestAWXCatalogImport: templates become items with forms from their
// surveys (or are linked to the seeded items launching them), an imported
// item can be requested, a changed survey is flagged with what changed, and
// templates no longer chosen are flagged gone.
func TestAWXCatalogImport(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log, Filter: awx.TemplateFilter{Labels: []string{"rvav"}}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", "tnguyen", nil, 403, nil)
		var list api.AwxTemplateList
		h.json("POST", "/api/v1/admin/awx/templates/sync", "mkeller", nil, 200, &list)
		byName := map[string]api.AwxTemplate{}
		for _, tpl := range list.Items {
			byName[tpl.Name] = tpl
			if tpl.ItemId == nil || tpl.SurveyChanged || tpl.Gone {
				t.Errorf("after the first sync: %+v", tpl)
			}
		}
		if len(list.Items) != len(h.awx.Plays) {
			t.Fatalf("%d templates, the fake has %d", len(list.Items), len(h.awx.Plays))
		}
		if id := byName["ddi-dns-record"].ItemId; id == nil || *id != "itm_dns-record" {
			t.Fatalf("the seeded DNS record item was not linked: %+v", byName["ddi-dns-record"])
		}
		if id := byName["fw-flow-request"].ItemId; id == nil || *id != "itm_firewall-flow" || !byName["fw-flow-request"].Workflow {
			t.Fatalf("the seeded firewall workflow was not linked: %+v", byName["fw-flow-request"])
		}

		// The imported monitoring agent item, with its survey as the form.
		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/monitoring-agent?project_id="+web, "tnguyen", nil, 200, &item)
		if item.Type != api.AwxJobTemplate || item.Category != "automation" || item.Id != *byName["monitoring-agent"].ItemId {
			t.Fatalf("imported item = %+v", item)
		}
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/monitoring-agent/form?project_id="+web, "tnguyen", nil, 200, &form)
		if f := form.UiSchema.Fields["listen_port"]; f.Label != "Port" || f.Widget != api.UiWidgetNumber || form.InitialValues["listen_port"] != float64(9100) {
			t.Fatalf("port field = %+v, initial %v", f, form.InitialValues)
		}
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"monitoring_agent": "telegraf", "listen_port": 80}}
		status, body, _ := h.do("POST", "/api/v1/requests", "tnguyen", draft, "Idempotency-Key", uuid.NewString())
		if status != 422 || !strings.Contains(string(body), "listen_port") {
			t.Fatalf("a port below the survey's minimum = %d %s", status, body)
		}
		draft.Inputs["listen_port"] = 9273
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("imported item request = %s", req.Status)
		}

		// The survey changes in AWX: flagged, with what changed; the form stays.
		h.awx.Surveys["monitoring-agent"][1].Max = new(int)
		*h.awx.Surveys["monitoring-agent"][1].Max = 32767
		h.json("POST", "/api/v1/admin/awx/templates/sync", "mkeller", nil, 200, &list)
		for _, tpl := range list.Items {
			if tpl.Name == "monitoring-agent" && (!tpl.SurveyChanged || strings.Join(tpl.SurveyDiff, "; ") != "listen_port: maximum 65535 → 32767") {
				t.Fatalf("changed survey = %+v", tpl)
			}
		}

		// No longer chosen: flagged gone, the items stay.
		h.svc.AWXCatalog.Filter.Labels = []string{"retired"}
		h.json("POST", "/api/v1/admin/awx/templates/sync", "mkeller", nil, 200, &list)
		for _, tpl := range list.Items {
			if !tpl.Gone {
				t.Fatalf("not gone: %+v", tpl)
			}
		}
		h.json("GET", "/api/v1/catalog/items/monitoring-agent?project_id="+web, "tnguyen", nil, 200, &item)
	})
}

// TestAnsibleInventory: AWX's view of rvav's VMs, read with an API token of
// the auditor service account.
func TestAnsibleInventory(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.acts.AWXInventorySource = 1
		h.runWorkflow(req.Id, nil)

		// The post-configuration job ran against rvav's inventory.
		step, err := h.store.GetStepByKey(ctx, req.Id, "awx.job:linux-baseline")
		if err != nil || step.ExternalID == nil {
			t.Fatalf("job step = %+v, %v", step, err)
		}
		var jobID int
		fmt.Sscan(*step.ExternalID, &jobID)
		if inv := h.awx.LaunchedInventory(jobID); inv != awx.FakeInventory {
			t.Fatalf("the job ran against inventory %d", inv)
		}

		token := auth.NewToken()
		if err := h.store.CreateAPIToken(ctx, "tok_test", "usr_svc-awx", "awx inventory", auth.HashToken(token), auth.ScopeInventory, nil); err != nil {
			t.Fatal(err)
		}
		status, body, _ := h.do("GET", "/api/v1/ansible/inventory", "", nil, "Authorization", "Bearer "+token)
		if status != 200 {
			t.Fatalf("inventory = %d %s", status, body)
		}
		var inv struct {
			Meta struct {
				Hostvars map[string]map[string]any `json:"hostvars"`
			} `json:"_meta"`
			All struct{ Children []string }
		}
		_ = json.Unmarshal(body, &inv)
		var groups map[string]json.RawMessage
		_ = json.Unmarshal(body, &groups)
		vars := inv.Meta.Hostvars["web-dev-app001"]
		if vars["ansible_host"] != "10.31.10.20" || vars["rvav_project"] != "web" || vars["rvav_environment"] != "dev" ||
			vars["rvav_fqdn"] != "web-dev-app001.corp.example" || vars["rvav_os_family"] != "linux" || vars["rvav_owner"] != "thomas.nguyen@corp.example" {
			t.Fatalf("host vars = %v", vars)
		}
		if inputs, _ := vars["rvav_inputs"].(map[string]any); len(inputs) != 1 || inputs["role"] != "app" {
			t.Fatalf("inputs exposed to the inventory = %v", vars["rvav_inputs"])
		}
		for _, g := range []string{"rvav", "project_web", "env_dev", "item_rhel9_server", "os_linux", "tag_linux", "tag_rhel"} {
			var members struct{ Hosts []string }
			_ = json.Unmarshal(groups[g], &members)
			if !slices.Contains(inv.All.Children, g) || !slices.Contains(members.Hosts, "web-dev-app001") {
				t.Fatalf("group %s = %s (children %v)", g, groups[g], inv.All.Children)
			}
		}
		h.json("GET", "/api/v1/ansible/inventory", "tnguyen", nil, 403, nil)
		// The token reads the inventory and nothing else.
		if status, body, _ := h.do("GET", "/api/v1/requests?project_id=prj_web", "", nil, "Authorization", "Bearer "+token); status != 403 {
			t.Fatalf("the inventory token read requests: %d %s", status, body)
		}

		// A deployment being deleted is no longer in the inventory.
		if _, err := h.store.Pool.Exec(ctx, `update deployments set status = 'deleting' where created_by_request_id = $1`, req.Id); err != nil {
			t.Fatal(err)
		}
		_, body, _ = h.do("GET", "/api/v1/ansible/inventory", "", nil, "Authorization", "Bearer "+token)
		if strings.Contains(string(body), "web-dev-app001") {
			t.Fatalf("inventory with the deployment being deleted = %s", body)
		}
	})
}

// TestAWXDay2Action: an AWX template with the day2 label becomes a day-2
// action, with its form from the survey, offered where its condition holds,
// and run as an AWX job on rvav's inventory with the VM as limit (S6).
func TestAWXDay2Action(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log,
			Filter: awx.TemplateFilter{Labels: []string{"catalog"}}, ActionLabels: []string{"day2"}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", "mkeller", nil, 200, nil)
		const key = "awx.monitoring-agent"
		var list api.AwxActionList
		h.json("GET", "/api/v1/admin/awx/actions", "mkeller", nil, 200, &list)
		if len(list.Items) != 1 || list.Items[0].Key != key || list.Items[0].Name != "Monitoring agent" || !list.Items[0].Enabled ||
			len(list.Items[0].Types) != 1 || list.Items[0].Types[0] != "vsphere.vm" || list.Items[0].Fields == nil || len(*list.Items[0].Fields) != 2 {
			t.Fatalf("AWX actions = %+v", list.Items)
		}
		h.json("GET", "/api/v1/admin/awx/actions", "tnguyen", nil, 403, nil)

		d := buildVM(h, model)
		a, ok := d.actions("tnguyen")[key]
		if !ok || !a.Enabled || a.Group != api.Automation || a.Form == nil || a.Form.InitialValues["monitoring_agent"] != "node_exporter" {
			t.Fatalf("the AWX action on the VM = %+v", a)
		}

		// Offered only where the condition holds; a bad condition is refused.
		patch := func(u api.AwxActionUpdate, want int) {
			t.Helper()
			h.json("PATCH", "/api/v1/admin/awx/actions/"+key, "mkeller", u, want, nil)
		}
		windows, linux, broken := "resource.os_family == 'windows'", "resource.os_family == 'linux'", "(("
		patch(api.AwxActionUpdate{Condition: &broken}, 422)
		patch(api.AwxActionUpdate{Condition: &windows}, 200)
		if _, ok := d.actions("tnguyen")[key]; ok {
			t.Fatal("offered on a Linux VM with a Windows condition")
		}
		d.run("tnguyen", key, map[string]any{"monitoring_agent": "telegraf", "listen_port": 9100}, 422)
		name := "Install monitoring agent"
		patch(api.AwxActionUpdate{Condition: &linux, Name: &name}, 200)

		// The form is checked against the survey, then the job runs on rvav's inventory.
		d.run("tnguyen", key, map[string]any{"monitoring_agent": "telegraf", "listen_port": 80}, 422)
		h.acts.AWXInventorySource = 1
		r := d.succeed("tnguyen", key, map[string]any{"monitoring_agent": "telegraf", "listen_port": 9100})
		if r.Title != "Install monitoring agent · web-dev-app001" {
			t.Fatalf("title = %q", r.Title)
		}
		step, err := h.store.GetStepByKey(ctx, r.Id, "awx.job:monitoring-agent")
		if err != nil || step.ExternalID == nil || step.Status != "done" {
			t.Fatalf("job step = %+v, %v", step, err)
		}
		if _, err := h.store.GetStepByKey(ctx, r.Id, "awx.inventory"); err != nil {
			t.Fatalf("no inventory refresh: %v", err)
		}
		var jobID int
		fmt.Sscan(*step.ExternalID, &jobID)
		job := h.awx.Launched(jobID)
		if job.Limit != "web-dev-app001" || job.Inventory != awx.FakeInventory || job.ExtraVars["monitoring_agent"] != "telegraf" {
			t.Fatalf("launched %+v", job)
		}
		rvav, _ := job.ExtraVars["rvav"].(map[string]any)
		resources, _ := rvav["resources"].([]any)
		if rvav["request_id"] != r.Id || rvav["project"] != "web" || len(resources) != 1 {
			t.Fatalf("rvav context = %v", job.ExtraVars["rvav"])
		}

		// Turned off, or gone from AWX, it is no longer offered.
		off := false
		patch(api.AwxActionUpdate{Enabled: &off}, 200)
		if _, ok := d.actions("tnguyen")[key]; ok {
			t.Fatal("offered while turned off")
		}
		d.run("tnguyen", key, nil, 422)
	})
}

// TestAWXValueSources: the awx.inventories and awx.credentials pickers list
// what the AWX organisation has, credentials of one kind when asked.
func TestAWXValueSources(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web = "prj_web"
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/dns-record/form?project_id="+web, "lmartin", nil, 200, &form)
		if _, err := h.store.Pool.Exec(ctx, `update catalog_item_versions set ui_schema = jsonb_set(jsonb_set(ui_schema,
			'{fields,inventory}', '{"widget": "select", "label": "Inventory", "value_source": {"kind": "builtin", "name": "awx.inventories"}}'),
			'{fields,machine}', '{"widget": "select", "label": "Machine credential", "value_source": {"kind": "builtin", "name": "awx.credentials", "params": {"kind": "ssh"}}}')
			where id = $1`, form.VersionId); err != nil {
			t.Fatal(err)
		}
		values := func(field string) []api.Option {
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", "lmartin", api.FieldValuesQuery{VersionId: form.VersionId, ProjectId: web, Field: field}, 200, &out)
			if out.Options == nil {
				t.Fatalf("%s has no options", field)
			}
			return *out.Options
		}
		if inv := values("inventory"); len(inv) != 3 || inv[1].Value != "2" || inv[1].Label != "rvav" || deref(inv[2].Meta) != "Smart inventory" {
			t.Fatalf("awx.inventories = %+v", inv)
		}
		if cred := values("machine"); len(cred) != 1 || cred[0].Value != "4" || cred[0].Label != "Linux machine" || deref(cred[0].Meta) != "Machine" {
			t.Fatalf("awx.credentials of kind ssh = %+v", cred)
		}

		// A value the picker doesn't offer is refused on submit.
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/dns-record?project_id="+web, "lmartin", nil, 200, &item)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"name": "www", "type": "A", "value": "10.31.10.9", "zone": "elsewhere.example"}}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/requests", "lmartin", draft, 422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/inputs/zone" || deref(problem.Errors[0].Code) != "not_allowed" {
			t.Fatalf("a zone of no DNS server = %+v", problem.Errors)
		}
		draft.Inputs["zone"] = "corp.example"
		h.json("POST", "/api/v1/requests", "lmartin", draft, 202, nil, "Idempotency-Key", uuid.NewString())
	})
}

// TestAWXDay2ActionGuards: what keeps AWX actions from running other than
// as meant: no inputs without a form, an action turned off while its
// request waited, a template that doesn't prompt for an inventory, two
// templates with the same key, and a run on the whole deployment.
func TestAWXDay2ActionGuards(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		h.awx.Labels["role-nginx"] = []string{"day2"}         // no survey
		h.awx.Plays["Role_nginx"] = h.awx.Plays["role-nginx"] // the same key as role-nginx
		h.awx.Labels["Role_nginx"] = []string{"day2"}
		h.awx.NoInventoryPrompt = map[string]bool{"Role_nginx": true}
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log,
			Filter: awx.TemplateFilter{Labels: []string{"catalog"}}, ActionLabels: []string{"day2"}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", "mkeller", nil, 200, nil)
		var list api.AwxActionList
		h.json("GET", "/api/v1/admin/awx/actions", "mkeller", nil, 200, &list)
		byTemplate := map[string]api.AwxAction{}
		for _, a := range list.Items {
			byTemplate[a.Template] = a
		}
		nginx, other := byTemplate["role-nginx"], byTemplate["Role_nginx"]
		if len(list.Items) != 3 || nginx.Key == other.Key || !strings.HasPrefix(nginx.Key, "awx.role-nginx") || !strings.HasPrefix(other.Key, "awx.role-nginx") {
			t.Fatalf("actions = %+v", list.Items)
		}
		if other.Enabled || len(other.Warnings) == 0 {
			t.Fatalf("an action whose template doesn't prompt for an inventory = %+v", other)
		}

		d := buildVM(h, model)
		h.acts.AWXInventorySource = 1

		// No form: no inputs, and only rvav's context reaches AWX.
		d.run("tnguyen", nginx.Key, map[string]any{"ansible_ssh_common_args": "-o ProxyCommand=x"}, 422)
		r := d.succeed("tnguyen", nginx.Key, nil)
		step, _ := h.store.GetStepByKey(ctx, r.Id, "awx.job:role-nginx")
		var jobID int
		fmt.Sscan(*step.ExternalID, &jobID)
		if vars := h.awx.Launched(jobID).ExtraVars; len(vars) != 1 || vars["rvav"] == nil {
			t.Fatalf("extra vars = %v", vars)
		}

		// Turned on anyway, the action without the inventory prompt is
		// refused before launching.
		on := true
		h.json("PATCH", "/api/v1/admin/awx/actions/"+other.Key, "mkeller", api.AwxActionUpdate{Enabled: &on}, 200, nil)
		r = d.run("tnguyen", other.Key, nil, 202)
		h.runWorkflow(r.Id, nil)
		failed := h.waitStatus(r.Id, "tnguyen", api.RequestStatusFailed)
		if failed.Error == nil || !strings.Contains(failed.Error.Message, "does not prompt for an inventory") {
			t.Fatalf("error = %+v", failed.Error)
		}

		// Turned off while its request waited: the job is not launched.
		r = d.run("tnguyen", nginx.Key, nil, 202)
		off := false
		h.json("PATCH", "/api/v1/admin/awx/actions/"+nginx.Key, "mkeller", api.AwxActionUpdate{Enabled: &off}, 200, nil)
		h.runWorkflow(r.Id, nil)
		failed = h.waitStatus(r.Id, "tnguyen", api.RequestStatusFailed)
		if failed.Error == nil || !strings.Contains(failed.Error.Message, "was turned off since it was asked for") {
			t.Fatalf("error = %+v", failed.Error)
		}

		// On the whole deployment: every VM in the limit, no resource. A
		// condition a deployment can't evaluate is refused when saved.
		h.json("PATCH", "/api/v1/admin/awx/actions/"+nginx.Key, "mkeller", api.AwxActionUpdate{Enabled: &on,
			Types: &[]api.AwxActionUpdateTypes{"deployment"}, Condition: new("resource.power_state == 'on'")}, 422, nil)
		h.json("PATCH", "/api/v1/admin/awx/actions/"+nginx.Key, "mkeller", api.AwxActionUpdate{Enabled: &on,
			Types: &[]api.AwxActionUpdateTypes{"deployment"}, Condition: new("resource.os_family == 'linux'")}, 200, nil)
		var whole api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: nginx.Key, Inputs: map[string]any{}},
			202, &whole, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(whole.Id, nil)
		h.waitStatus(whole.Id, "tnguyen", api.RequestStatusSucceeded)
		step, _ = h.store.GetStepByKey(ctx, whole.Id, "awx.job:role-nginx")
		fmt.Sscan(*step.ExternalID, &jobID)
		if job := h.awx.Launched(jobID); job.Limit != "web-dev-app001" || job.Inventory != awx.FakeInventory {
			t.Fatalf("launched on the deployment: %+v", job)
		}
	})
}

// TestAWXLaunchSettingsFromTheForm: an AWX item's form picks the inventory
// and credentials the job launches with, which are not extra variables;
// items linked to an AWX template launch it by ID (S8.6).
func TestAWXLaunchSettingsFromTheForm(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin = "prj_web", "lmartin"
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []any{"role"}, "properties": map[string]any{
				"role": map[string]any{"type": "string"}, "inventory": map[string]any{"type": "string"}, "machine": map[string]any{"type": "string"}}},
			UiSchema: api.UiSchema{Fields: map[string]api.UiField{
				"role":      {Widget: api.UiWidgetText, Label: "Role"},
				"inventory": {Widget: api.UiWidgetAwxInventory, Label: "Inventory", ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("awx.inventories")}},
				"machine": {Widget: api.UiWidgetAwxCredential, Label: "Machine credential", ValueSource: &api.ValueSource{Kind: api.Builtin,
					Name: new("awx.credentials"), Params: &map[string]any{"kind": "ssh"}}},
			}},
			Blueprint: map[string]any{"kind": "awx", "awx": map[string]any{"title": "Create role", "job_template": "pg-create-role",
				"inventory": "inventory", "credentials": []any{"role"}}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Role", Fields: []string{"role", "inventory", "machine"}}}
		create := api.CatalogItemCreate{Slug: "pg-role", Name: "PostgreSQL role", Summary: "A role.", Category: "compute",
			Type: api.AwxJobTemplate, Definition: &def}

		// The blueprint's launch inputs must be picked from AWX.
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/definition/blueprint/awx/credentials/0" {
			t.Fatalf("a launch input not picked from AWX = %+v", problem.Errors)
		}
		def.Blueprint["awx"].(map[string]any)["credentials"] = []any{"machine"}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/pg-role/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/pg-role/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		submit := func(inputs map[string]any, want int) api.Request {
			t.Helper()
			var req api.Request
			var out any
			if want == 202 {
				out = &req
			}
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: web, Environment: "dev", Inputs: inputs}, want, out, "Idempotency-Key", uuid.NewString())
			return req
		}
		submit(map[string]any{"role": "app", "inventory": "999", "machine": "4"}, 422) // not an inventory of the organisation

		// Whatever the inputs' types, every picked value is checked (S8 review).
		loose := def
		loose.FormSchema = map[string]any{"type": "object", "required": []any{"role"}, "properties": map[string]any{
			"role": map[string]any{"type": "string"}, "inventory": map[string]any{"type": "integer"},
			"machine": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}
		var looseItem api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pg-role-loose", Name: "PostgreSQL role (loose)",
			Summary: "A role.", Category: "compute", Type: api.AwxJobTemplate, Definition: &loose}, 201, &looseItem)
		h.json("POST", "/api/v1/admin/catalog/items/pg-role-loose/versions/"+looseItem.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/pg-role-loose/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		for _, inputs := range []map[string]any{
			{"role": "app", "inventory": 999, "machine": []any{"4"}},
			{"role": "app", "inventory": 3, "machine": []any{"4", "777"}},
		} {
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: looseItem.Id, VersionId: looseItem.DraftVersion.Id,
				ProjectId: web, Environment: "dev", Inputs: inputs}, 422, &problem, "Idempotency-Key", uuid.NewString())
			if !slices.ContainsFunc(problem.Errors, func(e api.FieldError) bool { return deref(e.Code) == "not_allowed" }) {
				t.Fatalf("%v: errors = %+v", inputs, problem.Errors)
			}
		}

		req := submit(map[string]any{"role": "app", "inventory": "3", "machine": "4"}, 202)
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		step, err := h.store.GetStepByKey(ctx, req.Id, "awx.job:pg-create-role")
		if err != nil || step.ExternalID == nil {
			t.Fatalf("job step = %+v, %v", step, err)
		}
		var jobID int
		fmt.Sscan(*step.ExternalID, &jobID)
		job := h.awx.Launched(jobID)
		if job.Inventory != 3 || fmt.Sprint(job.Credentials) != "[4]" || job.ExtraVars["role"] != "app" ||
			job.ExtraVars["inventory"] != nil || job.ExtraVars["machine"] != nil {
			t.Fatalf("launched %+v", job)
		}

		// A template that doesn't prompt for credentials fails the request.
		h.awx.NoCredentialPrompt = map[string]bool{"pg-create-role": true}
		req = submit(map[string]any{"role": "app", "inventory": "3", "machine": "4"}, 202)
		h.runWorkflow(req.Id, nil)
		if failed := h.waitStatus(req.Id, "tnguyen", api.RequestStatusFailed); failed.Error == nil ||
			!strings.Contains(failed.Error.Message, "does not prompt for credentials") {
			t.Fatalf("error = %+v", failed.Error)
		}
		h.awx.NoCredentialPrompt = nil

		// A seeded AWX item, once linked to its template by the sync, launches it by ID.
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log, Filter: awx.TemplateFilter{Labels: []string{"rvav"}}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", "mkeller", nil, 200, nil)
		var dns api.RequestForm
		h.json("GET", "/api/v1/catalog/items/dns-record/form?project_id="+web, "tnguyen", nil, 200, &dns)
		var seeded api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/dns-record?project_id="+web, "tnguyen", nil, 200, &seeded)
		var dnsReq api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: seeded.Id, VersionId: dns.VersionId, ProjectId: web,
			Environment: "dev", Inputs: map[string]any{"name": "www", "type": "A", "value": "10.31.10.9", "zone": "corp.example"}},
			202, &dnsReq, "Idempotency-Key", uuid.NewString())
		var templateID int
		if err := h.store.Pool.QueryRow(ctx, `select coalesce((plan->'awx'->>'template_id')::int, 0) from requests where id = $1`, dnsReq.Id).Scan(&templateID); err != nil || templateID == 0 {
			t.Fatalf("the seeded item's job has no template ID: %d, %v", templateID, err)
		}
	})
}
