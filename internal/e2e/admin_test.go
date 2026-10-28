package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/store"
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

// TestPlacementAdministration: placement rules by name and by vSphere tag,
// a preview of what they select, and network mappings set by hand that
// the EfficientIP sync leaves alone (S9.3, ADR-0020).
func TestPlacementAdministration(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		if len(rules.Items) != 3 {
			t.Fatalf("Web Platform's rules = %+v", rules.Items)
		}
		h.json("GET", "/api/v1/admin/placement-rules", "tnguyen", nil, 403, nil)
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", "lmartin", nil, 200, nil) // its project admin

		var preview api.PlacementPreview
		h.json("GET", "/api/v1/admin/placement-preview?project_id=prj_web&environment=dev", admin, nil, 200, &preview)
		if len(preview.Clusters) != 1 || preview.Clusters[0].Name != "GVA-DEV-CL01" || len(preview.Datastores) == 0 ||
			len(preview.Networks) == 0 || preview.Folder == "" || len(preview.Problems) != 0 {
			t.Fatalf("web/dev preview = %+v", preview)
		}

		// A project placed by tag: gold clusters and gold datastores.
		var gold api.AdminProject
		h.json("POST", "/api/v1/admin/projects", admin, api.AdminProjectCreate{Code: "gold", Name: "Gold tier", Environments: []string{"prd"}}, 201, &gold)
		h.json("GET", "/api/v1/admin/placement-preview?project_id="+gold.Id+"&environment=prd", admin, nil, 200, &preview)
		if len(preview.Problems) != 1 || len(preview.Clusters) != 0 {
			t.Fatalf("a project without rules = %+v", preview)
		}
		rule := api.PlacementRuleWrite{ProjectId: gold.Id, Environment: "prd", Clusters: []string{}, Datastores: []string{},
			ClusterTags: &[]string{"rvav-tier/gold"}, DatastoreTags: &[]string{"rvav-tier/gold"},
			Networks: []string{"dvpg-*-prd-*"}, Folder: "/GVA/vm/rvav/web/prd"}
		var problem struct{ Errors []api.FieldError }
		bad := rule
		bad.Environment, bad.ClusterTags = "dev", &[]string{"gold"}
		h.json("POST", "/api/v1/admin/placement-rules", admin, bad, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Contains(fields, "/environment") || !slices.Contains(fields, "/cluster_tags/0") {
			t.Fatalf("a bad rule = %v", fields)
		}
		var created api.PlacementRule
		h.json("POST", "/api/v1/admin/placement-rules", admin, rule, 201, &created)
		h.json("GET", "/api/v1/admin/placement-preview?project_id="+gold.Id+"&environment=prd", admin, nil, 200, &preview)
		datastores := []string{}
		for _, d := range preview.Datastores {
			datastores = append(datastores, d.Name)
		}
		if len(preview.Clusters) != 1 || preview.Clusters[0].Name != "GVA-PRD-CL01" ||
			fmt.Sprint(datastores) != "[vsan-gva-prd-cl01]" || len(preview.Networks) == 0 {
			t.Fatalf("placed by tag: clusters %+v, datastores %v, networks %d, problems %v", preview.Clusters, datastores,
				len(preview.Networks), preview.Problems)
		}
		rule.Priority = new(50)
		h.json("PUT", "/api/v1/admin/placement-rules/"+created.Id, admin, rule, 200, &created)
		if created.Priority != 50 {
			t.Fatalf("updated rule = %+v", created)
		}
		h.json("DELETE", "/api/v1/admin/placement-rules/"+created.Id, admin, nil, 204, nil)

		// A network's subnet by hand wins over EfficientIP's, until removed.
		const net = "dvpg-db-dev-vl320"
		mapping := api.NetworkMappingWrite{Subnet: "10.32.20.0/24", Gateway: "10.32.20.1", DnsZone: "db.corp.example"}
		h.json("PUT", "/api/v1/admin/network-mappings/"+net, admin, api.NetworkMappingWrite{Subnet: "10.32.20.0/24", Gateway: "10.9.9.9",
			DnsZone: "db.corp.example"}, 422, nil)
		var set api.NetworkMapping
		h.json("PUT", "/api/v1/admin/network-mappings/"+net, admin, mapping, 200, &set)
		if set.Source != api.NetworkMappingSourceManual || set.Subnet != "10.32.20.0/24" {
			t.Fatalf("set = %+v", set)
		}
		if _, _, err := h.store.SyncNetworkSubnets(ctx, []store.NetworkSubnet{{Network: net, Subnet: "10.99.0.0/24", Gateway: "10.99.0.1",
			DNSZone: "corp.example", DNSServers: []string{}, EfficientIPSubnetID: "99"}}, nil); err != nil {
			t.Fatal(err)
		}
		var mappings api.NetworkMappingList
		h.json("GET", "/api/v1/admin/network-mappings", admin, nil, 200, &mappings)
		i := slices.IndexFunc(mappings.Items, func(m api.NetworkMapping) bool { return m.Network == net })
		if i < 0 || mappings.Items[i].Subnet != "10.32.20.0/24" || mappings.Items[i].Source != api.NetworkMappingSourceManual {
			t.Fatalf("after the sync: %+v", mappings.Items)
		}
		h.json("DELETE", "/api/v1/admin/network-mappings/"+net, admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/network-mappings/"+net, admin, nil, 404, nil)
	})
}

