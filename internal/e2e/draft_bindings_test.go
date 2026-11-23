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

// TestDraftBindings: a draft's save takes a number for a binding as the
// constant it means, and refuses a binding it can't read or one reading an
// input the form hasn't, at its field, in words (#208). A draft stored
// before it refused them still loads, and shows them when published.
func TestDraftBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin = "prj_web", "lmartin"

		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "bound-vm", Name: "Bound VM",
			Summary: "Its bindings written by hand.", Category: "compute", Type: api.VmBlueprint}, 201, &item)
		path := "/api/v1/admin/catalog/items/bound-vm/versions/" + item.DraftVersion.Id
		var draft api.CatalogItemVersionDetail
		h.json("GET", path, admin, nil, 200, &draft)
		def := draft.Definition
		def.Blueprint["template"] = "tpl-rhel9.4-2026.10"
		bindings := map[string]any{"vcpu": 2, "memory_gb": 4, "network": `"dvpg-app-dev-vl310"`}
		def.Blueprint["bindings"] = bindings
		refused := func(method, at string, d *api.CatalogVersionDefinition, field, code, message string) {
			t.Helper()
			var body any
			if d != nil {
				body = api.CatalogItemVersionUpdate{Definition: *d}
			}
			var problem struct{ Errors []api.FieldError }
			h.json(method, path+at, admin, body, 422, &problem)
			if !slices.ContainsFunc(problem.Errors, func(e api.FieldError) bool {
				return e.Field == field && *e.Code == code && (message == "" || e.Message == message)
			}) {
				t.Fatalf("refused with %+v, want %s %s", problem.Errors, field, code)
			}
		}

		// A list where a binding's CEL is: refused at the binding, in words.
		bindings["memory_gb"] = []any{4}
		refused("PUT", "", &def, "/definition/blueprint/bindings/memory_gb", "invalid",
			`This is a list, where the blueprint takes CEL in quotes ("inputs.network") or a number.`)

		// An input the form hasn't: refused where it is read.
		bindings["memory_gb"], bindings["network"] = 4, "inputs.zone"
		refused("PUT", "", &def, "/definition/blueprint/bindings/network", "unknown_input",
			`This reads inputs.zone, and "zone" is not an input of the form's schema.`)
		def.Blueprint["disks"] = []any{map[string]any{"size_gb": "inputs.data_gb"}}
		bindings["network"] = `"dvpg-app-dev-vl310"`
		refused("PUT", "", &def, "/definition/blueprint/disks/0/size_gb", "unknown_input", "")
		delete(def.Blueprint, "disks")

		// A draft stored before the save refused it still loads, and
		// publishing it says why it isn't.
		h.json("PUT", path, admin, api.CatalogItemVersionUpdate{Definition: def}, 200, nil)
		if _, err := h.store.Pool.Exec(ctx, `update catalog_item_versions
			set blueprint = jsonb_set(blueprint, '{bindings,network}', '"inputs.zone"') where id = $1`, item.DraftVersion.Id); err != nil {
			t.Fatal(err)
		}
		var stored api.CatalogItemVersionDetail
		h.json("GET", path, admin, nil, 200, &stored)
		if got := stored.Definition.Blueprint["bindings"].(map[string]any)["network"]; got != "inputs.zone" {
			t.Fatalf("the stored draft's network = %v", got)
		}
		refused("POST", "/publish", nil, "/definition/blueprint/bindings/network", "unknown_input", "")

		// Numbers for its size, published and requested: its VM has them.
		h.json("PUT", path, admin, api.CatalogItemVersionUpdate{Definition: def}, 200, nil)
		h.json("POST", path+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/bound-vm/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{web}}, 200, nil)
		h.json("POST", "/api/v1/admin/naming-policies", "mkeller", api.NamingPolicyWrite{Name: "Web names",
			Template: "{project}-{environment}-vm{seq:02}", Offers: &[]api.Offer{{ProjectId: new(web)}}}, 201, nil)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: web, Environment: "dev", Inputs: map[string]any{"name": "first"}}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		var raw []byte
		if err := h.store.Pool.QueryRow(ctx, `select plan from requests where id = $1`, req.Id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plan struct {
			VMs []struct {
				VCPU     int `json:"vcpu"`
				MemoryGB int `json:"memory_gb"`
			} `json:"vms"`
		}
		if err := json.Unmarshal(raw, &plan); err != nil || len(plan.VMs) != 1 || plan.VMs[0].VCPU != 2 || plan.VMs[0].MemoryGB != 4 {
			t.Fatalf("plan = %s (%v)", raw, err)
		}
	})
}
