package e2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/ssh"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/vra"
)

// t1Template is a vRA cloud template of a database on DHCP and the web
// servers that read its address, named and tagged by their index.
const t1Template = `formatVersion: 1
inputs:
  environment:
    type: string
    title: Environment
    enum: [dev, tst, prd]
  app:
    type: string
    title: Application
    pattern: ^[a-z]{2,12}$
resources:
  db:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      cpuCount: 2
      totalMemoryMB: 4096
      networks:
        - network: ${resource.app_net.id}
          assignment: dynamic
      constraints:
        - tag: env:${input.environment}
        - tag: '!t1-zone:pci'
      storage:
        constraints:
          - tag: t1-tier:gold:soft
      remoteAccess:
        authentication: generatedPublicPrivateKey
        username: ops
      cloudConfig: |
        #cloud-config
        hostname: ${self.resourceName}
  web:
    type: Cloud.vSphere.Machine
    properties:
      count: 2
      name: shop-web-${count.index}
      image: rhel9
      cpuCount: 2
      totalMemoryMB: 4096
      networks:
        - network: ${resource.app_net.id}
      constraints:
        - tag: env:${input.environment}
        - tag: '!t1-zone:pci'
      storage:
        constraints:
          - tag: t1-tier:gold:soft
      tags:
        - key: app
          value: node${count.index}
      remoteAccess:
        authentication: generatedPublicPrivateKey
        username: ops
      cloudConfig: |
        #cloud-config
        hostname: ${self.resourceName}
        write_files:
          - path: /etc/shop.conf
            content: |
              db=${resource.db.address}
              node=${count.index}
  app_net:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
      constraints:
        - tag: net:app
`

// t1YAML is the template written in rvav's own catalog YAML: what both
// components share once, on the blueprint.
const t1YAML = `apiVersion: rvav.dev/v1
kind: CatalogItem
metadata:
  slug: shop-yaml
  name: Shop, in rvav's YAML
  summary: A database on DHCP and the web servers that read its address.
  category: compute
  type: vm_blueprint
  icon: server
versions:
  - number: "1.0"
    form_schema:
      type: object
      required: [app]
      properties:
        app: {type: string, pattern: "^[a-z]{2,12}$"}
    ui_schema:
      fields:
        app: {widget: text, label: Application}
      layout:
        kind: page
        sections:
          - {key: main, title: Shop, fields: [app]}
    blueprint:
      kind: vm
      deployment_name: "{project}-{environment}-shop-yaml"
      template: tpl-rhel9.4-2026.10
      bindings: {vcpu: "2", memory_gb: "4"}
      constraints:
        compute: ["env/${environment}", "!t1-zone/pci"]
        storage: ["t1-tier/gold:soft"]
        network: [net/app]
      guest:
        os_family: linux
        domain: ""
        remote_access: {kind: generated_key, username: ops}
      components:
        - key: db
          naming: "{project}-{environment}-db{seq:02}"
          ipam: dhcp
          guest:
            cloud_init:
              user_data: |
                #cloud-config
                hostname: {{ .vm.name }}
        - key: web
          count: 2
          naming: "{project}-{environment}-web{seq:02}"
          bindings:
            tags: "['app/node' + string(self.index)]"
          guest:
            cloud_init:
              user_data: |
                #cloud-config
                hostname: {{ .vm.name }}
                write_files:
                  - path: /etc/shop.conf
                    content: |
                      db={{ (index .components.db.vms 0).address }}
                      node={{ .vm.index }}
`

// t1Built is what a request of the acceptance's item built.
type t1Built struct {
	req        api.Request
	deployment string
	preview    map[string]api.PreviewComponent
	steps      map[string]api.Step
}

// t1VM is one of its VMs as vCenter and its guest have it.
type t1VM struct {
	moref, host, pool string
	datastores, tags  []string
	user              map[string]any
}

