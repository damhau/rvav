package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestResourceWithKeys: a draft's save refuses an input a custom
// resource's with binds that its type's create workflow doesn't take, at
// the key, naming those it takes and name (#208), in a VM blueprint's
// resources as in a composite's resource step. A type rvav can't tell the
// inputs of, or one that doesn't exist yet, takes any.
func TestResourceWithKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		inputs := map[string]any{"type": "object", "properties": map[string]any{
			"members": map[string]any{"type": "array"}, "port": map[string]any{"type": "integer"}}}
		for key, schema := range map[string]*map[string]any{"lb-create": &inputs, "lb-delete": nil, "alias-create": nil} {
			h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new(key), Title: key,
				WorkflowType: key + ".v1", TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageResourceType},
				InputSchema: schema}, 201, nil)
		}
		for _, ty := range []struct{ key, title, create string }{
			{"load-balancer", "Load balancer", "lb-create"}, {"dns-alias", "DNS alias", "alias-create"},
		} {
			h.json("POST", "/api/v1/admin/resource-types", admin, api.ResourceTypeWrite{Key: new(ty.key), Title: ty.title,
				Create:         api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: ty.create},
				Delete:         api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "lb-delete"},
				PropertySchema: &map[string]any{"type": "object", "properties": map[string]any{"vip": map[string]any{"type": "string"}}}}, 201, nil)
		}
		refused := func(slug string, typ api.CatalogItemType, def api.CatalogVersionDefinition, field string) {
			t.Helper()
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug,
				Summary: "Its resource binds an input.", Category: "compute", Type: typ, Definition: &def}, 422, &problem)
			if !slices.ContainsFunc(problem.Errors, func(e api.FieldError) bool {
				return e.Field == field && *e.Code == "unknown_input" &&
					e.Message == "Load balancer takes members, name and port: pool isn't one of its inputs."
			}) {
				t.Fatalf("%s refused with %+v, want %s", slug, problem.Errors, field)
			}
		}

		// The load balancer's create takes members and port: pool is refused.
		lb := map[string]any{"key": "lb", "type": "load-balancer",
			"with": map[string]any{"members": "components.web.addresses", "port": 443, "name": `"farm-lb"`, "pool": `"web"`}}
		refused("farm-pool", api.VmBlueprint, farmWithResources(lb), "/definition/blueprint/resources/0/with/pool")

		step := vmWithDBUser()
		step.Blueprint["steps"] = []any{
			step.Blueprint["steps"].([]any)[0],
			map[string]any{"key": "lb", "resource": map[string]any{"type": "load-balancer"},
				"with": map[string]any{"members": "[steps.vm.ip]", "pool": `"web"`}},
		}
		refused("step-pool", api.Composite, step, "/definition/blueprint/steps/1/with/pool")

		// Its own inputs, a type whose create declares none and one that
		// doesn't exist yet are saved.
		delete(lb["with"].(map[string]any), "pool")
		alias := map[string]any{"key": "alias", "type": "dns-alias", "with": map[string]any{"target": "components.web.fqdns[0]"}}
		later := map[string]any{"key": "later", "type": "not-yet", "with": map[string]any{"anything": "1"}}
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "farm-lb", Name: "Farm",
			Summary: "Its resources bind their inputs.", Category: "compute", Type: api.VmBlueprint,
			Definition: new(farmWithResources(lb, alias, later))}, 201, nil)
	})
}
