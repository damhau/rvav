package e2e

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/provision"
)

// threeTier is a multi-VM item on NSX (ADR-0028): web, app and database
// tiers on one segment made for the deployment, each in a group of its own,
// with the firewall rules between them.
func threeTier() api.CatalogVersionDefinition {
	fields := []string{"cluster", "datastore"}
	props := map[string]any{}
	ui := map[string]api.UiField{}
	for _, f := range fields {
		props[f] = map[string]any{"type": "string", "minLength": 1}
		ui[f] = api.UiField{Widget: api.UiWidgetText, Label: f}
	}
	tier := func(key, title string, count int) map[string]any {
		return map[string]any{"key": key, "title": title, "count": count, "groups": []any{key}}
	}
	rule := func(from, service string) map[string]any {
		return map[string]any{"from": []any{from}, "services": []any{service}}
	}
	def := api.CatalogVersionDefinition{
		FormSchema:    map[string]any{"type": "object", "required": fields, "properties": props},
		UiSchema:      api.UiSchema{Fields: ui},
		InitialValues: map[string]any{},
		Blueprint: map[string]any{
			"kind": "vm", "naming": "{project}-{environment}-tt{component}{seq:02}", "deployment_name": "{project}-{environment}-threetier",
			"rollback_on_failure": true, "template": "tpl-rhel9.4-2026.10", "guest": map[string]any{"os_family": "linux", "domain": "corp.example"},
			"bindings": map[string]any{"vcpu": "2", "memory_gb": "4", "disk_gb": "40", "cluster": "inputs.cluster",
				"datastore": "inputs.datastore", "network": "segments.app"},
			"segments":   []any{map[string]any{"key": "app", "title": "Application"}},
			"components": []any{tier("web", "Web", 2), tier("app", "Application", 1), tier("db", "Database", 1)},
			"security_groups": []any{
				map[string]any{"key": "web", "title": "Web", "rules": []any{rule("any", "HTTPS")}},
				map[string]any{"key": "app", "title": "Application", "rules": []any{rule("web", "HTTP")}},
				map[string]any{"key": "db", "title": "Database", "rules": []any{rule("app", "PostgreSQL")}},
			},
		},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Three tiers", Fields: fields}}
	return def
}

// TestN1Acceptance is the NSX milestone's acceptance (N1.5): a three-tier
// item builds its VMs on a new segment of the project's Tier-1, each tier
// in its own security group with the firewall rules between them; a day-2
// action moves a VM into another group; deleting the deployment removes the
// segment, the groups and the rules. (The vRA side, a template with NSX
// networks and security groups imported, is TestMapNSXTemplate's.)
func TestN1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		fakeNSX := nsx.NewFakeServer()
		fakeNSX.RealizeAfter, fakeNSX.AnyVM, fakeNSX.SeeAfter = 1, true, 1
		fakeNSX.OnSegment = portGroups(t, ctx, c)
		client := useFakeNSX(h, fakeNSX)
		h.acts.NSX, h.acts.NSXTimeout = client, 20*time.Second
		// The development EfficientIP has the nsx-gva-dev block (10.60.0.0/22).
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		r := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })]
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Priority: &r.Priority, Clusters: r.Clusters, Datastores: r.Datastores, Networks: r.Networks, Folder: r.Folder,
			ClusterTags: &r.ClusterTags, DatastoreTags: &r.DatastoreTags, Segments: &api.PlacementSegments{Tier1: "t1-gva-dev",
				IpBlock: "nsx-gva-dev", PrefixLength: 26, DnsZone: "corp.example", DnsServers: &[]string{"10.10.0.53"}}}, 200, nil)

		def := threeTier()
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "three-tier", Name: "Three-tier application",
			Summary: "Web, application and database tiers on NSX.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/three-tier/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/three-tier/entitlements", admin, api.CatalogEntitlements{AllProjects: true,
			ProjectIds: []string{}}, 200, nil)

		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01"}},
			202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)

		var depID string
		if err := h.store.Pool.QueryRow(ctx, `select id from deployments where created_by_request_id = $1`, req.Id).Scan(&depID); err != nil {
			t.Fatal(err)
		}
		// One segment of the deployment, on the project's Tier-1: every VM on it.
		segID := provision.SegmentName(depID, "app")
		seg, err := client.Segment(ctx, segID)
		if err != nil || seg.ConnectivityPath != nsx.Tier1Path+"t1-gva-dev" || !nsx.Owned(seg.Tags, "rvav") {
			t.Fatalf("segment = %+v, %v", seg, err)
		}
		type vmRow struct{ name, uuid, component string }
		var vms []vmRow
		rows, _ := h.store.Pool.Query(ctx, `select name, external_id, properties->'vm'->>'component' from resources
			where deployment_id = $1 and type = 'vsphere.vm' order by name`, depID)
		for rows.Next() {
			var v vmRow
			_ = rows.Scan(&v.name, &v.uuid, &v.component)
			vms = append(vms, v)
		}
		rows.Close()
		if len(vms) != 4 {
			t.Fatalf("VMs = %+v", vms)
		}
		for _, v := range vms {
			vm, err := h.vc.FindVM(ctx, v.name)
			if err != nil || len(vm.NICs) == 0 || vm.NICs[0].Network != segID {
				t.Fatalf("%s: %+v, %v", v.name, vm, err)
			}
		}
		var addresses []string
		rows, _ = h.store.Pool.Query(ctx, `select name from resources where deployment_id = $1 and type = 'ipam.address'`, depID)
		for rows.Next() {
			var a string
			_ = rows.Scan(&a)
			addresses = append(addresses, a)
		}
		rows.Close()
		if len(addresses) != 4 || slices.ContainsFunc(addresses, func(a string) bool { return !strings.HasPrefix(a, "10.60.0.") }) {
			t.Fatalf("addresses = %v", addresses)
		}

		// Each tier in its own group; the rules between them.
		group := func(key string) string { return provision.GroupName(depID, key) }
		for _, key := range []string{"web", "app", "db"} {
			if g, err := client.Group(ctx, group(key)); err != nil || !nsx.Owned(g.Tags, "rvav") {
				t.Fatalf("group %s = %+v, %v", key, g, err)
			}
		}
		pol, err := client.SecurityPolicy(ctx, provision.PolicyName(depID))
		if err != nil || len(pol.Rules) != 3 {
			t.Fatalf("policy = %+v, %v", pol, err)
		}
		into := map[string]nsx.Rule{}
		for _, rule := range pol.Rules {
			into[strings.TrimPrefix(rule.DestinationGroups[0], nsx.GroupPath)] = rule
		}
		if r := into[group("db")]; !slices.Equal(r.SourceGroups, []string{nsx.GroupPath + group("app")}) ||
			!slices.Equal(r.Services, []string{nsx.ServicePath + "PostgreSQL"}) {
			t.Fatalf("into db = %+v", r)
		}
		if r := into[group("app")]; !slices.Equal(r.SourceGroups, []string{nsx.GroupPath + group("web")}) {
			t.Fatalf("into app = %+v", r)
		}
		if r := into[group("web")]; !slices.Equal(r.SourceGroups, []string{"ANY"}) {
			t.Fatalf("into web = %+v", r)
		}
		tagOf := func(key string) nsx.Tag { return nsx.Tag{Scope: nsx.GroupScope, Tag: group(key)} }
		for _, v := range vms {
			vm, _ := fakeNSX.VM(v.uuid)
			if !slices.Contains(vm.Tags, tagOf(v.component)) || len(vm.Tags) != 3 {
				t.Fatalf("%s (%s) tags = %+v", v.name, v.component, vm.Tags)
			}
		}

		// Day-2: a web server moves into the application tier.
		web := vms[slices.IndexFunc(vms, func(v vmRow) bool { return v.component == "web" })]
		var webRes string
		_ = h.store.Pool.QueryRow(ctx, `select id from resources where deployment_id = $1 and type = 'vsphere.vm' and name = $2`,
			depID, web.name).Scan(&webRes)
		move := func(action, key string) {
			t.Helper()
			var r api.Request
			h.json("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: action, ResourceId: &webRes,
				Inputs: map[string]any{"group": group(key)}}, 202, &r, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(r.Id, nil)
			h.waitStatus(r.Id, "tnguyen", api.RequestStatusSucceeded)
		}
		move("nsx_group_add", "app")
		move("nsx_group_remove", "web")
		if vm, _ := fakeNSX.VM(web.uuid); !slices.Contains(vm.Tags, tagOf("app")) || slices.Contains(vm.Tags, tagOf("web")) {
			t.Fatalf("%s moved: tags %+v", web.name, vm.Tags)
		}

		// Deleted: the segment, its port group, the groups and the rules.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		if _, err := client.Segment(ctx, segID); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("segment after delete: %v", err)
		}
		if ok, _ := h.vc.NetworkExists(ctx, segID); ok {
			t.Fatal("the segment's port group is still in vCenter")
		}
		if _, err := client.SecurityPolicy(ctx, provision.PolicyName(depID)); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("policy after delete: %v", err)
		}
		for _, key := range []string{"web", "app", "db"} {
			if _, err := client.Group(ctx, group(key)); !errors.Is(err, nsx.ErrNotFound) {
				t.Fatalf("group %s after delete: %v", key, err)
			}
		}
	}, simulator.VPX())
}
