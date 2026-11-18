package e2e

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
)

// targetItem is a VM on the application network, rvav choosing its
// cluster and datastore unless bindings say, with constraints.
func targetItem(network string, bindings map[string]any, constraints map[string]any) api.CatalogVersionDefinition {
	fields := []string{"role"}
	b := map[string]any{"vcpu": "2", "memory_gb": "4", "disk_gb": "60", "network": "'" + network + "'"}
	maps.Copy(b, bindings)
	bp := map[string]any{"kind": "vm", "naming": "{project}-{environment}-{inputs.role}{seq:02}", "template": "tpl-rhel9.4-2026.10",
		"guest": map[string]any{"os_family": "linux", "domain": "corp.example"}, "bindings": b}
	if constraints != nil {
		bp["constraints"] = constraints
	}
	def := api.CatalogVersionDefinition{
		FormSchema: map[string]any{"type": "object", "required": fields, "properties": map[string]any{
			"role": map[string]any{"type": "string", "minLength": 1}}},
		UiSchema:      api.UiSchema{Fields: map[string]api.UiField{"role": {Widget: api.UiWidgetText, Label: "Role"}}},
		InitialValues: map[string]any{},
		Blueprint:     bp,
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "VM", Fields: fields}}
	return def
}

// vmPlacement is where vCenter has a VM: its host, its resource pool's
// path below the root pool, and its datastores.
func vmPlacement(t *testing.T, ctx context.Context, c *vim25.Client, name string) (host, pool string, datastores []string) {
	t.Helper()
	vm, err := find.NewFinder(c, true).VirtualMachine(ctx, "/GVA/vm/rvav/web/dev/"+name)
	if err != nil {
		t.Fatal(err)
	}
	pc := property.DefaultCollector(c)
	var m mo.VirtualMachine
	if err := pc.RetrieveOne(ctx, vm.Reference(), []string{"resourcePool", "runtime.host", "datastore"}, &m); err != nil {
		t.Fatal(err)
	}
	var h mo.HostSystem
	if err := pc.RetrieveOne(ctx, *m.Runtime.Host, []string{"name"}, &h); err != nil {
		t.Fatal(err)
	}
	var path []string
	for at := *m.ResourcePool; ; {
		var p mo.ResourcePool
		if err := pc.RetrieveOne(ctx, at, []string{"name", "parent"}, &p); err != nil {
			t.Fatal(err)
		}
		if p.Parent == nil || p.Parent.Type != "ResourcePool" {
			break
		}
		path, at = append([]string{p.Name}, path...), *p.Parent
	}
	for _, ref := range m.Datastore {
		var ds mo.Datastore
		if err := pc.RetrieveOne(ctx, ref, []string{"name"}, &ds); err != nil {
			t.Fatal(err)
		}
		datastores = append(datastores, ds.Name)
	}
	return h.Name, strings.Join(path, "/"), datastores
}

