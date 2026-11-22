package e2e

import (
	"context"
	"crypto/tls"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/nsxmanagers"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/vcenters"
)

const sitesVC01, sitesVC02, sitesNSX02 = "vc01.corp.example", "vc02.corp.example", "nsx02.corp.example"

// nsxSites is Geneva's vCenter, served by the NSX integration's Manager,
// and Zurich's, with more memory free, and its own NSX Manager once
// addZurich links it (ADR-0062).
type nsxSites struct {
	h                          *harness
	geneva, zurich             *nsx.FakeServer
	genevaClient, zurichClient *nsx.Client
	zurichServer               *httptest.Server
}

func setupNSXSites(t *testing.T, ctx context.Context, c *vim25.Client) *nsxSites {
	h := setup(t, ctx, c)
	s := &nsxSites{h: h, geneva: nsx.NewFakeServer(), zurich: nsx.NewFakeServer()}
	s.geneva.AnyVM, s.geneva.OnSegment = true, portGroups(t, ctx, c)
	s.genevaClient = useFakeNSX(h, s.geneva)
	h.acts.NSX, h.acts.NSXTimeout = s.genevaClient, 20*time.Second
	m := simulator.VPX()
	if err := m.Create(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Remove)
	m.Service.TLS = new(tls.Config)
	m.Service.RegisterEndpoints = true
	srv := m.Service.NewServer()
	t.Cleanup(srv.Close)
	gc, err := govmomi.NewClient(ctx, srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := vsphere.Bootstrap(ctx, gc.Client, zurich()); err != nil {
		t.Fatal(err)
	}
	h.svc.Config.VCenter.Name, h.svc.Config.VCenter.URL = sitesVC01, "https://vc01.corp.example/sdk"
	u := *srv.URL
	u.User = nil
	h.json("POST", "/api/v1/admin/vcenters", "mkeller", api.VCenterWrite{Name: sitesVC02, Url: u.String(), Username: "user",
		Password: present.Ptr("pass"), Insecure: present.Ptr(true)}, 201, nil)
	set := &vcenters.Set{Store: h.store, Secrets: h.svc.Secrets, PrimaryName: sitesVC01,
		Primary: func(context.Context) (vsphere.Driver, error) { return h.vc, nil }}
	h.acts.VCenters = set
	h.svc.VCenter = func(ctx context.Context, name string) (core.ConsoleSource, error) { return set.Get(ctx, name) }
	if err := (&inventory.Syncer{Store: h.store, VCenters: set, Events: h.acts.Events, Log: h.acts.Log}).SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	h.json("POST", "/api/v1/admin/placement-rules", "mkeller", api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
		Clusters: []string{"ZRH-DEV-*"}, Datastores: []string{"vsan-zrh-*"}, Networks: []string{"dvpg-app-dev-*"},
		Folder: "/ZRH/vm/rvav/web/dev", Vcenter: present.Ptr(sitesVC02)}, 201, nil)
	s.zurich.AnyVM, s.zurich.OnSegment = true, portGroups(t, ctx, gc.Client)
	s.zurichServer = httptest.NewTLSServer(s.zurich)
	t.Cleanup(s.zurichServer.Close)
	if s.zurichClient, err = nsx.New(nsx.Options{URL: s.zurichServer.URL, Username: nsx.FakeUser, Password: nsx.FakePassword,
		Insecure: true}); err != nil {
		t.Fatal(err)
	}
	h.acts.NSXManagers = &nsxmanagers.Set{Store: h.store, Secrets: h.svc.Secrets, Primary: s.genevaClient}
	return s
}

func (s *nsxSites) addZurich() {
	s.h.json("POST", "/api/v1/admin/nsx-managers", "mkeller", api.NsxManagerWrite{Name: sitesNSX02, Url: s.zurichServer.URL,
		Username: present.Ptr(nsx.FakeUser), Password: present.Ptr(nsx.FakePassword), Insecure: present.Ptr(true),
		Vcenters: &[]string{sitesVC02}}, 201, nil)
}

