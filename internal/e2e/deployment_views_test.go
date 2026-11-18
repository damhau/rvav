package e2e

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// vmProps is a VM's properties as rvav records them.
func vmProps(vcenter, cluster, power, template, fqdn string, cpu, memory int, disks []int, nics map[string][]string) []byte {
	var ds, ns []string
	for i, gb := range disks {
		ds = append(ds, fmt.Sprintf(`{"label":"Hard disk %d","capacity_gb":%d,"datastore":"ds1"}`, i+1, gb))
	}
	for _, network := range slices.Sorted(func(yield func(string) bool) {
		for k := range nics {
			if !yield(k) {
				return
			}
		}
	}) {
		ips := `[]`
		if len(nics[network]) > 0 {
			ips = `["` + strings.Join(nics[network], `","`) + `"]`
		}
		ns = append(ns, fmt.Sprintf(`{"network":%q,"mac":"00:50:56:00:00:01","ip_addresses":%s,"dhcp":false}`, network, ips))
	}
	return fmt.Appendf(nil, `{"vm":{"moref":"vm-1","vcenter":%q,"cluster":%q,"power_state":%q,"template":%q,"fqdn":%q,
		"cpu_count":%d,"memory_mb":%d,"disks":[%s],"nics":[%s]}}`,
		vcenter, cluster, power, template, fqdn, cpu, memory, strings.Join(ds, ","), strings.Join(ns, ","))
}

