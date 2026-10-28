package e2e

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestRequestDrafts: a request saved to finish later is its author's only,
// kept without the values of secret fields, resumed, saved again and
// discarded (S11.6).
func TestRequestDrafts(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		// An item with a secret field.
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", "mkeller", nil, 200, &rhel)
		var published api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, "mkeller", nil, 200, &published)
		def := published.Definition
		def.FormSchema["properties"].(map[string]any)["root_password"] = map[string]any{"type": "string"}
		def.UiSchema.Fields["root_password"] = api.UiField{Widget: api.UiWidgetSecret, Label: "Root password"}
		def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "root_password")
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", "mkeller", api.CatalogItemCreate{Slug: "rhel-secret", Name: "RHEL with a password",
			Summary: "A VM and its root password.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)

		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "app", "size": "M", "root_password": "hunter2"}, Count: new(3),
			Justification: new("Load test next week")}
		var saved api.SavedDraft
		h.json("POST", "/api/v1/me/drafts", "tnguyen", api.SavedDraftWrite{Draft: draft}, 201, &saved)
		if saved.Name != "RHEL with a password" || saved.CatalogItem.Slug != "rhel-secret" || saved.Project.Code != "web" ||
			saved.Draft.Inputs["role"] != "app" || saved.Draft.Count == nil || *saved.Draft.Count != 3 {
			t.Fatalf("saved = %+v", saved)
		}
		if _, kept := saved.Draft.Inputs["root_password"]; kept {
			t.Fatal("a secret was kept in a draft")
		}

		// Only its author's.
		h.json("GET", "/api/v1/me/drafts/"+saved.Id, "lmartin", nil, 404, nil)
		h.json("PUT", "/api/v1/me/drafts/"+saved.Id, "lmartin", api.SavedDraftWrite{Draft: draft}, 404, nil)
		h.json("DELETE", "/api/v1/me/drafts/"+saved.Id, "lmartin", nil, 404, nil)
		var theirs api.SavedDraftList
		h.json("GET", "/api/v1/me/drafts", "lmartin", nil, 200, &theirs)
		if len(theirs.Items) != 0 {
			t.Fatalf("someone else's drafts = %+v", theirs.Items)
		}

		// Resumed, saved again under a name; checked only for what it names.
		draft.Inputs["size"] = "L"
		h.json("PUT", "/api/v1/me/drafts/"+saved.Id, "tnguyen", api.SavedDraftWrite{Name: new("Load test VMs"), Draft: draft}, 200, &saved)
		var list api.SavedDraftList
		h.json("GET", "/api/v1/me/drafts", "tnguyen", nil, 200, &list)
		if len(list.Items) != 1 || list.Items[0].Name != "Load test VMs" || list.Items[0].Draft.Inputs["size"] != "L" {
			t.Fatalf("drafts = %+v", list.Items)
		}
		bad := draft
		bad.CatalogItemId, bad.ProjectId = "itm_nope", "prj_nope"
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/me/drafts", "tnguyen", api.SavedDraftWrite{Draft: bad}, 422, &problem)
		if len(problem.Errors) != 3 {
			t.Fatalf("a draft of nothing = %+v", problem.Errors)
		}
		h.json("DELETE", "/api/v1/me/drafts/"+saved.Id, "tnguyen", nil, 204, nil)
		h.json("GET", "/api/v1/me/drafts/"+saved.Id, "tnguyen", nil, 404, nil)
	})
}
