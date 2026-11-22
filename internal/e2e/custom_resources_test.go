package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/pkg/rvavflow"
)

// lbCreate makes a load balancer in front of the members it is given.
func lbCreate(_ workflow.Context, rc rvavflow.Context) (map[string]any, error) {
	if rc.Resource == nil || rc.Resource.Phase != "create" {
		return nil, errors.New("no resource to make")
	}
	members, _ := rc.Inputs["members"].([]any)
	return map[string]any{"vip": "10.31.20.10", "members": len(members), "port": rc.Inputs["port"]}, nil
}

// lbUpdate gives it the members it is given now; its address stays.
func lbUpdate(_ workflow.Context, rc rvavflow.Context) (map[string]any, error) {
	members, _ := rc.Inputs["members"].([]any)
	return map[string]any{"vip": rc.Resource.Properties["vip"], "members": len(members), "port": rc.Inputs["port"]}, nil
}

// lbDelete deletes the load balancer whose address it is given.
func lbDelete(_ workflow.Context, rc rvavflow.Context) (map[string]any, error) {
	if rc.Resource == nil || rc.Resource.Properties["vip"] != "10.31.20.10" {
		return nil, fmt.Errorf("no load balancer to delete: %+v", rc.Resource)
	}
	return map[string]any{"deleted": rc.Resource.Name}, nil
}

// lbDrain is the day-2 action the load balancer type offers.
func lbDrain(_ workflow.Context, rc rvavflow.Context) (map[string]any, error) {
	if rc.Resource == nil {
		return nil, errors.New("no resource to drain")
	}
	return map[string]any{"drained": rc.Resource.Name, "vip": rc.Resource.Properties["vip"],
		"deployment_id": rc.Resource.DeploymentID, "phase": rc.Resource.Phase}, nil
}

func registerLB(env *testsuite.TestWorkflowEnvironment) {
	for name, fn := range map[string]func(workflow.Context, rvavflow.Context) (map[string]any, error){
		"lb-create.v1": lbCreate, "lb-update.v1": lbUpdate, "lb-delete.v1": lbDelete, "lb-drain.v1": lbDrain} {
		env.RegisterWorkflowWithOptions(fn, workflow.RegisterOptions{Name: name})
	}
}

// farmWithResources is the web farm with these custom resources.
func farmWithResources(resources ...map[string]any) api.CatalogVersionDefinition {
	def := webFarm(false)
	list := []any{}
	for _, r := range resources {
		list = append(list, r)
	}
	def.Blueprint["resources"] = list
	return def
}

// customOf finds the deployment's custom resources by key.
func customOf(h *harness, user, depID string) map[string]api.CustomResource {
	h.t.Helper()
	var dep api.DeploymentDetail
	h.json("GET", "/api/v1/deployments/"+depID, user, nil, 200, &dep)
	out := map[string]api.CustomResource{}
	for _, r := range dep.Resources {
		if v, _ := r.ValueByDiscriminator(); v != nil {
			if c, ok := v.(api.CustomResource); ok {
				out[c.Key] = c
			}
		}
	}
	return out
}

// customEvents are the custom resource events of a request, in order:
// topic and the resource's key.
func customEvents(ctx context.Context, h *harness, requestID string) []string {
	h.t.Helper()
	rows, err := h.store.Pool.Query(ctx, `select topic, payload from integration_events
		where request_id = $1 and topic like 'custom.%' order by seq`, requestID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var topic string
		var payload []byte
		if err := rows.Scan(&topic, &payload); err != nil {
			h.t.Fatal(err)
		}
		var ev struct {
			Data struct {
				CustomResource struct {
					Key string `json:"key"`
				} `json:"custom_resource"`
			} `json:"data"`
		}
		_ = json.Unmarshal(payload, &ev)
		out = append(out, topic+" "+ev.Data.CustomResource.Key)
	}
	return out
}

