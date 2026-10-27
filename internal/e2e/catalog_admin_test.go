package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
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

// rawYAML is a request body sent as it is (the harness sends others as JSON).
type rawYAML string

func versionOf(it api.AdminCatalogItem, id string) api.CatalogItemVersion {
	for _, v := range it.Versions {
		if v.Id == id {
			return v
		}
	}
	return api.CatalogItemVersion{}
}

func stateOf(it api.AdminCatalogItem, id string) string { return string(versionOf(it, id).State) }

// TestCatalogEntitlementsCategoriesYAML: who may request an item,
// categories and icons, and items exported to YAML and imported back
// (S8.3).
func TestCatalogEntitlementsCategoriesYAML(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "lmartin"
		inCatalog := func(project, slug string) bool {
			t.Helper()
			var page api.CatalogItemPage
			h.json("GET", "/api/v1/catalog/items?limit=200&project_id="+project, "mkeller", nil, 200, &page)
			return slices.ContainsFunc(page.Items, func(i api.CatalogItem) bool { return i.Slug == slug })
		}

		// Entitlements: no project, chosen projects, every project.
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		var published api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, admin, nil, 200, &published)
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "linux-small", Name: "Small Linux VM",
			Summary: "A small VM.", Category: "compute", Type: api.VmBlueprint, Icon: new("cpu"), Definition: &published.Definition}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/linux-small/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		if inCatalog("prj_web", "linux-small") {
			t.Fatal("open to a project before any entitlement")
		}
		set := func(e api.CatalogEntitlements, want int) {
			t.Helper()
			h.json("PUT", "/api/v1/admin/catalog/items/linux-small/entitlements", admin, e, want, nil)
		}
		set(api.CatalogEntitlements{ProjectIds: []string{"prj_nope"}}, 422)
		set(api.CatalogEntitlements{ProjectIds: []string{"prj_web"}}, 200)
		if !inCatalog("prj_web", "linux-small") || inCatalog("prj_data", "linux-small") {
			t.Fatal("open to the wrong projects")
		}
		set(api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200)
		if !inCatalog("prj_data", "linux-small") {
			t.Fatal("not open to every project")
		}

		// Categories and icons.
		var icons struct{ Items []string }
		h.json("GET", "/api/v1/admin/catalog/icons", admin, nil, 200, &icons)
		if !slices.Contains(icons.Items, "server") || !slices.Contains(icons.Items, "cpu") {
			t.Fatalf("icons = %v", icons.Items)
		}
		h.json("PATCH", "/api/v1/admin/catalog/items/linux-small", admin, api.CatalogItemUpdate{Icon: new("upload:evil")}, 422, nil)
		h.json("POST", "/api/v1/admin/catalog/categories", admin, api.AdminCatalogCategory{Key: "storage", Name: "Storage"}, 201, nil)
		h.json("POST", "/api/v1/admin/catalog/categories", admin, api.AdminCatalogCategory{Key: "storage", Name: "Again"}, 409, nil)
		h.json("PATCH", "/api/v1/admin/catalog/categories/storage", admin, api.AdminCatalogCategoryUpdate{Name: new("Storage and backup")}, 200, nil)
		h.json("DELETE", "/api/v1/admin/catalog/categories/compute", admin, nil, 409, nil)
		h.json("DELETE", "/api/v1/admin/catalog/categories/storage", admin, nil, 204, nil)

		// YAML: exported, imported as a new item, exported again: the same definition.
		status, body, headers := h.do("GET", "/api/v1/admin/catalog/items/rhel9-server/export", admin, nil)
		if status != 200 || !strings.HasPrefix(headers.Get("Content-Type"), "application/yaml") || !strings.Contains(string(body), "apiVersion: rvav.dev/v1") {
			t.Fatalf("export = %d %s %s", status, headers.Get("Content-Type"), body)
		}
		copyYAML := strings.Replace(string(body), "slug: rhel9-server", "slug: rhel9-copy", 1)
		var imported api.CatalogImportResult
		h.json("POST", "/api/v1/admin/catalog/import", admin, rawYAML(copyYAML), 200, &imported, "Content-Type", "application/yaml")
		if !imported.Created || len(imported.Versions) != 1 || imported.Versions[0].State != "draft" {
			t.Fatalf("imported = %+v", imported)
		}
		var copied api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-copy/versions/"+imported.Versions[0].Id, admin, nil, 200, &copied)
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-copy/versions/"+copied.Id+"/publish", admin, nil, 200, nil)
		_, again, _ := h.do("GET", "/api/v1/admin/catalog/items/rhel9-copy/export", admin, nil)
		if strings.Replace(string(again), "slug: rhel9-copy", "slug: rhel9-server", 1) != string(body) {
			t.Fatalf("a round trip changed the item:\n%s\n---\n%s", body, again)
		}

		// The slug is taken; into the item, the file becomes a draft once.
		h.json("POST", "/api/v1/admin/catalog/import", admin, rawYAML(copyYAML), 409, nil, "Content-Type", "application/yaml")
		h.json("POST", "/api/v1/admin/catalog/import?item_id=rhel9-copy", admin, rawYAML(copyYAML), 200, &imported, "Content-Type", "application/yaml")
		if imported.Created || len(imported.Versions) != 2 || imported.Versions[0].State != "draft" || imported.Versions[0].Number == imported.Versions[1].Number {
			t.Fatalf("imported into the item = %+v", imported)
		}
		h.json("POST", "/api/v1/admin/catalog/import?item_id=rhel9-copy", admin, rawYAML(copyYAML), 409, nil, "Content-Type", "application/yaml")

		// A problem names its line.
		broken := strings.Replace(copyYAML, "slug: rhel9-copy", "slug: rhel9-broken", 1)
		broken = strings.Replace(broken, "label: Size\n", "label: \"\"\n", 1)
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/import", admin, rawYAML(broken), 422, &problem, "Content-Type", "application/yaml")
		if len(problem.Errors) != 1 || !strings.HasSuffix(problem.Errors[0].Field, "/label") || !strings.Contains(problem.Errors[0].Message, "(line ") {
			t.Fatalf("errors = %+v", problem.Errors)
		}
		h.json("POST", "/api/v1/admin/catalog/import", "tnguyen", rawYAML(copyYAML), 403, nil, "Content-Type", "application/yaml")
	})
}

