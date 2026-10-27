package e2e

import (
	"context"
	"encoding/json"
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
		h.runWorkflow(req.Id, nil)

		token := auth.NewToken()
		if err := h.store.CreateAPIToken(ctx, "tok_test", "usr_svc-awx", "awx inventory", auth.HashToken(token), nil); err != nil {
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
		for _, g := range []string{"rvav", "project_web", "env_dev", "item_rhel9_server", "os_linux"} {
			var members struct{ Hosts []string }
			_ = json.Unmarshal(groups[g], &members)
			if !slices.Contains(inv.All.Children, g) || !slices.Contains(members.Hosts, "web-dev-app001") {
				t.Fatalf("group %s = %s (children %v)", g, groups[g], inv.All.Children)
			}
		}
		h.json("GET", "/api/v1/ansible/inventory", "tnguyen", nil, 403, nil)
	})
}
