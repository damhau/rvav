package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestProjectAdministration: administrators create and archive projects,
// map OIDC groups and manage environments; project admins manage their
// project's members and can't leave it without one (S9.2, ADR-0020).
func TestProjectAdministration(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }

		// Projects: administrators only; the code is checked and unique.
		create := api.AdminProjectCreate{Code: "pay", Name: "Payments", Environments: []string{"dev", "tst"},
			ProjectAdmins: &[]string{seed.UserID("sbrunner")}}
		h.json("POST", "/api/v1/admin/projects", "lmartin", create, 403, nil)
		var pay api.AdminProject
		h.json("POST", "/api/v1/admin/projects", admin, create, 201, &pay)
		if pay.Code != "pay" || pay.Members != 1 || pay.Archived {
			t.Fatalf("created %+v", pay)
		}
		h.json("POST", "/api/v1/admin/projects", admin, create, 409, nil)
		h.json("POST", "/api/v1/admin/projects", admin, api.AdminProjectCreate{Code: "Pay!", Name: "x", Environments: []string{"qa"}}, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Contains(fields, "/code") || !slices.Contains(fields, "/environments/0") {
			t.Fatalf("bad project = %v", fields)
		}

		// Members: the project admin manages them; members can't.
		members := "/api/v1/projects/" + pay.Id + "/members/"
		h.json("PUT", members+seed.UserID("tnguyen"), "sbrunner", api.ProjectMemberWrite{Role: api.Member}, 200, nil)
		h.json("PUT", members+seed.UserID("lmartin"), "tnguyen", api.ProjectMemberWrite{Role: api.Member}, 403, nil)
		h.json("PUT", members+seed.UserID("sbrunner"), "sbrunner", api.ProjectMemberWrite{Role: api.Member}, 409, nil) // the last project admin
		h.json("DELETE", members+seed.UserID("sbrunner"), "sbrunner", nil, 409, nil)

		// A group gives a role; its members are changed through the mapping.
		var mapping api.GroupMapping
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "rvav-pay", ProjectId: &pay.Id, Role: "member"}, 201, &mapping)
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "rvav-pay", ProjectId: &pay.Id, Role: "member"}, 409, nil)
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "rvav-pay", ProjectId: &pay.Id, Role: "admin"}, 422, nil)
		if _, err := h.store.Pool.Exec(ctx, `insert into user_groups (user_id, group_name) values ($1, 'rvav-pay')`, seed.UserID("afavre")); err != nil {
			t.Fatal(err)
		}
		var list api.ProjectMemberList
		h.json("GET", "/api/v1/projects/"+pay.Id+"/members", "sbrunner", nil, 200, &list)
		roles := map[string]string{}
		for _, m := range list.Items {
			roles[m.User.Id] = string(m.Role)
			if m.User.Id == seed.UserID("afavre") && (m.Direct || !slices.Contains(m.Groups, "rvav-pay")) {
				t.Fatalf("afavre should be a member through rvav-pay only: %+v", m)
			}
		}
		if roles[seed.UserID("sbrunner")] != "project-admin" || roles[seed.UserID("tnguyen")] != "member" || roles[seed.UserID("afavre")] != "member" {
			t.Fatalf("members = %v", roles)
		}
		h.json("DELETE", members+seed.UserID("afavre"), "sbrunner", nil, 409, nil) // through the group only

		// The last way anyone is an administrator can't be removed.
		if _, err := h.store.Pool.Exec(ctx, `update users set global_roles = array_remove(global_roles, 'admin')`); err != nil {
			t.Fatal(err)
		}
		var mappings api.GroupMappingList
		h.json("GET", "/api/v1/admin/group-mappings", admin, nil, 200, &mappings)
		i := slices.IndexFunc(mappings.Items, func(m api.GroupMapping) bool { return m.Role == "admin" && m.Project == nil })
		if i < 0 {
			t.Fatalf("no admin mapping in %+v", mappings.Items)
		}
		h.json("DELETE", "/api/v1/admin/group-mappings/"+mappings.Items[i].Id, admin, nil, 409, nil)
		h.json("DELETE", "/api/v1/admin/group-mappings/"+mapping.Id, admin, nil, 204, nil)

		// Environments: added, changed, removed when unused.
		h.json("POST", "/api/v1/admin/environments", admin, api.AdminEnvironment{Key: "uat", Name: "Acceptance", Order: 5, SnapshotMaxCount: 2}, 201, nil)
		h.json("POST", "/api/v1/admin/environments", admin, api.AdminEnvironment{Key: "uat", Name: "Acceptance", Order: 5}, 409, nil)
		var uat api.AdminEnvironment
		h.json("PATCH", "/api/v1/admin/environments/uat", admin, api.AdminEnvironmentUpdate{Name: new("User acceptance"), Production: new(true)}, 200, &uat)
		if uat.Name != "User acceptance" || !uat.Production {
			t.Fatalf("updated uat = %+v", uat)
		}
		h.json("DELETE", "/api/v1/admin/environments/dev", admin, nil, 409, nil)
		h.json("DELETE", "/api/v1/admin/environments/uat", admin, nil, 204, nil)

		// Archived, a project requests nothing new.
		var archived api.AdminProject
		h.json("PATCH", "/api/v1/admin/projects/"+pay.Id, admin, api.AdminProjectUpdate{Archived: new(true)}, 200, &archived)
		if !archived.Archived || archived.ArchivedAt == nil {
			t.Fatalf("archived = %+v", archived)
		}
		var tls api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/tls-cert?project_id=prj_web", "tnguyen", nil, 200, &tls)
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: tls.Id, VersionId: tls.Version.Id, ProjectId: pay.Id,
			Environment: "dev", Inputs: map[string]any{}}, 422, &problem, "Idempotency-Key", uuid.NewString())
		if !slices.Contains(fieldsOf(problem.Errors), "/project_id") {
			t.Fatalf("a request in an archived project = %+v", problem.Errors)
		}
		var all api.AdminProjectList
		h.json("GET", "/api/v1/admin/projects", admin, nil, 200, &all)
		if slices.ContainsFunc(all.Items, func(p api.AdminProject) bool { return p.Id == pay.Id }) {
			t.Fatal("an archived project is listed without archived=true")
		}

		// The seed leaves what administrators manage alone once they changed it.
		h.json("GET", "/api/v1/admin/group-mappings", admin, nil, 200, &mappings)
		data := slices.IndexFunc(mappings.Items, func(m api.GroupMapping) bool { return m.OidcGroup == "rvav-data" })
		h.json("DELETE", "/api/v1/admin/group-mappings/"+mappings.Items[data].Id, admin, nil, 204, nil)
		if err := seed.Run(ctx, h.store); err != nil {
			t.Fatal(err)
		}
		var after api.GroupMappingList
		h.json("GET", "/api/v1/admin/group-mappings", admin, nil, 200, &after)
		if slices.ContainsFunc(after.Items, func(m api.GroupMapping) bool { return m.OidcGroup == "rvav-data" }) {
			t.Fatal("the seed brought back a group mapping an administrator removed")
		}

		// People, for pickers.
		var people api.UserList
		h.json("GET", "/api/v1/users?q=thom", "tnguyen", nil, 200, &people)
		if len(people.Items) != 1 || people.Items[0].Id != seed.UserID("tnguyen") {
			t.Fatalf("search = %+v", people.Items)
		}

		var audited int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action in ('project.create', 'member.set',
			'group_mapping.create', 'group_mapping.delete', 'environment.create', 'environment.delete', 'project.archive')`).Scan(&audited)
		if audited != 8 {
			t.Fatalf("audited %d of 8 changes", audited)
		}
	})
}

func fieldsOf(errs []api.FieldError) []string {
	out := []string{}
	for _, e := range errs {
		out = append(out, e.Field)
	}
	return out
}
