package e2e

import (
	"context"
	"crypto/tls"
	"errors"
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

// TestNSXFollowsVCenter: two vCenters, each served by its own NSX Manager
// (I1.1, ADR-0062). A request whose vCenter no NSX Manager serves is
// refused when evaluated; once Zurich's is linked, each request makes its
// segment and security group on its vCenter's NSX Manager alone, the
// form's NSX choices are that NSX Manager's, the tag sync asks each its
// VMs, and the deletion removes them where they were made.
func TestNSXFollowsVCenter(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		const vc01, vc02, nsx02 = "vc01.corp.example", "vc02.corp.example", "nsx02.corp.example"

		geneva := nsx.NewFakeServer()
		geneva.AnyVM, geneva.OnSegment = true, portGroups(t, ctx, c)
		client1 := useFakeNSX(h, geneva)
		h.acts.NSX, h.acts.NSXTimeout = client1, 20*time.Second

		m := simulator.VPX()
		defer m.Remove()
		if err := m.Create(); err != nil {
			t.Fatal(err)
		}
		m.Service.TLS = new(tls.Config)
		m.Service.RegisterEndpoints = true
		srv := m.Service.NewServer()
		defer srv.Close()
		gc, err := govmomi.NewClient(ctx, srv.URL, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := vsphere.Bootstrap(ctx, gc.Client, zurich()); err != nil {
			t.Fatal(err)
		}
		h.svc.Config.VCenter.Name, h.svc.Config.VCenter.URL = vc01, "https://vc01.corp.example/sdk"
		url := *srv.URL
		url.User = nil
		h.json("POST", "/api/v1/admin/vcenters", admin, api.VCenterWrite{Name: vc02, Url: url.String(), Username: "user",
			Password: present.Ptr("pass"), Insecure: present.Ptr(true)}, 201, nil)
		set := &vcenters.Set{Store: h.store, Secrets: h.svc.Secrets, PrimaryName: vc01,
			Primary: func(context.Context) (vsphere.Driver, error) { return h.vc, nil }}
		h.acts.VCenters = set
		h.svc.VCenter = func(ctx context.Context, name string) (core.ConsoleSource, error) { return set.Get(ctx, name) }
		if err := (&inventory.Syncer{Store: h.store, VCenters: set, Events: h.acts.Events, Log: h.acts.Log}).SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}

		// Geneva's dev rule makes segments on its vCenter's NSX Manager;
		// Zurich's dev rule none yet.
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		r := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })]
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Priority: &r.Priority, Clusters: r.Clusters, Datastores: r.Datastores, Networks: r.Networks, Folder: r.Folder,
			ClusterTags: &r.ClusterTags, DatastoreTags: &r.DatastoreTags, Vcenter: present.Ptr(vc01), Segments: &api.PlacementSegments{
				Tier1: "t1-gva-dev", IpBlock: "nsx-gva-dev", PrefixLength: 26, DnsZone: "corp.example", DnsServers: &[]string{"10.10.0.53"}}}, 200, nil)
		zrhRule := api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev", Clusters: []string{"ZRH-DEV-*"},
			Datastores: []string{"vsan-zrh-*"}, Networks: []string{"dvpg-app-dev-*"}, Folder: "/ZRH/vm/rvav/web/dev", Vcenter: present.Ptr(vc02)}
		zrhSegments := &api.PlacementSegments{Tier1: "t1-zrh-prd", IpBlock: "nsx-gva-dev", PrefixLength: 26, DnsZone: "corp.example"}
		var problem struct{ Errors []api.FieldError }
		withSegments := zrhRule
		withSegments.Segments = zrhSegments
		h.json("POST", "/api/v1/admin/placement-rules", admin, withSegments, 422, &problem)
		if len(problem.Errors) != 1 || deref(problem.Errors[0].Code) != "no_nsx" || !strings.Contains(problem.Errors[0].Message, vc02) {
			t.Fatalf("segments in a vCenter no NSX Manager serves = %+v", problem.Errors)
		}
		var zrh api.PlacementRule
		h.json("POST", "/api/v1/admin/placement-rules", admin, zrhRule, 201, &zrh)

		item, version := rhelVariant(h, "rhel9-segment-groups", "RHEL 9 on its own segment, in groups", func(bp map[string]any) {
			bp["bindings"].(map[string]any)["network"] = "segments.app"
			bp["segments"] = []any{map[string]any{"key": "app", "title": "Application"}}
			groupBlueprint("nsx:grp-backup-clients", "SSH")(bp)
		})
		gvaInputs := segmentInputs()
		zrhInputs := segmentInputs()
		zrhInputs["role"], zrhInputs["cluster"], zrhInputs["datastore"] = "zrh", "ZRH-DEV-CL01", "vsan-zrh-dev-cl01"
		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item, VersionId: version, ProjectId: "prj_web", Environment: "dev", Inputs: inputs}
		}

		// Zurich has no NSX Manager: refused before anything is made.
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft(zrhInputs), 200, &pv)
		if i := slices.IndexFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "no_nsx" }); pv.Valid || i < 0 ||
			!strings.Contains(pv.Errors[i].Message, vc02) {
			t.Fatalf("Zurich without an NSX Manager = %+v", pv.Errors)
		}
		h.json("POST", "/api/v1/requests", "tnguyen", draft(zrhInputs), 422, nil, "Idempotency-Key", uuid.NewString())

		// Zurich's NSX Manager, linked to vc02; its rule makes segments on a
		// Tier-1 it has.
		zurichNSX := nsx.NewFakeServer()
		zurichNSX.AnyVM, zurichNSX.OnSegment = true, portGroups(t, ctx, gc.Client)
		zsrv := httptest.NewTLSServer(zurichNSX)
		defer zsrv.Close()
		h.json("POST", "/api/v1/admin/nsx-managers", admin, api.NsxManagerWrite{Name: nsx02, Url: zsrv.URL, Username: present.Ptr(nsx.FakeUser),
			Password: present.Ptr(nsx.FakePassword), Insecure: present.Ptr(true), Vcenters: &[]string{vc02}}, 201, nil)
		client2, err := nsx.New(nsx.Options{URL: zsrv.URL, Username: nsx.FakeUser, Password: nsx.FakePassword, Insecure: true})
		if err != nil {
			t.Fatal(err)
		}
		h.acts.NSXManagers = &nsxmanagers.Set{Store: h.store, Secrets: h.svc.Secrets, Primary: client1}
		withSegments.Priority = &zrh.Priority
		h.json("PUT", "/api/v1/admin/placement-rules/"+zrh.Id, admin, withSegments, 200, nil)

		// The form's NSX groups: those of the cluster's vCenter's NSX
		// Manager, else of every vCenter the placement allows.
		if err := client2.PutGroup(ctx, nsx.Group{ID: "grp-zrh-db", DisplayName: "grp-zrh-db",
			Expression: []nsx.Condition{nsx.TagCondition(nsx.Tag{Scope: "role", Tag: "db"})}}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Pool.Exec(ctx, `update catalog_item_versions set ui_schema = jsonb_set(ui_schema, '{fields,pick_group}',
			'{"widget": "multiselect", "label": "Groups", "value_source": {"kind": "builtin", "name": "nsx.groups", "params": {"cluster": "${inputs.cluster}"}}}')
			where id = $1`, version); err != nil {
			t.Fatal(err)
		}
		dev := api.EnvironmentKey("dev")
		groups := func(cluster string) []string {
			t.Helper()
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", "tnguyen", api.FieldValuesQuery{VersionId: version, ProjectId: "prj_web",
				Environment: &dev, Field: "pick_group", Inputs: map[string]any{"cluster": cluster}}, 200, &out)
			var values []string
			for _, o := range *out.Options {
				values = append(values, o.Value)
			}
			return values
		}
		if got := groups("ZRH-DEV-CL01"); !slices.Equal(got, []string{"nsx:grp-backup-clients", "nsx:grp-web-servers", "nsx:grp-zrh-db"}) {
			t.Fatalf("Zurich's groups = %v", got)
		}
		if got := groups("GVA-DEV-CL01"); !slices.Equal(got, []string{"nsx:grp-backup-clients", "nsx:grp-web-servers"}) {
			t.Fatalf("Geneva's groups = %v", got)
		}
		if got := groups(""); !slices.Contains(got, "nsx:grp-zrh-db") {
			t.Fatalf("every vCenter's groups = %v", got)
		}

		build := func(inputs map[string]any) string {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", draft(inputs), 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			req = h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
			return (*req.DeploymentIds)[0]
		}
		gva, zrhDep := build(gvaInputs), build(zrhInputs)
		var zrhVM string
		_ = h.store.Pool.QueryRow(ctx, `select name from resources where deployment_id = $1 and type = 'vsphere.vm'`, zrhDep).Scan(&zrhVM)
		if !vmExists(ctx, gc.Client, zrhVM) || vmExists(ctx, c, zrhVM) {
			t.Fatalf("the Zurich VM %q isn't in Zurich's vCenter alone", zrhVM)
		}

		// Each made on its own NSX Manager alone.
		made := func(client *nsx.Client, dep string) (segment, group, policy bool) {
			t.Helper()
			_, err := client.Segment(ctx, provision.SegmentName(dep, "app"))
			segment = err == nil
			_, err = client.Group(ctx, provision.GroupName(dep, "web"))
			group = err == nil
			_, err = client.SecurityPolicy(ctx, provision.PolicyName(dep))
			policy = err == nil
			return
		}
		for _, x := range []struct {
			on     string
			client *nsx.Client
			dep    string
			want   bool
		}{{"Geneva", client1, gva, true}, {"Zurich", client2, gva, false}, {"Zurich", client2, zrhDep, true}, {"Geneva", client1, zrhDep, false}} {
			if s, g, p := made(x.client, x.dep); s != x.want || g != x.want || p != x.want {
				t.Fatalf("%s on %s's NSX Manager: segment %v, group %v, policy %v; want %v", x.dep, x.on, s, g, p, x.want)
			}
		}
		managerOf := func(dep string) map[string]string {
			t.Helper()
			out := map[string]string{}
			rows, err := h.store.Pool.Query(ctx, `select type, coalesce(properties->>'nsx_manager', '') from resources
				where deployment_id = $1 and type like 'nsx.%'`, dep)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var typ, m string
				_ = rows.Scan(&typ, &m)
				out[typ] = m
			}
			return out
		}
		if got := managerOf(zrhDep); len(got) != 4 || got["nsx.segment"] != nsx02 || got["nsx.group"] != nsx02 ||
			got["nsx.policy"] != nsx02 || got["nsx.membership"] != nsx02 {
			t.Fatalf("Zurich's NSX resources = %v", got)
		}
		if got := managerOf(gva); len(got) != 4 || got["nsx.segment"]+got["nsx.group"]+got["nsx.policy"]+got["nsx.membership"] != "" {
			t.Fatalf("Geneva's NSX resources = %v", got)
		}

		// The tag sync asks Zurich's NSX Manager for Zurich's VM.
		var zrhUUID string
		_ = h.store.Pool.QueryRow(ctx, `select external_id from resources where deployment_id = $1 and type = 'vsphere.vm'`,
			zrhDep).Scan(&zrhUUID)
		zurichNSX.SetVMTagsDirectly(zrhUUID, nil)
		tagSync := &inventory.NSXTagSync{Store: h.store, NSX: client1, NSXManagers: h.acts.NSXManagers, Events: h.acts.Events, Log: h.acts.Log}
		if err := tagSync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		state := func(dep string) string {
			var s string
			_ = h.store.Pool.QueryRow(ctx, `select sync_state from deployments where id = $1`, dep).Scan(&s)
			return s
		}
		if state(zrhDep) != "drifted" || state(gva) == "drifted" {
			t.Fatalf("after Zurich's VM lost its tags: Zurich %s, Geneva %s", state(zrhDep), state(gva))
		}

		// An NSX Manager with deployments on it stays.
		h.json("DELETE", "/api/v1/admin/nsx-managers/"+nsx02, admin, nil, 409, nil)

		// Deleted where they were made.
		for _, dep := range []string{gva, zrhDep} {
			var del api.Request
			h.json("POST", "/api/v1/deployments/"+dep+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
				202, &del, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(del.Id, nil)
			h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		}
		for _, x := range []struct {
			client *nsx.Client
			dep    string
		}{{client1, gva}, {client2, zrhDep}} {
			if s, g, p := made(x.client, x.dep); s || g || p {
				t.Fatalf("%s after its deletion: segment %v, group %v, policy %v", x.dep, s, g, p)
			}
		}
		if _, err := client2.Group(ctx, "grp-zrh-db"); errors.Is(err, nsx.ErrNotFound) {
			t.Fatal("the deletion took a group rvav didn't make")
		}
		zvc, err := set.Get(ctx, vc02)
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := zvc.NetworkExists(ctx, provision.SegmentName(zrhDep, "app")); ok {
			t.Fatal("Zurich's segment's port group is still in Zurich's vCenter")
		}
		h.json("DELETE", "/api/v1/admin/nsx-managers/"+nsx02, admin, nil, 204, nil)
	}, simulator.VPX())
}