// TestTemplatesAndSizes: templates and size presets offered per project and
// environment are what VM forms choose from, and a request gets the vCenter
// template, its OS family and the preset's resources (S9.4, ADR-0020).
func TestTemplatesAndSizes(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, web = "mkeller", "prj_web"
		h.json("POST", "/api/v1/admin/templates", admin, api.VmTemplateWrite{Key: "ubuntu", Label: "Ubuntu 24.04",
			VcenterTemplate: "tpl-ubuntu24.04-2026.10", OsFamily: api.VmTemplateWriteOsFamilyLinux, Offers: &[]api.Offer{{ProjectId: new(web)}}}, 201, nil)
		h.json("POST", "/api/v1/admin/templates", admin, api.VmTemplateWrite{Key: "win", Label: "Windows Server 2025",
			VcenterTemplate: "tpl-win2025-std-2026.09", OsFamily: api.VmTemplateWriteOsFamilyWindows, Offers: &[]api.Offer{{Environment: new("dev")}}}, 201, nil)
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/templates", admin, api.VmTemplateWrite{Key: "lib", Label: "From a library", VcenterTemplate: "library:os/rhel",
			OsFamily: api.VmTemplateWriteOsFamilyLinux, Offers: &[]api.Offer{{ProjectId: new("prj_nope")}}}, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Contains(fields, "/vcenter_template") || !slices.Contains(fields, "/offers/0/project_id") {
			t.Fatalf("bad template = %v", fields)
		}
		for _, z := range []api.SizePresetWrite{
			{Key: "S", Label: "Small", Vcpu: 2, MemoryGb: 4, DiskGb: 40, Offers: &[]api.Offer{{}}},
			{Key: "M", Label: "Medium", Vcpu: 4, MemoryGb: 16, DiskGb: 80, Offers: &[]api.Offer{{}}},
			{Key: "XL", Label: "Extra large", Vcpu: 16, MemoryGb: 64, DiskGb: 200, Offers: &[]api.Offer{{Environment: new("prd")}}},
		} {
			h.json("POST", "/api/v1/admin/sizes", admin, z, 201, nil)
		}
		var templates api.VmTemplateList
		h.json("GET", "/api/v1/admin/templates", admin, nil, 200, &templates)
		if len(templates.Items) != 2 || templates.Items[0].InInventory == nil || !*templates.Items[0].InInventory {
			t.Fatalf("templates = %+v", templates.Items)
		}

		// A VM item that chooses from what is offered.
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		var published api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, admin, nil, 200, &published)
		def := published.Definition
		props := def.FormSchema["properties"].(map[string]any)
		props["size"] = map[string]any{"type": "string"}
		props["template"] = map[string]any{"type": "string"}
		def.FormSchema["required"] = append(def.FormSchema["required"].([]any), "template")
		def.UiSchema.Fields["size"] = api.UiField{Widget: api.UiWidgetSelect, Label: "Size", ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("rvav.sizes")}}
		def.UiSchema.Fields["template"] = api.UiField{Widget: api.UiWidgetSelect, Label: "Operating system",
			ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("rvav.templates")}}
		def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "template")
		delete(def.Blueprint, "sizes")
		def.Blueprint["template"] = ""
		def.Blueprint["bindings"].(map[string]any)["template"] = "inputs.template"
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "any-vm", Name: "A VM", Summary: "From the offered templates.",
			Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/any-vm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/any-vm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		options := func(user, project, env, field string) []string {
			t.Helper()
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", user, api.FieldValuesQuery{VersionId: item.DraftVersion.Id, ProjectId: project,
				Environment: new(api.EnvironmentKey(env)), Field: field, Inputs: map[string]any{}}, 200, &out)
			keys := []string{}
			for _, o := range *out.Options {
				keys = append(keys, o.Value)
			}
			return keys
		}
		if got := options("tnguyen", web, "dev", "template"); fmt.Sprint(got) != "[ubuntu win]" {
			t.Fatalf("templates for web/dev = %v", got)
		}
		if got := options("sbrunner", "prj_data", "dev", "template"); fmt.Sprint(got) != "[win]" {
			t.Fatalf("templates for data/dev = %v", got)
		}
		if got := options("tnguyen", web, "dev", "size"); fmt.Sprint(got) != "[S M]" {
			t.Fatalf("sizes for web/dev = %v", got)
		}
		if got := options("tnguyen", web, "prd", "size"); fmt.Sprint(got) != "[S M XL]" {
			t.Fatalf("sizes for web/prd = %v", got)
		}

		// A request gets the vCenter template, its OS family and the preset; what isn't offered is refused.
		inputs := map[string]any{"role": "api", "size": "M", "template": "win", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: web, Environment: "dev", Inputs: inputs}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		var plan struct {
			VMs []struct {
				Template string `json:"template"`
				VCPU     int    `json:"vcpu"`
			} `json:"vms"`
			Guest struct {
				OSFamily string `json:"os_family"`
			} `json:"guest"`
		}
		var raw []byte
		if err := h.store.Pool.QueryRow(ctx, `select plan from requests where id = $1`, req.Id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(raw, &plan)
		if len(plan.VMs) != 1 || plan.VMs[0].Template != "tpl-win2025-std-2026.09" || plan.VMs[0].VCPU != 4 || plan.Guest.OSFamily != "windows" {
			t.Fatalf("plan = %s", raw)
		}
		inputs["size"] = "XL"
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 422, &problem, "Idempotency-Key", uuid.NewString())
		if !slices.ContainsFunc(problem.Errors, func(e api.FieldError) bool { return e.Field == "/inputs/size" }) {
			t.Fatalf("a size not offered in dev = %+v", problem.Errors)
		}
	})
}