// TestDeploymentViews: the deployments list filters by item, VM template,
// owner, environment and status (expired included), sorts by every column,
// and lists every project the caller sees and nothing else; saved views
// are each person's own; the Resources list finds VMs across projects
// (X1.2).
func TestDeploymentViews(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		soon, past := time.Now().Add(30*24*time.Hour), time.Now().Add(-24*time.Hour)
		for _, d := range []struct {
			id, name, status, project, env, item, owner string
			lease                                       *time.Time
		}{
			{"dep_web_a", "web-dev-app001", "ready", "prj_web", "dev", "rhel9-server", "tnguyen", &soon},
			{"dep_web_b", "web-prd-db001", "ready", "prj_web", "prd", "ubuntu-server", "lmartin", &past},
			{"dep_web_c", "web-tst-api001", "failed", "prj_web", "tst", "rhel9-server", "tnguyen", nil},
			{"dep_web_d", "web-dev-old001", "deleted", "prj_web", "dev", "rhel9-server", "tnguyen", nil},
			{"dep_data_a", "data-prd-pg001", "ready", "prj_data", "prd", "ubuntu-server", "sbrunner", nil},
			{"dep_fin_a", "fin-dev-app001", "ready", "prj_fin", "dev", "rhel9-server", "sbrunner", nil},
		} {
			if err := h.store.InsertDeployment(ctx, h.store.Pool, store.Deployment{ID: d.id, Name: d.name, Status: d.status,
				ProjectID: d.project, Environment: d.env, ItemID: "itm_" + d.item, VersionID: "ver_" + d.item + "_1",
				OwnerID: "usr_" + d.owner, LeaseExpiresAt: d.lease, ExpiryAction: "power_off_then_delete"}); err != nil {
				t.Fatal(err)
			}
		}
		for _, r := range []struct {
			id, deployment, typ, name string
			props                     []byte
		}{
			{"res_a1", "dep_web_a", "vsphere.vm", "web-dev-app001", vmProps("vc1", "c1", "on", "rhel9-tpl", "web-dev-app001.corp.example",
				2, 4096, []int{50, 100}, map[string][]string{"VLAN-10": {"10.0.0.11"}})},
			{"res_a2", "dep_web_a", "vsphere.vm", "web-dev-app002", vmProps("vc1", "c2", "off", "rhel9-tpl", "",
				4, 8192, []int{50}, map[string][]string{"VLAN-20": {"10.0.0.12"}})},
			{"res_a1_ip4", "dep_web_a", "ipam.address", "10.0.0.10", []byte(`{"address":"10.0.0.10","hostname":"web-dev-app001"}`)},
			{"res_a1_ip6", "dep_web_a", "ipam.address", "2001:db8::10", []byte(`{"address":"2001:db8::10","hostname":"web-dev-app001"}`)},
			{"res_b1", "dep_web_b", "vsphere.vm", "web-prd-db001", vmProps("vc2", "c3", "on", "ubuntu-tpl", "",
				8, 16384, []int{200}, map[string][]string{"VLAN-30": nil})},
			{"res_d1", "dep_web_d", "vsphere.vm", "web-dev-old001", vmProps("vc1", "c1", "on", "rhel9-tpl", "", 1, 1024, nil, nil)},
			{"res_data1", "dep_data_a", "vsphere.vm", "data-prd-pg001", vmProps("vc1", "c1", "on", "pg-tpl", "",
				4, 8192, []int{100}, map[string][]string{"VLAN-10": {"10.0.0.21"}})},
			{"res_fin1", "dep_fin_a", "vsphere.vm", "fin-dev-app001", vmProps("vc3", "c9", "on", "rhel9-tpl", "",
				2, 4096, []int{50}, map[string][]string{"VLAN-90": nil})},
		} {
			if _, err := h.store.UpsertResource(ctx, h.store.Pool, store.Resource{ID: r.id, DeploymentID: r.deployment,
				Type: r.typ, Name: r.name, ExternalID: new("ext-" + r.id), Properties: r.props}); err != nil {
				t.Fatal(err)
			}
		}

		deployments := func(user, query string) []string {
			t.Helper()
			var page api.DeploymentPage
			h.json("GET", "/api/v1/deployments?"+query, user, nil, 200, &page)
			var out []string
			for _, d := range page.Items {
				out = append(out, d.Id)
			}
			return out
		}
		for _, c := range []struct{ user, query string }{
			{"tnguyen", ""},
			{"tnguyen", "project_id=prj_data"},
			{"sbrunner", ""},
			{"sbrunner", "project_id=prj_web"},
			{"mkeller", ""},
		} {
			got := deployments(c.user, c.query)
			want := map[string][]string{
				"tnguyen":  {"dep_web_a", "dep_web_b", "dep_web_c"},
				"sbrunner": {"dep_data_a", "dep_fin_a"},
				"mkeller":  {"dep_data_a", "dep_fin_a", "dep_web_a", "dep_web_b", "dep_web_c"},
			}[c.user]
			if c.query != "" {
				want = nil
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s's deployments in every project, %q = %v, want %v", c.user, c.query, got, want)
			}
		}

		for query, want := range map[string][]string{
			"item=rhel9-server":               {"dep_web_a", "dep_web_c"},
			"item=itm_ubuntu-server":          {"dep_web_b"},
			"item=rhel9-server,ubuntu-server": {"dep_web_a", "dep_web_b", "dep_web_c"},
			"template=rhel9-tpl":              {"dep_web_a"},
			"template=pg-tpl":                 nil,
			"owner=usr_lmartin":               {"dep_web_b"},
			"owner=me":                        {"dep_web_a", "dep_web_c"},
			"environment=dev,prd":             {"dep_web_a", "dep_web_b"},
			"status=expired":                  {"dep_web_b"},
			"status=failed,expired":           {"dep_web_b", "dep_web_c"},
			"status=deleted":                  {"dep_web_d"},
			"status=ready&environment=prd":    {"dep_web_b"},
			"q=10.0.0.10":                     {"dep_web_a"},
		} {
			if got := deployments("tnguyen", query); !slices.Equal(got, want) {
				t.Errorf("deployments with %s = %v, want %v", query, got, want)
			}
		}

		for sort, want := range map[string][]string{
			"name":             {"dep_data_a", "dep_fin_a", "dep_web_a", "dep_web_b", "dep_web_c"},
			"-name":            {"dep_web_c", "dep_web_b", "dep_web_a", "dep_fin_a", "dep_data_a"},
			"status":           {"dep_web_c", "dep_data_a", "dep_fin_a", "dep_web_a", "dep_web_b"},
			"-status":          {"dep_web_b", "dep_web_a", "dep_fin_a", "dep_data_a", "dep_web_c"},
			"environment":      {"dep_fin_a", "dep_web_a", "dep_data_a", "dep_web_b", "dep_web_c"},
			"owner":            {"dep_web_b", "dep_data_a", "dep_fin_a", "dep_web_a", "dep_web_c"},
			"item":             {"dep_fin_a", "dep_web_a", "dep_web_c", "dep_data_a", "dep_web_b"},
			"project":          {"dep_data_a", "dep_fin_a", "dep_web_a", "dep_web_b", "dep_web_c"},
			"vm_count":         {"dep_web_c", "dep_data_a", "dep_fin_a", "dep_web_b", "dep_web_a"},
			"-vm_count":        {"dep_web_a", "dep_web_b", "dep_fin_a", "dep_data_a", "dep_web_c"},
			"lease_expires_at": {"dep_web_b", "dep_web_a", "dep_data_a", "dep_fin_a", "dep_web_c"},
			"created_at":       nil,
			"-created_at":      nil,
		} {
			all := deployments("mkeller", "limit=100&sort="+sort)
			if want != nil && !slices.Equal(all, want) {
				t.Errorf("deployments by %s = %v, want %v", sort, all, want)
			}
			var paged []string
			for cursor := ""; ; {
				var page api.DeploymentPage
				h.json("GET", "/api/v1/deployments?limit=2&sort="+sort+"&cursor="+url.QueryEscape(cursor), "mkeller", nil, 200, &page)
				for _, d := range page.Items {
					paged = append(paged, d.Id)
				}
				if page.NextCursor == nil {
					break
				}
				cursor = *page.NextCursor
			}
			if !slices.Equal(paged, all) {
				t.Errorf("deployments by %s, two by two = %v, want %v", sort, paged, all)
			}
		}

		var facets api.DeploymentFacets
		h.json("GET", "/api/v1/deployments/facets", "tnguyen", nil, 200, &facets)
		var items []string
		for _, i := range facets.Items {
			items = append(items, i.Slug)
		}
		if !slices.Equal(items, []string{"rhel9-server", "ubuntu-server"}) || !slices.Equal(facets.Templates, []string{"rhel9-tpl", "ubuntu-tpl"}) ||
			!slices.Equal(facets.Environments, []string{"dev", "prd", "tst"}) {
			t.Errorf("tnguyen's facets = %v %v %v", items, facets.Templates, facets.Environments)
		}
		h.json("GET", "/api/v1/deployments/facets", "sbrunner", nil, 200, &facets)
		if !slices.Equal(facets.Templates, []string{"pg-tpl", "rhel9-tpl"}) || !slices.Equal(facets.Environments, []string{"dev", "prd"}) {
			t.Errorf("sbrunner's facets = %v %v", facets.Templates, facets.Environments)
		}
		h.json("GET", "/api/v1/deployments/facets?project_id=prj_web", "sbrunner", nil, 200, &facets)
		if len(facets.Items)+len(facets.Templates)+len(facets.Environments) != 0 {
			t.Errorf("sbrunner's facets of web = %+v", facets)
		}

		var people api.UserList
		h.json("GET", "/api/v1/users?q=usr_lmartin", "tnguyen", nil, 200, &people)
		if len(people.Items) != 1 || people.Items[0].Id != "usr_lmartin" {
			t.Errorf("people with ID usr_lmartin = %+v", people.Items)
		}

		testSavedViews(t, h)
		testResources(t, h)
	})
}

