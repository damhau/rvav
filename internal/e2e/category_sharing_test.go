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
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/trust"
)

// TestCategorySharing: a category shared with Data Engineering shares the
// items added or moved into it later, with that project's members and
// nobody else's; ServiceNow is offered them, the move is audited, a role
// the category names can't be deleted, and removing the share takes them
// back (G2.2, ADR-0019).
func TestCategorySharing(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, web, data = "lmartin", "prj_web", "prj_data"
		h.json("POST", "/api/v1/admin/catalog/categories", admin, api.AdminCatalogCategory{Key: "restricted", Name: "Restricted"}, 201, nil)
		h.json("PUT", "/api/v1/admin/catalog/categories/restricted/entitlements", "tnguyen", api.CatalogEntitlements{ProjectIds: []string{data}}, 403, nil)
		h.json("PUT", "/api/v1/admin/catalog/categories/nope/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{data}}, 404, nil)
		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/catalog/categories/restricted/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{},
			Audiences: &[]api.CatalogProjectAudience{{ProjectId: web, UserIds: []string{}, Groups: []string{"g"}, Roles: []string{}}}}, 422, &problem)
		if len(problem.Errors) != 1 || !strings.HasPrefix(problem.Errors[0].Message, "The category isn't shared with") {
			t.Fatalf("an audience outside the category's projects = %+v", problem.Errors)
		}
		var shared api.CatalogEntitlements
		h.json("PUT", "/api/v1/admin/catalog/categories/restricted/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{data}}, 200, &shared)
		if shared.AllProjects || !slices.Equal(shared.ProjectIds, []string{data}) || shared.Inherited != nil {
			t.Fatalf("the category's sharing = %+v", shared)
		}
		var cats api.AdminCatalogCategoryList
		h.json("GET", "/api/v1/admin/catalog/categories", admin, nil, 200, &cats)
		if i := slices.IndexFunc(cats.Items, func(c api.AdminCatalogCategory) bool { return c.Key == "restricted" }); i < 0 ||
			cats.Items[i].Sharing == nil || cats.Items[i].Sharing.AllProjects || cats.Items[i].Sharing.Projects != 1 {
			t.Fatalf("the categories = %+v", cats.Items)
		}
		var target struct{ Type, Name string }
		if err := h.store.Pool.QueryRow(ctx, `select target_type, target_name from audit_events
			where action = 'catalog.category.entitlements' and target_id = 'restricted'`).Scan(&target.Type, &target.Name); err != nil ||
			target.Type != "catalog_category" || target.Name != "Restricted" {
			t.Fatalf("the share's audit = %+v, %v", target, err)
		}

		// An item made in the category afterwards, with no share of its own.
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		var published api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, admin, nil, 200, &published)
		publish := func(slug, category string) (string, string) {
			t.Helper()
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: "Restricted VM " + slug,
				Summary: "A VM for some.", Category: category, Type: api.VmBlueprint, Definition: &published.Definition}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			return item.Id, item.DraftVersion.Id
		}
		laterID, laterVersion := publish("later-vm", "restricted")
		listed := func(user, project, slug string) bool {
			t.Helper()
			var page api.CatalogItemPage
			h.json("GET", "/api/v1/catalog/items?limit=200&q=restricted&project_id="+project, user, nil, 200, &page)
			return slices.ContainsFunc(page.Items, func(i api.CatalogItem) bool { return i.Slug == slug })
		}
		draft := func(itemID, versionID, project string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: itemID, VersionId: versionID, ProjectId: project, Environment: "dev",
				Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
					"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		}
		if !listed("sbrunner", data, "later-vm") || listed("tnguyen", web, "later-vm") {
			t.Fatal("the category's share doesn't reach its later item, or reaches others")
		}
		h.json("GET", "/api/v1/catalog/items/later-vm?project_id="+web, "tnguyen", nil, 404, nil)
		h.json("POST", "/api/v1/requests", "tnguyen", draft(laterID, laterVersion, web), 404, nil, "Idempotency-Key", uuid.NewString())
		h.json("POST", "/api/v1/requests", "sbrunner", draft(laterID, laterVersion, data), 202, nil, "Idempotency-Key", uuid.NewString())

		// The item's sharing says where its share comes from.
		var ent api.CatalogEntitlements
		h.json("GET", "/api/v1/admin/catalog/items/later-vm/entitlements", admin, nil, 200, &ent)
		if ent.AllProjects || len(ent.ProjectIds) != 0 || ent.Inherited == nil || len(*ent.Inherited) != 1 {
			t.Fatalf("the item's sharing = %+v", ent)
		}
		if ih := (*ent.Inherited)[0]; ih.From != (api.CatalogShareOrigin{Kind: api.CatalogShareOriginKindCategory, Key: "restricted", Name: "Restricted"}) ||
			ih.AllProjects || ih.ProjectId == nil || *ih.ProjectId != data || len(ih.UserIds)+len(ih.Groups)+len(ih.Roles) != 0 {
			t.Fatalf("the inherited share = %+v", ih)
		}
		// Sharing the item with Alain alone in Data still lets Sophie request it there, through its category.
		var who api.CatalogRequesters
		h.json("POST", "/api/v1/admin/catalog/items/later-vm/entitlements/requesters", admin, api.CatalogProjectAudience{ProjectId: data,
			UserIds: []string{seed.UserID("afavre")}, Groups: []string{}, Roles: []string{}}, 200, &who)
		sophie := slices.IndexFunc(who.People, func(p api.CatalogRequester) bool { return p.User.Id == seed.UserID("sbrunner") })
		if sophie < 0 || who.People[sophie].Named || len(who.People[sophie].Inherited) != 1 || who.People[sophie].Inherited[0].Key != "restricted" {
			t.Fatalf("Data's requesters = %+v", who)
		}

		// ServiceNow is offered it, for Sophie in Data.
		snow, srv := snowInstance(t)
		h.svc.Config.ServiceNow = config.ServiceNowConfig{URL: srv.URL, Auth: servicenow.AuthBasic, Username: snow.Username,
			Secret: snow.Password, CertSHA256: trust.Fingerprint(srv.Certificate()), RequestsPerMinute: 30}
		var made api.ServiceNowTokenMade
		h.json("POST", "/api/v1/admin/integrations/servicenow/token", "mkeller", nil, 201, &made)
		token := []string{"Authorization", "Bearer " + made.Secret}
		var cat api.ServiceNowCatalog
		h.json("GET", "/api/v1/servicenow/catalog", "", nil, 200, &cat, token...)
		if !slices.ContainsFunc(cat.Items, func(it api.ServiceNowCatalogItem) bool { return it.Id == laterID }) {
			t.Fatalf("ServiceNow isn't offered the item shared by its category: %+v", cat.Items)
		}
		var snowEnt api.ServiceNowEntitlements
		h.json("GET", "/api/v1/servicenow/entitlements?user=sophie.brunner@corp.example", "", nil, 200, &snowEnt, token...)
		if !slices.ContainsFunc(snowEnt.Projects, func(p api.ServiceNowEntitlement) bool {
			return p.Project.Id == data && slices.Contains(p.Items, laterID)
		}) {
			t.Fatalf("Sophie's ServiceNow entitlements = %+v", snowEnt)
		}

		// An item moved into the category is shared as it is, and the move is audited.
		movedID, movedVersion := publish("moved-vm", "compute")
		if listed("sbrunner", data, "moved-vm") {
			t.Fatal("an item shared nowhere is listed")
		}
		h.json("PATCH", "/api/v1/admin/catalog/items/moved-vm", admin, api.CatalogItemUpdate{Category: new("restricted")}, 200, nil)
		if !listed("sbrunner", data, "moved-vm") {
			t.Fatal("the moved item isn't shared as its new category")
		}
		h.json("POST", "/api/v1/requests/preview", "sbrunner", draft(movedID, movedVersion, data), 200, nil)
		var from, to string
		if err := h.store.Pool.QueryRow(ctx, `select detail->>'from', detail->>'to' from audit_events
			where action = 'catalog.item.category' and target_id = $1`, movedID).Scan(&from, &to); err != nil || from != "compute" || to != "restricted" {
			t.Fatalf("the move's audit: %s → %s, %v", from, to, err)
		}

		// A role the category's sharing names can't be deleted.
		h.json("POST", "/api/v1/admin/roles", "mkeller", api.RoleCreate{Key: "ordering", Name: "Ordering",
			Permissions: []string{"project.see", "request.submit"}}, 201, nil)
		h.json("PUT", "/api/v1/admin/catalog/categories/restricted/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{data},
			Audiences: &[]api.CatalogProjectAudience{{ProjectId: data, UserIds: []string{}, Groups: []string{}, Roles: []string{"ordering"}}}}, 200, nil)
		var conflict struct{ Detail string }
		h.json("DELETE", "/api/v1/admin/roles/ordering", "mkeller", nil, 409, &conflict)
		if !strings.Contains(conflict.Detail, "The categories Restricted") {
			t.Fatalf("the role's deletion = %q", conflict.Detail)
		}
		if listed("sbrunner", data, "later-vm") {
			t.Fatal("a category shared with a role Sophie doesn't hold still reaches her")
		}

		// Removing the share takes the items back.
		h.json("PUT", "/api/v1/admin/catalog/categories/restricted/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{}}, 200, nil)
		if listed("sbrunner", data, "later-vm") || listed("sbrunner", data, "moved-vm") {
			t.Fatal("the items stay shared once their category's share is removed")
		}
		h.json("POST", "/api/v1/requests", "sbrunner", draft(laterID, laterVersion, data), 404, nil, "Idempotency-Key", uuid.NewString())
		h.json("GET", "/api/v1/servicenow/catalog", "", nil, 200, &cat, token...)
		if slices.ContainsFunc(cat.Items, func(it api.ServiceNowCatalogItem) bool { return it.Id == laterID }) {
			t.Fatal("ServiceNow is still offered an item shared nowhere")
		}
		h.json("DELETE", "/api/v1/admin/roles/ordering", "mkeller", nil, 204, nil)
	})
}

