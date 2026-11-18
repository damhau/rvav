package e2e

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vapi/tags"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/vra"
)

// tagObjects attaches category/tag to vCenter clusters, datastores,
// networks, hosts and datastore clusters through the tagging service,
// making the category and the tag as needed.
func tagObjects(t *testing.T, ctx context.Context, c *vim25.Client, tag string, objects ...string) {
	t.Helper()
	rc := rest.NewClient(c)
	if err := rc.Login(ctx, url.UserPassword("user", "pass")); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Logout(context.Background()) }()
	m := tags.NewManager(rc)
	category, name, _ := strings.Cut(tag, "/")
	cat, err := m.GetCategory(ctx, category)
	catID := ""
	if err == nil {
		catID = cat.ID
	} else if catID, err = m.CreateCategory(ctx, &tags.Category{Name: category, Cardinality: "MULTIPLE",
		AssociableTypes: []string{"ClusterComputeResource", "HostSystem", "Datastore", "StoragePod", "Network", "DistributedVirtualPortgroup"}}); err != nil {
		t.Fatal(err)
	}
	tagID, err := m.CreateTag(ctx, &tags.Tag{Name: name, CategoryID: catID})
	if err != nil {
		t.Fatal(err)
	}
	finder := find.NewFinder(c, true)
	for _, o := range objects {
		var ref types.ManagedObjectReference
		if cc, err := finder.ClusterComputeResource(ctx, "/GVA/host/"+o); err == nil {
			ref = cc.Reference()
		} else if ds, err := finder.Datastore(ctx, "/GVA/datastore/"+o); err == nil {
			ref = ds.Reference()
		} else if n, err := finder.Network(ctx, "/GVA/network/"+o); err == nil {
			ref = n.Reference()
		} else if host, err := finder.HostSystem(ctx, "/GVA/host/*/"+o); err == nil {
			ref = host.Reference()
		} else if pod, err := finder.DatastoreCluster(ctx, "/GVA/datastore/"+o); err == nil {
			ref = pod.Reference()
		} else {
			t.Fatalf("no cluster, datastore, network, host or datastore cluster %s", o)
		}
		if err := m.AttachTag(ctx, tagID, ref); err != nil {
			t.Fatal(err)
		}
	}
}

// taggedItem places its VM by constraint tags alone (ADR-0035): a
// production cluster outside the PCI zone, a datastore with the backup the
// form asks for, platinum if any, and a network outside the DMZ.
func taggedItem() api.CatalogVersionDefinition {
	fields := []string{"role", "backup"}
	def := api.CatalogVersionDefinition{
		FormSchema: map[string]any{"type": "object", "required": fields, "properties": map[string]any{
			"role": map[string]any{"type": "string", "minLength": 1}, "backup": map[string]any{"type": "string", "minLength": 1}}},
		UiSchema: api.UiSchema{Fields: map[string]api.UiField{"role": {Widget: api.UiWidgetText, Label: "Role"},
			"backup": {Widget: api.UiWidgetText, Label: "Backup"}}},
		InitialValues: map[string]any{},
		Blueprint: map[string]any{"kind": "vm", "naming": "{project}-{environment}-{inputs.role}{seq:02}", "template": "tpl-rhel9.4-2026.10",
			"guest":    map[string]any{"os_family": "linux", "domain": "corp.example"},
			"bindings": map[string]any{"vcpu": "2", "memory_gb": "4", "disk_gb": "60"},
			"constraints": map[string]any{
				"compute": []any{"rvav-env/prod", "!rvav-zone/pci"},
				"storage": []any{"rvav-backup/${inputs.backup}", "rvav-tier/platinum:soft"},
				"network": []any{"!rvav-zone/dmz"},
			}},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "VM", Fields: fields}}
	return def
}

// vraTagged is taggedItem's constraints as vRA writes them.
const vraTagged = `formatVersion: 1
inputs:
  role:
    type: string
  backup:
    type: string
resources:
  vm:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
      constraints:
        - tag: rvav-env:prod
        - tag: '!rvav-zone:pci'
      storage:
        constraints:
          - tag: rvav-backup:${input.backup}
          - tag: rvav-tier:platinum:soft
      networks:
        - network: ${resource.net.id}
  net:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
      constraints:
        - tag: '!rvav-zone:dmz'
`