// farmDefinition is a multi-VM item whose components join a security
// group of their deployment, open to service; bound says which
// components' cluster the form gives (rvav places the others).
func farmDefinition(service string, bound map[string]bool) *api.CatalogVersionDefinition {
	fields := []string{"app", "size"}
	props := map[string]any{}
	var components []any
	for _, key := range slices.Sorted(maps.Keys(bound)) {
		bindings := map[string]any{"post_config": `[]`}
		if bound[key] {
			bindings["cluster"], bindings["datastore"], bindings["network"] = "inputs.cluster_"+key, "inputs.datastore_"+key, "inputs.network"
			fields = append(fields, "cluster_"+key, "datastore_"+key)
		}
		components = append(components, map[string]any{"key": key, "title": key, "count": "inputs.nodes_" + key, "bindings": bindings})
		fields = append(fields, "nodes_"+key)
	}
	if slices.Contains(slices.Collect(maps.Values(bound)), true) {
		fields = append(fields, "network")
	}
	ui := map[string]api.UiField{}
	for _, f := range fields {
		props[f] = map[string]any{"type": "string", "minLength": 1}
		if strings.HasPrefix(f, "nodes_") {
			props[f] = map[string]any{"type": "integer", "minimum": 0, "maximum": 3}
		}
		ui[f] = api.UiField{Widget: api.UiWidgetText, Label: f}
	}
	def := api.CatalogVersionDefinition{
		FormSchema:    map[string]any{"type": "object", "required": fields, "properties": props},
		UiSchema:      api.UiSchema{Fields: ui},
		InitialValues: map[string]any{},
		Blueprint: map[string]any{
			"kind": "vm", "naming": "{project}-{environment}-{inputs.app}{component}{seq:02}",
			"deployment_name": "{project}-{environment}-{inputs.app}",
			"template":        "tpl-rhel9.4-2026.10", "guest": map[string]any{"os_family": "linux", "domain": "corp.example"},
			"sizes": map[string]any{"S": map[string]any{"label": "Small", "vcpu": 2, "memory_gb": 4, "disk_gb": 50}},
			"bindings": map[string]any{"vcpu": "sizes[inputs.size].vcpu", "memory_gb": "sizes[inputs.size].memory_gb",
				"disk_gb": "sizes[inputs.size].disk_gb"},
			"groups": []any{"web"},
			"security_groups": []any{map[string]any{"key": "web", "title": "Web", "rules": []any{
				map[string]any{"from": []any{"any"}, "services": []any{service}}}}},
			"components": components,
		},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Farm", Fields: fields}}
	return &def
}

func farmItem(slug string, def *api.CatalogVersionDefinition) api.CatalogItemCreate {
	return api.CatalogItemCreate{Slug: slug, Name: slug, Summary: "Farm.", Category: "compute", Type: api.VmBlueprint, Definition: def}
}

// nsxFarm publishes farmDefinition's item for every project.
func nsxFarm(h *harness, slug, service string, bound map[string]bool) (string, string) {
	var item api.AdminCatalogItem
	h.json("POST", "/api/v1/admin/catalog/items", "mkeller", farmItem(slug, farmDefinition(service, bound)), 201, &item)
	h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", "mkeller", nil, 200, nil)
	h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", "mkeller", api.CatalogEntitlements{AllProjects: true,
		ProjectIds: []string{}}, 200, nil)
	return item.Id, item.DraftVersion.Id
}

// downNSX hands out a client of an NSX Manager that doesn't answer.
type downNSX struct{ c *nsx.Client }

func (d downNSX) Get(context.Context, string) (*nsx.Client, error) { return d.c, nil }

