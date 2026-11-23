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

// TestNewVMItemSize: the VM item New item makes is requested with a size
// (#207), and publishing refuses a VM item whose CPU or memory can't be
// worked out, at the binding.
func TestNewVMItemSize(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin = "prj_web", "lmartin"

		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "starter-vm", Name: "Starter VM",
			Summary: "As New item makes it.", Category: "compute", Type: api.VmBlueprint}, 201, &item)
		var draft api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/starter-vm/versions/"+item.DraftVersion.Id, admin, nil, 200, &draft)
		def := draft.Definition
		if b, _ := def.Blueprint["bindings"].(map[string]any); b["vcpu"] != "2" || b["memory_gb"] != "4" {
			t.Fatalf("the starter's bindings = %v", def.Blueprint["bindings"])
		}
		save := func(d api.CatalogVersionDefinition, status int, out any) {
			t.Helper()
			h.json("PUT", "/api/v1/admin/catalog/items/starter-vm/versions/"+item.DraftVersion.Id, admin,
				api.CatalogItemVersionUpdate{Definition: d}, status, out)
		}
		publishRefused := func(want ...string) {
			t.Helper()
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/admin/catalog/items/starter-vm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 422, &problem)
			got := fieldsOf(problem.Errors)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Fatalf("publishing refused at %v, want %v (%+v)", got, want, problem.Errors)
			}
		}
		def.Blueprint["template"] = "tpl-rhel9.4-2026.10"

		// Without its CPU, or with none, it isn't published.
		bindings := def.Blueprint["bindings"].(map[string]any)
		delete(bindings, "vcpu")
		save(def, 200, nil)
		publishRefused("/definition/blueprint/bindings/vcpu")
		bindings["vcpu"], bindings["memory_gb"] = "0", "2 - 2"
		save(def, 200, nil)
		publishRefused("/definition/blueprint/bindings/memory_gb", "/definition/blueprint/bindings/vcpu")

		// A component takes the blueprint's, or its own.
		multi := def
		multi.Blueprint = map[string]any{"kind": "vm", "template": "tpl-rhel9.4-2026.10", "guest": map[string]any{"os_family": "linux"},
			"bindings": map[string]any{"vcpu": "2"},
			"components": []any{
				map[string]any{"key": "web", "bindings": map[string]any{"memory_gb": "8"}},
				map[string]any{"key": "db"},
			}}
		save(multi, 200, nil)
		publishRefused("/definition/blueprint/components/1/bindings/memory_gb")

		// As New item made it, it is published and requested: its VM has that size.
		bindings["vcpu"], bindings["memory_gb"] = "2", "4"
		bindings["network"] = `"dvpg-app-dev-vl310"`
		save(def, 200, nil)
		h.json("POST", "/api/v1/admin/catalog/items/starter-vm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/starter-vm/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{web}}, 200, nil)
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
