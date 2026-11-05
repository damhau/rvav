package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/nsx"
)

// TestRequestFormTopology: the request form carries what the item builds,
// for the form to draw (D1.2, ADR-0032), and nothing of the blueprint only
// its administrators see.
func TestRequestFormTopology(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		useFakeNSX(h, nsx.NewFakeServer()) // an item with segments needs NSX

		// N1's three tiers, with guest settings a requester mustn't see.
		def := threeTier()
		def.Blueprint["guest"] = map[string]any{"os_family": "linux", "domain": "corp.example", "time_zone": "Europe/Zurich",
			"cloud_init": map[string]any{"user_data": "#cloud-config\nruncmd:\n  - echo tt-cloud-init\n"}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "n1-three-tier", Name: "N1 three tiers",
			Summary: "Web, application and database tiers on NSX.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/n1-three-tier/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/n1-three-tier/entitlements", admin, api.CatalogEntitlements{AllProjects: true,
			ProjectIds: []string{}}, 200, nil)

		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/n1-three-tier/form?project_id=prj_web", "tnguyen", nil, 200, &form)
		if form.Topology == nil {
			t.Fatal("the three-tier form has no topology")
		}
		got, _ := json.Marshal(*form.Topology)
		var top struct {
			Kind       string `json:"kind"`
			Guest      map[string]any
			Bindings   map[string]any
			Segments   []map[string]any
			Components []struct {
				Key    string
				Title  string
				Count  int
				Groups []string
			}
			SecurityGroups []struct {
				Key   string
				Rules []struct{ From, Services []string }
			} `json:"security_groups"`
		}
		if err := json.Unmarshal(got, &top); err != nil {
			t.Fatal(err)
		}
		if top.Kind != "vm" || len(top.Segments) != 1 || top.Segments[0]["key"] != "app" || len(top.Components) != 3 ||
			top.Components[0].Key != "web" || top.Components[0].Count != 2 || top.Components[2].Groups[0] != "db" ||
			len(top.SecurityGroups) != 3 || top.SecurityGroups[2].Rules[0].From[0] != "app" ||
			top.SecurityGroups[2].Rules[0].Services[0] != "PostgreSQL" {
			t.Fatalf("topology = %s", got)
		}
		if len(top.Guest) != 1 || top.Guest["os_family"] != "linux" || len(top.Bindings) != 1 || top.Bindings["network"] != "segments.app" {
			t.Fatalf("guest %v, bindings %v: only the OS family and the network", top.Guest, top.Bindings)
		}
		for _, secret := range []string{"tt-cloud-init", "corp.example", "Europe/Zurich", "naming", "inputs.cluster", "deployment_name"} {
			if strings.Contains(string(got), secret) {
				t.Errorf("the topology has %q: %s", secret, got)
			}
		}

		// The seeded composite: its steps, bindings that name no earlier step left out.
		var pg api.RequestForm
		h.json("GET", "/api/v1/catalog/items/postgres-cluster/form?project_id=prj_data", "sbrunner", nil, 200, &pg)
		if pg.Topology == nil {
			t.Fatal("the composite's form has no topology")
		}
		got, _ = json.Marshal(*pg.Topology)
		const want = `{"kind":"composite","rollback_on_failure":true,"steps":[` +
			`{"item":"pg-node","key":"nodes","title":"Database nodes","with":{}},` +
			`{"item":"db-user","key":"owner","title":"The database's owner","with":{}}]}`
		if string(got) != want {
			t.Fatalf("composite topology = %s", got)
		}

		// An AWX item's form has none.
		var awx api.RequestForm
		h.json("GET", "/api/v1/catalog/items/firewall-flow/form?project_id=prj_web", "lmartin", nil, 200, &awx)
		if awx.Topology != nil {
			t.Fatalf("an AWX item's topology: %v", *awx.Topology)
		}
	})
}

