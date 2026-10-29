package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// shopApp is a multi-VM blueprint (ADR-0024): web servers kept on separate
// hosts, and a database server, in one deployment.
func shopApp() api.CatalogVersionDefinition {
	fields := []string{"app", "web_nodes", "size", "web_cluster", "web_datastore", "db_cluster", "db_datastore", "network", "db_post"}
	props := map[string]any{}
	ui := map[string]api.UiField{}
	for _, f := range fields {
		props[f] = map[string]any{"type": "string", "minLength": 1}
		ui[f] = api.UiField{Widget: api.UiWidgetText, Label: f}
	}
	props["web_nodes"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 5}
	props["db_post"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	def := api.CatalogVersionDefinition{
		FormSchema:    map[string]any{"type": "object", "required": fields[:8], "properties": props},
		UiSchema:      api.UiSchema{Fields: ui},
		InitialValues: map[string]any{},
		Blueprint: map[string]any{
			"kind": "vm", "naming": "{project}-{environment}-{inputs.app}{component}{seq:02}",
			"deployment_name": "{project}-{environment}-{inputs.app}", "rollback_on_failure": true,
			"template": "tpl-rhel9.4-2026.10", "guest": map[string]any{"os_family": "linux", "domain": "corp.example"},
			"sizes": map[string]any{"S": map[string]any{"label": "Small", "vcpu": 2, "memory_gb": 4, "disk_gb": 50}},
			"bindings": map[string]any{"vcpu": "sizes[inputs.size].vcpu", "memory_gb": "sizes[inputs.size].memory_gb",
				"disk_gb": "sizes[inputs.size].disk_gb", "network": "inputs.network"},
			"awx_templates": map[string]any{
				"linux-baseline":   map[string]any{"title": "Linux baseline", "job_template": "linux-baseline"},
				"monitoring-agent": map[string]any{"title": "Monitoring", "job_template": "monitoring-agent"}},
			"components": []any{
				map[string]any{"key": "web", "title": "Web servers", "count": "inputs.web_nodes", "anti_affinity": true,
					"bindings": map[string]any{"cluster": "inputs.web_cluster", "datastore": "inputs.web_datastore",
						"post_config": `["linux-baseline"]`, "tags": `["rvav-tier/" + inputs.app]`},
					"disks": []any{map[string]any{"size_gb": 20}}, "tags": []any{"rvav-tier/gold"}},
				map[string]any{"key": "db", "title": "Database", "template": "tpl-win2025-std-2026.09",
					"guest": map[string]any{"os_family": "windows"},
					"bindings": map[string]any{"cluster": "inputs.db_cluster", "datastore": "inputs.db_datastore", "vcpu": "4",
						"post_config": "inputs.db_post"},
					"disks": []any{map[string]any{"size_gb": 100, "datastore": `"vsan-gva-dev-cl01"`}, map[string]any{"size_gb": 10}},
					"nics": []any{map[string]any{"key": "backup", "network": `"dvpg-db-dev-vl320"`},
						map[string]any{"network": "inputs.network", "ipam": "dhcp"}}},
			},
		},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Shop", Fields: fields}}
	return def
}

// TestMultiVMBlueprint builds a blueprint's two components as one
// deployment: two web servers on separate hosts, kept apart by a DRS rule,
// and a database server, in one timeline; a failure rolls all of them back;
// deleting the deployment removes the rule too (S12.2, ADR-0024).
func TestMultiVMBlueprint(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		def := shopApp()
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "shop-app", Name: "Shop application",
			Summary: "Web servers and a database.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/shop-app/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/shop-app/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		draft := func(app string, webNodes int, dbPost ...string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"app": app, "web_nodes": webNodes, "size": "S", "web_cluster": "GVA-DEV-CL01",
					"web_datastore": "vsan-gva-dev-cl01", "db_cluster": "GVA-DEV-CL01", "db_datastore": "nfs-gva-bronze-01",
					"network": "dvpg-app-dev-vl310", "db_post": append([]string{}, dbPost...)}}
		}

		// The preview says what each component builds.
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft("shop", 2), 200, &pv)
		if !pv.Valid || pv.Components == nil || len(*pv.Components) != 2 {
			t.Fatalf("preview = %+v", pv)
		}
		web, db := (*pv.Components)[0], (*pv.Components)[1]
		if web.Key != "web" || web.Count != 2 || !web.AntiAffinity || !slices.Equal(web.Names, []string{"web-dev-shopweb01", "web-dev-shopweb02"}) ||
			db.Count != 1 || db.Vcpu != 4 || deref((*db.Placement).Datastore) != "nfs-gva-bronze-01" || db.OsFamily == nil || *db.OsFamily != "windows" {
			t.Fatalf("components = %+v / %+v", web, db)
		}
		// Quotas count every VM.
		for _, q := range pv.Quota {
			if q.Environment == nil && q.Resource == api.VmCount && q.Adding != 3 || q.Environment == nil && q.Resource == api.Vcpu && q.Adding != 8 {
				t.Fatalf("quota %s adds %d", q.Resource, q.Adding)
			}
		}
		// Four VMs kept apart need four hosts; the request's own count stays 1.
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft("shop", 4), 200, &pv)
		if pv.Valid || !slices.Contains(fieldsOf(pv.Errors), "/inputs/web_cluster") {
			t.Fatalf("four web servers on three hosts = %+v", pv.Errors)
		}
		many := draft("shop", 2)
		many.Count = new(2)
		h.json("POST", "/api/v1/requests/preview", "tnguyen", many, 200, &pv)
		if pv.Valid || !slices.Contains(fieldsOf(pv.Errors), "/count") {
			t.Fatalf("count 2 = %+v", pv.Errors)
		}

		submit := func(d api.RequestDraft) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", d, 202, &req, "Idempotency-Key", uuid.NewString())
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

		// Built: one deployment, three VMs, the web servers on two hosts.
		req := submit(draft("shop", 2))
		if req.Status != api.RequestStatusSucceeded || req.DeploymentIds == nil || len(*req.DeploymentIds) != 1 {
			t.Fatalf("request = %s %s, deployments %v", req.Status, errorOf(req), req.DeploymentIds)
		}
		steps := timeline(req.Id)
		for _, key := range []string{"component:web", "component:db", "component:web/vsphere.clone@web-dev-shopweb02",
			"component:db/vsphere.power_on@web-dev-shopdb01", "component:web/vsphere.drs_rule", "component:web/awx.job:linux-baseline"} {
			if steps[key].Status != api.StepStatusDone {
				t.Fatalf("%s = %s (steps %v)", key, steps[key].Status, keysOf(steps))
			}
		}
		if _, ok := steps["component:db/vsphere.drs_rule"]; ok {
			t.Fatal("a component of one VM has a DRS rule")
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+(*req.DeploymentIds)[0], "tnguyen", nil, 200, &dep)
		if dep.Name != "web-dev-shop" {
			t.Fatalf("deployment name = %s", dep.Name)
		}
		hosts := map[string]string{}
		components := map[string]string{}
		var rule api.DrsRuleResource
		var morefs []string
		for _, r := range dep.Resources {
			switch v, _ := r.ValueByDiscriminator(); v := v.(type) {
			case api.VmResource:
				hosts[v.Name], components[v.Name] = deref(v.Vm.Host), deref(v.Vm.Component)
				if deref(v.Vm.Component) == "web" {
					morefs = append(morefs, v.Vm.Moref)
				}
			case api.DrsRuleResource:
				rule = v
			}
		}
		if len(hosts) != 3 || components["web-dev-shopweb01"] != "web" || components["web-dev-shopdb01"] != "db" {
			t.Fatalf("VMs = %v", components)
		}
		if hosts["web-dev-shopweb01"] == hosts["web-dev-shopweb02"] || hosts["web-dev-shopweb01"] == "" {
			t.Fatalf("web servers on hosts %v", hosts)
		}
		if rule.Rule.Cluster != "GVA-DEV-CL01" || !slices.Equal(rule.Rule.Vms, []string{"web-dev-shopweb01", "web-dev-shopweb02"}) {
			t.Fatalf("DRS rule = %+v", rule)
		}
		inVC, err := h.vc.AntiAffinityRule(ctx, "GVA-DEV-CL01", rule.Name)
		slices.Sort(inVC)
		slices.Sort(morefs)
		if err != nil || !slices.Equal(inVC, morefs) {
			t.Fatalf("rule in vCenter = %v (%v), want %v", inVC, err, morefs)
		}
		// Every VM its own addresses; the database's backup adapter one more,
		// with its records; added disks, adapters and tags (S12.3).
		var addrs []string
		_ = h.store.Pool.QueryRow(ctx, `select array_agg(name order by name) from resources where deployment_id = $1 and type = 'ipam.address'`,
			dep.Id).Scan(&addrs)
		if len(addrs) != 4 || len(slices.Compact(slices.Clone(addrs))) != 4 {
			t.Fatalf("addresses = %v", addrs)
		}
		var backupA int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources where deployment_id = $1 and type = 'dns.record'
			and properties->>'fqdn' like 'web-dev-shopdb01-backup.%' and properties->>'record_type' = 'A'`, dep.Id).Scan(&backupA)
		if backupA != 1 {
			t.Fatal("no A record for the database's backup adapter")
		}
		for _, r := range dep.Resources {
			v, _ := r.ValueByDiscriminator()
			vm, ok := v.(api.VmResource)
			if !ok {
				continue
			}
			switch deref(vm.Vm.Component) {
			case "db":
				if len(vm.Vm.Nics) != 3 || vm.Vm.Nics[1].Network != "dvpg-db-dev-vl320" || len(vm.Vm.Disks) != 3 ||
					vm.Vm.Disks[1].CapacityGb != 100 || deref(vm.Vm.Disks[1].Datastore) != "vsan-gva-dev-cl01" {
					t.Fatalf("db VM = %+v / %+v", vm.Vm.Nics, vm.Vm.Disks)
				}
			case "web":
				if len(vm.Vm.Disks) != 2 || vm.Vm.Disks[1].CapacityGb != 20 {
					t.Fatalf("web VM disks = %+v", vm.Vm.Disks)
				}
				if tags, err := h.vc.VMTags(ctx, vm.Vm.Moref); err != nil || !slices.Equal(tags, []string{"rvav-tier/gold", "rvav-tier/shop"}) {
					t.Fatalf("web VM tags = %v (%v)", tags, err)
				}
			}
		}

		// AWX's inventory groups them by component.
		var inv map[string]json.RawMessage
		h.json("GET", "/api/v1/ansible/inventory", admin, nil, 200, &inv)
		var group struct{ Hosts []string }
		_ = json.Unmarshal(inv["component_web"], &group)
		if !slices.Equal(group.Hosts, []string{"web-dev-shopweb01", "web-dev-shopweb02"}) {
			t.Fatalf("component_web = %s", inv["component_web"])
		}

		// A component failing rolls every VM back, and the rule.
		h.awx.FailTask = "monitoring : install node_exporter"
		failed := submit(draft("cart", 2, "monitoring-agent"))
		h.awx.FailTask = ""
		steps = timeline(failed.Id)
		if failed.Status != api.RequestStatusFailed || steps["component:db"].Status != api.StepStatusFailed ||
			steps["component:web"].Status != api.StepStatusDone || steps["rollback"].Status != api.StepStatusDone {
			t.Fatalf("failed request = %s (steps %v)", failed.Status, keysOf(steps))
		}
		for _, name := range []string{"web-dev-cartweb01", "web-dev-cartweb02", "web-dev-cartdb01"} {
			if vmExists(ctx, c, name) {
				t.Fatalf("%s is still there after the rollback", name)
			}
		}
		if got, _ := h.vc.AntiAffinityRule(ctx, "GVA-DEV-CL01", "rvav-"+(*failed.DeploymentIds)[0]+"-web"); got != nil {
			t.Fatalf("the rolled back rule is still there: %v", got)
		}
		// Its names stay given: the next request gets the next ones.
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft("cart", 1), 200, &pv)
		if got := (*pv.Components)[0].Names; !slices.Equal(got, []string{"web-dev-cartweb03"}) {
			t.Fatalf("names after a rolled back request = %v", got)
		}

		// Retried, it builds them all again.
		h.json("POST", "/api/v1/requests/"+failed.Id+"/retry", "tnguyen", nil, 202, nil)
		h.runWorkflow(failed.Id, nil)
		h.json("GET", "/api/v1/requests/"+failed.Id, "tnguyen", nil, 200, &failed)
		steps = timeline(failed.Id)
		if failed.Status != api.RequestStatusSucceeded || steps["component:db"].Status != api.StepStatusDone ||
			steps["component:web/vsphere.drs_rule"].Status != api.StepStatusDone || !vmExists(ctx, c, "web-dev-cartweb02") {
			t.Fatalf("retried = %s %s (steps %v)", failed.Status, errorOf(failed), keysOf(steps))
		}

		// Without rollback, a retry resumes: the VMs built are kept.
		keep := shopApp()
		keep.Blueprint["rollback_on_failure"] = false
		var kept api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "shop-app-keep", Name: "Shop application, kept",
			Summary: "Kept on failure.", Category: "compute", Type: api.VmBlueprint, Definition: &keep}, 201, &kept)
		h.json("POST", "/api/v1/admin/catalog/items/shop-app-keep/versions/"+kept.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/shop-app-keep/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		resumable := draft("pay", 2, "monitoring-agent")
		resumable.CatalogItemId, resumable.VersionId = kept.Id, kept.DraftVersion.Id
		h.awx.FailTask = "monitoring : install node_exporter"
		paused := submit(resumable)
		h.awx.FailTask = ""
		if paused.Status != api.RequestStatusFailed || !vmExists(ctx, c, "web-dev-payweb02") {
			t.Fatalf("kept on failure = %s", paused.Status)
		}
		var morefBefore string
		_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where name = 'web-dev-paydb01'`).Scan(&morefBefore)
		h.json("POST", "/api/v1/requests/"+paused.Id+"/retry", "tnguyen", nil, 202, nil)
		h.runWorkflow(paused.Id, nil)
		h.json("GET", "/api/v1/requests/"+paused.Id, "tnguyen", nil, 200, &paused)
		var morefAfter string
		_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where name = 'web-dev-paydb01'`).Scan(&morefAfter)
		if paused.Status != api.RequestStatusSucceeded || morefBefore == "" || morefAfter != morefBefore {
			t.Fatalf("resumed = %s %s, db moref %s then %s (steps %v)", paused.Status, errorOf(paused), morefBefore, morefAfter, keysOf(timeline(paused.Id)))
		}

		// Deleting the deployment removes the rule and every VM.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+dep.Id+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.json("GET", "/api/v1/requests/"+del.Id, "tnguyen", nil, 200, &del)
		if del.Status != api.RequestStatusSucceeded {
			t.Fatalf("delete = %s %s", del.Status, errorOf(del))
		}
		if got, _ := h.vc.AntiAffinityRule(ctx, "GVA-DEV-CL01", rule.Name); got != nil || vmExists(ctx, c, "web-dev-shopweb01") {
			t.Fatalf("after delete: rule %v", got)
		}
		var left int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources where deployment_id = $1`, dep.Id).Scan(&left)
		if left != 0 {
			t.Fatalf("%d resources left", left)
		}
	})
}