// TestNamingPolicies: the most specific naming policy offered wins over the
// blueprint's template, counters are per prefix or per policy, and a name
// rvav gave once is never given again (S9.5, ADR-0020).
func TestNamingPolicies(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, web = "mkeller", "prj_web"
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/naming-policies", admin, api.NamingPolicyWrite{Name: "No counter", Template: "{project}-{role}"}, 422, &problem)
		h.json("POST", "/api/v1/admin/naming-policies", admin, api.NamingPolicyWrite{Name: "Underscores", Template: "{project}_{role}{seq:02}"}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/template" {
			t.Fatalf("an invalid host name = %+v", problem.Errors)
		}
		var short, global api.NamingPolicy
		h.json("POST", "/api/v1/admin/naming-policies", admin, api.NamingPolicyWrite{Name: "Short", Template: "{project}{environment}{role}{seq:02}",
			Offers: &[]api.Offer{{ProjectId: new(web)}}}, 201, &short)
		policy := api.NamingPolicyWriteCounterPolicy
		h.json("POST", "/api/v1/admin/naming-policies", admin, api.NamingPolicyWrite{Name: "Everyone", Template: "{project}-{role}-{seq:04}",
			Counter: &policy, Offers: &[]api.Offer{{}}}, 201, &global)

		var preview api.NamingPreview
		h.json("POST", "/api/v1/admin/naming-preview", admin, api.NamingPreviewQuery{Template: short.Template, ProjectId: web,
			Environment: "dev", Inputs: &map[string]any{"role": "api"}, Count: new(2)}, 200, &preview)
		if fmt.Sprint(preview.Names) != "[webdevapi01 webdevapi02]" {
			t.Fatalf("preview = %+v", preview)
		}

		// Requests: Web Platform gets its own policy, Data Engineering the one for everyone.
		var rhel api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "tnguyen", nil, 200, &rhel)
		inputs := map[string]any{"role": "api", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}
		submit := func() string {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: rhel.Id, VersionId: rhel.Version.Id, ProjectId: web,
				Environment: "dev", Inputs: inputs}, 202, &req, "Idempotency-Key", uuid.NewString())
			var name string
			if err := h.store.Pool.QueryRow(ctx, `select name from deployments where created_by_request_id = $1`, req.Id).Scan(&name); err != nil {
				t.Fatal(err)
			}
			return name
		}
		if name := submit(); name != "webdevapi01" {
			t.Fatalf("first name = %s", name)
		}
		// Even with its counter back at zero, a name rvav gave is not given again.
		if _, err := h.store.Pool.Exec(ctx, `delete from name_counters where prefix = 'webdevapi'`); err != nil {
			t.Fatal(err)
		}
		if name := submit(); name != "webdevapi02" {
			t.Fatalf("second name = %s", name)
		}
		var dataPreview api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "sbrunner", api.RequestDraft{CatalogItemId: rhel.Id, VersionId: rhel.Version.Id,
			ProjectId: "prj_data", Environment: "dev", Inputs: map[string]any{"role": "etl", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-db-dev-vl320", "post_config": []string{"linux-baseline"}}}, 200, &dataPreview)
		if dataPreview.Names == nil || fmt.Sprint(*dataPreview.Names) != "[data-etl-0001]" {
			t.Fatalf("Data Engineering's names = %v", dataPreview.Names)
		}
		var policies api.NamingPolicyList
		h.json("GET", "/api/v1/admin/naming-policies", admin, nil, 200, &policies)
		if len(policies.Items) != 2 {
			t.Fatalf("policies = %+v", policies.Items)
		}
		h.json("DELETE", "/api/v1/admin/naming-policies/"+short.Id, admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/naming-policies/"+short.Id, admin, nil, 404, nil)
	})
}