// TestAWXSourceSharing: the AWX source is shared with every project, as
// the sync shared each item it made before; shared with Data Engineering
// only, the item a sync makes afterwards is for Data's members and nobody
// else's, its sharing says it comes from AWX, and removing the share takes
// it back (G2.2, ADR-0019).
func TestAWXSourceSharing(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, web, data = "mkeller", "prj_web", "prj_data"
		var src api.CatalogEntitlements
		h.json("GET", "/api/v1/admin/awx/entitlements", admin, nil, 200, &src)
		if !src.AllProjects || len(src.ProjectIds) != 0 || src.Inherited != nil {
			t.Fatalf("the AWX source's sharing = %+v", src)
		}
		h.json("PUT", "/api/v1/admin/awx/entitlements", "tnguyen", api.CatalogEntitlements{ProjectIds: []string{data}}, 403, nil)
		h.json("PUT", "/api/v1/admin/awx/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{data}}, 200, &src)
		if src.AllProjects || !slices.Equal(src.ProjectIds, []string{data}) {
			t.Fatalf("the AWX source's sharing = %+v", src)
		}

		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log, Filter: awx.TemplateFilter{Labels: []string{"rvav"}}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", admin, nil, 200, nil)
		h.json("GET", "/api/v1/catalog/items/monitoring-agent?project_id="+web, "tnguyen", nil, 404, nil)
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/monitoring-agent?project_id="+data, "sbrunner", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/monitoring-agent/form?project_id="+data, "sbrunner", nil, 200, &form)
		draft := func(project string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: project, Environment: "dev",
				Inputs: map[string]any{"monitoring_agent": "telegraf", "listen_port": 9273}}
		}
		h.json("POST", "/api/v1/requests", "tnguyen", draft(web), 404, nil, "Idempotency-Key", uuid.NewString())
		h.json("POST", "/api/v1/requests", "sbrunner", draft(data), 202, nil, "Idempotency-Key", uuid.NewString())

		var ent api.CatalogEntitlements
		h.json("GET", "/api/v1/admin/catalog/items/monitoring-agent/entitlements", admin, nil, 200, &ent)
		if ent.AllProjects || len(ent.ProjectIds) != 0 || ent.Inherited == nil || len(*ent.Inherited) != 1 ||
			(*ent.Inherited)[0].From != (api.CatalogShareOrigin{Kind: api.CatalogShareOriginKindSource, Key: "awx", Name: "AWX"}) {
			t.Fatalf("the imported item's sharing = %+v", ent)
		}
		// The seeded AWX items keep the shares they have of their own.
		h.json("GET", "/api/v1/catalog/items/dns-record?project_id="+web, "tnguyen", nil, 200, nil)

		var who api.CatalogRequesters
		h.json("POST", "/api/v1/admin/awx/entitlements/requesters", admin, api.CatalogProjectAudience{ProjectId: data,
			UserIds: []string{}, Groups: []string{"rvav-nobody"}, Roles: []string{}}, 200, &who)
		if slices.ContainsFunc(who.People, func(p api.CatalogRequester) bool { return p.User.Id == seed.UserID("sbrunner") }) ||
			!slices.ContainsFunc(who.LeftOut, func(u api.UserRef) bool { return u.Id == seed.UserID("sbrunner") }) {
			t.Fatalf("Data's requesters for a group nobody is in = %+v", who)
		}

		h.json("PUT", "/api/v1/admin/awx/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{}}, 200, nil)
		h.json("GET", "/api/v1/catalog/items/monitoring-agent?project_id="+data, "sbrunner", nil, 404, nil)
		h.json("POST", "/api/v1/requests", "sbrunner", draft(data), 404, nil, "Idempotency-Key", uuid.NewString())
		var kind string
		if err := h.store.Pool.QueryRow(ctx, `select target_type from audit_events where action = 'catalog.source.entitlements'
			and target_id = 'awx' order by id desc limit 1`).Scan(&kind); err != nil || kind != "catalog_source" {
			t.Fatalf("the share's audit: %q, %v", kind, err)
		}

		h.json("PUT", "/api/v1/admin/awx/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		h.json("GET", "/api/v1/catalog/items/monitoring-agent?project_id="+web, "tnguyen", nil, 200, nil)
	})
}