// TestCustomResources: a blueprint with a custom resource whose type custom
// workflows drive; the deployment shows it with the properties its create
// gave, offers its type's day-2 action on it, updates it when the VMs it
// balances change, adds and deletes resources as the blueprint does, and
// deletes it first when the deployment goes (E1.4, ADR-0056).
func TestCustomResources(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"
		var problem struct{ Errors []api.FieldError }
		for _, key := range []string{"lb-create", "lb-update", "lb-delete"} {
			h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new(key), Title: key,
				WorkflowType: key + ".v1", TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageResourceType}}, 201, nil)
		}
		// Drain is offered on load balancers only: their type lists it.
		h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new("lb-drain"), Title: "Drain",
			WorkflowType: "lb-drain.v1", TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageDay2Action},
			Day2: &api.WorkflowDay2{Name: "Drain", Types: []api.WorkflowDay2Types{api.WorkflowDay2TypesDeployment}}}, 201, nil)

		lbOf := func(key string) map[string]any {
			return map[string]any{"key": key, "type": "load-balancer", "title": "Load balancer",
				"with": map[string]any{"members": "components.web.addresses", "port": "443", "name": `inputs.app + "-` + key + `"`}}
		}
		// A draft may name a type not defined yet; publishing it may not.
		def := farmWithResources(lbOf("lb"))
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-farm", Name: "Web farm",
			Summary: "Web servers behind a load balancer.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/definition/blueprint/resources/0/type") &&
			!slices.Contains(fieldsOf(problem.Errors), "/blueprint/resources/0/type") {
			t.Fatalf("publishing with an unknown type = %+v", problem.Errors)
		}
		h.json("POST", "/api/v1/admin/resource-types", admin, api.ResourceTypeWrite{Key: new("load-balancer"), Title: "Load balancer",
			Create: api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-create"},
			Update: &api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-update"},
			Delete: api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-delete"},
			PropertySchema: &map[string]any{"type": "object", "properties": map[string]any{
				"vip": map[string]any{"type": "string", "title": "Virtual IP"}}},
			Actions: &[]string{"wf.lb-drain"}}, 201, nil)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-farm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		v1 := item.DraftVersion.Id

		// Requested: the VMs, then the load balancer in front of them.
		inputs := map[string]any{"app": "farm", "web_nodes": 2, "size": "S", "cluster": "GVA-DEV-CL01", "network": "dvpg-app-dev-vl310"}
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: v1, ProjectId: web,
			Environment: "dev", Inputs: inputs}, 202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, registerLB)
		h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		depID := (*built.DeploymentIds)[0]
		steps := timelineOf(h, owner, built.Id)
		if s := steps["resource:lb"]; s.Status != api.StepStatusDone || deref(s.Detail) != "farm-lb" {
			t.Fatalf("the load balancer's step = %+v (steps %v)", s, keysOf(steps))
		}
		lb := customOf(h, owner, depID)["lb"]
		if lb.Name != "farm-lb" || lb.ResourceType.Key != "load-balancer" || lb.Properties["vip"] != "10.31.20.10" ||
			lb.Properties["members"] != float64(2) || lb.Properties["port"] != float64(443) || lb.PropertyTitles == nil ||
			(*lb.PropertyTitles)["vip"] != "Virtual IP" {
			t.Fatalf("the load balancer = %+v", lb)
		}
		if got := customEvents(ctx, h, built.Id); !slices.Equal(got, []string{"custom.create.pre lb", "custom.create.post lb"}) {
			t.Fatalf("events = %v", got)
		}
		// Its run data: what the create workflow was given and gave (ADR-0057).
		if d := stepDataOf(h, admin, built.Id, steps["resource:lb"].Id); valueOf(d.Inputs)["name"] != "farm-lb" ||
			fmt.Sprint(valueOf(d.Inputs)["port"]) != "443" || valueOf(d.Outputs)["vip"] != "10.31.20.10" {
			t.Fatalf("the load balancer's data = %+v", d)
		}

		// Its type's day-2 action is offered on it, and nowhere else.
		var actions struct{ Items []api.Day2Action }
		h.json("GET", "/api/v1/deployments/"+depID+"/actions", owner, nil, 200, &actions)
		var drain []api.Day2Action
		for _, a := range actions.Items {
			if a.Key == "wf.lb-drain" {
				drain = append(drain, a)
			}
		}
		if len(drain) != 1 || deref(drain[0].ResourceId) != lb.Id || !drain[0].Enabled {
			t.Fatalf("drain = %+v", drain)
		}
		var drained api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", owner, api.Day2Draft{Action: "wf.lb-drain", ResourceId: &lb.Id,
			Inputs: map[string]any{}}, 202, &drained, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(drained.Id, registerLB)
		h.waitStatus(drained.Id, owner, api.RequestStatusSucceeded)
		var result map[string]any
		if err := h.store.Pool.QueryRow(ctx, `select result from workflow_runs where request_id = $1`, drained.Id).Scan(&result); err != nil ||
			result["drained"] != "farm-lb" || result["vip"] != "10.31.20.10" || result["deployment_id"] != depID || result["phase"] != "day2" {
			t.Fatalf("the drain = %v (%v)", result, err)
		}

		update := func(version string, in map[string]any, want []string) api.Request {
			t.Helper()
			var pv api.UpdatePreview
			h.json("POST", "/api/v1/deployments/"+depID+"/update/preview", owner, api.UpdateDraft{VersionId: version, Inputs: in}, 200, &pv)
			var kinds []string
			for _, c := range pv.Changes {
				kinds = append(kinds, string(c.Kind))
			}
			if !slices.Equal(kinds, want) || !pv.Allowed || len(pv.Refused) > 0 {
				t.Fatalf("update preview = %v, refused %+v", kinds, pv.Refused)
			}
			var req api.Request
			h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: version, Inputs: in}, 202, &req,
				"Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, registerLB)
			return h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
		}

		// A third web server: the load balancer gets it as a member.
		three := map[string]any{}
		for k, v := range inputs {
			three[k] = v
		}
		three["web_nodes"] = 3
		req := update(v1, three, []string{"vm_add", "resource_update"})
		if s := timelineOf(h, owner, req.Id)["resource:lb"]; s.Status != api.StepStatusDone {
			t.Fatalf("the load balancer's update = %+v", s)
		}
		if got := customOf(h, owner, depID)["lb"]; got.Id != lb.Id || got.Properties["members"] != float64(3) || got.Properties["vip"] != "10.31.20.10" {
			t.Fatalf("the load balancer after the update = %+v", got)
		}

		// A version with another load balancer instead: the update makes
		// the new one and deletes the old one.
		next := farmWithResources(lbOf("edge"))
		var draft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions", admin, api.CatalogItemVersionCreate{Definition: &next}, 201, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+draft.Id+"/publish", admin, nil, 200, nil)
		req = update(draft.Id, three, []string{"resource_add", "resource_remove"})
		custom := customOf(h, owner, depID)
		if _, ok := custom["lb"]; ok || custom["edge"].Name != "farm-edge" || custom["edge"].Properties["members"] != float64(3) {
			t.Fatalf("custom resources after the new version = %+v", custom)
		}
		if got := customEvents(ctx, h, req.Id); !slices.Equal(got, []string{"custom.create.pre edge", "custom.create.post edge",
			"custom.delete.pre lb", "custom.delete.post lb"}) {
			t.Fatalf("the update's events = %v", got)
		}

		// Deleting the deployment deletes the load balancer first.
		var gone api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", owner, api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &gone, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(gone.Id, registerLB)
		h.waitStatus(gone.Id, owner, api.RequestStatusSucceeded)
		if got := customEvents(ctx, h, gone.Id); !slices.Equal(got, []string{"custom.delete.pre edge", "custom.delete.post edge"}) {
			t.Fatalf("the deletion's events = %v", got)
		}
		var left int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources where deployment_id = $1 and type = 'custom'`, depID).Scan(&left)
		if left != 0 {
			t.Fatalf("%d custom resources left", left)
		}
		var deleted []string
		rows, _ := h.store.Pool.Query(ctx, `select coalesce(result->>'deleted', '') from workflow_runs where registration_key = 'lb-delete' order by started_at`)
		for rows.Next() {
			var name string
			_ = rows.Scan(&name)
			deleted = append(deleted, name)
		}
		rows.Close()
		if !slices.Equal(deleted, []string{"farm-lb", "farm-edge"}) {
			t.Fatalf("deleted = %v", deleted)
		}
	})
}

// TestCustomResourcesAWX: a custom resource type AWX templates drive: what
// its create gives with set_stats are its properties; an AWX day-2 action
// its type lists runs with the resource in rvav.resource, and its read
// gives it new properties after; its delete runs when the deployment goes
// (E1.4, ADR-0056).
func TestCustomResourcesAWX(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"
		for _, tpl := range []string{"alias-create", "alias-read", "alias-delete"} {
			h.awx.Plays[tpl] = []string{"Gathering Facts", "dns : " + tpl}
		}
		h.awx.Artifacts = map[string]map[string]any{
			"alias-create": {"fqdn": "farm.apps.corp.example"},
			"alias-read":   {"fqdn": "farm.apps.corp.example", "ttl": 300},
		}
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log,
			Filter: awx.TemplateFilter{Labels: []string{"catalog"}}, ActionLabels: []string{"day2"}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", admin, nil, 200, nil)
		const monitoring = "awx.monitoring-agent"
		h.json("POST", "/api/v1/admin/resource-types", admin, api.ResourceTypeWrite{Key: new("dns-alias"), Title: "DNS alias",
			Create:  api.ResourceTypeDriver{Kind: api.ResourceDriverAwx, Name: "alias-create"},
			Read:    &api.ResourceTypeDriver{Kind: api.ResourceDriverAwx, Name: "alias-read"},
			Delete:  api.ResourceTypeDriver{Kind: api.ResourceDriverAwx, Name: "alias-delete"},
			Actions: &[]string{monitoring}}, 201, nil)

		def := farmWithResources(map[string]any{"key": "alias", "type": "dns-alias", "with": map[string]any{"target": "components.web.fqdns[0]"}})
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-farm", Name: "Web farm",
			Summary: "Web servers with an alias.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-farm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		inputs := map[string]any{"app": "farm", "web_nodes": 1, "size": "S", "cluster": "GVA-DEV-CL01", "network": "dvpg-app-dev-vl310"}
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: web,
			Environment: "dev", Inputs: inputs}, 202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, nil)
		h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		depID := (*built.DeploymentIds)[0]
		launched := func(requestID, stepKey string) map[string]any {
			t.Helper()
			st, err := h.store.GetStepByKey(ctx, requestID, stepKey)
			if err != nil || st.ExternalID == nil {
				t.Fatalf("step %s = %+v (%v)", stepKey, st, err)
			}
			var id int
			fmt.Sscan(*st.ExternalID, &id)
			return h.awx.Launched(id).ExtraVars
		}
		vars := launched(built.Id, "resource:alias")
		rvav, _ := vars["rvav"].(map[string]any)
		if !strings.HasSuffix(fmt.Sprint(vars["target"]), ".corp.example") || rvav["deployment_id"] != depID {
			t.Fatalf("create's extra vars = %v", vars)
		}
		alias := customOf(h, owner, depID)["alias"]
		if alias.ResourceType.Key != "dns-alias" || alias.Name != "web-dev-farm-alias" || alias.Properties["fqdn"] != "farm.apps.corp.example" {
			t.Fatalf("the alias = %+v", alias)
		}
		created, _ := h.store.GetStepByKey(ctx, built.Id, "resource:alias")
		if d := stepDataOf(h, admin, built.Id, created.ID); !strings.HasSuffix(fmt.Sprint(valueOf(d.Inputs)["target"]), ".corp.example") ||
			valueOf(d.Inputs)["rvav"] != nil || valueOf(d.Outputs)["fqdn"] != "farm.apps.corp.example" {
			t.Fatalf("create's data = %+v", d)
		}

		// The AWX action its type lists runs on it, then its read.
		var r api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", owner, api.Day2Draft{Action: monitoring, ResourceId: &alias.Id,
			Inputs: map[string]any{}}, 202, &r, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, owner, api.RequestStatusSucceeded)
		vars = launched(r.Id, "awx.job:monitoring-agent")
		rvav, _ = vars["rvav"].(map[string]any)
		resource, _ := rvav["resource"].(map[string]any)
		vms, _ := rvav["resources"].([]any)
		if resource["key"] != "alias" || resource["id"] != alias.Id || resource["deployment_id"] != depID ||
			resource["phase"] != "day2" || rvav["resources"] == nil || len(vms) != 0 {
			t.Fatalf("the action's extra vars = %v", vars)
		}
		if got := customOf(h, owner, depID)["alias"]; got.Properties["ttl"] != float64(300) {
			t.Fatalf("the alias after its read = %+v", got)
		}

		// Deleting the deployment runs its delete template with what it is.
		var gone api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", owner, api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &gone, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(gone.Id, nil)
		h.waitStatus(gone.Id, owner, api.RequestStatusSucceeded)
		vars = launched(gone.Id, "custom.delete@"+alias.Id)
		rvav, _ = vars["rvav"].(map[string]any)
		resource, _ = rvav["resource"].(map[string]any)
		props, _ := resource["properties"].(map[string]any)
		if vars["fqdn"] != "farm.apps.corp.example" || props["ttl"] != float64(300) {
			t.Fatalf("delete's extra vars = %v", vars)
		}
		deleted, _ := h.store.GetStepByKey(ctx, gone.Id, "custom.delete@"+alias.Id)
		if d := stepDataOf(h, admin, gone.Id, deleted.ID); valueOf(d.Inputs)["ttl"] != float64(300) || valueOf(d.Inputs)["rvav"] != nil {
			t.Fatalf("delete's data = %+v", d)
		}
		if got := customEvents(ctx, h, gone.Id); !slices.Equal(got, []string{"custom.delete.pre alias", "custom.delete.post alias"}) {
			t.Fatalf("the deletion's events = %v", got)
		}
	})
}

// TestCustomResourceStep: a composite's resource step makes a custom
// resource in the deployment of the VM step before it, with inputs from the
// steps before; later steps read its properties. When a later step fails,
// the rollback deletes it with its type's delete before the VM (E1.4,
// ADR-0056).
func TestCustomResourceStep(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner = "mkeller", "tnguyen"
		var problem struct{ Errors []api.FieldError }
		inputs := map[string]any{"type": "object", "required": []any{"members"}, "properties": map[string]any{
			"members": map[string]any{"type": "array", "title": "Members"}, "port": map[string]any{"type": "integer"}}}
		for _, key := range []string{"lb-create", "lb-delete"} {
			h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new(key), Title: key,
				WorkflowType: key + ".v1", TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageResourceType},
				InputSchema: &inputs}, 201, nil)
		}
		schema := map[string]any{"type": "object", "properties": map[string]any{"vip": map[string]any{"type": "string", "title": "Virtual IP"}}}
		h.json("POST", "/api/v1/admin/resource-types", admin, api.ResourceTypeWrite{Key: new("load-balancer"), Title: "Load balancer",
			Create:         api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-create"},
			Delete:         api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-delete"},
			PropertySchema: &schema}, 201, nil)

		// The designer offers it: create's inputs, the schema's properties, and whatever else create returns.
		var targets api.CompositeStepTargetList
		h.json("GET", "/api/v1/admin/catalog/composite-steps", admin, nil, 200, &targets)
		i := slices.IndexFunc(targets.Items, func(t api.CompositeStepTarget) bool { return t.Kind == api.StepTargetResource })
		if i < 0 {
			t.Fatalf("no resource type among the composite steps: %+v", targets.Items)
		}
		lbt := targets.Items[i]
		if lbt.Ref != "load-balancer" || lbt.Runs != api.StepRunsResource || lbt.Planned || !lbt.OpenOutputs ||
			len(lbt.Inputs) != 2 || lbt.Inputs[0].Name != "members" || !lbt.Inputs[0].Required ||
			len(lbt.Outputs) != 1 || lbt.Outputs[0].Name != "vip" || lbt.Outputs[0].Description != "Virtual IP" {
			t.Fatalf("the load balancer as a step = %+v", lbt)
		}

		def := vmWithDBUser()
		def.Blueprint["steps"] = []any{
			def.Blueprint["steps"].([]any)[0],
			map[string]any{"key": "lb", "title": "Its load balancer", "resource": map[string]any{"type": "load-balancer"},
				"with": map[string]any{"members": "[steps.vm.ip]", "port": "443"}},
			map[string]any{"key": "monitoring", "awx": map[string]any{"job_template": "monitoring-agent", "title": "Monitoring"},
				"with": map[string]any{"target": "steps.lb.vip"}},
		}
		bad := vmWithDBUser()
		bad.Blueprint["steps"] = []any{
			map[string]any{"key": "lb", "resource": map[string]any{"type": ""}, "undo": map[string]any{"job_template": "x"}},
		}
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bad-lb", Name: "Bad",
			Summary: "Bad.", Category: "compute", Type: api.Composite, Definition: &bad}, 422, &problem)
		for _, want := range []string{"/definition/blueprint/steps/0/resource/type", "/definition/blueprint/steps/0/undo"} {
			if !slices.Contains(fieldsOf(problem.Errors), want) {
				t.Fatalf("a bad resource step = %v, no %s", fieldsOf(problem.Errors), want)
			}
		}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "vm-lb", Name: "VM behind a load balancer",
			Summary: "A VM and its load balancer.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/vm-lb/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/vm-lb/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		request := func() api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "web", "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "database": "orders", "role_name": "orders_ro"}},
				202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, registerLB)
			h.json("GET", "/api/v1/requests/"+req.Id, owner, nil, 200, &req)
			return req
		}

		ok := request()
		if ok.Status != api.RequestStatusSucceeded {
			t.Fatalf("the composite = %s %s", ok.Status, errorOf(ok))
		}
		depID := (*ok.DeploymentIds)[0]
		lb := customOf(h, owner, depID)["lb"]
		if lb.Properties["vip"] != "10.31.20.10" || lb.Properties["members"] != float64(1) || lb.ResourceType.Key != "load-balancer" {
			t.Fatalf("the load balancer = %+v", lb)
		}
		steps := timelineOf(h, owner, ok.Id)
		job, _ := h.store.GetStepByKey(ctx, ok.Id, "step:monitoring")
		var jobID int
		fmt.Sscan(deref(job.ExternalID), &jobID)
		if steps["step:lb"].Status != api.StepStatusDone || h.awx.Launched(jobID).ExtraVars["target"] != "10.31.20.10" {
			t.Fatalf("lb %+v, monitoring got %v", steps["step:lb"], h.awx.Launched(jobID).ExtraVars)
		}

		// A later step fails: the load balancer is deleted, then the VM.
		h.awx.FailTask = "monitoring : install node_exporter"
		failed := request()
		h.awx.FailTask = ""
		if failed.Status != api.RequestStatusFailed {
			t.Fatalf("the failed composite = %s", failed.Status)
		}
		if got := customEvents(ctx, h, failed.Id); !slices.Equal(got, []string{"custom.create.pre lb", "custom.create.post lb",
			"custom.delete.pre lb", "custom.delete.post lb"}) {
			t.Fatalf("the failed request's events = %v", got)
		}
		failedDep := (*failed.DeploymentIds)[0]
		var left int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources where deployment_id = $1 and type = 'custom'`, failedDep).Scan(&left)
		rolledBack := ""
		for key, s := range timelineOf(h, owner, failed.Id) {
			if strings.HasPrefix(key, "rollback:resource:") {
				rolledBack = string(s.Status)
			}
		}
		if left != 0 || rolledBack != string(api.StepStatusDone) {
			t.Fatalf("after the rollback: %d custom resources left, its step %q", left, rolledBack)
		}

		// Its create workflow offered to other projects only: the request is refused.
		var reg api.WorkflowRegistration
		h.json("GET", "/api/v1/admin/workflows/lb-create", admin, nil, 200, &reg)
		h.json("PUT", "/api/v1/admin/workflows/lb-create", admin, api.WorkflowRegistrationWrite{Title: reg.Title,
			WorkflowType: reg.WorkflowType, TaskQueue: reg.TaskQueue, Usages: reg.Usages, InputSchema: &reg.InputSchema,
			ProjectIds: &[]string{"prj_data"}}, 200, nil)
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "web", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "database": "orders", "role_name": "orders_ro"}},
			422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) == 0 || !strings.Contains(problem.Errors[0].Message, "isn't offered to this project") {
			t.Fatalf("a resource whose create workflow isn't offered to the project = %+v", problem.Errors)
		}
	})
}