// TestConstraintTags places a VM by the vSphere tags of the clusters,
// datastores and networks its placement rules allow: hard constraints rule
// out, a negation too, a soft one nothing meets is let go with a note on
// the timeline, and a hard one nothing meets fails before any clone. A vRA
// template with the same constraint tags, imported, is placed the same way
// (T1.2, ADR-0035).
func TestConstraintTags(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		// Both production clusters are prod, the second one in the PCI zone;
		// a datastore of each has a daily backup.
		tagObjects(t, ctx, c, "rvav-env/prod", "GVA-PRD-CL01", "GVA-PRD-CL02")
		tagObjects(t, ctx, c, "rvav-zone/pci", "GVA-PRD-CL02")
		tagObjects(t, ctx, c, "rvav-backup/daily", "nfs-gva-silver-02", "vsan-gva-prd-cl02")
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		// Web's dev VMs may go to the production clusters too.
		h.json("POST", "/api/v1/admin/placement-rules", admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Clusters: []string{"GVA-PRD-*"}, Datastores: []string{"vsan-gva-prd-*", "nfs-gva-silver-*"}, Networks: []string{"dvpg-*-prd-*"},
			Folder: "/GVA/vm/rvav/web/dev"}, 201, nil)

		// Saved with a warning: no datastore is platinum.
		def := taggedItem()
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "tagged", Name: "Placed by tags",
			Summary: "Placed by constraint tags.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		var version api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/tagged/versions/"+item.DraftVersion.Id, admin, nil, 200, &version)
		if version.Warnings == nil || len(*version.Warnings) != 1 || (*version.Warnings)[0].Field != "/definition/blueprint/constraints/storage/1" ||
			deref((*version.Warnings)[0].Code) != "unknown_tag" {
			t.Fatalf("warnings = %+v", version.Warnings)
		}
		h.json("POST", "/api/v1/admin/catalog/items/tagged/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/tagged/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		// Written the vRA way: refused at save.
		vraWay := taggedItem()
		vraWay.Blueprint["constraints"] = map[string]any{"compute": []any{"env:prod"}}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "vra-way", Name: "vRA way",
			Summary: "Constraints written as in vRA.", Category: "compute", Type: api.VmBlueprint, Definition: &vraWay}, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/definition/blueprint/constraints/compute/0") {
			t.Fatalf("vRA's syntax = %+v", problem.Errors)
		}

		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "tag", "backup": "daily"}}
		// The preview says which constraint chose or ruled out what.
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if !pv.Valid {
			t.Fatalf("preview errors = %+v", pv.Errors)
		}
		pl := (*pv.Components)[0].Placement
		if deref(pl.Cluster) != "GVA-PRD-CL01" || deref(pl.Datastore) != "nfs-gva-silver-02" || pl.Constraints == nil {
			t.Fatalf("placement = %+v", pl)
		}
		said := map[string]api.PlacementConstraint{}
		for _, k := range *pl.Constraints {
			said[k.Tag] = k
		}
		if k := said["rvav-env/prod"]; !k.Met || !slices.Equal(k.Excluded, []string{"GVA-DEV-CL01"}) {
			t.Errorf("hard compute = %+v", k)
		}
		if k := said["rvav-zone/pci"]; !k.Negated || !k.Met || !slices.Equal(k.Excluded, []string{"GVA-PRD-CL02"}) {
			t.Errorf("negation = %+v", k)
		}
		if k := said["rvav-backup/daily"]; !k.Met || deref(k.Object) != "nfs-gva-silver-02" || !slices.Equal(k.Excluded, []string{"vsan-gva-prd-cl01"}) {
			t.Errorf("hard storage = %+v", k)
		}
		if k := said["rvav-tier/platinum"]; !k.Soft || k.Met || deref(k.Object) != "nfs-gva-silver-02" {
			t.Errorf("soft storage = %+v", k)
		}
		if k := said["rvav-zone/dmz"]; k.Kind != api.Network || !k.Met || deref(k.Object) != "dvpg-app-prd-vl220" ||
			!slices.Contains(k.Excluded, "dvpg-web-prd-vl210") {
			t.Errorf("network = %+v", k)
		}

		// Built where the constraints said, with the soft one's fallback on
		// the timeline.
		placedAsSaid := func(draft api.RequestDraft) {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
			if req.Status != api.RequestStatusSucceeded {
				t.Fatalf("request = %s %s", req.Status, errorOf(req))
			}
			vm := (&vmDeployment{h: h, id: (*req.DeploymentIds)[0], viewer: "tnguyen"}).vm()
			if deref(vm.Cluster) != "GVA-PRD-CL01" || len(vm.Disks) == 0 || deref(vm.Disks[0].Datastore) != "nfs-gva-silver-02" ||
				len(vm.Nics) == 0 || vm.Nics[0].Network != "dvpg-app-prd-vl220" {
				t.Fatalf("VM landed on %s / %+v / %+v", deref(vm.Cluster), vm.Disks, vm.Nics)
			}
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &steps)
			i := slices.IndexFunc(steps.Items, func(s api.Step) bool { return s.Key == "placement" })
			if i != 1 || steps.Items[i].Status != api.StepStatusDone || !strings.Contains(deref(steps.Items[i].Detail),
				"Placed by rvav-env/prod, !rvav-zone/pci, rvav-backup/daily, !rvav-zone/dmz; soft constraint rvav-tier/platinum not met: placed on nfs-gva-silver-02 without it") {
				t.Fatalf("timeline = %+v", steps.Items)
			}
		}
		placedAsSaid(draft)

		// The same constraints in a vRA template, imported: the form asks
		// for no cluster, datastore or network, and the VM goes to the same
		// place.
		e := vra.SampleEstate(time.Now())
		e.Blueprints = []vra.Blueprint{{ID: "bp-tagged", Name: "Tagged in vRA", Versions: []vra.BlueprintVersion{
			{Version: "1", Status: "RELEASED", Content: vraTagged}}}}
		res := importVRAItems(h, &e)["bp-tagged"]
		imported := api.RequestDraft{CatalogItemId: res.ItemId, VersionId: publishVRAItem(h, res), ProjectId: "prj_web",
			Environment: "dev", Inputs: map[string]any{"role": "vra", "backup": "daily"}}
		placedAsSaid(imported)

		// A backup no datastore has: refused before any clone, saying why.
		weekly := draft
		weekly.Inputs = map[string]any{"role": "weekly", "backup": "weekly"}
		h.json("POST", "/api/v1/requests/preview", "tnguyen", weekly, 200, &pv)
		want := "This item needs a datastore tagged rvav-backup/weekly, and no cluster allowed here has one."
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return e.Message == want }) {
			t.Fatalf("weekly preview = %+v", pv.Errors)
		}
		h.json("POST", "/api/v1/requests", "tnguyen", weekly, 422, &problem, "Idempotency-Key", uuid.NewString())
		if !slices.ContainsFunc(problem.Errors, func(e api.FieldError) bool { return e.Message == want }) {
			t.Fatalf("weekly = %+v", problem.Errors)
		}
		if vmExists(ctx, c, "web-dev-weekly01") {
			t.Fatal("a VM was cloned")
		}
	})
}