// TestNSXSites: Geneva's vCenter served by the NSX integration's Manager,
// Zurich's, with more room, by none at first (I1.6, ADR-0062). rvav
// places VMs that need NSX in a vCenter an NSX Manager serves, and says so
// when no cluster allowed is in one. The NSX integration can't take an
// added NSX Manager's name. With one NSX Manager down, the tag sync still
// compares the other's VMs and names the one down, and an item saves when
// what it names is on an NSX Manager that answers.
func TestNSXSites(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		s := setupNSXSites(t, ctx, c)
		h := s.h
		const admin, owner = "mkeller", "tnguyen"

		auto, autoV := nsxFarm(h, "farm-auto", "HTTPS", map[string]bool{"web": false})
		preview := func() api.RequestPreview {
			t.Helper()
			var pv api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", owner, api.RequestDraft{CatalogItemId: auto, VersionId: autoV, ProjectId: "prj_web",
				Environment: "dev", Inputs: map[string]any{"app": "auto", "size": "S", "nodes_web": 1}}, 200, &pv)
			return pv
		}
		pv := preview()
		if pv.Components == nil {
			t.Fatalf("previewed without components: %+v", pv.Errors)
		}
		var placed []string
		for _, cp := range *pv.Components {
			if cp.Placement != nil {
				placed = append(placed, deref(cp.Placement.Cluster))
			}
		}
		if !pv.Valid || len(placed) != 1 || !strings.HasPrefix(placed[0], "GVA-") {
			t.Fatalf("placed with Zurich unserved: valid %v on %v, %+v", pv.Valid, placed, pv.Errors)
		}
		h.json("PUT", "/api/v1/admin/nsx-managers/127.0.0.1/vcenters", admin, api.NsxManagerVCenters{Vcenters: []string{}}, 200, nil)
		pv = preview()
		if i := slices.IndexFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "no_nsx" }); pv.Valid || i < 0 ||
			!strings.Contains(pv.Errors[i].Message, "no cluster allowed here is in a vCenter an NSX Manager serves") {
			t.Fatalf("placed with no vCenter served = %+v", pv.Errors)
		}
		h.json("PUT", "/api/v1/admin/nsx-managers/127.0.0.1/vcenters", admin, api.NsxManagerVCenters{Vcenters: []string{sitesVC01}}, 200, nil)

		// The integration named as an added NSX Manager would be listed
		// twice, and the added one could no longer be changed.
		s.addZurich()
		code, body, _ := h.do("PUT", "/api/v1/admin/integrations/nsx", admin, api.IntegrationWrite{Settings: map[string]any{
			"url": "https://NSX02.corp.example", "username": nsx.FakeUser}, Secret: present.Ptr(nsx.FakePassword)})
		if code != 422 || !strings.Contains(string(body), "nsx02.corp.example is an NSX Manager added besides the integration") {
			t.Fatalf("the integration named as an added NSX Manager = %d %s", code, body)
		}

		// A deployment on each NSX Manager, both VMs' tags then taken off.
		farm, farmV := nsxFarm(h, "farm", "HTTPS", map[string]bool{"web": true})
		build := func(app, cluster, datastore string) string {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: farm, VersionId: farmV, ProjectId: "prj_web",
				Environment: "dev", Inputs: map[string]any{"app": app, "size": "S", "nodes_web": 1, "cluster_web": cluster,
					"datastore_web": datastore, "network": "dvpg-app-dev-vl310"}}, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			req = h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
			return (*req.DeploymentIds)[0]
		}
		gva, zrh := build("gfarm", "GVA-DEV-CL01", "vsan-gva-dev-cl01"), build("zfarm", "ZRH-DEV-CL01", "vsan-zrh-dev-cl01")
		uuidOf := func(dep string) string {
			var u string
			_ = h.store.Pool.QueryRow(ctx, `select external_id from resources where deployment_id = $1 and type = 'vsphere.vm'`, dep).Scan(&u)
			return u
		}
		s.geneva.SetVMTagsDirectly(uuidOf(gva), nil)
		s.zurich.SetVMTagsDirectly(uuidOf(zrh), nil)
		gone := httptest.NewTLSServer(http.NotFoundHandler())
		gone.Close()
		down, err := nsx.New(nsx.Options{URL: gone.URL, Username: nsx.FakeUser, Password: nsx.FakePassword, Insecure: true})
		if err != nil {
			t.Fatal(err)
		}
		state := func(dep string) string {
			var st string
			_ = h.store.Pool.QueryRow(ctx, `select sync_state from deployments where id = $1`, dep).Scan(&st)
			return st
		}
		for _, x := range []struct {
			sync       inventory.NSXTagSync
			downOne    string
			comparedOn string
		}{
			{inventory.NSXTagSync{NSX: s.genevaClient, NSXManagers: downNSX{down}}, sitesNSX02, gva},
			{inventory.NSXTagSync{NSX: down, NSXManagers: h.acts.NSXManagers}, "the NSX integration's", zrh},
		} {
			x.sync.Store, x.sync.Events, x.sync.Log = h.store, h.acts.Events, h.acts.Log
			err := x.sync.SyncOnce(ctx)
			if err == nil || !strings.Contains(err.Error(), x.downOne) || state(x.comparedOn) != "drifted" {
				t.Fatalf("NSX Manager %s down: the tag sync = %v, the other's deployment %s", x.downOne, err, state(x.comparedOn))
			}
		}

		// Zurich's NSX Manager down: an item whose service Geneva's has
		// saves; one naming what neither answering one has waits for it.
		s.zurichServer.Close()
		h.json("POST", "/api/v1/admin/catalog/items", admin, farmItem("farm-later", farmDefinition("HTTPS", map[string]bool{"web": false})),
			201, nil)
		code, body, _ = h.do("POST", "/api/v1/admin/catalog/items", admin, farmItem("farm-smb", farmDefinition("SMB",
			map[string]bool{"web": false})))
		if code != 502 || !strings.Contains(string(body), "NSX Manager "+sitesNSX02+" did not answer") {
			t.Fatalf("an item naming a service no NSX Manager that answers has = %d %s", code, body)
		}
	}, simulator.VPX())
}
