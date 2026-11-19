package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestResourceTypes: administrators define custom resource types, each
// driven by custom workflows registered for them or by AWX templates;
// auditors read them; one in use can't be removed (ADR-0056).
func TestResourceTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }
		h.awx.Plays["lb-create"] = []string{"Gathering Facts", "f5 : create the virtual server"}

		register := func(key string, usages ...api.WorkflowUsage) {
			h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new(key), Title: key,
				WorkflowType: key + ".v1", TaskQueue: "platform-workflows", Usages: usages}, 201, nil)
		}
		register("lb-delete", api.WorkflowUsageResourceType)
		register("lb-report", api.WorkflowUsageCompositeStep)
		h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new("lb-drain"), Title: "Drain",
			WorkflowType: "lb-drain.v1", TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageDay2Action},
			Day2: &api.WorkflowDay2{Name: "Drain", Types: []api.WorkflowDay2Types{api.WorkflowDay2TypesDeployment}}}, 201, nil)

		lb := api.ResourceTypeWrite{Key: new("load-balancer"), Title: "Load balancer",
			Create: api.ResourceTypeDriver{Kind: api.ResourceDriverAwx, Name: "lb-create"},
			Delete: api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-delete"},
			PropertySchema: &map[string]any{"type": "object", "properties": map[string]any{
				"vip": map[string]any{"type": "string", "title": "Virtual IP"}}},
			Actions: &[]string{"wf.lb-drain"}}
		h.json("POST", "/api/v1/admin/resource-types", "lmartin", lb, 403, nil)

		bad := lb
		bad.Key, bad.Title = new("Load Balancer"), " "
		bad.Create = api.ResourceTypeDriver{Kind: api.ResourceDriverAwx, Name: "no-such-template"}
		bad.Delete = api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-report"}
		bad.Update = &api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "nothing"}
		bad.PropertySchema = &map[string]any{"type": "array"}
		bad.Actions = &[]string{"power_off", "wf.lb-report", "awx.nothing", "wf.lb-drain", "wf.lb-drain"}
		h.json("POST", "/api/v1/admin/resource-types", admin, bad, 422, &problem)
		for _, want := range []string{"/key", "/title", "/create/name", "/delete/name", "/update/name", "/property_schema/type",
			"/actions/0", "/actions/1", "/actions/2", "/actions/4"} {
			if !slices.Contains(fieldsOf(problem.Errors), want) {
				t.Fatalf("a bad type = %v, no %s", fieldsOf(problem.Errors), want)
			}
		}
		if slices.Contains(fieldsOf(problem.Errors), "/actions/3") {
			t.Fatalf("a day-2 workflow refused as an action: %v", problem.Errors)
		}

		var made api.ResourceType
		h.json("POST", "/api/v1/admin/resource-types", admin, lb, 201, &made)
		h.json("POST", "/api/v1/admin/resource-types", admin, lb, 409, nil)
		if made.Key != "load-balancer" || made.Create.Name != "lb-create" || !made.Enabled || made.Resources != 0 ||
			len(made.UsedBy) != 0 || made.Update != nil || !slices.Equal(made.Actions, []string{"wf.lb-drain"}) {
			t.Fatalf("defined = %+v", made)
		}

		// Auditors read types; the workflows they run say what uses them.
		var list api.ResourceTypeList
		h.json("GET", "/api/v1/admin/resource-types", "svc-awx", nil, 200, &list)
		if len(list.Items) != 1 || list.Items[0].Key != "load-balancer" {
			t.Fatalf("types = %+v", list.Items)
		}
		var reg api.WorkflowRegistration
		for _, key := range []string{"lb-delete", "lb-drain"} {
			h.json("GET", "/api/v1/admin/workflows/"+key, admin, nil, 200, &reg)
			if !slices.Equal(reg.UsedBy, []string{"resource type load-balancer"}) {
				t.Fatalf("%s is used by %v", key, reg.UsedBy)
			}
		}
		h.json("DELETE", "/api/v1/admin/workflows/lb-delete", admin, nil, 409, nil)

		// Changing it: an update step, the key unchanged.
		register("lb-resize", api.WorkflowUsageResourceType)
		change := lb
		change.Key, change.Title = new("ignored"), "F5 load balancer"
		change.Update = &api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-resize"}
		change.Enabled = new(false)
		var changed api.ResourceType
		h.json("PUT", "/api/v1/admin/resource-types/load-balancer", admin, change, 200, &changed)
		if changed.Key != "load-balancer" || changed.Title != "F5 load balancer" || changed.Enabled ||
			changed.Update == nil || changed.Update.Name != "lb-resize" {
			t.Fatalf("changed = %+v", changed)
		}
		h.json("PUT", "/api/v1/admin/resource-types/nothing", admin, change, 404, nil)

		h.json("DELETE", "/api/v1/admin/resource-types/load-balancer", "svc-awx", nil, 403, nil)
		h.json("DELETE", "/api/v1/admin/resource-types/load-balancer", admin, nil, 204, nil)
		h.json("GET", "/api/v1/admin/resource-types/load-balancer", admin, nil, 404, nil)
		h.json("DELETE", "/api/v1/admin/workflows/lb-delete", admin, nil, 204, nil)
	})
}
