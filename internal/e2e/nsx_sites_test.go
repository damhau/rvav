package e2e

import (
	"context"
	"crypto/tls"
	"errors"
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
	"github.com/damhau/rvav/internal/provision"
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
	// Served by the integration's NSX Manager, the only one, as every
	// vCenter was before there were several; Zurich has its own.
	if got := servedBy(h, "127.0.0.1"); !slices.Equal(got, []string{sitesVC01, sitesVC02}) {
		t.Fatalf("vCenters the integration's NSX Manager serves, alone = %v", got)
	}
	h.json("PUT", "/api/v1/admin/nsx-managers/127.0.0.1/vcenters", "mkeller", api.NsxManagerVCenters{Vcenters: []string{sitesVC01}}, 200, nil)
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

// servedBy are the vCenters an NSX Manager serves.
func servedBy(h *harness, manager string) []string {
	h.t.Helper()
	var list api.NsxManagerList
	h.json("GET", "/api/v1/admin/nsx-managers", "mkeller", nil, 200, &list)
	for _, m := range list.Items {
		if m.Name == manager {
			return m.Vcenters
		}
	}
	return nil
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
	props["vc"] = map[string]any{"type": "string"}
	ui["vc"] = api.UiField{Widget: api.UiWidgetText, Label: "vc"}
	def := api.CatalogVersionDefinition{
		FormSchema:    map[string]any{"type": "object", "required": fields, "properties": props},
		UiSchema:      api.UiSchema{Fields: ui},
		InitialValues: map[string]any{},
		Blueprint: map[string]any{
			"kind": "vm", "naming": "{project}-{environment}-{inputs.app}{component}{seq:02}",
			"deployment_name": "{project}-{environment}-{inputs.app}",
			"template":        "tpl-rhel9.4-2026.10", "guest": map[string]any{"os_family": "linux", "domain": "corp.example"},
			"sizes": map[string]any{"S": map[string]any{"label": "Small", "vcpu": 2, "memory_gb": 4, "disk_gb": 50},
				"M": map[string]any{"label": "Medium", "vcpu": 4, "memory_gb": 8, "disk_gb": 50}},
			"bindings": map[string]any{"vcpu": "sizes[inputs.size].vcpu", "memory_gb": "sizes[inputs.size].memory_gb",
				"disk_gb": "sizes[inputs.size].disk_gb"},
			"groups": []any{"web"},
			"security_groups": []any{map[string]any{"key": "web", "title": "Web", "rules": []any{
				map[string]any{"from": []any{"any"}, "services": []any{service}}}}},
			"components": components,
		},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Farm", Fields: append(fields, "vc")}}
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
// Zurich's, with more room, by none at first (I1.6, ADR-0062). A vCenter
// added is served by the integration's NSX Manager while it is the only
// one, by none once another is added. rvav places VMs that need NSX in a
// vCenter an NSX Manager serves, and says so when no cluster allowed is
// in one. The NSX integration can't take an added NSX Manager's name, nor
// can an NSX Manager a request waiting is to use be removed. A form's NSX
// choices for a vCenter rvav doesn't know are refused. An update adding
// no VM goes on in a vCenter no NSX Manager serves. With one NSX Manager
// down, the tag sync still compares the other's VMs and names the one
// down, and an item saves when what it names is on an NSX Manager that
// answers.
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
		h.json("POST", "/api/v1/admin/vcenters", admin, api.VCenterWrite{Name: "vc03.corp.example", Url: "https://vc03.corp.example/sdk",
			Username: "rvav", Password: present.Ptr("vc03-password-1")}, 201, nil)
		if got := servedBy(h, "127.0.0.1"); !slices.Equal(got, []string{sitesVC01}) {
			t.Fatalf("a vCenter added with two NSX Managers is served by the integration's: %v", got)
		}
		code, body, _ := h.do("PUT", "/api/v1/admin/integrations/nsx", admin, api.IntegrationWrite{Settings: map[string]any{
			"url": "https://NSX02.corp.example", "username": nsx.FakeUser}, Secret: present.Ptr(nsx.FakePassword)})
		if code != 422 || !strings.Contains(string(body), "nsx02.corp.example is an NSX Manager added besides the integration") {
			t.Fatalf("the integration named as an added NSX Manager = %d %s", code, body)
		}

		farm, farmV := nsxFarm(h, "farm", "HTTPS", map[string]bool{"web": true})
		submit := func(app, cluster, datastore string) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: farm, VersionId: farmV, ProjectId: "prj_web",
				Environment: "dev", Inputs: map[string]any{"app": app, "size": "S", "nodes_web": 1, "cluster_web": cluster,
					"datastore_web": datastore, "network": "dvpg-app-dev-vl310"}}, 202, &req, "Idempotency-Key", uuid.NewString())
			return req
		}

		// Zurich's NSX Manager stays while a request waiting is to use it.
		queued := submit("qfarm", "ZRH-DEV-CL01", "vsan-zrh-dev-cl01")
		code, body, _ = h.do("DELETE", "/api/v1/admin/nsx-managers/"+sitesNSX02, admin, nil)
		if code != 409 || !strings.Contains(string(body), "Request "+queued.Number+" is to make NSX segments or security groups on "+sitesNSX02) {
			t.Fatalf("an NSX Manager removed under a request waiting = %d %s", code, body)
		}
		h.json("POST", "/api/v1/requests/"+queued.Id+"/cancel", owner, nil, 202, nil)

		// A form's NSX choices for a vCenter rvav doesn't know: refused,
		// not those of the integration's NSX Manager.
		if _, err := h.store.Pool.Exec(ctx, `update catalog_item_versions set ui_schema = jsonb_set(ui_schema, '{fields,pick_group}',
			'{"widget": "multiselect", "label": "Groups", "value_source": {"kind": "builtin", "name": "nsx.groups", "params": {"vcenter": "${inputs.vc}"}}}')
			where id = $1`, farmV); err != nil {
			t.Fatal(err)
		}
		dev := api.EnvironmentKey("dev")
		for vc, want := range map[string]int{sitesVC02: 200, "vc99.corp.example": 400} {
			code, body, _ := h.do("POST", "/api/v1/forms/values", owner, api.FieldValuesQuery{VersionId: farmV, ProjectId: "prj_web",
				Environment: &dev, Field: "pick_group", Inputs: map[string]any{"vc": vc}})
			if code != want || want == 400 && !strings.Contains(string(body), "rvav manages no vCenter vc99.corp.example.") {
				t.Fatalf("NSX groups for %s = %d %s", vc, code, body)
			}
		}

		// A deployment on each NSX Manager.
		build := func(app, cluster, datastore string) string {
			t.Helper()
			req := submit(app, cluster, datastore)
			h.runWorkflow(req.Id, nil)
			req = h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
			return (*req.DeploymentIds)[0]
		}
		gva, zrh := build("gfarm", "GVA-DEV-CL01", "vsan-gva-dev-cl01"), build("zfarm", "ZRH-DEV-CL01", "vsan-zrh-dev-cl01")

		// Zurich served by none: a resize goes on, its VMs' NSX objects
		// staying where they are; a VM added is refused.
		h.json("PUT", "/api/v1/admin/nsx-managers/"+sitesNSX02+"/vcenters", admin, api.NsxManagerVCenters{Vcenters: []string{}}, 200, nil)
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+zrh, owner, nil, 200, &dep)
		update := func(change map[string]any) api.UpdatePreview {
			t.Helper()
			in := maps.Clone(dep.Inputs)
			maps.Copy(in, change)
			var up api.UpdatePreview
			h.json("POST", "/api/v1/deployments/"+zrh+"/update/preview", owner, api.UpdateDraft{VersionId: farmV, Inputs: in}, 200, &up)
			return up
		}
		if up := update(map[string]any{"size": "M"}); len(up.Errors) > 0 || len(up.Refused) > 0 {
			t.Fatalf("a resize in a vCenter no NSX Manager serves: %+v, refused %+v", up.Errors, up.Refused)
		}
		if up := update(map[string]any{"nodes_web": 2}); !slices.ContainsFunc(up.Errors, func(e api.FieldError) bool {
			return deref(e.Code) == "no_nsx"
		}) {
			t.Fatalf("a VM added in a vCenter no NSX Manager serves: %+v, refused %+v", up.Errors, up.Refused)
		}
		h.json("PUT", "/api/v1/admin/nsx-managers/"+sitesNSX02+"/vcenters", admin, api.NsxManagerVCenters{Vcenters: []string{sitesVC02}}, 200, nil)

		// Both VMs' tags taken off.
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