func testSavedViews(t *testing.T, h *harness) {
	views := func(user, list string) []string {
		t.Helper()
		var out api.SavedViewList
		h.json("GET", "/api/v1/me/views?list="+list, user, nil, 200, &out)
		var names []string
		for _, v := range out.Items {
			names = append(names, v.Name)
		}
		return names
	}
	var prod api.SavedView
	h.json("POST", "/api/v1/me/views", "tnguyen", api.SavedViewWrite{List: api.ViewListDeployments, Name: " Production web ",
		Query: map[string]string{"environment": "prd", "sort": "-name", "layout": "cards"}}, 201, &prod)
	if prod.Name != "Production web" || prod.Query["environment"] != "prd" || !strings.HasPrefix(prod.Id, "viw_") {
		t.Errorf("saved view = %+v", prod)
	}
	h.json("POST", "/api/v1/me/views", "tnguyen", api.SavedViewWrite{List: api.ViewListDeployments, Name: "Expired",
		Query: map[string]string{"status": "expired"}}, 201, nil)
	h.json("POST", "/api/v1/me/views", "tnguyen", api.SavedViewWrite{List: api.ViewListResources, Name: "Production web",
		Query: map[string]string{"environment": "prd"}}, 201, nil)
	var taken struct{ Errors []api.FieldError }
	h.json("POST", "/api/v1/me/views", "tnguyen", api.SavedViewWrite{List: api.ViewListDeployments, Name: "Production web",
		Query: map[string]string{}}, 422, &taken)
	if len(taken.Errors) != 1 || deref(taken.Errors[0].Code) != "taken" {
		t.Errorf("a second view named Production web: %+v", taken.Errors)
	}
	h.json("POST", "/api/v1/me/views", "tnguyen", api.SavedViewWrite{List: api.ViewListDeployments, Name: "  ",
		Query: map[string]string{}}, 422, nil)

	if got := views("tnguyen", "deployments"); !slices.Equal(got, []string{"Expired", "Production web"}) {
		t.Errorf("tnguyen's deployment views = %v", got)
	}
	if got := views("tnguyen", "resources"); !slices.Equal(got, []string{"Production web"}) {
		t.Errorf("tnguyen's resource views = %v", got)
	}
	if got := views("lmartin", "deployments"); got != nil {
		t.Errorf("lmartin sees views %v", got)
	}
	h.json("GET", "/api/v1/me/views?list=nothing", "tnguyen", nil, 400, nil)

	rename := api.SavedViewWrite{List: api.ViewListDeployments, Name: "Web in production", Query: map[string]string{"environment": "prd"}}
	h.json("PUT", "/api/v1/me/views/"+prod.Id, "lmartin", rename, 404, nil)
	h.json("DELETE", "/api/v1/me/views/"+prod.Id, "lmartin", nil, 404, nil)
	var renamed api.SavedView
	h.json("PUT", "/api/v1/me/views/"+prod.Id, "tnguyen", rename, 200, &renamed)
	if renamed.Name != "Web in production" || len(renamed.Query) != 1 || !renamed.CreatedAt.Equal(prod.CreatedAt) {
		t.Errorf("renamed view = %+v", renamed)
	}
	rename.Name = "Expired"
	h.json("PUT", "/api/v1/me/views/"+prod.Id, "tnguyen", rename, 422, nil)
	h.json("DELETE", "/api/v1/me/views/"+prod.Id, "tnguyen", nil, 204, nil)
	h.json("DELETE", "/api/v1/me/views/"+prod.Id, "tnguyen", nil, 404, nil)
	if got := views("tnguyen", "deployments"); !slices.Equal(got, []string{"Expired"}) {
		t.Errorf("tnguyen's deployment views after the delete = %v", got)
	}
}

