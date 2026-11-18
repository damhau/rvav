package e2e

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// TestPropertyGroups: a property group's inputs join the form of an item
// naming it as if declared, and bindings read its constants; the item
// keeps the version it names; a group a published version names can't be
// deleted, and a draft naming one that's gone is refused when saved (T1.5,
// ADR-0038).
func TestPropertyGroups(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }
		yes := true
		opts := []string{"CC-1001", "CC-2002"}
		ops := api.PropertyGroupWrite{Key: present.Ptr("ops"), Name: "Operations", Inputs: []api.PropertyGroupInput{
			{Name: "costCenter", Label: "Cost centre", Type: api.PropertyGroupInputTypeString, Required: &yes, Options: &opts},
			{Name: "backup", Label: "Back up", Type: api.PropertyGroupInputTypeBoolean, Default: true},
		}, Constants: map[string]any{"domain": "corp.example", "tier": "gold", "copies": 2}}
		var g api.PropertyGroup
		h.json("POST", "/api/v1/admin/property-groups", admin, ops, 201, &g)
		if g.Version != 1 || !slices.Equal(g.Versions, []int{1}) || g.Constants["copies"] != 2.0 {
			t.Fatalf("created = %+v", g)
		}
		h.json("POST", "/api/v1/admin/property-groups", admin, ops, 409, nil)
		h.json("GET", "/api/v1/admin/property-groups", "tnguyen", nil, 403, nil)
		bad := ops
		bad.Key, bad.Constants = present.Ptr("ops2"), map[string]any{"nested": map[string]any{"a": 1}}
		h.json("POST", "/api/v1/admin/property-groups", admin, bad, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Equal(f, []string{"/constants/nested"}) {
			t.Fatalf("a nested constant = %+v", problem.Errors)
		}

		// Version 2 changes the constants: an item naming version 1 keeps its.
		ops.Key, ops.Constants = nil, map[string]any{"domain": "new.example", "tier": "silver"}
		h.json("PUT", "/api/v1/admin/property-groups/ops", admin, ops, 200, &g)
		if g.Version != 2 || !slices.Equal(g.Versions, []int{1, 2}) {
			t.Fatalf("saved = %+v", g)
		}

		def := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		def.Blueprint["property_groups"] = []any{map[string]any{"key": "ops", "version": 9}}
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pg-bad", Name: "p", Summary: "p",
			Category: "compute", Type: api.VmBlueprint, Definition: &def}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/definition/blueprint/property_groups/0/version" ||
			!strings.Contains(problem.Errors[0].Message, "its latest is 2") {
			t.Fatalf("a version that doesn't exist = %+v", problem.Errors)
		}
		clash := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		clash.FormSchema["properties"].(map[string]any)["backup"] = map[string]any{"type": "boolean"}
		clash.UiSchema.Fields["backup"] = api.UiField{Widget: api.UiWidgetToggle, Label: "Backup"}
		clash.UiSchema.Layout.Sections[0].Fields = append(clash.UiSchema.Layout.Sections[0].Fields, "backup")
		clash.Blueprint["property_groups"] = []any{map[string]any{"key": "ops", "version": 1}}
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pg-clash", Name: "p", Summary: "p",
			Category: "compute", Type: api.VmBlueprint, Definition: &clash}, 422, &problem)
		if len(problem.Errors) != 1 || !strings.HasPrefix(problem.Errors[0].Message, "backup is an input of the form and of property group ops") {
			t.Fatalf("an input in the form and the group = %+v", problem.Errors)
		}

		def.Blueprint["property_groups"] = []any{map[string]any{"key": "ops", "version": 1}}
		def.Blueprint["properties"] = map[string]any{"cost": "${inputs.costCenter}", "domain": "${propgroup.ops.domain}",
			"tier": "${propgroup.ops.tier + (inputs.backup ? '-backed-up' : '')}"}
		def.UiSchema.Fields["role"] = api.UiField{Widget: api.UiWidgetText, Label: "role",
			Rules: &api.UiRules{Visible: new(`propgroup.ops.tier == "gold"`)}}
		item := publishItem(h, "pg-item", def)

		// The request form has the group's inputs, in a section of their own.
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/"+item.Id+"/form?project_id=prj_web", "tnguyen", nil, 200, &form)
		props := form.Schema["properties"].(map[string]any)
		required, _ := form.Schema["required"].([]any)
		sections := form.UiSchema.Layout.Sections
		if props["costCenter"] == nil || !slices.Contains(required, any("costCenter")) || form.InitialValues["backup"] != true ||
			form.UiSchema.Fields["costCenter"].Widget != api.UiWidgetSelect || len(sections) != 2 ||
			sections[1].Key != "propgroup-ops" || !slices.Equal(sections[1].Fields, []string{"costCenter", "backup"}) {
			t.Fatalf("form = %+v / %+v / %+v", form.Schema, form.UiSchema, form.InitialValues)
		}
		// The definition stays as written.
		var detail api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/pg-item/versions/"+item.DraftVersion.Id, admin, nil, 200, &detail)
		if detail.Definition.FormSchema["properties"].(map[string]any)["costCenter"] != nil {
			t.Fatalf("the definition has the group's inputs: %+v", detail.Definition.FormSchema)
		}

		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "pg", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310",
				"backup": true}}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return strings.Contains(e.Field, "costCenter") }) {
			t.Fatalf("preview without the group's required input = %+v", pv.Errors)
		}
		if role := (*pv.Fields)["role"]; !role.Visible || role.Error != nil {
			t.Fatalf("a form rule reading a constant = %+v", role)
		}
		draft.Inputs["costCenter"] = "CC-2002"
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)

		var vm mo.VirtualMachine
		if err := property.DefaultCollector(c).RetrieveOne(ctx, vmRefByName(t, ctx, c, "web-dev-pg01"),
			[]string{"customValue", "availableField"}, &vm); err != nil {
			t.Fatal(err)
		}
		names := map[int32]string{}
		for _, f := range vm.AvailableField {
			names[f.Key] = f.Name
		}
		attrs := map[string]string{}
		for _, v := range vm.CustomValue {
			if s, ok := v.(*types.CustomFieldStringValue); ok && names[s.Key] != "costCenter" && names[s.Key] != "owner" {
				attrs[names[s.Key]] = s.Value
			}
		}
		if want := map[string]string{"cost": "CC-2002", "domain": "corp.example", "tier": "gold-backed-up"}; !maps.Equal(attrs, want) {
			t.Fatalf("custom attributes = %v", attrs)
		}

		// In use by a published version: listed, and kept.
		h.json("GET", "/api/v1/admin/property-groups/ops?version=1", admin, nil, 200, &g)
		if g.Version != 1 || g.Constants["domain"] != "corp.example" || !slices.Equal(g.UsedBy, []string{"pg-item"}) {
			t.Fatalf("version 1 = %+v", g)
		}
		h.json("DELETE", "/api/v1/admin/property-groups/ops", admin, nil, 409, nil)

		// Only a draft names it: it may go, and the draft is refused when saved.
		temp := api.PropertyGroupWrite{Key: present.Ptr("temp"), Name: "Temporary", Inputs: []api.PropertyGroupInput{},
			Constants: map[string]any{"x": "y"}}
		h.json("POST", "/api/v1/admin/property-groups", admin, temp, 201, nil)
		later := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		later.Blueprint["property_groups"] = []any{map[string]any{"key": "temp", "version": 1}}
		var drafted api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pg-draft", Name: "p", Summary: "p",
			Category: "compute", Type: api.VmBlueprint, Definition: &later}, 201, &drafted)
		var list api.PropertyGroupList
		h.json("GET", "/api/v1/admin/property-groups", admin, nil, 200, &list)
		if len(list.Items) != 2 || list.Items[1].Key != "temp" || len(list.Items[1].UsedBy) != 0 || list.Items[0].Version != 2 {
			t.Fatalf("groups = %+v", list.Items)
		}
		h.json("DELETE", "/api/v1/admin/property-groups/temp", admin, nil, 204, nil)
		h.json("GET", "/api/v1/admin/property-groups/temp", admin, nil, 404, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/pg-draft/versions/"+drafted.DraftVersion.Id, admin,
			api.CatalogItemVersionUpdate{Definition: later}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/definition/blueprint/property_groups/0/key" {
			t.Fatalf("a draft naming a group that's gone = %+v", problem.Errors)
		}

		// A delete that has found no user holds the group: a publish naming
		// it waits, then finds it gone (T1.7).
		h.json("POST", "/api/v1/admin/property-groups", admin, api.PropertyGroupWrite{Key: present.Ptr("race"), Name: "Race",
			Inputs: []api.PropertyGroupInput{}, Constants: map[string]any{"x": "y"}}, 201, nil)
		later.Blueprint["property_groups"] = []any{map[string]any{"key": "race", "version": 1}}
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pg-race", Name: "p", Summary: "p",
			Category: "compute", Type: api.VmBlueprint, Definition: &later}, 201, &drafted)
		tx, err := h.store.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := store.LockPropertyGroup(ctx, tx, "race"); err != nil {
			t.Fatal(err)
		}
		type result struct {
			status int
			body   []byte
		}
		published := make(chan result, 1)
		go func() {
			status, body, _ := h.do("POST", "/api/v1/admin/catalog/items/pg-race/versions/"+drafted.DraftVersion.Id+"/publish", admin, nil)
			published <- result{status, body}
		}()
		var got *result
		for deadline := time.Now().Add(10 * time.Second); got == nil && time.Now().Before(deadline); {
			select {
			case r := <-published:
				got = &r
			case <-time.After(20 * time.Millisecond):
				var waiting int
				_ = h.store.Pool.QueryRow(ctx, `select count(*) from pg_locks where locktype = 'advisory' and not granted`).Scan(&waiting)
				if waiting > 0 {
					got = &result{}
				}
			}
		}
		if _, err := tx.Exec(ctx, `delete from property_groups where key = 'race'`); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if got == nil || got.status == 0 {
			r := <-published
			got = &r
		}
		if got.status != 409 || !strings.Contains(string(got.body), "Property group race was deleted while version") {
			t.Fatalf("publish while the group is deleted = %d %s", got.status, got.body)
		}
	})
}
