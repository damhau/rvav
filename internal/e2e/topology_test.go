package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "three-tier", Name: "Three-tier application",
			Summary: "Web, application and database tiers on NSX.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/three-tier/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/three-tier/entitlements", admin, api.CatalogEntitlements{AllProjects: true,
			ProjectIds: []string{}}, 200, nil)

		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/three-tier/form?project_id=prj_web", "tnguyen", nil, 200, &form)
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
