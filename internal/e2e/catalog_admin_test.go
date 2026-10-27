package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestCatalogVersions: a catalog admin makes an item, publishes it, changes
// it into a new version, compares, rolls back and withdraws it; deployments
// keep the version they were made with, and drafts stay out of users' sight
// (S8.2, ADR-0019).
func TestCatalogVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin = "prj_web", "lmartin" // lmartin is a catalog admin (group rvav-catalog-admins)
		h.json("GET", "/api/v1/admin/catalog/items", "tnguyen", nil, 403, nil)

		// The RHEL item's definition is the starting point.
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		if rhel.PublishedVersion == nil || rhel.DraftVersion != nil || len(rhel.Versions) != 1 {
			t.Fatalf("rhel9-server = %+v", rhel)
		}
		var published api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, admin, nil, 200, &published)
		def := published.Definition

		// A new item starts as a draft numbered 1.0.
		create := api.CatalogItemCreate{Slug: "ubuntu-small", Name: "Ubuntu server", Summary: "An Ubuntu 24.04 VM.",
			Category: "compute", Type: api.VmBlueprint, Definition: &def}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 201, &item)
		if item.DraftVersion == nil || item.DraftVersion.Number != "1.0" || item.PublishedVersion != nil {
			t.Fatalf("new item = %+v", item)
		}
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 409, nil)
		draft := item.DraftVersion.Id

		// A definition is checked as a whole, with pointers to each problem.
		bad := def
		bad.UiSchema.Fields = map[string]api.UiField{}
		for k, v := range def.UiSchema.Fields {
			bad.UiSchema.Fields[k] = v
		}
		bad.UiSchema.Fields["bogus"] = api.UiField{Widget: api.UiWidgetText, Label: "Bogus"}
		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/catalog/items/ubuntu-small/versions/"+draft, admin, api.CatalogItemVersionUpdate{Definition: bad}, 422, &problem)
		fields := []string{}
		for _, e := range problem.Errors {
			fields = append(fields, e.Field)
		}
		if !slices.Contains(fields, "/definition/ui_schema/fields/bogus") {
			t.Fatalf("errors = %+v", problem.Errors)
		}

		if _, err := h.store.Pool.Exec(ctx, `insert into catalog_entitlements (item_id, project_id) values ($1, $2)`, item.Id, web); err != nil {
			t.Fatal(err)
		}

		// Published, it is requested; the deployment keeps version 1.0.
		h.json("POST", "/api/v1/admin/catalog/items/ubuntu-small/versions/"+draft+"/publish", admin,
			api.CatalogItemVersionPublish{Changelog: new("First version.")}, 200, &item)
		if item.PublishedVersion == nil || item.PublishedVersion.Id != draft || item.PublishedVersion.Changelog != "First version." {
			t.Fatalf("after publishing = %+v", item)
		}
		v1 := draft
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: v1, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)

		// Version 1.1 drops the XL size; 1.0 is deprecated, the deployment keeps it.
		var v11 api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/ubuntu-small/versions", admin, api.CatalogItemVersionCreate{}, 201, &v11)
		if v11.Number != "1.1" || v11.State != "draft" {
			t.Fatalf("second draft = %+v", v11)
		}
		h.json("POST", "/api/v1/admin/catalog/items/ubuntu-small/versions", admin, api.CatalogItemVersionCreate{}, 409, nil)
		// Drafts are for catalog admins.
		h.json("GET", "/api/v1/catalog/items/ubuntu-small/form?project_id="+web+"&version_id="+v11.Id, "tnguyen", nil, 404, nil)
		h.json("GET", "/api/v1/catalog/items/ubuntu-small/form?project_id="+web+"&version_id="+v11.Id, admin, nil, 200, nil)
		h.json("POST", "/api/v1/forms/values", "tnguyen", api.FieldValuesQuery{VersionId: v11.Id, ProjectId: web, Field: "cluster",
			Environment: new(api.EnvironmentKey("dev")), Inputs: map[string]any{}}, 404, nil)
		changed := v11.Definition
		size := changed.FormSchema["properties"].(map[string]any)["size"].(map[string]any)
		size["enum"] = []any{"S", "M", "L"}
		h.json("PUT", "/api/v1/admin/catalog/items/ubuntu-small/versions/"+v11.Id, admin,
			api.CatalogItemVersionUpdate{Definition: changed, Changelog: new("No XL.")}, 200, nil)
		h.json("POST", "/api/v1/admin/catalog/items/ubuntu-small/versions/"+v11.Id+"/publish", admin, nil, 200, &item)
		if item.PublishedVersion.Id != v11.Id || stateOf(item, v1) != "deprecated" {
			t.Fatalf("after publishing 1.1 = %+v", item)
		}
		var depVersion string
		if err := h.store.Pool.QueryRow(ctx, `select version_id from deployments where created_by_request_id = $1`, req.Id).Scan(&depVersion); err != nil || depVersion != v1 {
			t.Fatalf("the deployment's version = %s, %v", depVersion, err)
		}
		if deployments := versionOf(item, v1).Deployments; deployments != 1 {
			t.Fatalf("deployments of 1.0 = %d", deployments)
		}

		// What changed, and published versions never change.
		var diff api.CatalogVersionDiff
		h.json("GET", "/api/v1/admin/catalog/items/ubuntu-small/compare?from="+v1+"&to="+v11.Id, admin, nil, 200, &diff)
		summaries := []string{}
		for _, c := range diff.Changes {
			summaries = append(summaries, c.Summary)
		}
		if !slices.Contains(summaries, "properties/size/enum: XL removed") {
			t.Fatalf("changes = %v", summaries)
		}
		h.json("PUT", "/api/v1/admin/catalog/items/ubuntu-small/versions/"+v1, admin, api.CatalogItemVersionUpdate{Definition: def}, 409, nil)

		// Roll back: 1.0 is published again, 1.1 deprecated.
		h.json("POST", "/api/v1/admin/catalog/items/ubuntu-small/versions/"+v1+"/publish", admin, nil, 200, &item)
		if item.PublishedVersion.Id != v1 || stateOf(item, v11.Id) != "deprecated" {
			t.Fatalf("after rolling back = %+v", item)
		}
		var rolledBack int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action = 'catalog.version.rollback' and target_id = $1`, item.Id).Scan(&rolledBack)
		if rolledBack != 1 {
			t.Fatal("the rollback is not in the audit log")
		}

		// Withdrawn: no longer in the catalog; requested, it can't be deleted.
		var withdrawn api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items/ubuntu-small/versions/"+v1+"/deprecate", admin, nil, 200, &withdrawn)
		if withdrawn.PublishedVersion != nil || withdrawn.DraftVersion != nil {
			t.Fatalf("after withdrawing = %+v", withdrawn)
		}
		h.json("GET", "/api/v1/catalog/items/ubuntu-small/form?project_id="+web, "tnguyen", nil, 404, nil)
		h.json("DELETE", "/api/v1/admin/catalog/items/ubuntu-small", admin, nil, 409, nil)

		// An item nobody requested can be deleted; a draft discarded.
		var spare api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "spare", Name: "Spare", Summary: "Not needed.",
			Category: "compute", Type: api.AwxJobTemplate}, 201, &spare)
		if spare.DraftVersion == nil || spare.Icon != "terminal" {
			t.Fatalf("spare = %+v", spare)
		}
		h.json("DELETE", "/api/v1/admin/catalog/items/spare/versions/"+spare.DraftVersion.Id, admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/catalog/items/spare", admin, nil, 204, nil)

		// Once an admin has made a version of a seeded item, the seed leaves it alone.
		var rhelDraft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions", admin, api.CatalogItemVersionCreate{Changelog: new("Mine.")}, 201, &rhelDraft)
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhelDraft.Id+"/publish", admin, nil, 200, nil)
		if err := seed.Run(ctx, h.store); err != nil {
			t.Fatal(err)
		}
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		if rhel.PublishedVersion == nil || rhel.PublishedVersion.Id != rhelDraft.Id || !strings.HasPrefix(rhel.PublishedVersion.Number, "3.") {
			t.Fatalf("rhel9-server after seeding again = %+v", rhel.PublishedVersion)
		}
	})
}

func versionOf(it api.AdminCatalogItem, id string) api.CatalogItemVersion {
	for _, v := range it.Versions {
		if v.Id == id {
			return v
		}
	}
	return api.CatalogItemVersion{}
}

func stateOf(it api.AdminCatalogItem, id string) string { return string(versionOf(it, id).State) }
