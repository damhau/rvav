package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
)

// TestGroupSecretInputs: a property group's secret input is a secret input
// like the form's own. A remote access may read its password from it, and
// no folder may read it, nor the form's own (T1.7).
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
	})
}