func testResources(t *testing.T, h *harness) {
	vms := func(user, query string) []string {
		t.Helper()
		var page api.VmPage
		h.json("GET", "/api/v1/resources/vms?"+query, user, nil, 200, &page)
		var out []string
		for _, v := range page.Items {
			out = append(out, v.Name)
		}
		return out
	}
	for _, c := range []struct {
		user, query string
		want        []string
	}{
		{"tnguyen", "", []string{"web-dev-app001", "web-dev-app002", "web-prd-db001"}},
		{"tnguyen", "project_id=prj_data", nil},
		{"sbrunner", "", []string{"data-prd-pg001", "fin-dev-app001"}},
		{"sbrunner", "q=web", nil},
		{"tnguyen", "environment=prd", []string{"web-prd-db001"}},
		{"tnguyen", "power_state=off", []string{"web-dev-app002"}},
		{"tnguyen", "vcenter=vc1", []string{"web-dev-app001", "web-dev-app002"}},
		{"tnguyen", "vcenter=vc1&cluster=c2", []string{"web-dev-app002"}},
		{"tnguyen", "network=VLAN-20", []string{"web-dev-app002"}},
		{"tnguyen", "q=10.0.0.10", []string{"web-dev-app001"}},
		{"tnguyen", "q=2001:DB8", []string{"web-dev-app001"}},
		{"tnguyen", "q=10.0.0.12", []string{"web-dev-app002"}},
		{"tnguyen", "q=corp.example", []string{"web-dev-app001"}},
		{"tnguyen", "sort=-cpu_count", []string{"web-prd-db001", "web-dev-app002", "web-dev-app001"}},
		{"tnguyen", "sort=disk_gb", []string{"web-dev-app002", "web-dev-app001", "web-prd-db001"}},
		{"tnguyen", "sort=-memory_mb", []string{"web-prd-db001", "web-dev-app002", "web-dev-app001"}},
		{"tnguyen", "sort=power_state", []string{"web-dev-app002", "web-dev-app001", "web-prd-db001"}},
		{"tnguyen", "sort=-environment", []string{"web-prd-db001", "web-dev-app002", "web-dev-app001"}},
		{"mkeller", "sort=project", []string{"data-prd-pg001", "fin-dev-app001", "web-dev-app001", "web-dev-app002", "web-prd-db001"}},
		{"mkeller", "sort=-deployment", []string{"web-prd-db001", "web-dev-app002", "web-dev-app001", "fin-dev-app001", "data-prd-pg001"}},
	} {
		if got := vms(c.user, c.query); !slices.Equal(got, c.want) {
			t.Errorf("%s's VMs with %q = %v, want %v", c.user, c.query, got, c.want)
		}
	}

	for _, sort := range []string{"name", "-name", "deployment", "project", "-environment", "power_state", "cpu_count",
		"-memory_mb", "disk_gb", "-disk_gb"} {
		all := vms("mkeller", "limit=100&sort="+sort)
		var paged []string
		for cursor := ""; ; {
			var page api.VmPage
			h.json("GET", "/api/v1/resources/vms?limit=2&sort="+sort+"&cursor="+url.QueryEscape(cursor), "mkeller", nil, 200, &page)
			for _, v := range page.Items {
				paged = append(paged, v.Name)
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		if len(all) != 5 || !slices.Equal(paged, all) {
			t.Errorf("VMs by %s, two by two = %v, want %v", sort, paged, all)
		}
	}

	var page api.VmPage
	h.json("GET", "/api/v1/resources/vms?q=web-dev-app001", "tnguyen", nil, 200, &page)
	if len(page.Items) != 1 {
		t.Fatalf("web-dev-app001: %+v", page.Items)
	}
	vm := page.Items[0]
	if !slices.Equal(vm.Addresses, []string{"10.0.0.10", "2001:db8::10", "10.0.0.11"}) || vm.Deployment.Id != "dep_web_a" ||
		vm.Deployment.Name != "web-dev-app001" || vm.Project.Code == "" || vm.Environment != "dev" ||
		vm.Vm.Cluster == nil || *vm.Vm.Cluster != "c1" || len(vm.Vm.Disks) != 2 {
		t.Errorf("web-dev-app001 = %+v", vm)
	}

	var facets api.VmFacets
	h.json("GET", "/api/v1/resources/vms/facets", "tnguyen", nil, 200, &facets)
	got := fmt.Sprint(facets.Vcenters, facets.Networks)
	if want := "[{[c1 c2] vc1} {[c3] vc2}] [VLAN-10 VLAN-20 VLAN-30]"; got != want {
		t.Errorf("tnguyen's VM facets = %s, want %s", got, want)
	}
	h.json("GET", "/api/v1/resources/vms/facets?project_id=prj_web", "sbrunner", nil, 200, &facets)
	if len(facets.Vcenters)+len(facets.Networks) != 0 {
		t.Errorf("sbrunner's VM facets of web = %+v", facets)
	}
}
