package e2e

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestNewVMItemNaming: the VM item New item makes names its VMs where no
// naming policy applies, and a policy still wins over it; a VM item with
// no naming is published with a warning, and refused at request with a
// 422 that sends the requester to a catalog admin, never a 500, while a
// policy may still name its VMs (#216).
func TestNewVMItemNaming(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin, member = "prj_web", "lmartin", "tnguyen"

		var starter api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "starter-named", Name: "Starter VM",
			Summary: "As New item makes it.", Category: "compute", Type: api.VmBlueprint}, 201, &starter)
		var draft api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/starter-named/versions/"+starter.DraftVersion.Id, admin, nil, 200, &draft)
		def := draft.Definition
		if got := def.Blueprint["naming"]; got != "{project}-{environment}-{component}{seq:03}" {
			t.Fatalf("the starter's naming = %v", got)
		}
		def.Blueprint["template"] = "tpl-rhel9.4-2026.10"
		def.Blueprint["bindings"].(map[string]any)["network"] = `"dvpg-app-dev-vl310"`
		publish := func(slug, versionID string, d api.CatalogVersionDefinition) {
			t.Helper()
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/versions/"+versionID, admin, api.CatalogItemVersionUpdate{Definition: d}, 200, nil)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+versionID+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{web}}, 200, nil)
		}
		publish("starter-named", starter.DraftVersion.Id, def)
		requestOf := func(item api.AdminCatalogItem) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: web, Environment: "dev",
				Inputs: map[string]any{"name": "first"}}
		}
		names := func(item api.AdminCatalogItem) []string {
			t.Helper()
			var p api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", member, requestOf(item), 200, &p)
			if !p.Valid || p.Names == nil {
				t.Fatalf("preview of %s = %+v", item.Slug, p)
			}
			return *p.Names
		}
		planned := func(id string) string {
			t.Helper()
			var raw []byte
			if err := h.store.Pool.QueryRow(ctx, `select plan from requests where id = $1`, id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var plan struct {
				VMs []struct{ Name string } `json:"vms"`
			}
			if err := json.Unmarshal(raw, &plan); err != nil || len(plan.VMs) != 1 {
				t.Fatalf("plan = %s (%v)", raw, err)
			}
			return plan.VMs[0].Name
		}

		// No naming policy applies: the starter's naming names the VM.
		if got := names(starter); !slices.Equal(got, []string{"web-dev-001"}) {
			t.Fatalf("names without a policy = %v", got)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", member, requestOf(starter), 202, &req, "Idempotency-Key", uuid.NewString())
		if got := planned(req.Id); got != "web-dev-001" {
			t.Fatalf("the VM planned = %q", got)
		}
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, member, api.RequestStatusSucceeded)

		// An item without a naming: published, as a policy may name its VMs,
		// and refused at request while none does, its VM name left blank.
		unnamed := def
		unnamed.Blueprint = maps.Clone(def.Blueprint)
		unnamed.Blueprint["naming"] = ""
		unnamed.FormSchema = maps.Clone(def.FormSchema)
		unnamed.FormSchema["properties"] = map[string]any{"name": def.FormSchema["properties"].(map[string]any)["name"],
			"vm_name": map[string]any{"type": "string", "readOnly": true}}
		unnamed.UiSchema.Fields = map[string]api.UiField{"name": def.UiSchema.Fields["name"],
			"vm_name": {Widget: api.UiWidgetComputed, Label: "VM name", ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("naming.preview")}}}
		unnamed.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Unnamed VM", Fields: []string{"name", "vm_name"}}}
		var bare api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "unnamed-vm", Name: "Unnamed VM",
			Summary: "No naming of its own.", Category: "compute", Type: api.VmBlueprint, Definition: &unnamed}, 201, &bare)
		publish("unnamed-vm", bare.DraftVersion.Id, unnamed)
		warned := func(slug, versionID string) []string {
			t.Helper()
			var v api.CatalogItemVersionDetail
			h.json("GET", "/api/v1/admin/catalog/items/"+slug+"/versions/"+versionID, admin, nil, 200, &v)
			out := []string{}
			if v.Warnings == nil {
				return out
			}
			for _, w := range *v.Warnings {
				if deref(w.Code) == "no_naming" {
					out = append(out, w.Field+" "+w.Message)
				}
			}
			return out
		}
		const needs = "its requests need a naming policy, and are refused in a project and environment no policy applies to."
		if got := warned("unnamed-vm", bare.DraftVersion.Id); !slices.Equal(got, []string{
			"/definition/blueprint/naming The item gives its VMs no naming: " + needs}) {
			t.Fatalf("the warnings of the item without a naming = %q", got)
		}
		if got := warned("starter-named", starter.DraftVersion.Id); len(got) > 0 {
			t.Fatalf("the starter's warnings = %q", got)
		}
		// A component without a naming, beside one with: at that component.
		half := unnamed
		half.Blueprint = maps.Clone(unnamed.Blueprint)
		half.Blueprint["components"] = []any{
			map[string]any{"key": "web", "naming": "{project}-{environment}-web{seq:02}"},
			map[string]any{"key": "db", "title": "Database"},
		}
		var halfNamed api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "half-named", Name: "Half named",
			Summary: "One component named.", Category: "compute", Type: api.VmBlueprint, Definition: &half}, 201, &halfNamed)
		if got := warned("half-named", halfNamed.DraftVersion.Id); !slices.Equal(got, []string{
			"/definition/blueprint/components/1/naming Database has no naming, and the item gives its VMs none: " + needs}) {
			t.Fatalf("the warnings of a component without a naming = %q", got)
		}
		const refusal = "No naming policy applies to Web Platform in Development, and the item gives the VMs no naming of its own. Ask a catalog admin to check this item."
		refused := func(errs []api.FieldError) bool {
			return slices.ContainsFunc(errs, func(e api.FieldError) bool {
				return e.Field == "" && deref(e.Code) == "no_naming" && e.Message == refusal
			})
		}
		var p api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", member, requestOf(bare), 200, &p)
		if p.Valid || !refused(p.Errors) || p.Names != nil && len(*p.Names) > 0 {
			t.Fatalf("preview without any naming = %+v", p)
		}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/requests", member, requestOf(bare), 422, &problem, "Idempotency-Key", uuid.NewString())
		if !refused(problem.Errors) {
			t.Fatalf("a request without any naming = %+v", problem.Errors)
		}
		vmName := func() any {
			t.Helper()
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", member, api.FieldValuesQuery{VersionId: bare.DraftVersion.Id, ProjectId: web,
				Environment: new(api.EnvironmentKey("dev")), Field: "vm_name", Inputs: map[string]any{"name": "first"}}, 200, &out)
			return out.Value
		}
		if v := vmName(); v != nil {
			t.Fatalf("the VM name previewed without any naming = %v", v)
		}

		// A naming policy wins over the starter's naming, and names the
		// item without one.
		h.json("POST", "/api/v1/admin/naming-policies", "mkeller", api.NamingPolicyWrite{Name: "Web names",
			Template: "{project}-{environment}-pol{seq:02}", Offers: &[]api.Offer{{ProjectId: new(web)}}}, 201, nil)
		if got := names(starter); !slices.Equal(got, []string{"web-dev-pol01"}) {
			t.Fatalf("names under the policy = %v", got)
		}
		if got := names(bare); !slices.Equal(got, []string{"web-dev-pol01"}) {
			t.Fatalf("names of the item without a naming, under the policy = %v", got)
		}
		if v, _ := vmName().(string); !strings.HasPrefix(v, "web-dev-pol") {
			t.Fatalf("the VM name previewed under the policy = %q", v)
		}
		h.json("POST", "/api/v1/requests", member, requestOf(bare), 202, &req, "Idempotency-Key", uuid.NewString())
		if got := planned(req.Id); got != "web-dev-pol01" {
			t.Fatalf("the VM planned under the policy = %q", got)
		}

		// A policy for one project leaves the warning, one for everywhere
		// doesn't.
		if got := warned("unnamed-vm", bare.DraftVersion.Id); len(got) != 1 {
			t.Fatalf("the warnings under Web Platform's policy = %q", got)
		}
		h.json("POST", "/api/v1/admin/naming-policies", "mkeller", api.NamingPolicyWrite{Name: "All names",
			Template: "{project}-{environment}-all{seq:02}", Offers: &[]api.Offer{{}}}, 201, nil)
		if got := warned("unnamed-vm", bare.DraftVersion.Id); len(got) > 0 {
			t.Fatalf("the warnings under a policy for everywhere = %q", got)
		}
	})
}
