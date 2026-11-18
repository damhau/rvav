package e2e

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/vcenters"
)

// sourceOn is a catalogue template's one source: on the vCenter
// integration's when vcenter is "".
func sourceOn(vcenter, template string) []api.VmTemplateSourceWrite {
	return []api.VmTemplateSourceWrite{{Vcenter: vcenter, Template: template}}
}

const vc02 = "vc02.corp.example"

// zurichVCenter adds Zurich, another vcsim, as vc02 besides Geneva (the
// harness's, vc01), with a template of its own name, syncs both, and
// places the web project's dev VMs in both.
func zurichVCenter(t *testing.T, h *harness, ctx context.Context) *govmomi.Client {
	t.Helper()
	m := simulator.VPX()
	t.Cleanup(m.Remove)
	if err := m.Create(); err != nil {
		t.Fatal(err)
	}
	m.Service.TLS = new(tls.Config)
	m.Service.RegisterEndpoints = true
	srv := m.Service.NewServer()
	t.Cleanup(srv.Close)
	gc, err := govmomi.NewClient(ctx, srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	layout := zurich()
	layout.Templates[0].Name = "tpl-rhel9.4-zrh"
	if err := vsphere.Bootstrap(ctx, gc.Client, layout); err != nil {
		t.Fatal(err)
	}
	h.svc.Config.VCenter.Name, h.svc.Config.VCenter.URL = "vc01.corp.example", "https://vc01.corp.example/sdk"
	url := *srv.URL
	url.User = nil
	h.json("POST", "/api/v1/admin/vcenters", "mkeller", api.VCenterWrite{Name: vc02, Url: url.String(), Username: "user",
		Password: present.Ptr("pass"), Insecure: present.Ptr(true)}, 201, nil)
	set := &vcenters.Set{Store: h.store, Secrets: h.svc.Secrets, PrimaryName: "vc01.corp.example",
		Primary: func(context.Context) (vsphere.Driver, error) { return h.vc, nil }}
	h.acts.VCenters = set
	h.svc.VCenter = func(ctx context.Context, name string) (core.ConsoleSource, error) { return set.Get(ctx, name) }
	sync := &inventory.Syncer{Store: h.store, VCenters: set, Events: h.acts.Events, Log: h.acts.Log}
	if err := sync.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	h.json("POST", "/api/v1/admin/placement-rules", "mkeller", api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
		Clusters: []string{"ZRH-DEV-*"}, Datastores: []string{"vsan-zrh-*"}, Networks: []string{"dvpg-app-dev-*"},
		Folder: "/ZRH/vm/rvav/web/dev", Vcenter: present.Ptr(vc02)}, 201, nil)
	return gc
}

// imageItem publishes an item whose template is the catalogue template
// key, its cluster and datastore chosen by rvav.
func imageItem(h *harness, slug, key string, guest map[string]any) api.AdminCatalogItem {
	h.t.Helper()
	def := guestItem("", guest)
	b := def.Blueprint["bindings"].(map[string]any)
	delete(b, "cluster")
	delete(b, "datastore")
	b["template"] = `"` + key + `"`
	return publishItem(h, slug, def)
}

