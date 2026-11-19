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

// TestItemSharing: an item shared in Web Platform with a user, a group and
// a role, and with all of Data Engineering's members; the catalog, the
// item, its form and a request agree for each person, a role named can't
// be deleted, and a draft stays when sharing narrows (G1.2, ADR-0019).
func TestItemSharing(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, web, data = "lmartin", "prj_web", "prj_data"
		// Alain is named; Claire, a member too, is left out.
		for _, u := range []string{"afavre", "cdubois"} {
			if err := h.store.SetMemberRoles(ctx, web, seed.UserID(u), []string{"member"}); err != nil {
				t.Fatal(err)
			}
		}

		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		var published api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, admin, nil, 200, &published)
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "linux-shared", Name: "Shared Linux VM",
			Summary: "A VM for some.", Category: "compute", Type: api.VmBlueprint, Definition: &published.Definition}, 201, &item)
		version := item.DraftVersion.Id
		h.json("POST", "/api/v1/admin/catalog/items/linux-shared/versions/"+version+"/publish", admin, nil, 200, nil)

		// The seed's items are shared as they were: with all members.
		var seeded api.CatalogEntitlements
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/entitlements", admin, nil, 200, &seeded)
		if seeded.Audiences == nil || len(*seeded.Audiences) != 0 || seeded.Everywhere == nil || len(seeded.Everywhere.UserIds)+
			len(seeded.Everywhere.Groups)+len(seeded.Everywhere.Roles) != 0 {
			t.Fatalf("rhel9-server's sharing = %+v", seeded)
		}

		webAudience := api.CatalogProjectAudience{ProjectId: web, UserIds: []string{seed.UserID("afavre")},
			Groups: []string{"rvav-web"}, Roles: []string{"project-admin"}}
		set := func(e api.CatalogEntitlements, want int) api.CatalogEntitlements {
			t.Helper()
			var out api.CatalogEntitlements
			h.json("PUT", "/api/v1/admin/catalog/items/linux-shared/entitlements", admin, e, want, &out)
			return out
		}
		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/catalog/items/linux-shared/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{data},
			Audiences: &[]api.CatalogProjectAudience{webAudience, {ProjectId: data, UserIds: []string{"usr_nobody"},
				Groups: []string{" "}, Roles: []string{"admin"}}},
			Everywhere: &api.CatalogAudience{UserIds: []string{}, Groups: []string{"rvav-web"}, Roles: []string{}}}, 422, &problem)
		fields := []string{}
		for _, e := range problem.Errors {
			fields = append(fields, e.Field)
		}
		if want := []string{"/audiences/0/project_id", "/audiences/1/groups/0", "/audiences/1/roles/0", "/audiences/1/user_ids/0",
			"/everywhere"}; !slices.Equal(fields, want) {
			t.Fatalf("errors = %v, want %v", fields, want)
		}
		got := set(api.CatalogEntitlements{ProjectIds: []string{web, data}, Audiences: &[]api.CatalogProjectAudience{webAudience}}, 200)
		if got.AllProjects || !slices.Equal(got.ProjectIds, []string{data, web}) || len(*got.Audiences) != 1 ||
			(*got.Audiences)[0].ProjectId != web || len(*got.Users) != 1 || (*got.Users)[0].Name != "Alain Favre" ||
			!slices.ContainsFunc(*got.Roles, func(r api.RoleRef) bool { return r.Key == "supervisor" }) ||
			slices.ContainsFunc(*got.Roles, func(r api.RoleRef) bool { return r.Key == "admin" }) {
			t.Fatalf("sharing = %+v", got)
		}

		listed := func(user, project string) bool {
			t.Helper()
			var page api.CatalogItemPage
			h.json("GET", "/api/v1/catalog/items?limit=200&q=shared&project_id="+project, user, nil, 200, &page)
			return slices.ContainsFunc(page.Items, func(i api.CatalogItem) bool { return i.Slug == "linux-shared" })
		}
		counted := func(user, project string) int {
			t.Helper()
			var cats struct{ Items []api.CatalogCategory }
			h.json("GET", "/api/v1/catalog/categories?project_id="+project, user, nil, 200, &cats)
			for _, c := range cats.Items {
				if c.Key == "compute" {
					return c.ItemCount
				}
			}
			return 0
		}
		draft := func(project string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: version, ProjectId: project, Environment: "dev",
				Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
					"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		}
		h.json("POST", "/api/v1/me/drafts", "tnguyen", api.SavedDraftWrite{Draft: draft(web)}, 201, nil)
		computeFor := map[string]int{}
		for _, u := range []string{"afavre", "cdubois"} {
			computeFor[u] = counted(u, web)
		}
		// Alain by name, Thomas through rvav-web, Léa as a project admin; Sophie as one of Data's members.
		for _, c := range []struct {
			user, project string
			shared        bool
		}{{"afavre", web, true}, {"tnguyen", web, true}, {admin, web, true}, {"cdubois", web, false}, {"sbrunner", data, true}} {
			itemWant, formWant, previewWant := 200, 200, 200
			if !c.shared {
				itemWant, formWant, previewWant = 404, 404, 403
			}
			if listed(c.user, c.project) != c.shared {
				t.Fatalf("%s in %s: listed %v", c.user, c.project, !c.shared)
			}
			h.json("GET", "/api/v1/catalog/items/linux-shared?project_id="+c.project, c.user, nil, itemWant, nil)
			h.json("GET", "/api/v1/catalog/items/linux-shared/form?project_id="+c.project, c.user, nil, formWant, nil)
			h.json("POST", "/api/v1/requests/preview", c.user, draft(c.project), previewWant, nil)
		}
		if computeFor["afavre"] != computeFor["cdubois"]+1 {
			t.Fatalf("compute counts: Alain %d, Claire %d", computeFor["afavre"], computeFor["cdubois"])
		}
		h.json("POST", "/api/v1/requests", "afavre", draft(web), 202, nil, "Idempotency-Key", uuid.NewString())
		var refused struct{ Detail string }
		h.json("POST", "/api/v1/requests", "cdubois", draft(web), 403, &refused, "Idempotency-Key", uuid.NewString())
		if refused.Detail == "" {
			t.Fatal("a refusal without a reason")
		}

		// Who could request, before saving.
		var who api.CatalogRequesters
		h.json("POST", "/api/v1/admin/catalog/items/linux-shared/entitlements/requesters", admin, webAudience, 200, &who)
		people := map[string]api.CatalogRequester{}
		for _, p := range who.People {
			people[p.User.Id] = p
		}
		if len(people) != 3 || !people[seed.UserID("afavre")].Named || !slices.Equal(people[seed.UserID("tnguyen")].Groups, []string{"rvav-web"}) ||
			len(people[seed.UserID(admin)].Roles) != 1 || people[seed.UserID(admin)].Roles[0].Name != "Project admin" ||
			len(who.LeftOut) != 1 || who.LeftOut[0].Id != seed.UserID("cdubois") || len(who.Groups) != 1 || who.Groups[0].Seen != 1 {
			t.Fatalf("requesters = %+v", who)
		}
		h.json("POST", "/api/v1/admin/catalog/items/linux-shared/entitlements/requesters", admin,
			api.CatalogProjectAudience{ProjectId: data, UserIds: []string{seed.UserID("tnguyen")}, Groups: []string{}, Roles: []string{}}, 200, &who)
		if len(who.People) != 0 || len(who.NotRequesters) != 1 || len(who.LeftOut) != 1 {
			t.Fatalf("Data's requesters for Thomas = %+v", who)
		}
		h.json("POST", "/api/v1/admin/catalog/items/linux-shared/entitlements/requesters", "tnguyen", webAudience, 403, nil)

		// Without audiences, each project keeps its own; Finance Apps, added, is shared with all its members.
		got = set(api.CatalogEntitlements{ProjectIds: []string{web, data, "prj_fin"}}, 200)
		if len(*got.Audiences) != 1 || (*got.Audiences)[0].ProjectId != web || !listed("sbrunner", "prj_fin") {
			t.Fatalf("sharing kept = %+v", got)
		}

		// A role an item's sharing names can't be deleted.
		h.json("POST", "/api/v1/admin/roles", "mkeller", api.RoleCreate{Key: "ordering", Name: "Ordering",
			Permissions: []string{"project.see", "request.submit"}}, 201, nil)
		set(api.CatalogEntitlements{ProjectIds: []string{web}, Audiences: &[]api.CatalogProjectAudience{{ProjectId: web,
			UserIds: []string{}, Groups: []string{}, Roles: []string{"ordering"}}}}, 200)
		h.json("DELETE", "/api/v1/admin/roles/ordering", "mkeller", nil, 409, nil)

		// Thomas's saved draft stays once the item is no longer for him; submitting it is refused.
		var drafts api.SavedDraftList
		h.json("GET", "/api/v1/me/drafts", "tnguyen", nil, 200, &drafts)
		if len(drafts.Items) != 1 || listed("tnguyen", web) {
			t.Fatalf("Thomas's drafts = %+v", drafts.Items)
		}
		h.json("POST", "/api/v1/requests", "tnguyen", draft(web), 403, nil, "Idempotency-Key", uuid.NewString())

		// Every project, with one group in each.
		got = set(api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{},
			Everywhere: &api.CatalogAudience{UserIds: []string{}, Groups: []string{"rvav-data"}, Roles: []string{}}}, 200)
		if !got.AllProjects || !slices.Equal(got.Everywhere.Groups, []string{"rvav-data"}) || !listed("sbrunner", "prj_fin") ||
			listed("tnguyen", web) || listed("afavre", web) {
			t.Fatalf("every project, for rvav-data = %+v", got)
		}
		h.json("DELETE", "/api/v1/admin/roles/ordering", "mkeller", nil, 204, nil)
	})
}

