package e2e

import (
	"context"
	"encoding/base64"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
)

// TestComponentReferences: web servers whose cloud-init and tags read the
// database's address and their own index, named through a string
// function, are built after the database, with those values; a cycle and
// a reference to no component are refused when saved, with their line
// (T1.1, ADR-0024).
func TestComponentReferences(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		userData := "#cloud-config\nwrite_files:\n- path: /etc/shop.conf\n  content: |\n" +
			"    db={{ (index .components.db.vms 0).address }} {{ index .components.db.fqdns 0 }}\n" +
			"    node={{ .vm.index }} of {{ .components.web.count }}\n"
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []string{"app"},
				"properties": map[string]any{"app": map[string]any{"type": "string", "minLength": 1}}},
			UiSchema:      api.UiSchema{Fields: map[string]api.UiField{"app": {Widget: api.UiWidgetText, Label: "Application"}}},
			InitialValues: map[string]any{},
			Blueprint: map[string]any{
				"kind": "vm", "naming": "{project}-{bindings.app}{component}{seq:02}", "template": "tpl-ubuntu24.04-2026.10",
				"guest": map[string]any{"os_family": "linux", "domain": "corp.example"},
				"bindings": map[string]any{"vcpu": "2", "memory_gb": "4", "network": `"dvpg-app-dev-vl310"`,
					"app": "inputs.app.trim().lowerAscii().replace('_', '')"},
				"components": []any{
					map[string]any{"key": "web", "title": "Web servers", "count": 2,
						"bindings": map[string]any{"tags": `['app/node' + string(self.index), 'app/db-' + components.db.addresses[0].replace('.', '-')]`},
						"guest":    map[string]any{"cloud_init": map[string]any{"user_data": userData}}},
					map[string]any{"key": "db", "title": "Database"},
				},
			},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Shop", Fields: []string{"app"}}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "shop-refs", Name: "Shop with references",
			Summary: "Web servers reading their database.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/shop-refs/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/shop-refs/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"app": " Shop_API "}}

		// The preview: names through the binding, web after db.
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if !pv.Valid || pv.Components == nil || len(*pv.Components) != 2 {
			t.Fatalf("preview = %+v", pv)
		}
		web, db := (*pv.Components)[0], (*pv.Components)[1]
		if !slices.Equal(web.Names, []string{"web-shopapiweb01", "web-shopapiweb02"}) || !slices.Equal(db.Names, []string{"web-shopapidb01"}) ||
			web.DependsOn == nil || !slices.Equal(*web.DependsOn, []string{"db"}) || db.DependsOn != nil {
			t.Fatalf("components = %+v / %+v", web, db)
		}

		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		var mu sync.Mutex
		var calls []string
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			cloudInitGuest(h, c)(env)
			env.OnActivity(h.acts.ReserveIP, mock.Anything, mock.Anything).Return(func(ctx context.Context, in provision.VMInput) (string, error) {
				mu.Lock()
				calls = append(calls, "ip "+in.VM.Name)
				mu.Unlock()
				return h.acts.ReserveIP(ctx, in)
			})
			env.OnActivity(h.acts.CloneVM, mock.Anything, mock.Anything).Return(func(ctx context.Context, in provision.VMInput) (string, error) {
				moref, err := h.acts.CloneVM(ctx, in)
				mu.Lock()
				calls = append(calls, "cloned "+in.VM.Name)
				mu.Unlock()
				return moref, err
			})
		})
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("request = %s %s", req.Status, errorOf(req))
		}
		// The database is cloned before either web server has an address.
		at := func(s string) int { return slices.Index(calls, s) }
		if at("cloned web-shopapidb01") < 0 || at("cloned web-shopapidb01") > at("ip web-shopapiweb01") ||
			at("cloned web-shopapidb01") > at("ip web-shopapiweb02") {
			t.Fatalf("build order = %v", calls)
		}

		var dbIP string
		_ = h.store.Pool.QueryRow(ctx, `select name from resources where request_id = $1 and type = 'ipam.address'
			and properties->>'hostname' = 'web-shopapidb01'`, req.Id).Scan(&dbIP)
		if dbIP == "" {
			t.Fatal("the database has no address")
		}
		morefs := map[string]string{}
		rows, err := h.store.Pool.Query(ctx, `select name, properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`, req.Id)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, moref string
			if err := rows.Scan(&name, &moref); err != nil {
				t.Fatal(err)
			}
			morefs[name] = moref
		}
		rows.Close()
		for i, name := range []string{"web-shopapiweb01", "web-shopapiweb02"} {
			var vm mo.VirtualMachine
			if err := property.DefaultCollector(c).RetrieveOne(ctx, types.ManagedObjectReference{Type: "VirtualMachine", Value: morefs[name]},
				[]string{"config.extraConfig"}, &vm); err != nil {
				t.Fatal(err)
			}
			var user string
			for _, o := range vm.Config.ExtraConfig {
				if ov := o.GetOptionValue(); ov.Key == "guestinfo.userdata" {
					b, _ := base64.StdEncoding.DecodeString(ov.Value.(string))
					user = string(b)
				}
			}
			for _, want := range []string{"db=" + dbIP + " web-shopapidb01.", "node=" + string(rune('0'+i)) + " of 2"} {
				if !strings.Contains(user, want) {
					t.Fatalf("%s's user data lacks %q:\n%s", name, want, user)
				}
			}
			wantTags := []string{"app/db-" + strings.ReplaceAll(dbIP, ".", "-"), "app/node" + string(rune('0'+i))}
			if tags, err := h.vc.VMTags(ctx, morefs[name]); err != nil || !slices.Equal(tags, wantTags) {
				t.Fatalf("%s's tags = %v (%v), want %v", name, tags, err, wantTags)
			}
		}

		// Saved with a cycle, or a reference to no component: refused, with
		// the line.
		refused := func(components []any) []api.FieldError {
			t.Helper()
			y := rhelYAML(h, "rhel9-refs", "RHEL 9 references", func(bp map[string]any) { bp["components"] = components })
			var problem api.Problem
			h.json("POST", "/api/v1/admin/catalog/import", admin, rawYAML(y), 422, &problem, "Content-Type", "application/yaml")
			return *problem.Errors
		}
		errs := refused([]any{
			map[string]any{"key": "web", "depends_on": []any{"db"}},
			map[string]any{"key": "db", "bindings": map[string]any{"tags": "['app/' + components.web.names[0]]"}}})
		if len(errs) != 1 || errs[0].Field != "/versions/0/blueprint/components/0/depends_on/0" || deref(errs[0].Code) != "cycle" ||
			!strings.Contains(errs[0].Message, "web after db after web") || !strings.Contains(errs[0].Message, "(line ") {
			t.Fatalf("a cycle: %+v", errs)
		}
		errs = refused([]any{map[string]any{"key": "web",
			"guest": map[string]any{"cloud_init": map[string]any{"user_data": "db={{ .components.ghost.addresses }}"}}}})
		if len(errs) != 1 || errs[0].Field != "/versions/0/blueprint/components/0/guest/cloud_init/user_data" ||
			deref(errs[0].Code) != "unknown_component" || !strings.Contains(errs[0].Message, "(line ") {
			t.Fatalf("an unknown component: %+v", errs)
		}
	})
}