// TestImagesPerVCenter: a catalogue template has a source per vCenter;
// rvav chooses the cluster among the vCenters it has one in, then clones
// that vCenter's; a vCenter without one is ruled out, and said so
// (T1.4, ADR-0037).
func TestImagesPerVCenter(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		gc := zurichVCenter(t, h, ctx)

		// Sources are checked: a vCenter rvav manages, once each.
		var problem struct{ Errors []api.FieldError }
		write := api.VmTemplateWrite{Key: "rhel9", Label: "RHEL 9.4", OsFamily: api.VmTemplateWriteOsFamilyLinux,
			Offers: &[]api.Offer{{}}, Sources: []api.VmTemplateSourceWrite{}}
		h.json("POST", "/api/v1/admin/templates", admin, write, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/sources") {
			t.Fatalf("no source = %+v", problem.Errors)
		}
		write.Sources = []api.VmTemplateSourceWrite{{Vcenter: "vc01.corp.example", Template: "tpl-rhel9.4-2026.10"},
			{Vcenter: "", Template: "tpl-rhel9.4-2026.10"}, {Vcenter: "vc03.corp.example", Template: "tpl-rhel9.4-zrh"}}
		h.json("POST", "/api/v1/admin/templates", admin, write, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Contains(f, "/sources/1/vcenter") || !slices.Contains(f, "/sources/2/vcenter") {
			t.Fatalf("bad sources = %+v", problem.Errors)
		}
		write.Sources = []api.VmTemplateSourceWrite{{Vcenter: "vc01.corp.example", Template: "tpl-rhel9.4-2026.10"},
			{Vcenter: vc02, Template: "tpl-rhel9.4-zrh"}}
		var tpl api.VmTemplate
		h.json("POST", "/api/v1/admin/templates", admin, write, 201, &tpl)
		if len(tpl.Sources) != 2 || tpl.Sources[0].Vcenter != "vc01.corp.example" || tpl.Sources[1].Vcenter != vc02 ||
			!*tpl.Sources[0].InInventory || !*tpl.Sources[1].InInventory {
			t.Fatalf("template = %+v", tpl.Sources)
		}

		// Both have it: Zurich's cluster has the most free memory, and its
		// own template is cloned there.
		item := imageItem(h, "rhel-anywhere", "rhel9", map[string]any{"os_family": "linux", "domain": "corp.example"})
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "zrh", "cluster": "-", "datastore": "-", "network": "dvpg-app-dev-vl310"}}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		comp := (*pv.Components)[0]
		if pl := comp.Placement; !pv.Valid || deref(pl.Vcenter) != vc02 || deref(comp.Template) != "tpl-rhel9.4-zrh" ||
			pl.Image == nil || pl.Image.Key != "rhel9" || len(pl.Image.Excluded) != 0 {
			t.Fatalf("preview = %+v / %+v / %+v", pv.Errors, pl, comp.Template)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		done := h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		if !vmExists(ctx, gc.Client, "web-dev-zrh01") {
			t.Fatal("the VM isn't in Zurich")
		}
		d := &vmDeployment{h: h, id: (*done.DeploymentIds)[0], viewer: "tnguyen"}
		if vm := d.vm(); vm.Vcenter != vc02 || deref(vm.Template) != "tpl-rhel9.4-zrh" {
			t.Fatalf("VM = %s from %s", vm.Vcenter, deref(vm.Template))
		}

		// Only in Geneva: Zurich is ruled out, and the preview says so.
		write.Sources = write.Sources[:1]
		h.json("PUT", "/api/v1/admin/templates/rhel9", admin, write, 200, nil)
		draft.Inputs["role"] = "gva"
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		comp = (*pv.Components)[0]
		if pl := comp.Placement; !pv.Valid || deref(pl.Vcenter) != "vc01.corp.example" || deref(pl.Cluster) != "GVA-DEV-CL01" ||
			deref(comp.Template) != "tpl-rhel9.4-2026.10" || pl.Image == nil || !slices.Equal(pl.Image.Excluded, []string{vc02}) {
			t.Fatalf("Geneva only = %+v / %+v", pv.Errors, pl)
		}
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		if !vmExists(ctx, c, "web-dev-gva01") {
			t.Fatal("the VM isn't in Geneva")
		}

		// Only in Zurich, and Geneva's cluster given: refused, and why.
		write.Sources = []api.VmTemplateSourceWrite{{Vcenter: vc02, Template: "tpl-rhel9.4-zrh"}}
		h.json("PUT", "/api/v1/admin/templates/rhel9", admin, write, 200, nil)
		given := guestItem("", map[string]any{"os_family": "linux", "domain": "corp.example"})
		given.Blueprint["bindings"].(map[string]any)["template"] = `"rhel9"`
		var gi api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "rhel-given", Name: "rhel-given",
			Summary: "Given cluster.", Category: "compute", Type: api.VmBlueprint, Definition: &given}, 201, &gi)
		h.json("POST", "/api/v1/admin/catalog/items/rhel-given/versions/"+gi.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/rhel-given/entitlements", admin,
			api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		h.json("POST", "/api/v1/requests/preview", "tnguyen", api.RequestDraft{CatalogItemId: gi.Id, VersionId: gi.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "x", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 200, &pv)
		if pv.Valid || !hasError(pv.Errors, "/inputs/cluster", "RHEL 9.4 has no template in vCenter vc01.corp.example, where cluster GVA-DEV-CL01 is. Ask an administrator.") {
			t.Fatalf("given cluster = %+v", pv.Errors)
		}

		// In no vCenter allowed here: nothing to choose, and why.
		write.Sources = []api.VmTemplateSourceWrite{{Vcenter: vc02, Template: "tpl-rhel9.4-zrh"}}
		var rules struct{ Items []api.PlacementRule }
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		for _, r := range rules.Items {
			if r.Environment == "dev" && r.Vcenter == vc02 {
				h.json("DELETE", "/api/v1/admin/placement-rules/"+r.Id, admin, nil, 204, nil)
			}
		}
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if pv.Valid || !hasError(pv.Errors, "", "RHEL 9.4 has no template in vCenter vc01.corp.example now, where the clusters allowed here are. Ask an administrator.") {
			t.Fatalf("nowhere = %+v", pv.Errors)
		}
	})
}