// TestPlacementTargets places VMs by what placement rules and bindings
// name beyond clusters (T1.3, ADR-0036): a resource pool, the strategy
// choosing among the clusters left (binpack and spread choose otherwise
// than the most free memory), a host whose tag meets a compute constraint,
// and a datastore cluster whose datastores carry its tags.
func TestPlacementTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		tagObjects(t, ctx, c, "rvav-t13hw/gpu", "esx-gva-prd2-02.corp.example")
		tagObjects(t, ctx, c, "rvav-t13tier/gold", "dsc-gva-dev-01")
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		rule := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })]
		if rule.Strategy != api.MostFreeMemory || rule.ResourcePool != "" || rule.DatastoreCluster != "" || len(rule.Hosts) != 0 {
			t.Fatalf("seeded rule = %+v", rule)
		}
		// Web's dev VMs go to the production clusters, in the pool named.
		prd := func(strategy api.PlacementStrategy, pool string) {
			t.Helper()
			h.json("PUT", "/api/v1/admin/placement-rules/"+rule.Id, admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
				Clusters: []string{"GVA-PRD-*"}, Datastores: []string{"vsan-gva-prd-*"}, Networks: []string{"dvpg-app-prd-*"},
				Folder: "/GVA/vm/rvav/web/dev", Strategy: &strategy, ResourcePool: &pool}, 200, &rule)
		}
		prd(api.MostFreeMemory, "rvav/prd")
		if rule.ResourcePool != "rvav/prd" || rule.Strategy != api.MostFreeMemory {
			t.Fatalf("rule = %+v", rule)
		}
		var preview api.PlacementPreview
		h.json("GET", "/api/v1/admin/placement-preview?project_id=prj_web&environment=dev", admin, nil, 200, &preview)
		if len(preview.Clusters) != 1 || preview.Clusters[0].Name != "GVA-PRD-CL01" || deref(preview.Clusters[0].ResourcePool) != "rvav/prd" ||
			!slices.Contains(preview.Problems, "Cluster GVA-PRD-CL02 has no resource pool rvav/prd: its rule leaves it out.") {
			t.Fatalf("admin preview = %+v", preview)
		}

		item := func(slug string, def api.CatalogVersionDefinition) api.AdminCatalogItem {
			t.Helper()
			var it api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: "Placement targets.",
				Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &it)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+it.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			return it
		}
		type built struct {
			name, cluster, host, pool string
			datastores                []string
			placement                 api.ComponentPlacement
			step                      *api.Step
			steps                     []api.Step
		}
		build := func(it api.AdminCatalogItem, role string) built {
			t.Helper()
			draft := api.RequestDraft{CatalogItemId: it.Id, VersionId: it.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"role": role}}
			var pv api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
			if !pv.Valid {
				t.Fatalf("%s: preview errors = %+v", role, pv.Errors)
			}
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
			if req.Status != api.RequestStatusSucceeded {
				t.Fatalf("%s: request = %s %s", role, req.Status, errorOf(req))
			}
			vm := (&vmDeployment{h: h, id: (*req.DeploymentIds)[0], viewer: "tnguyen"}).vm()
			b := built{name: "web-dev-" + role + "01", cluster: deref(vm.Cluster), placement: *(*pv.Components)[0].Placement}
			b.host, b.pool, b.datastores = vmPlacement(t, ctx, c, b.name)
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &steps)
			if i := slices.IndexFunc(steps.Items, func(s api.Step) bool { return s.Key == "placement" }); i >= 0 {
				b.step = &steps.Items[i]
			}
			b.steps = steps.Items
			return b
		}
		app := item("t13-app", targetItem("dvpg-app-prd-vl220", nil, nil))

		// A resource pool: GVA-PRD-CL01's rvav/prd, the only cluster with it.
		b := build(app, "pool")
		if b.cluster != "GVA-PRD-CL01" || b.pool != "rvav/prd" || deref(b.placement.ResourcePool) != "rvav/prd" || b.step != nil {
			t.Fatalf("pool: %+v", b)
		}
		// No pool, the default strategy: the most free memory, GVA-PRD-CL01
		// with three hosts; the pool is the root one, as before.
		prd(api.MostFreeMemory, "")
		b = build(app, "free")
		if b.cluster != "GVA-PRD-CL01" || b.pool != "" || b.placement.Strategy == nil || *b.placement.Strategy != api.MostFreeMemory || b.step != nil {
			t.Fatalf("most free memory: %+v", b)
		}
		// Spread: GVA-PRD-CL02 has none of rvav's VMs, GVA-PRD-CL01 two.
		prd(api.Spread, "")
		b = build(app, "spread")
		if b.cluster != "GVA-PRD-CL02" || *b.placement.Strategy != api.Spread || b.step == nil || b.step.Title != "Placed by rules" ||
			deref(b.step.Detail) != "Spread chose GVA-PRD-CL02: the fewest of rvav's VMs (0)" {
			t.Fatalf("spread: %+v %+v", b, b.step)
		}
		// Binpack: the least free memory with room, GVA-PRD-CL02's two hosts.
		prd(api.Binpack, "")
		b = build(app, "binpack")
		if b.cluster != "GVA-PRD-CL02" || *b.placement.Strategy != api.Binpack || b.step == nil ||
			deref(b.step.Detail) != "Binpack chose GVA-PRD-CL02: the least free memory with room for a VM of 4 GB" {
			t.Fatalf("binpack: %+v %+v", b, b.step)
		}

		// A host's tag meets a compute constraint: the VM goes to that host,
		// whatever the strategy.
		prd(api.MostFreeMemory, "")
		gpu := item("t13-gpu", targetItem("dvpg-app-prd-vl220", nil, map[string]any{"compute": []any{"rvav-t13hw/gpu"}}))
		b = build(gpu, "gpu")
		if b.cluster != "GVA-PRD-CL02" || b.host != "esx-gva-prd2-02.corp.example" || b.placement.Hosts == nil ||
			!slices.Equal(*b.placement.Hosts, []string{"esx-gva-prd2-02.corp.example"}) {
			t.Fatalf("host tag: %+v", b)
		}
		if b.step == nil || b.step.Title != "Placed by constraint tags" || deref(b.step.Detail) != "Placed by rvav-t13hw/gpu" {
			t.Fatalf("host tag timeline: %+v", b.step)
		}

		// A datastore cluster the rule names: the VM on one of its
		// datastores, whose tags it gives them; in the pool a binding names.
		pod := "dsc-gva-dev-01"
		h.json("PUT", "/api/v1/admin/placement-rules/"+rule.Id, admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Clusters: []string{"GVA-DEV-*"}, Datastores: []string{}, DatastoreCluster: &pod, Networks: []string{"dvpg-app-dev-*"},
			Folder: "/GVA/vm/rvav/web/dev"}, 200, &rule)
		gold := item("t13-gold", targetItem("dvpg-app-dev-vl310", map[string]any{"resource_pool": "'rvav/dev'"},
			map[string]any{"storage": []any{"rvav-t13tier/gold"}}))
		b = build(gold, "gold")
		members := []string{"nfs-gva-dev-sdrs-01", "nfs-gva-dev-sdrs-02"}
		if b.cluster != "GVA-DEV-CL01" || b.pool != "rvav/dev" || len(b.datastores) != 1 || !slices.Contains(members, b.datastores[0]) ||
			deref(b.placement.DatastoreCluster) != pod || !slices.Contains(members, deref(b.placement.Datastore)) {
			t.Fatalf("datastore cluster: %+v", b)
		}
		if !slices.ContainsFunc(b.steps, func(s api.Step) bool { return strings.Contains(deref(s.Detail), " in "+pod+" · ") }) {
			t.Errorf("clone step = %+v", b.steps)
		}
		// A pool outside the rule's, or one the cluster lacks: refused.
		devPool := "rvav"
		h.json("PUT", "/api/v1/admin/placement-rules/"+rule.Id, admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Clusters: []string{"GVA-DEV-*"}, Datastores: []string{"vsan-gva-dev-*"}, Networks: []string{"dvpg-app-dev-*"},
			Folder: "/GVA/vm/rvav/web/dev", ResourcePool: &devPool}, 200, &rule)
		refused := func(slug string, bindings map[string]any, want string) {
			t.Helper()
			it := item(slug, targetItem("dvpg-app-dev-vl310", bindings, nil))
			var pv api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", "tnguyen", api.RequestDraft{CatalogItemId: it.Id, VersionId: it.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "other"}}, 200, &pv)
			if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return e.Message == want }) {
				t.Fatalf("%s = %+v", slug, pv.Errors)
			}
		}
		refused("t13-other", map[string]any{"resource_pool": "'other'"},
			"No cluster allowed here has resource pool other, or room for these VMs there.")
		refused("t13-outside", map[string]any{"resource_pool": "'other'", "cluster": "'GVA-DEV-CL01'"},
			"Resource pool other is not within rvav, where VMs go on GVA-DEV-CL01.")
		refused("t13-missing", map[string]any{"resource_pool": "'rvav/none'", "cluster": "'GVA-DEV-CL01'"},
			"Cluster GVA-DEV-CL01 has no resource pool rvav/none.")
	})
}