// TestValueSourcesRESTAndCEL: a dropdown from a CMDB's REST API, with a
// stored secret, only on allowed hosts, cached; and options and values
// from CEL (S8.4).
func TestValueSourcesRESTAndCEL(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin = "prj_web", "lmartin"
		var calls atomic.Int32
		cmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Header.Get("Authorization") != "Bearer cmdb-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			app := r.URL.Query().Get("app")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data": [{"code": "%s-01", "title": "%s main", "env": "dev"}, {"code": "%s-02", "title": "%s spare", "env": "tst"}]}`, app, app, app, app)
		}))
		defer cmdb.Close()
		host := strings.TrimPrefix(cmdb.URL, "http://")

		// Secrets: stored, listed without their value.
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb", Kind: api.SecretWriteKindToken, Value: new("cmdb-token")}, 201, nil)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb", Kind: api.SecretWriteKindToken, Value: new("x")}, 409, nil)
		_, listed, _ := h.do("GET", "/api/v1/admin/secrets", admin, nil)
		if strings.Contains(string(listed), "cmdb-token") || !strings.Contains(string(listed), `"name":"cmdb"`) {
			t.Fatalf("secrets = %s", listed)
		}
		h.json("GET", "/api/v1/admin/secrets", "tnguyen", nil, 403, nil)

		// An item with a REST dropdown and CEL options; the host must be allowed.
		rest := map[string]any{"url": cmdb.URL + "/apps?app=${inputs.app}", "secret": "cmdb", "items": "$.data", "value": "$.code",
			"label": "$.title", "meta": "$.env", "cache_seconds": float64(60)}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "properties": map[string]any{"app": map[string]any{"type": "string"},
				"service": map[string]any{"type": "string"}, "tier": map[string]any{"type": "string"}}},
			UiSchema: api.UiSchema{Fields: map[string]api.UiField{
				"app":     {Widget: api.UiWidgetText, Label: "Application"},
				"service": {Widget: api.UiWidgetSelect, Label: "Service", DependsOn: &[]string{"app"}, ValueSource: &api.ValueSource{Kind: api.Rest, Params: &rest}},
				"tier": {Widget: api.UiWidgetSelect, Label: "Tier", ValueSource: &api.ValueSource{Kind: api.Cel,
					Params: &map[string]any{"options": `environment == "prd" ? ["gold"] : ["bronze", "silver"]`, "value": `inputs.app + "-" + environment`}}},
			}},
			Blueprint: map[string]any{"kind": "awx", "awx": map[string]any{"title": "Register", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Service", Fields: []string{"app", "service", "tier"}}}
		create := api.CatalogItemCreate{Slug: "service-register", Name: "Register a service", Summary: "From the CMDB.",
			Category: "compute", Type: api.AwxJobTemplate, Definition: &def}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/definition/ui_schema/fields/service/value_source/params/url" {
			t.Fatalf("a host not allowed = %+v", problem.Errors)
		}
		h.svc.REST.Hosts = []string{host}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 201, &item)
		h.json("DELETE", "/api/v1/admin/secrets/cmdb", admin, nil, 409, nil) // in use
		h.json("POST", "/api/v1/admin/catalog/items/service-register/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/service-register/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		values := func(field string, inputs map[string]any) api.FieldValues {
			t.Helper()
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", "tnguyen", api.FieldValuesQuery{VersionId: item.DraftVersion.Id, ProjectId: web,
				Environment: new(api.EnvironmentKey("dev")), Field: field, Inputs: inputs}, 200, &out)
			return out
		}
		got := values("service", map[string]any{"app": "pay roll"})
		if got.Options == nil || len(*got.Options) != 2 || (*got.Options)[0].Value != "pay roll-01" || (*got.Options)[0].Label != "pay roll main" ||
			deref((*got.Options)[0].Meta) != "dev" {
			t.Fatalf("REST options = %+v", got.Options)
		}
		values("service", map[string]any{"app": "pay roll"})
		if calls.Load() != 1 {
			t.Fatalf("the CMDB was called %d times; the answer should be cached", calls.Load())
		}
		tier := values("tier", map[string]any{"app": "web"})
		if tier.Options == nil || len(*tier.Options) != 2 || (*tier.Options)[1].Value != "silver" || tier.Value != "web-dev" {
			t.Fatalf("CEL options = %+v, value %v", tier.Options, tier.Value)
		}

		// A new secret value is used; a CMDB that refuses is an upstream error.
		h.json("PUT", "/api/v1/admin/secrets/cmdb", admin, api.SecretWrite{Name: "cmdb", Kind: api.SecretWriteKindToken, Value: new("wrong")}, 200, nil)
		h.json("POST", "/api/v1/forms/values", "tnguyen", api.FieldValuesQuery{VersionId: item.DraftVersion.Id, ProjectId: web,
			Environment: new(api.EnvironmentKey("dev")), Field: "service", Inputs: map[string]any{"app": "billing"}}, 502, nil)
	})
}

// TestFormRulesOnServer: the preview carries each field's rule results, a
// computed value is an input, and a field a rule makes required is checked
// on submit (S8.5).
func TestFormRulesOnServer(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin = "prj_web", "lmartin"
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "properties": map[string]any{
				"name": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}, "label": map[string]any{"type": "string"}}},
			UiSchema: api.UiSchema{Fields: map[string]api.UiField{
				"name":   {Widget: api.UiWidgetText, Label: "Name"},
				"reason": {Widget: api.UiWidgetText, Label: "Why production", Rules: &api.UiRules{Visible: new(`environment == "prd"`), Required: new(`environment == "prd"`)}},
				"label":  {Widget: api.UiWidgetText, Label: "Label", Rules: &api.UiRules{Value: new(`project.code + "-" + inputs.name`)}},
			}},
			Blueprint: map[string]any{"kind": "awx", "awx": map[string]any{"title": "Note", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Note", Fields: []string{"name", "reason", "label"}}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "note", Name: "Note", Summary: "A note.",
			Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/note/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/note/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		preview := func(env string) api.RequestPreview {
			t.Helper()
			var out api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: web, Environment: env, Inputs: map[string]any{"name": "orders"}}, 200, &out)
			return out
		}
		dev := preview("dev")
		if dev.Fields == nil || (*dev.Fields)["reason"].Visible || !(*dev.Fields)["label"].ReadOnly || (*dev.Fields)["label"].Value != "web-orders" || !dev.Valid {
			t.Fatalf("dev preview = %+v", dev)
		}
		prd := preview("prd")
		reason := (*prd.Fields)["reason"]
		if !reason.Visible || !reason.Required || prd.Valid || !slices.ContainsFunc(prd.Errors, func(e api.FieldError) bool { return e.Field == "/inputs/reason" }) {
			t.Fatalf("prd preview = %+v", prd)
		}
	})
}
