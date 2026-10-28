package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/secrets"
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
		// An administrator who is also a member still manages the members.
		h.json("PUT", members+seed.UserID(admin), admin, api.ProjectMemberWrite{Role: api.Member}, 200, nil)
		h.json("PUT", members+seed.UserID("cdubois"), admin, api.ProjectMemberWrite{Role: api.Viewer}, 200, nil)
		h.json("DELETE", members+seed.UserID("cdubois"), admin, nil, 204, nil)
		h.json("DELETE", members+seed.UserID(admin), admin, nil, 204, nil)
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
		if audited != 10 {
			t.Fatalf("audited %d of 10 changes", audited)
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
		h.json("POST", "/api/v1/admin/naming-policies", admin, api.NamingPolicyWrite{Name: "Odd counter", Template: "{project}-{role}{seq:02}",
			Counter: new(api.NamingPolicyWriteCounter("global"))}, 422, nil)
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

// TestPolicyAdministration: quotas, lease, approval and day-2 policies are
// managed from the API, their CEL checked on sample variables when saved,
// and the explain query says which decide where and why (S9.6, ADR-0020).
func TestPolicyAdministration(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, web = "mkeller", "prj_web"
		var problem struct{ Errors []api.FieldError }

		// Quotas: replaced as a whole, checked against the project.
		var quotas api.QuotaLimitList
		h.json("GET", "/api/v1/admin/projects/"+web+"/quotas", admin, nil, 200, &quotas)
		if len(quotas.Items) != 4 {
			t.Fatalf("Web Platform's quotas = %+v", quotas.Items)
		}
		bad := api.QuotaLimitList{Items: []api.QuotaLimit{
			{Environment: "uat", Resource: api.Vcpu, Limit: new(10), Enforcement: api.QuotaLimitEnforcementHard},
			{Environment: "", Resource: api.Vcpu, Limit: new(-1), Enforcement: api.QuotaLimitEnforcementHard},
		}}
		h.json("PUT", "/api/v1/admin/projects/"+web+"/quotas", admin, bad, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Contains(fields, "/items/0/environment") || !slices.Contains(fields, "/items/1/limit") {
			t.Fatalf("bad quotas = %v", fields)
		}
		h.json("PUT", "/api/v1/admin/projects/"+web+"/quotas", admin, api.QuotaLimitList{Items: []api.QuotaLimit{
			{Environment: "", Resource: api.Vcpu, Limit: new(200), Enforcement: api.QuotaLimitEnforcementHard},
			{Environment: "prd", Resource: api.VmCount, Limit: new(10), Enforcement: api.QuotaLimitEnforcementSoft},
		}}, 200, &quotas)
		if len(quotas.Items) != 2 {
			t.Fatalf("set quotas = %+v", quotas.Items)
		}

		// Lease: a project's override of an environment's policy.
		h.json("PUT", "/api/v1/admin/lease-policies", admin, api.LeasePolicy{Environment: "dev", ProjectId: new(web), DefaultDays: new(30),
			MaxDays: new(14), ExpiryAction: api.PowerOffThenDelete}, 422, nil)
		h.json("PUT", "/api/v1/admin/lease-policies", admin, api.LeasePolicy{Environment: "dev", ProjectId: new(web), DefaultDays: new(14),
			MaxDays: new(30), MaxExtensions: 1, GraceDays: 3, ExpiryAction: api.PowerOffThenDelete}, 200, nil)

		// Approval policies: the condition must compile and give true or false.
		write := api.ApprovalPolicyWrite{Name: "Extra large in production", Condition: `environment == "prd" && inputs.size == "XL"`,
			Levels: []api.ApprovalLevel{{Name: "Infrastructure leads", Group: new("rvav-infra-leads")}}, Priority: new(5)}
		for _, b := range []api.ApprovalPolicyWrite{
			{Name: "x", Condition: `environment ==`, Levels: write.Levels},
			{Name: "x", Condition: `vm.vcpu + 1`, Levels: write.Levels},
			{Name: "x", Condition: "true", Levels: []api.ApprovalLevel{{Name: "Nobody"}}},
			{Name: "x", Condition: "true", Levels: []api.ApprovalLevel{{Name: "Late", Group: new("g"), Timeout: new("two days")}}},
			{Name: "x", Condition: "vm.cpus > 8", Levels: write.Levels}, // a typo outside the inputs
			{Name: "x", Condition: "true", Levels: write.Levels, ItemType: new(api.CatalogItemType("vm"))},
			{Name: "x", Condition: "true", Levels: []api.ApprovalLevel{{Name: "L", Group: new("g"), Mode: new(api.ApprovalLevelMode("ALL"))}}},
			{Name: "x", Condition: "true", Levels: []api.ApprovalLevel{{Name: "L", Group: new("g"), Timeout: new("1h"),
				OnTimeout: new(api.ApprovalLevelOnTimeout("Approve"))}}},
			{Name: "x", Condition: "true", Levels: []api.ApprovalLevel{{Name: "L", Group: new("g"), Timeout: new("1h"),
				OnTimeout: new(api.ApprovalLevelOnTimeoutEscalate), EscalateTo: &struct {
					Approvers *[]string `json:"approvers,omitempty"`
					Group     *string   `json:"group,omitempty"`
				}{}}}}, // escalating to nobody
		} {
			h.json("POST", "/api/v1/admin/approval-policies", admin, b, 422, nil)
		}
		var xl api.ApprovalPolicy
		h.json("POST", "/api/v1/admin/approval-policies", admin, write, 201, &xl)

		// Day-2 policies: roles, effect and condition checked.
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "x", Effect: api.Day2PolicyWriteEffectAllow,
			Roles: &[]string{"boss"}}, 422, nil)
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "x", Effect: api.Day2PolicyWriteEffectApproval}, 422, nil)
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "x", Effect: api.Day2PolicyWriteEffectDeny,
			Condition: new("action.key ==")}, 422, nil)
		h.json("POST", "/api/v1/admin/day2-policies", admin, api.Day2PolicyWrite{Name: "No snapshots for viewers", Effect: api.Day2PolicyWriteEffectDeny,
			Actions: &[]string{"snapshot_*"}, Roles: &[]string{"viewer"}, Priority: new(5)}, 201, nil)

		// What applies here.
		var rhel api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, admin, nil, 200, &rhel)
		var why api.PolicyExplanation
		h.json("POST", "/api/v1/admin/policies/explain", admin, api.PolicyExplainQuery{ProjectId: web, Environment: "prd", ItemId: &rhel.Id,
			Inputs: &map[string]any{"size": "XL"}, Action: new("delete"), AsUserId: new(seed.UserID("tnguyen"))}, 200, &why)
		if why.Approval == nil || why.Approval.DecidedBy == nil || !strings.Contains(*why.Approval.DecidedBy, "Extra large in production") {
			t.Fatalf("approval = %+v", why.Approval)
		}
		if why.Day2 == nil || why.Day2.DecidedBy == nil || *why.Day2.DecidedBy != "Deleting in production" {
			t.Fatalf("day-2 = %+v", why.Day2)
		}
		if why.Lease.DecidedBy == nil || *why.Lease.DecidedBy != "Production" || why.Naming.DecidedBy != nil || len(why.Quota) == 0 {
			t.Fatalf("lease %+v, naming %+v, quota %+v", why.Lease, why.Naming, why.Quota)
		}
		h.json("POST", "/api/v1/admin/policies/explain", admin, api.PolicyExplainQuery{ProjectId: web, Environment: "dev"}, 200, &why)
		if why.Lease.DecidedBy == nil || *why.Lease.DecidedBy != "Web Platform in Development" {
			t.Fatalf("dev lease = %+v", why.Lease)
		}
		h.json("POST", "/api/v1/admin/policies/explain", "tnguyen", api.PolicyExplainQuery{ProjectId: web, Environment: "dev"}, 403, nil)
		h.json("POST", "/api/v1/admin/policies/explain", "lmartin", api.PolicyExplainQuery{ProjectId: web, Environment: "dev",
			AsUserId: new(seed.UserID("sbrunner"))}, 422, nil) // not in Web Platform

		h.json("DELETE", "/api/v1/admin/lease-policies?environment=dev&project_id="+web, admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/approval-policies/"+xl.Id, admin, nil, 204, nil)
		var audited int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action like 'policy.%'`).Scan(&audited)
		if audited != 6 {
			t.Fatalf("audited %d policy changes, want 6", audited)
		}
	})
}

// TestIntegrationSettings: connections set from the API, their secret
// sealed and never returned, tested before or after saving; a restart is
// said to be needed; secrets sealed again under a new key (S9.7, ADR-0020).
func TestIntegrationSettings(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		h.json("GET", "/api/v1/admin/integrations", "lmartin", nil, 403, nil)
		vcsim := c.URL().String()
		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/integrations/awx", admin, api.IntegrationWrite{Settings: map[string]any{"colour": "blue",
			"insecure": "yes"}}, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Contains(fields, "/settings/colour") || !slices.Contains(fields, "/settings/insecure") {
			t.Fatalf("bad settings = %v", fields)
		}

		var vc api.Integration
		h.json("PUT", "/api/v1/admin/integrations/vcenter", admin, api.IntegrationWrite{Settings: map[string]any{"url": vcsim,
			"username": "user", "insecure": true, "name": "vc01.corp.example"}, Secret: new("pass")}, 200, &vc)
		if !vc.SecretSet || !vc.RestartNeeded || vc.Settings["url"] != vcsim || vc.Settings["password"] != nil {
			t.Fatalf("saved vcenter = %+v", vc)
		}
		_, raw, _ := h.do("GET", "/api/v1/admin/integrations", admin, nil)
		if strings.Contains(string(raw), `"pass"`) {
			t.Fatal("the password came back")
		}

		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/integrations/vcenter/test", admin, nil, 200, &test)
		if !test.Ok || len(test.Checks) != 3 {
			t.Fatalf("vCenter test = %+v", test)
		}
		// The saved password goes only where it was given for: tested or saved
		// with another address, it is asked for again and nothing is sent.
		var hits atomic.Int32
		trap := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
		defer trap.Close()
		elsewhere := map[string]any{"url": trap.URL + "/sdk", "username": "user", "insecure": true, "name": "vc01.corp.example"}
		h.json("POST", "/api/v1/admin/integrations/vcenter/test", admin, api.IntegrationWrite{Settings: elsewhere}, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Contains(fields, "/secret") {
			t.Fatalf("another address without the password = %v", fields)
		}
		h.json("PUT", "/api/v1/admin/integrations/vcenter", admin, api.IntegrationWrite{Settings: elsewhere}, 422, nil)
		if hits.Load() != 0 {
			t.Fatalf("the trap got %d requests", hits.Load())
		}
		// Given again, settings are tested without being saved (vcsim takes any password: a wrong address here).
		h.json("POST", "/api/v1/admin/integrations/vcenter/test", admin, api.IntegrationWrite{Settings: map[string]any{
			"url": "https://127.0.0.1:1/sdk"}, Secret: new("pass")}, 200, &test)
		if test.Ok || test.Checks[0].Ok {
			t.Fatalf("an unreachable vCenter tested fine: %+v", test)
		}
		h.json("POST", "/api/v1/admin/integrations/awx/test", admin, api.IntegrationWrite{Settings: map[string]any{
			"url": "http://127.0.0.1:1", "organization": "rvav-test"}, Secret: new("t")}, 200, &test)
		if test.Ok {
			t.Fatalf("an unreachable AWX tested fine: %+v", test)
		}

		// Secrets sealed again under a new key; the old one can go.
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb", Kind: api.SecretWriteKindToken, Value: new("cmdb-token")}, 201, nil)
		oldKey := base64.StdEncoding.EncodeToString([]byte("e2e-only-secrets-key-32-bytes!!!"))
		newKey := base64.StdEncoding.EncodeToString([]byte("another-e2e-secrets-key-32-bytes"))
		both, err := secrets.New(newKey, oldKey)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := core.RotateSecrets(ctx, h.store, both); err != nil || n != 2 {
			t.Fatalf("rotated %d, %v", n, err)
		}
		onlyNew, _ := secrets.New(newKey)
		v, err := h.store.GetSecret(ctx, "cmdb")
		if err != nil {
			t.Fatal(err)
		}
		if plain, err := onlyNew.Open(v.Name, v.KeyID, v.Nonce, v.Ciphertext); err != nil || string(plain) != "cmdb-token" {
			t.Fatalf("after rotation: %q %v", plain, err)
		}
		if n, _ := core.RotateSecrets(ctx, h.store, both); n != 0 {
			t.Fatalf("rotated %d again", n)
		}
		// A value replaced while a rotation runs is not put back.
		if done, err := h.store.ResealSecret(ctx, "cmdb", []byte("not the nonce read"), "k", []byte("n"), []byte("c")); err != nil || done {
			t.Fatalf("resealed a changed secret: %v %v", done, err)
		}
	})
}

// TestTokensAndAudit: users make, use and revoke their own API tokens,
// administrators everyone's; the audit log is searched and exported, each
// seeing what they may (S9.8, ADR-0020).
func TestTokensAndAudit(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		h.json("POST", "/api/v1/tokens", "tnguyen", api.ApiTokenCreate{Name: "ci", ExpiresAt: new(time.Now().Add(400 * 24 * time.Hour))}, 422, nil)
		h.json("POST", "/api/v1/tokens", "tnguyen", api.ApiTokenCreate{Name: "ci", ExpiresAt: new(time.Now().Add(-time.Hour))}, 422, nil)
		// An unknown scope is refused, never taken as all.
		h.json("POST", "/api/v1/tokens", "tnguyen", api.ApiTokenCreate{Name: "ci", Scope: new(api.ApiTokenCreateScope("Inventory"))}, 422, nil)
		var made api.ApiTokenCreated
		h.json("POST", "/api/v1/tokens", "tnguyen", api.ApiTokenCreate{Name: "ci pipeline"}, 201, &made)
		if !strings.HasPrefix(made.Token, "rvav_pat_") || made.ExpiresAt == nil || made.Scope != api.ApiTokenCreatedScopeAll {
			t.Fatalf("made %+v", made)
		}
		var me api.Me
		h.json("GET", "/api/v1/me", "", nil, 200, &me, "Authorization", "Bearer "+made.Token)
		if me.User.Id != seed.UserID("tnguyen") {
			t.Fatalf("the token acts as %s", me.User.Id)
		}
		var mine api.ApiTokenList
		h.json("GET", "/api/v1/tokens", "tnguyen", nil, 200, &mine)
		if len(mine.Items) != 1 || mine.Items[0].LastUsedAt == nil {
			t.Fatalf("tokens = %+v", mine.Items)
		}
		h.json("GET", "/api/v1/tokens?all=true", "tnguyen", nil, 403, nil)
		var all api.ApiTokenList
		h.json("GET", "/api/v1/tokens?all=true", admin, nil, 200, &all)
		if !slices.ContainsFunc(all.Items, func(x api.ApiToken) bool { return x.Id == made.Id }) {
			t.Fatalf("everyone's tokens = %+v", all.Items)
		}
		h.json("DELETE", "/api/v1/tokens/"+made.Id, "lmartin", nil, 404, nil)
		h.json("DELETE", "/api/v1/tokens/"+made.Id, "tnguyen", nil, 204, nil)
		h.json("GET", "/api/v1/me", "", nil, 401, nil, "Authorization", "Bearer "+made.Token)

		// The audit log: searched, by prefix, per project, as CSV.
		h.json("PATCH", "/api/v1/admin/environments/dev", admin, api.AdminEnvironmentUpdate{Order: new(1)}, 200, nil)
		h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID("cdubois"), "lmartin", api.ProjectMemberWrite{Role: api.Viewer}, 200, nil)
		var page api.AuditEventPage
		h.json("GET", "/api/v1/audit?action=token.", admin, nil, 200, &page)
		if len(page.Items) != 2 || page.Items[0].Action != "token.revoke" || page.Items[0].Actor == nil {
			t.Fatalf("token events = %+v", page.Items)
		}
		h.json("GET", "/api/v1/audit?limit=1", admin, nil, 200, &page)
		if len(page.Items) != 1 || page.NextCursor == nil {
			t.Fatalf("first page = %+v", page)
		}
		first := page.Items[0].Id
		h.json("GET", "/api/v1/audit?limit=1&cursor="+*page.NextCursor, admin, nil, 200, &page)
		if len(page.Items) != 1 || page.Items[0].Id == first {
			t.Fatalf("second page = %+v", page.Items)
		}
		h.json("GET", "/api/v1/audit", "tnguyen", nil, 403, nil)
		var webOnly api.AuditEventPage
		h.json("GET", "/api/v1/audit", "lmartin", nil, 200, &webOnly)
		if len(webOnly.Items) == 0 || slices.ContainsFunc(webOnly.Items, func(e api.AuditEvent) bool { return e.Project == nil || e.Project.Id != "prj_web" }) {
			t.Fatalf("a project admin's view = %+v", webOnly.Items)
		}
		code, csv, hdr := h.do("GET", "/api/v1/audit?format=csv&q=cdubois", admin, nil)
		if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") ||
			!strings.HasPrefix(string(csv), "at,actor,action,target_type,target_id,project,detail\n") || !strings.Contains(string(csv), "member.set") {
			t.Fatalf("CSV %d %s:\n%s", code, hdr.Get("Content-Type"), csv)
		}
	})
}