// TestImageUserDataAndConstraints: a catalogue template's own cloud-init
// user data is merged with the item's, and its constraint tags are added
// to the item's (T1.4, ADR-0037).
func TestImageUserDataAndConstraints(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		h.json("POST", "/api/v1/admin/placement-rules", admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Clusters: []string{"GVA-PRD-*"}, Datastores: []string{"vsan-gva-prd-*"}, Networks: []string{"dvpg-app-prd-*"},
			Folder: "/GVA/vm/rvav/web/dev"}, 201, nil)

		// Checked at save: Linux only, tags as they are.
		var problem struct{ Errors []api.FieldError }
		write := api.VmTemplateWrite{Key: "rhel9-ci", Label: "RHEL 9.4 cloud-init", OsFamily: api.VmTemplateWriteOsFamilyWindows,
			Offers: &[]api.Offer{{}}, Sources: sourceOn("", "tpl-rhel9.4-2026.10"),
			UserData:    present.Ptr("#cloud-config\npackages: [chrony]\nwrite_files:\n  - path: /etc/rvav-image\n    content: {{ .vm.name | json }}\n"),
			Constraints: &api.TemplateConstraints{Compute: []string{"${inputs.tier}"}, Storage: []string{}, Network: []string{}}}
		h.json("POST", "/api/v1/admin/templates", admin, write, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Contains(f, "/user_data") || !slices.Contains(f, "/constraints/compute/0") {
			t.Fatalf("checks = %+v", problem.Errors)
		}
		write.OsFamily = api.VmTemplateWriteOsFamilyLinux
		write.Constraints.Compute = []string{"rvav-tier/gold"}
		var tpl api.VmTemplate
		h.json("POST", "/api/v1/admin/templates", admin, write, 201, &tpl)
		if tpl.UserData != strings.TrimSpace(*write.UserData) || !slices.Equal(tpl.Constraints.Compute, []string{"rvav-tier/gold"}) {
			t.Fatalf("template = %+v", tpl)
		}

		// Only the gold cluster, by the template's constraint; the item's own
		// user data merged with the template's.
		item := imageItem(h, "rhel-ci", "rhel9-ci", map[string]any{"os_family": "linux", "domain": "corp.example",
			"cloud_init": map[string]any{"user_data": "#cloud-config\npackages: [nginx]\nfqdn: {{ .vm.fqdn }}"}})
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "ci", "cluster": "-", "datastore": "-", "network": "dvpg-app-prd-vl220"}}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		pl := (*pv.Components)[0].Placement
		if !pv.Valid || deref(pl.Cluster) != "GVA-PRD-CL01" || pl.Constraints == nil || len(*pl.Constraints) != 1 {
			t.Fatalf("preview = %+v / %+v", pv.Errors, pl)
		}
		if k := (*pl.Constraints)[0]; k.Tag != "rvav-tier/gold" || !k.Met || len(k.Excluded) == 0 || slices.Contains(k.Excluded, "GVA-PRD-CL01") {
			t.Fatalf("constraint = %+v", k)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, cloudInitGuest(h, c))
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		var list struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &list)
		if i := slices.IndexFunc(list.Items, func(s api.Step) bool { return s.Key == "vsphere.customize" }); i < 0 ||
			!strings.HasSuffix(deref(list.Items[i].Detail), "the template's user data merged with the item's") {
			t.Fatalf("steps = %+v", list.Items)
		}
		var moref string
		_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`,
			req.Id).Scan(&moref)
		var vm mo.VirtualMachine
		if err := property.DefaultCollector(c).RetrieveOne(ctx, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref},
			[]string{"config.extraConfig"}, &vm); err != nil {
			t.Fatal(err)
		}
		var user []byte
		for _, o := range vm.Config.ExtraConfig {
			if ov := o.GetOptionValue(); ov.Key == "guestinfo.userdata" {
				user, _ = base64.StdEncoding.DecodeString(ov.Value.(string))
			}
		}
		var merged map[string]any
		if err := yaml.Unmarshal(user, &merged); err != nil || !strings.HasPrefix(string(user), "#cloud-config\n") ||
			!reflect.DeepEqual(merged, map[string]any{"packages": []any{"chrony", "nginx"}, "fqdn": "web-dev-ci01.corp.example",
				"write_files": []any{map[string]any{"path": "/etc/rvav-image", "content": "web-dev-ci01"}}}) {
			t.Fatalf("user data %q: %v", user, err)
		}

		// An item that names a customization specification can't use it.
		spec := imageItem(h, "rhel-spec", "rhel9-ci", map[string]any{"os_family": "linux", "customization_spec": "linux-base"})
		draft.CatalogItemId, draft.VersionId = spec.Id, spec.DraftVersion.Id
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool {
			return strings.Contains(e.Message, "RHEL 9.4 cloud-init configures its VMs with cloud-init, and this item names the customization specification linux-base.")
		}) {
			t.Fatalf("with a specification = %+v", pv.Errors)
		}
	})
}

func hasError(errs []api.FieldError, field, msg string) bool {
	return slices.ContainsFunc(errs, func(e api.FieldError) bool { return e.Field == field && e.Message == msg })
}