// TestCustomResourceRollbacks: a rollback whose custom resource delete
// fails stops before the VMs, which it may use, and says so: the
// deployment stays failed, with a "Roll back failed" step, until a
// rollback deletes it; a create that fails part way leaves a resource
// marked creating, which the rollback deletes with what it has (E1.6,
// ADR-0056).
func TestCustomResourceRollbacks(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner = "mkeller", "tnguyen"
		inputs := map[string]any{"type": "object", "required": []any{"members"}, "properties": map[string]any{
			"members": map[string]any{"type": "array", "title": "Members"}, "port": map[string]any{"type": "integer"}}}
		for _, key := range []string{"lb-create", "lb-delete"} {
			h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new(key), Title: key,
				WorkflowType: key + ".v1", TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageResourceType},
				InputSchema: &inputs}, 201, nil)
		}
		h.json("POST", "/api/v1/admin/resource-types", admin, api.ResourceTypeWrite{Key: new("load-balancer"), Title: "Load balancer",
			Create: api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-create"},
			Delete: api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-delete"}}, 201, nil)
		publish := func(slug string, rollback bool) api.AdminCatalogItem {
			t.Helper()
			def := vmWithDBUser()
			def.Blueprint["rollback_on_failure"] = rollback
			def.Blueprint["steps"] = []any{
				def.Blueprint["steps"].([]any)[0],
				map[string]any{"key": "lb", "title": "Its load balancer", "resource": map[string]any{"type": "load-balancer"},
					"with": map[string]any{"members": "[steps.vm.ip]", "port": "443"}},
				map[string]any{"key": "monitoring", "awx": map[string]any{"job_template": "monitoring-agent", "title": "Monitoring"},
					"with": map[string]any{"target": "steps.lb.vip"}},
			}
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: "VM behind a load balancer " + slug,
				Summary: "A VM and its load balancer.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			return item
		}
		type driver = func(workflow.Context, rvavflow.Context) (map[string]any, error)
		var deleted []rvavflow.CustomResource
		deleting := func(fail bool) driver {
			return func(_ workflow.Context, rc rvavflow.Context) (map[string]any, error) {
				deleted = append(deleted, *rc.Resource)
				if fail {
					return nil, errors.New("the load balancer API is down")
				}
				return map[string]any{}, nil
			}
		}
		halfCreate := func(_ workflow.Context, rc rvavflow.Context) (map[string]any, error) {
			return nil, errors.New("made the VIP 10.31.20.99, then the pool failed")
		}
		request := func(item api.AdminCatalogItem, create, del driver) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "web", "size": "S", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "database": "orders", "role_name": "orders_ro"}},
				202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
				env.RegisterWorkflowWithOptions(create, workflow.RegisterOptions{Name: "lb-create.v1"})
				env.RegisterWorkflowWithOptions(del, workflow.RegisterOptions{Name: "lb-delete.v1"})
			})
			h.json("GET", "/api/v1/requests/"+req.Id, owner, nil, 200, &req)
			return req
		}
		rollback := func(req api.Request, del driver) {
			t.Helper()
			h.json("POST", "/api/v1/requests/"+req.Id+"/rollback", owner, nil, 202, nil)
			var s testsuite.WorkflowTestSuite
			s.SetLogger(nil)
			env := s.NewTestWorkflowEnvironment()
			env.RegisterActivity(h.acts)
			env.RegisterWorkflowWithOptions(del, workflow.RegisterOptions{Name: "lb-delete.v1"})
			env.ExecuteWorkflow(provision.RollbackWorkflow, req.Id)
			if err := env.GetWorkflowError(); err != nil {
				t.Fatalf("rollback workflow: %v", err)
			}
		}
		type left struct {
			deployment  string
			custom, vms int
			step        api.Step
		}
		leftOf := func(req api.Request) left {
			t.Helper()
			var l left
			dep := (*req.DeploymentIds)[0]
			_ = h.store.Pool.QueryRow(ctx, `select status from deployments where id = $1`, dep).Scan(&l.deployment)
			_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources where deployment_id = $1 and type = 'custom'`, dep).Scan(&l.custom)
			_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources where deployment_id = $1 and type = 'vsphere.vm'`, dep).Scan(&l.vms)
			l.step = timelineOf(h, owner, req.Id)["rollback"]
			return l
		}

		// Rolled back on failure, the delete fails: the VM stays.
		auto := publish("vm-lb-auto", true)
		h.awx.FailTask = "monitoring : install node_exporter"
		failed := request(auto, lbCreate, deleting(true))
		if l := leftOf(failed); failed.Status != api.RequestStatusFailed || l.deployment != "failed" || l.custom != 1 || l.vms != 1 ||
			l.step.Title != "Roll back failed" || l.step.Status != api.StepStatusFailed || !strings.Contains(deref(l.step.Error), "the load balancer API is down") {
			t.Fatalf("a rollback whose delete fails: %s, left %+v", failed.Status, l)
		}

		// A create that fails part way: its resource is deleted with what it has.
		deleted = nil
		half := request(auto, halfCreate, deleting(false))
		h.awx.FailTask = ""
		if l := leftOf(half); half.Status != api.RequestStatusFailed || l.custom != 0 || l.vms != 0 || l.step.Status != api.StepStatusDone ||
			len(deleted) != 1 || !deleted[0].Creating || len(deleted[0].Properties) != 0 || deleted[0].Inputs["port"] != float64(443) {
			t.Fatalf("a create failing part way: %s, left %+v, deletes %+v", half.Status, l, deleted)
		}
		if got := customEvents(ctx, h, half.Id); !slices.Equal(got, []string{"custom.create.pre lb", "custom.delete.pre lb", "custom.delete.post lb"}) {
			t.Fatalf("its events = %v", got)
		}

		// Kept on failure, rolled back by hand: a failed delete keeps the
		// deployment failed, and a later rollback finishes it.
		kept := publish("vm-lb-kept", false)
		h.awx.FailTask = "monitoring : install node_exporter"
		manual := request(kept, lbCreate, deleting(true))
		h.awx.FailTask = ""
		if lb := customOf(h, owner, (*manual.DeploymentIds)[0])["lb"]; lb.Creating != nil {
			t.Fatalf("a load balancer made is still creating: %+v", lb)
		}
		rollback(manual, deleting(true))
		var notified int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from notifications where request_id = $1 and kind = 'request_rolled_back'`, manual.Id).Scan(&notified)
		if l := leftOf(manual); l.deployment != "failed" || l.custom != 1 || l.vms != 1 || l.step.Status != api.StepStatusFailed || notified != 0 {
			t.Fatalf("a rollback by hand whose delete fails: left %+v, %d notified", l, notified)
		}
		rollback(manual, lbDelete)
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from notifications where request_id = $1 and kind = 'request_rolled_back'`, manual.Id).Scan(&notified)
		if l := leftOf(manual); l.deployment != "deleted" || l.custom != 0 || l.vms != 0 || l.step.Title != "Rolled back" ||
			l.step.Status != api.StepStatusDone || l.step.Error != nil || notified != 1 {
			t.Fatalf("rolled back again: left %+v, %d notified", l, notified)
		}
	})
}