// TestItemSharingYAML: an item's sharing exports to YAML, projects by code
// and users by email, and a new item imported from the file is shared the
// same; importing into an item keeps its sharing (G1.2).
func TestItemSharingYAML(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "lmartin"
		h.json("PUT", "/api/v1/admin/catalog/items/rhel9-server/entitlements", admin, api.CatalogEntitlements{
			ProjectIds: []string{"prj_web", "prj_data"}, Audiences: &[]api.CatalogProjectAudience{{ProjectId: "prj_web",
				UserIds: []string{seed.UserID("afavre")}, Groups: []string{"rvav-web"}, Roles: []string{"project-admin"}}}}, 200, nil)
		status, body, _ := h.do("GET", "/api/v1/admin/catalog/items/rhel9-server/export", admin, nil)
		if status != 200 || !strings.Contains(string(body), "sharing:\n  projects:\n    - project: data\n    - project: web\n      users:\n        - alain.favre@corp.example\n      groups:\n        - rvav-web\n      roles:\n        - project-admin\n") {
			t.Fatalf("export = %d %s", status, body)
		}
		copyYAML := strings.Replace(string(body), "slug: rhel9-server", "slug: rhel9-copy", 1)
		var imported api.CatalogImportResult
		h.json("POST", "/api/v1/admin/catalog/import", admin, rawYAML(copyYAML), 200, &imported, "Content-Type", "application/yaml")
		var original, copied api.CatalogEntitlements
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/entitlements", admin, nil, 200, &original)
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-copy/entitlements", admin, nil, 200, &copied)
		if !slices.Equal(copied.ProjectIds, original.ProjectIds) || len(*copied.Audiences) != 1 ||
			!slices.Equal((*copied.Audiences)[0].UserIds, (*original.Audiences)[0].UserIds) {
			t.Fatalf("the copy is shared with %+v, the original with %+v", copied, original)
		}
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-copy/versions/"+imported.Versions[len(imported.Versions)-1].Id+"/publish",
			admin, nil, 200, nil)
		_, again, _ := h.do("GET", "/api/v1/admin/catalog/items/rhel9-copy/export", admin, nil)
		if strings.Replace(string(again), "slug: rhel9-copy", "slug: rhel9-server", 1) != string(body) {
			t.Fatalf("a round trip changed the item:\n%s\n---\n%s", body, again)
		}

		// Into an item, the file's sharing isn't taken.
		everyone := strings.Replace(copyYAML, "sharing:\n", "sharing:\n  all_projects: true\n", 1)
		h.json("POST", "/api/v1/admin/catalog/import?item_id=rhel9-copy", admin, rawYAML(everyone), 200, nil, "Content-Type", "application/yaml")
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-copy/entitlements", admin, nil, 200, &copied)
		if copied.AllProjects {
			t.Fatal("importing into an item changed its sharing")
		}

		// What the file names must exist here, each problem on its line.
		broken := strings.Replace(copyYAML, "slug: rhel9-copy", "slug: rhel9-broken", 1)
		broken = strings.NewReplacer("project: data", "project: nope", "alain.favre@corp.example", "nobody@corp.example",
			"- project-admin", "- admin").Replace(broken)
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/import", admin, rawYAML(broken), 422, &problem, "Content-Type", "application/yaml")
		fields := []string{}
		for _, e := range problem.Errors {
			fields = append(fields, e.Field)
			if !strings.Contains(e.Message, "(line ") {
				t.Errorf("%s: %s", e.Field, e.Message)
			}
		}
		if want := []string{"/sharing/projects/0/project", "/sharing/projects/1/users/0", "/sharing/projects/1/roles/0"}; !slices.Equal(fields, want) {
			t.Fatalf("errors = %v, want %v", fields, want)
		}
	})
}