// TestT1Acceptance is the templates' acceptance (T1.7, #140), through the
// API, a real Temporal dev server and worker, and vcsim. A vRA cloud
// template, read from vRA (the fake) and imported, and the same written in
// rvav's YAML: a database on DHCP and two web servers whose cloud-init
// reads its address and whose tag numbers them by index, placed by hard,
// soft and negated constraint tags within a placement rule naming a
// resource pool and a datastore cluster, with a key pair rvav makes. Both
// build alike: on the hosts the negated tag leaves, in the pool, on the
// datastore cluster's member the soft tag prefers, the database before the
// web servers, which get its address; the owner downloads the key once.
func TestT1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, queue = "mkeller", "tnguyen", "rvav-t1-e2e"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		h.acts.VSphere = cloudInitDriver{Driver: h.acts.VSphere, c: c}
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		// A host of the development cluster is in the PCI zone, and one
		// datastore of its datastore cluster is gold. Web Platform's
		// development VMs go in the cluster's rvav/dev pool, on the
		// datastore cluster.
		tagObjects(t, ctx, c, "t1-zone/pci", "esx-gva-dev-01.corp.example")
		tagObjects(t, ctx, c, "t1-tier/gold", "dsc-gva-dev-01/nfs-gva-dev-sdrs-02")
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		rule := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })]
		pool, pod := "rvav/dev", "dsc-gva-dev-01"
		h.json("PUT", "/api/v1/admin/placement-rules/"+rule.Id, admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Clusters: []string{"GVA-DEV-*"}, Datastores: []string{}, DatastoreCluster: &pod, Networks: []string{"dvpg-app-dev-*"},
			Folder: "/GVA/vm/rvav/web/dev", ResourcePool: &pool}, 200, nil)

		// The template, read from vRA and imported.
		e := vra.SampleEstate(time.Now())
		e.Blueprints = append(e.Blueprints, vra.Blueprint{ID: "bp-shop", Name: "Shop", ProjectID: "vra-prj-web",
			ProjectName: "Web Platform", Status: "RELEASED", Content: t1Template,
			Versions: []vra.BlueprintVersion{{ID: "bp-shop-v1", BlueprintID: "bp-shop", Version: "1", Status: "RELEASED", Content: t1Template}}})
		e.CatalogItems = append(e.CatalogItems, vra.CatalogItem{ID: "bp-shop", Name: "Shop", Type: vra.Ref{ID: "com.vmw.blueprint"},
			SourceID: "cs-templates", ProjectIDs: []string{"vra-prj-web"}, BulkRequestLimit: 1})
		fake := httptest.NewServer(vra.NewFakeServer(e))
		defer fake.Close()
		h.svc.Config.VRA.URL, h.svc.Config.VRA.Token = fake.URL, vra.FakeRefreshToken
		var snap api.VraSnapshot
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, nil, 201, &snap)
		if snap.Source != "vra" {
			t.Fatalf("snapshot = %+v", snap)
		}
		var p api.VraPlan
		h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
		shop := p.Items[slices.IndexFunc(p.Items, func(x api.VraItemPlan) bool { return x.BlueprintId == "bp-shop" })]
		// Its machine name by index is the one thing left out: rvav's naming
		// numbers the VMs across deployments.
		var unmapped []string
		for _, n := range shop.Notes {
			if n.Level == api.VraNoteLevelUnmapped {
				unmapped = append(unmapped, n.Where)
			}
		}
		if shop.Action != api.VraItemPlanActionCreate || shop.Status != api.VraItemPlanStatusPartial ||
			!slices.Equal(unmapped, []string{"resources.web.name"}) {
			t.Fatalf("plan = %+v", shop)
		}
		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if !slices.Contains(res.Created, "Shop") || len(res.Failed) != 0 {
			t.Fatalf("import = %+v / %+v", res.Created, res.Failed)
		}
		var imported api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/"+shop.Slug, admin, nil, 200, &imported)

		// The same in rvav's YAML.
		var written api.CatalogImportResult
		h.json("POST", "/api/v1/admin/catalog/import", admin, rawYAML([]byte(t1YAML)), 200, &written, "Content-Type", "application/yaml")
		if !written.Created {
			t.Fatalf("YAML import = %+v", written)
		}
		var byHand api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/"+written.ItemId, admin, nil, 200, &byHand)

		build := func(it api.AdminCatalogItem) t1Built {
			t.Helper()
			h.json("POST", "/api/v1/admin/catalog/items/"+it.Slug+"/versions/"+it.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+it.Slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			draft := api.RequestDraft{CatalogItemId: it.Id, VersionId: it.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"app": "shop"}}
			var pv api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", owner, draft, 200, &pv)
			if !pv.Valid || pv.Components == nil {
				t.Fatalf("%s: preview errors = %+v", it.Slug, pv.Errors)
			}
			b := t1Built{preview: map[string]api.PreviewComponent{}, steps: map[string]api.Step{}}
			for _, pc := range *pv.Components {
				b.preview[pc.Key] = pc
			}
			h.json("POST", "/api/v1/requests", owner, draft, 202, &b.req, "Idempotency-Key", uuid.NewString())
			b.req = h.waitStatus(b.req.Id, owner, api.RequestStatusSucceeded)
			wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := tc.GetWorkflow(wctx, provision.WorkflowID(b.req.Id), "").Get(wctx, nil); err != nil {
				t.Fatal(err)
			}
			b.deployment = (*b.req.DeploymentIds)[0]
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+b.req.Id+"/steps", owner, nil, 200, &steps)
			var walk func([]api.Step)
			walk = func(list []api.Step) {
				for _, s := range list {
					b.steps[s.Key] = s
					walk(s.Children)
				}
			}
			walk(steps.Items)
			return b
		}
		vmOf := func(b t1Built, name string) t1VM {
			t.Helper()
			var v t1VM
			if err := h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1
				and type = 'vsphere.vm' and name = $2`, b.req.Id, name).Scan(&v.moref); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			v.host, v.pool, v.datastores = vmPlacement(t, ctx, c, name)
			tags, err := h.vc.VMTags(ctx, v.moref)
			if err != nil {
				t.Fatal(err)
			}
			v.tags = tags
			info, err := guestInfoOf(ctx, c, types.ManagedObjectReference{Type: "VirtualMachine", Value: v.moref})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := base64.StdEncoding.DecodeString(info["guestinfo.userdata"])
			if err := yaml.Unmarshal(raw, &v.user); err != nil {
				t.Fatalf("%s's user data: %v\n%s", name, err, raw)
			}
			return v
		}

		hosts := []string{"esx-gva-dev-02.corp.example", "esx-gva-dev-03.corp.example"}
		var placed []api.ComponentPlacement
		var fingerprints []string
		for i, it := range []api.AdminCatalogItem{imported, byHand} {
			b := build(it)
			// Named by their index within each component, counting on from
			// the first build.
			dbName := fmt.Sprintf("web-dev-db%02d", i+1)
			webNames := []string{fmt.Sprintf("web-dev-web%02d", 2*i+1), fmt.Sprintf("web-dev-web%02d", 2*i+2)}
			db, web := b.preview["db"], b.preview["web"]
			if !slices.Equal(db.Names, []string{dbName}) || !slices.Equal(web.Names, webNames) || db.DependsOn != nil ||
				web.DependsOn == nil || !slices.Equal(*web.DependsOn, []string{"db"}) || db.Nics[0].Ipam != api.PreviewComponentNicsIpamDhcp {
				t.Fatalf("%s: components = %+v / %+v", it.Slug, db, web)
			}
			for _, pc := range []api.PreviewComponent{db, web} {
				pl := *pc.Placement
				if deref(pl.Cluster) != "GVA-DEV-CL01" || deref(pl.ResourcePool) != pool || deref(pl.DatastoreCluster) != pod ||
					deref(pl.Datastore) != "nfs-gva-dev-sdrs-02" || pl.Hosts == nil || !slices.Equal(*pl.Hosts, hosts) {
					t.Fatalf("%s: %s placed %+v", it.Slug, pc.Key, pl)
				}
				got := map[string]api.PlacementConstraint{}
				for _, k := range *pl.Constraints {
					got[k.Tag] = k
				}
				if k := got["env/dev"]; !k.Met || k.Soft || k.Negated || deref(k.Object) != "GVA-DEV-CL01" {
					t.Fatalf("%s: %s's hard constraint = %+v", it.Slug, pc.Key, k)
				}
				if k := got["t1-zone/pci"]; !k.Met || !k.Negated {
					t.Fatalf("%s: %s's negated constraint = %+v", it.Slug, pc.Key, k)
				}
				if k := got["t1-tier/gold"]; !k.Met || !k.Soft || deref(k.Object) != "nfs-gva-dev-sdrs-02" || !slices.Equal(k.Excluded, []string{"nfs-gva-dev-sdrs-01"}) {
					t.Fatalf("%s: %s's soft constraint = %+v", it.Slug, pc.Key, k)
				}
				if k := got["net/app"]; deref(k.Object) != "dvpg-app-dev-vl310" {
					t.Fatalf("%s: %s's network constraint = %+v", it.Slug, pc.Key, k)
				}
				placed = append(placed, pl)
			}

			// The database first: it is powered on, with its DHCP address,
			// before any web server's address is reserved.
			on := b.steps["component:db/vsphere.power_on@"+dbName]
			if on.Title != "Powered on, guest IP "+dhcpLease+" · "+dbName || on.FinishedAt == nil {
				t.Fatalf("%s: the database's power on = %+v", it.Slug, on)
			}
			for key, s := range b.steps {
				if strings.HasPrefix(key, "component:web/") && (s.StartedAt == nil || s.StartedAt.Before(*on.FinishedAt)) {
					t.Fatalf("%s: %s began before the database was on: %+v", it.Slug, key, s)
				}
			}

			var dep api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+b.deployment, owner, nil, 200, &dep)
			gk := dep.GeneratedKey
			if gk == nil || !gk.Available || gk.Username != "ops" || slices.Contains(fingerprints, gk.Fingerprint) {
				t.Fatalf("%s: generated key = %+v", it.Slug, gk)
			}
			fingerprints = append(fingerprints, gk.Fingerprint)
			reserved := 0
			for _, r := range dep.Resources {
				if typ, _ := r.Discriminator(); typ == "ipam.address" {
					reserved++
				}
			}
			if reserved != len(webNames) {
				t.Fatalf("%s: %d addresses reserved, want the web servers' %d: the database is on DHCP", it.Slug, reserved, len(webNames))
			}

			// In vCenter: in the pool, on an allowed host and the gold
			// datastore; the web servers' cloud-init has the database's
			// address and their index, their tag their index; every VM
			// takes the deployment's key.
			for j, name := range append([]string{dbName}, webNames...) {
				v := vmOf(b, name)
				if v.pool != pool || !slices.Contains(hosts, v.host) || !slices.Equal(v.datastores, []string{"nfs-gva-dev-sdrs-02"}) {
					t.Fatalf("%s: %s is on %s in %q on %v", it.Slug, name, v.host, v.pool, v.datastores)
				}
				if v.user["hostname"] != name {
					t.Fatalf("%s: %s's hostname = %v", it.Slug, name, v.user["hostname"])
				}
				users, _ := v.user["users"].([]any)
				ops, _ := users[len(users)-1].(map[string]any)
				keys, _ := ops["ssh_authorized_keys"].([]any)
				if ops["name"] != "ops" || len(keys) != 1 {
					t.Fatalf("%s: %s's users = %v", it.Slug, name, users)
				}
				if pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(keys[0].(string))); err != nil || ssh.FingerprintSHA256(pub) != gk.Fingerprint {
					t.Fatalf("%s: %s's key %v: %v", it.Slug, name, keys[0], err)
				}
				if j == 0 {
					continue
				}
				index := j - 1
				files, _ := v.user["write_files"].([]any)
				conf, _ := files[0].(map[string]any)
				if want := fmt.Sprintf("db=%s\nnode=%d\n", dhcpLease, index); conf["content"] != want {
					t.Fatalf("%s: %s's /etc/shop.conf = %q, want %q", it.Slug, name, conf["content"], want)
				}
				if want := []string{fmt.Sprintf("app/node%d", index)}; !slices.Equal(v.tags, want) {
					t.Fatalf("%s: %s's tags = %v, want %v", it.Slug, name, v.tags, want)
				}
			}

			// The owner downloads the key once.
			var key api.PrivateKey
			h.json("POST", "/api/v1/deployments/"+b.deployment+"/private-key", owner, nil, 200, &key)
			signer, err := ssh.ParsePrivateKey([]byte(key.PrivateKey))
			if err != nil || ssh.FingerprintSHA256(signer.PublicKey()) != gk.Fingerprint || key.Username != "ops" {
				t.Fatalf("%s: the private key: %v %+v", it.Slug, err, key.Username)
			}
			var problem api.Problem
			h.json("POST", "/api/v1/deployments/"+b.deployment+"/private-key", owner, nil, 409, &problem)
			if !strings.Contains(deref(problem.Detail), "Thomas Nguyen downloaded the private key") {
				t.Fatalf("%s: a second download = %+v", it.Slug, problem)
			}
			if i == 0 {
				recordHistory(t, ctx, tc, b.req.Id, "provision-components")
			}
			settle(t, ctx, tc, b.req.Id)
		}
		// Imported or written in YAML, its components are placed alike.
		if !reflect.DeepEqual(placed[:2], placed[2:]) {
			t.Fatalf("placed differently: %+v / %+v", placed[:2], placed[2:])
		}
	})
}