// TestNSXIntegrationMoved: the NSX integration pointed to another NSX
// Manager since a deployment's groups were made on it (I1.6). Its deletion
// doesn't take what the new one hasn't for gone: it fails naming both,
// and keeps what it recorded; pointed back, it deletes them there.
func TestNSXIntegrationMoved(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		first := nsx.NewFakeServer()
		first.AnyVM = true
		firstClient := useFakeNSX(h, first)
		firstURL := h.svc.Config.NSX.URL
		h.acts.NSX, h.acts.NSXTimeout = firstClient, 20*time.Second
		item, version := groupItem(h)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item, VersionId: version, ProjectId: "prj_web",
			Environment: "dev", Inputs: segmentInputs()}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		req = h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		dep := (*req.DeploymentIds)[0]
		recorded := func() []string {
			t.Helper()
			var hosts []string
			rows, err := h.store.Pool.Query(ctx, `select type || '@' || coalesce(properties->>'nsx_host', '') from resources
				where deployment_id = $1 and type like 'nsx.%' order by type`, dep)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var x string
				_ = rows.Scan(&x)
				hosts = append(hosts, x)
			}
			return hosts
		}
		on := firstClient.Host()
		want := []string{"nsx.group@" + on, "nsx.membership@" + on, "nsx.policy@" + on}
		if got := recorded(); !slices.Equal(got, want) {
			t.Fatalf("NSX resources = %v, want %v", got, want)
		}

		h.acts.NSX = useFakeNSX(h, nsx.NewFakeServer())
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		del = h.waitStatus(del.Id, "tnguyen", api.RequestStatusFailed)
		if del.Error == nil || !strings.Contains(del.Error.Message, "was made on NSX Manager "+on+", and the NSX integration now points to "+
			h.acts.NSX.Host()+", which doesn't have it") {
			t.Fatalf("deleted after the integration moved: %+v", del.Error)
		}
		if got := recorded(); !slices.Equal(got, want) {
			t.Fatalf("NSX resources kept = %v, want %v", got, want)
		}

		h.svc.Config.NSX.URL, h.acts.NSX = firstURL, firstClient
		h.json("POST", "/api/v1/requests/"+del.Id+"/retry", "tnguyen", nil, 202, nil)
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		if _, err := firstClient.Group(ctx, provision.GroupName(dep, "web")); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("the group on the NSX Manager it was made on, pointed back: %v", err)
		}
	}, simulator.VPX())
}