// TestRequestTopology: a request's page draws its run on what the version
// it was made from builds (D1.4, ADR-0032): the form's projection of that
// version, not of a later one, for whoever may read the request.
func TestRequestTopology(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		topologyOf := func(request, user string) string {
			t.Helper()
			var got api.RequestTopology
			h.json("GET", "/api/v1/requests/"+request+"/topology", user, nil, 200, &got)
			if got.Topology == nil {
				return ""
			}
			b, _ := json.Marshal(*got.Topology)
			return string(b)
		}

		// The seeded composite, requested: the topology its form had.
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/postgres-cluster/form?project_id=prj_data", "sbrunner", nil, 200, &form)
		var req api.Request
		h.json("POST", "/api/v1/requests", "sbrunner", api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId,
			ProjectId: "prj_data", Environment: "dev", Inputs: map[string]any{"name": "ledger", "pg_version": "16.6", "size": "S",
				"data_gb": 50, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-db-dev-vl320"}},
			202, &req, "Idempotency-Key", uuid.NewString())
		want, _ := json.Marshal(*form.Topology)
		if got := topologyOf(req.Id, "sbrunner"); got != string(want) {
			t.Fatalf("the request's topology = %s, its form's %s", got, want)
		}

		// A later version with a step more: the request keeps its own.
		var draft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/postgres-cluster/versions", admin, api.CatalogItemVersionCreate{}, 201, &draft)
		def := draft.Definition
		def.Blueprint["steps"] = append(def.Blueprint["steps"].([]any),
			map[string]any{"key": "monitor", "title": "Monitoring", "awx": map[string]any{"job_template": "monitoring-agent"}})
		h.json("PUT", "/api/v1/admin/catalog/items/postgres-cluster/versions/"+draft.Id, admin,
			api.CatalogItemVersionUpdate{Definition: def, BaseRevision: &draft.Revision}, 200, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/postgres-cluster/versions/"+draft.Id+"/publish", admin, nil, 200, nil)
		var later api.RequestForm
		h.json("GET", "/api/v1/catalog/items/postgres-cluster/form?project_id=prj_data", "sbrunner", nil, 200, &later)
		if b, _ := json.Marshal(*later.Topology); !strings.Contains(string(b), `"key":"monitor"`) {
			t.Fatalf("the later version's topology = %s", b)
		}
		if got := topologyOf(req.Number, "sbrunner"); got != string(want) {
			t.Fatalf("after a later version, the request's topology = %s", got)
		}
		// Whoever may read the request; nobody else.
		if got := topologyOf(req.Id, admin); got != string(want) {
			t.Fatalf("an administrator reads %s", got)
		}
		h.json("GET", "/api/v1/requests/"+req.Id+"/topology", "tnguyen", nil, 404, nil)

		// A VM item's: the form's projection, without its guest settings.
		var rhel api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", "tnguyen", nil, 200, &rhel)
		var vm api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: rhel.ItemId, VersionId: rhel.VersionId,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "topo", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
			202, &vm, "Idempotency-Key", uuid.NewString())
		got := topologyOf(vm.Id, "tnguyen")
		if want, _ := json.Marshal(*rhel.Topology); got != string(want) || !strings.Contains(got, `"kind":"vm"`) ||
			strings.Contains(got, "corp.example") {
			t.Fatalf("the VM item's request's topology = %s, its form's %s", got, want)
		}

		// An AWX item's request has none, and an approver who isn't a member
		// of its project reads that as whoever else may read the request:
		// afavre, a network engineer in core only, approves web's firewall
		// flows (pol_fw); sbrunner, neither, can't read it.
		var fwForm api.RequestForm
		h.json("GET", "/api/v1/catalog/items/firewall-flow/form?project_id=prj_web", "tnguyen", nil, 200, &fwForm)
		var fw api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: fwForm.ItemId, VersionId: fwForm.VersionId,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"source": "web-dev-app001", "destination": "10.22.30.10",
				"port": 5432, "protocol": "tcp"}}, 202, &fw, "Idempotency-Key", uuid.NewString())
		if fw.Status != api.RequestStatusPendingApproval {
			t.Fatalf("the firewall flow is %s, not waiting for approval", fw.Status)
		}
		for _, user := range []string{"tnguyen", "afavre"} {
			if got := topologyOf(fw.Id, user); got != "" {
				t.Fatalf("an AWX item's request's topology, as %s = %s", user, got)
			}
		}
		h.json("GET", "/api/v1/requests/"+fw.Id+"/topology", "sbrunner", nil, 404, nil)
		// Approving web's flows lets afavre read those, not data's cluster.
		h.json("GET", "/api/v1/requests/"+req.Id+"/topology", "afavre", nil, 404, nil)

		// A day-2 request runs an action, not the item: it has none.
		h.runWorkflow(vm.Id, nil)
		h.waitStatus(vm.Id, "tnguyen", api.RequestStatusSucceeded)
		h.json("GET", "/api/v1/requests/"+vm.Id, "tnguyen", nil, 200, &vm)
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+(*vm.DeploymentIds)[0]+"/actions", "tnguyen",
			api.Day2Draft{Action: "delete", Inputs: map[string]any{}}, 202, &del, "Idempotency-Key", uuid.NewString())
		if got := topologyOf(del.Id, "tnguyen"); got != "" {
			t.Fatalf("a day-2 request's topology = %s", got)
		}
	})
}
