package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestImageAndLinkFields: a form's image and link show and take no input.
// An address that isn't http(s) or rvav's own is refused on save, and a
// version can't be published without the address and an image's text
// alternative (F1.2).
func TestImageAndLinkFields(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		fields := map[string]api.UiField{
			"diagram": {Widget: api.UiWidgetImage, Label: "Diagram", Help: new("Where each tier runs.")},
			"sizes":   {Widget: api.UiWidgetLink, Label: "VM sizes explained", Url: new("javascript:alert(document.cookie)")},
			"role":    {Widget: api.UiWidgetText, Label: "Role"},
		}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "properties": map[string]any{"role": map[string]any{"type": "string"}}},
			UiSchema:   api.UiSchema{Fields: fields},
			Blueprint:  map[string]any{"kind": "awx", "awx": map[string]any{"title": "Create role", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Role", Fields: []string{"diagram", "sizes", "role"}}}
		create := api.CatalogItemCreate{Slug: "shown", Name: "Shown fields", Summary: "An image and a link.",
			Category: "compute", Type: api.AwxJobTemplate, Definition: &def}
		codes := func(errs []api.FieldError) []string {
			var out []string
			for _, e := range errs {
				out = append(out, e.Field+" "+*e.Code)
			}
			return out
		}

		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 422, &refused)
		if got, want := codes(refused.Errors), []string{"/definition/ui_schema/fields/sizes/url invalid"}; !slices.Equal(got, want) {
			t.Errorf("a javascript: link: %v, want %v", got, want)
		}

		fields["sizes"] = api.UiField{Widget: api.UiWidgetLink, Label: "VM sizes explained", Url: new("https://wiki.corp.example/sizes")}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 201, &item)
		refused.Errors = nil
		h.json("POST", "/api/v1/admin/catalog/items/shown/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 422, &refused)
		if got, want := codes(refused.Errors), []string{"/definition/ui_schema/fields/diagram/url required",
			"/definition/ui_schema/fields/diagram/alt required"}; !slices.Equal(got, want) {
			t.Errorf("publishing an image with no address or text alternative: %v, want %v", got, want)
		}

		var draft api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/shown/versions/"+item.DraftVersion.Id, admin, nil, 200, &draft)
		draft.Definition.UiSchema.Fields["diagram"] = api.UiField{Widget: api.UiWidgetImage, Label: "Diagram",
			Url: new("/assets/zones.png"), Alt: new("The three network zones"), Help: new("Where each tier runs.")}
		h.json("PUT", "/api/v1/admin/catalog/items/shown/versions/"+draft.Id, admin,
			api.CatalogItemVersionUpdate{Definition: draft.Definition, BaseRevision: &draft.Revision}, 200, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/shown/versions/"+draft.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/shown/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/shown/form?project_id=prj_web", requester, nil, 200, &form)
		image, link := form.UiSchema.Fields["diagram"], form.UiSchema.Fields["sizes"]
		if deref(image.Url) != "/assets/zones.png" || deref(image.Alt) != "The three network zones" || deref(link.Url) != "https://wiki.corp.example/sizes" {
			t.Errorf("the form's image %+v and link %+v", image, link)
		}
		var preview struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/requests/preview", requester, api.RequestDraft{CatalogItemId: item.Id, VersionId: draft.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "reader"}}, 200, &preview)
		if len(preview.Errors) != 0 {
			t.Errorf("a request with an image and a link: %+v", preview.Errors)
		}
	})
}
