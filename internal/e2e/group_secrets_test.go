package e2e

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/present"
)

// TestGroupSecretInputs: a property group's secret input is a secret input
// like the form's own. A remote access may read its password from it, no
// folder may read it, nor the form's own, and it never reaches the events
// or the AWX inventory, where the group's other inputs go as the form's
// (T1.7).
func TestGroupSecretInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		yes := true
		h.json("POST", "/api/v1/admin/property-groups", admin, api.PropertyGroupWrite{Key: present.Ptr("access"), Name: "Access",
			Inputs:    []api.PropertyGroupInput{{Name: "pw", Label: "Password", Type: api.PropertyGroupInputTypeString, Required: &yes, Secret: &yes}},
			Constants: map[string]any{}}, 201, nil)
		item := func(guest map[string]any, folder string) api.CatalogVersionDefinition {
			def := guestItem("tpl-rhel9.4-2026.10", guest)
			def.FormSchema["properties"].(map[string]any)["token"] = map[string]any{"type": "string"}
			def.UiSchema.Fields["token"] = api.UiField{Widget: api.UiWidgetSecret, Label: "Token"}
			def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "token")
			def.Blueprint["property_groups"] = []any{map[string]any{"key": "access", "version": 1}}
			if folder != "" {
				def.Blueprint["folder"] = folder
			}
			return def
		}
		linux := map[string]any{"os_family": "linux", "domain": "corp.example",
			"remote_access": map[string]any{"kind": "password", "username": "ops", "password": "${inputs.pw}"}}
		publishItem(h, "grp-access", item(linux, ""))

		var problem struct{ Errors []api.FieldError }
		for folder, read := range map[string]string{"apps/${inputs.pw}": "pw", "apps/${inputs.token.lowerAscii()}": "token",
			"apps/${inputs['pw']}": "pw, token"} {
			def := item(map[string]any{"os_family": "linux", "domain": "corp.example"}, folder)
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "grp-folder", Name: "f", Summary: "f",
				Category: "compute", Type: api.VmBlueprint, Definition: &def}, 422, &problem)
			if !slices.Equal(fieldsOf(problem.Errors), []string{"/definition/blueprint/folder"}) ||
				!strings.Contains(problem.Errors[0].Message, "reads secret input "+read+":") {
				t.Errorf("folder %s: %+v", folder, problem.Errors)
			}
		}
		def := item(map[string]any{"os_family": "linux", "domain": "corp.example"}, "")
		def.Blueprint["components"] = []any{map[string]any{"key": "web", "folder": "${inputs.token}"}}
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "grp-folder", Name: "f", Summary: "f",
			Category: "compute", Type: api.VmBlueprint, Definition: &def}, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/definition/blueprint/components/0/folder") {
			t.Errorf("a component's folder: %+v", problem.Errors)
		}

		// What reaches AWX and the events: a group's inputs as the form's,
		// its secret one never; the inventory has those it exposes.
		h.json("PUT", "/api/v1/admin/property-groups/access", admin, api.PropertyGroupWrite{Name: "Access", Inputs: []api.PropertyGroupInput{
			{Name: "pw", Label: "Password", Type: api.PropertyGroupInputTypeString, Required: &yes, Secret: &yes},
			{Name: "site", Label: "Site", Type: api.PropertyGroupInputTypeString, ExposeToInventory: &yes},
			{Name: "note", Label: "Note", Type: api.PropertyGroupInputTypeString},
		}, Constants: map[string]any{}}, 200, nil)
		def = item(map[string]any{"os_family": "linux", "domain": "corp.example"}, "")
		def.Blueprint["property_groups"] = []any{map[string]any{"key": "access", "version": 2}}
		awxItem := publishItem(h, "grp-awx", def)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: awxItem.Id, VersionId: awxItem.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "grp", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "token": "own-secret", "pw": "group-secret",
				"site": "gva", "note": "n"}}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		data, err := outbox.RequestData(ctx, h.store.Pool, req.Id)
		if err != nil {
			t.Fatal(err)
		}
		if in := data["inputs"].(map[string]any); in["site"] != "gva" || in["note"] != "n" || in["pw"] != nil || in["token"] != nil {
			t.Errorf("the events' inputs = %v", in)
		}
		var inv struct {
			Meta struct {
				Hostvars map[string]map[string]any `json:"hostvars"`
			} `json:"_meta"`
		}
		h.json("GET", "/api/v1/ansible/inventory", admin, nil, 200, &inv)
		if got := inv.Meta.Hostvars["web-dev-grp01"]["rvav_inputs"]; !reflect.DeepEqual(got, map[string]any{"site": "gva"}) {
			t.Errorf("the inventory's inputs = %v", got)
		}
	})
}
