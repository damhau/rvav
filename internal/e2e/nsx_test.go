package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/provision"
)

// TestNSXIntegration: the NSX Manager set from Administration ›
// Integrations, its password sealed, and tested (N1.1, ADR-0028); the
// notification channels' settings saved too, which the database refused
// until N1.1.
func TestNSXIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		fake := httptest.NewServer(nsx.NewFakeServer())
		defer fake.Close()

		var it api.Integration
		h.json("PUT", "/api/v1/admin/integrations/nsx", admin, api.IntegrationWrite{Settings: map[string]any{"url": fake.URL,
			"username": nsx.FakeUser}, Secret: new(nsx.FakePassword)}, 200, &it)
		if it.Kind != "nsx" || !it.SecretSet || it.Settings["url"] != fake.URL || it.Settings["password"] != nil {
			t.Fatalf("saved nsx = %+v", it)
		}
		var list api.IntegrationList
		h.json("GET", "/api/v1/admin/integrations", admin, nil, 200, &list)
		if !slices.ContainsFunc(list.Items, func(x api.Integration) bool { return x.Kind == "nsx" && x.Configured }) {
			t.Fatalf("integrations = %+v", list.Items)
		}

		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/integrations/nsx/test", admin, nil, 200, &test)
		if !test.Ok || len(test.Checks) != 4 || test.Checks[0].Message != "Signed in to NSX 4.1.2.0.0.22589037." ||
			test.Checks[1].Message != "3 Tier-1 gateways, for segments made for deployments." {
			t.Fatalf("NSX test = %+v", test)
		}
		h.json("POST", "/api/v1/admin/integrations/nsx/test", admin, api.IntegrationWrite{Settings: map[string]any{"url": fake.URL,
			"username": nsx.FakeUser}, Secret: new("wrong")}, 200, &test)
		if test.Ok || test.Checks[0].Name != "Sign in" || test.Checks[0].Ok {
			t.Fatalf("a wrong password tested fine: %+v", test)
		}
		h.json("PUT", "/api/v1/admin/integrations/nsx", admin, api.IntegrationWrite{Settings: map[string]any{"tier0": "x"}}, 422, nil)

		// Email and Teams settings are saved.
		h.json("PUT", "/api/v1/admin/integrations/smtp", admin, api.IntegrationWrite{Settings: map[string]any{
			"host": "smtp.corp.example", "port": 587, "from": "rvav <rvav@corp.example>"}}, 200, &it)
		if it.Settings["host"] != "smtp.corp.example" {
			t.Fatalf("saved smtp = %+v", it)
		}
		h.json("PUT", "/api/v1/admin/integrations/teams", admin, api.IntegrationWrite{Settings: map[string]any{
			"app_id": "a1", "tenant_id": "t1"}, Secret: new("s")}, 200, nil)
	})
}

// TestPlacementSegments: a placement rule's segments made for deployments
// (N1.2): refused without an NSX Manager, or with a Tier-1 NSX doesn't
// have, a subnet size out of range or a DNS server that isn't an address;
// saved, read back, and cleared.
func TestPlacementSegments(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		segments := &api.PlacementSegments{Tier1: "t1-gva-dev", IpBlock: "nsx-gva-dev", PrefixLength: 26, DnsZone: "corp.example",
			DnsServers: &[]string{"10.10.0.53"}}
		rule := api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev", Clusters: []string{"GVA-DEV-*"},
			Datastores: []string{"vsan-gva-dev-*"}, Networks: []string{"dvpg-app-dev-*"}, Folder: "/GVA/vm/rvav/web/dev", Segments: segments}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/placement-rules", admin, rule, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/segments/tier1" || deref(problem.Errors[0].Code) != "no_nsx" {
			t.Fatalf("without NSX = %+v", problem.Errors)
		}

		fake := httptest.NewServer(nsx.NewFakeServer())
		defer fake.Close()
		h.svc.Config.NSX.URL, h.svc.Config.NSX.Username, h.svc.Config.NSX.Password = fake.URL, nsx.FakeUser, nsx.FakePassword
		bad := *segments
		bad.Tier1, bad.PrefixLength, bad.DnsServers = "t1-nope", 30, &[]string{"ns1"}
		rule.Segments = &bad
		h.json("POST", "/api/v1/admin/placement-rules", admin, rule, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Equal(fields, []string{"/segments/prefix_length", "/segments/dns_servers/0", "/segments/tier1"}) {
			t.Fatalf("bad segments = %v", fields)
		}

		rule.Segments = segments
		var saved api.PlacementRule
		h.json("POST", "/api/v1/admin/placement-rules", admin, rule, 201, &saved)
		if saved.Segments == nil || saved.Segments.Tier1 != "t1-gva-dev" || saved.Segments.PrefixLength != 26 ||
			(*saved.Segments.DnsServers)[0] != "10.10.0.53" {
			t.Fatalf("saved = %+v", saved.Segments)
		}
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		if i := slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Id == saved.Id }); i < 0 || rules.Items[i].Segments == nil {
			t.Fatalf("listed = %+v", rules.Items)
		}
		rule.Segments = nil
		var cleared api.PlacementRule
		h.json("PUT", "/api/v1/admin/placement-rules/"+saved.Id, admin, rule, 200, &cleared)
		if cleared.Segments != nil {
			t.Fatalf("cleared = %+v", cleared.Segments)
		}
	})
}

// segmentItem is the seeded RHEL item made into one whose VM is on an NSX
// segment made for its deployment, published for every project: its ID and
// its version's.
func segmentItem(h *harness) (string, string) {
	h.t.Helper()
	return rhelVariant(h, "rhel9-segment", "RHEL 9 on its own segment", func(bp map[string]any) {
		bp["bindings"].(map[string]any)["network"] = "segments.app"
		bp["segments"] = []any{map[string]any{"key": "app", "title": "Application"}}
	})
}

// rhelVariant is the seeded RHEL item with its blueprint changed, imported
// under another slug and published for every project.
func rhelVariant(h *harness, slug, name string, change func(bp map[string]any)) (string, string) {
	h.t.Helper()
	res := importRHELVariant(h, slug, name, change)
	version := publishVRAItem(h, res)
	return res.ItemId, version
}

// importRHELVariant imports the seeded RHEL item with its blueprint
// changed, under another slug, as a draft.
func importRHELVariant(h *harness, slug, name string, change func(bp map[string]any)) api.CatalogImportResult {
	h.t.Helper()
	var res api.CatalogImportResult
	h.json("POST", "/api/v1/admin/catalog/import", "mkeller", rawYAML(rhelYAML(h, slug, name, change)), 200, &res,
		"Content-Type", "application/yaml")
	return res
}

// rhelYAML is the seeded RHEL item's export with its blueprint changed,
// under another slug.
func rhelYAML(h *harness, slug, name string, change func(bp map[string]any)) []byte {
	h.t.Helper()
	code, raw, _ := h.do("GET", "/api/v1/admin/catalog/items/rhel9-server/export", "mkeller", nil)
	if code != 200 {
		h.t.Fatalf("export = %d %s", code, raw)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		h.t.Fatal(err)
	}
	meta := doc["metadata"].(map[string]any)
	meta["slug"], meta["name"] = slug, name
	versions := doc["versions"].([]any)
	last := versions[len(versions)-1].(map[string]any)
	change(last["blueprint"].(map[string]any))
	doc["versions"] = []any{last}
	out, err := yaml.Marshal(doc)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

// useFakeNSX makes rvav's NSX Manager a fake one, for the API.
func useFakeNSX(h *harness, f *nsx.FakeServer) *nsx.Client {
	h.t.Helper()
	srv := httptest.NewServer(f)
	h.t.Cleanup(srv.Close)
	h.svc.Config.NSX.URL, h.svc.Config.NSX.Username, h.svc.Config.NSX.Password = srv.URL, nsx.FakeUser, nsx.FakePassword
	client, err := nsx.New(nsx.Options{URL: srv.URL, Username: nsx.FakeUser, Password: nsx.FakePassword})
	if err != nil {
		h.t.Fatal(err)
	}
	return client
}

// TestSegmentPlanning: a request for an item whose VM is on a segment made
// for its deployment (N1.2): refused while no placement rule makes
// segments; then previewed with the segment and its subnet to come, and
// submitted with the segment in its plan, its VM on it.
func TestSegmentPlanning(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		useFakeNSX(h, nsx.NewFakeServer())
		item, version := segmentItem(h)
		draft := api.RequestDraft{CatalogItemId: item, VersionId: version, ProjectId: "prj_web", Environment: "dev",
			Inputs: segmentInputs()}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "no_segments" }) {
			t.Fatalf("without segments: %+v", pv.Errors)
		}

		// Web Platform's dev rule makes them.
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		i := slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })
		r := rules.Items[i]
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Priority: &r.Priority, Clusters: r.Clusters, Datastores: r.Datastores, Networks: r.Networks, Folder: r.Folder,
			ClusterTags: &r.ClusterTags, DatastoreTags: &r.DatastoreTags, Segments: &api.PlacementSegments{Tier1: "t1-gva-dev",
				IpBlock: "nsx-gva-dev", PrefixLength: 26, DnsZone: "corp.example", DnsServers: &[]string{"10.10.0.53"}}}, 200, nil)

		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if !pv.Valid || pv.Components == nil {
			t.Fatalf("preview: %+v", pv.Errors)
		}
		nic := (*pv.Components)[0].Nics[0]
		if nic.Network != "A new NSX segment (app) on t1-gva-dev" || deref(nic.Subnet) != "/26 from nsx-gva-dev, when the request runs" {
			t.Fatalf("preview nic = %+v %v", nic, deref(nic.Subnet))
		}

		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		var raw []byte
		if err := h.store.Pool.QueryRow(ctx, `select plan from requests where id = $1`, req.Id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plan provision.Plan
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		if len(plan.Segments) != 1 || len(plan.VMs) != 1 {
			t.Fatalf("plan: %d segments, %d VMs", len(plan.Segments), len(plan.VMs))
		}
		sg, vm := plan.Segments[0], plan.VMs[0]
		if sg.ID != provision.SegmentName(vm.DeploymentID, "app") || sg.Tier1 != "t1-gva-dev" || sg.IPBlock != "nsx-gva-dev" ||
			sg.PrefixLength != 26 || vm.Network != sg.Name || vm.Segment != "app" || vm.Subnet != "" ||
			!strings.HasSuffix(vm.FQDN, ".corp.example") || vm.DNSServers[0] != "10.10.0.53" {
			t.Fatalf("segment %+v, vm %+v", sg, vm)
		}
	})
}

// segmentInputs are the seeded RHEL form's, in Web Platform's dev.
func segmentInputs() map[string]any {
	return map[string]any{"role": "web", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
		"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}
}

// portGroups makes and removes an NSX segment's port group in vcsim, as
// NSX does in vCenter once it has realised the segment.
func portGroups(t *testing.T, ctx context.Context, c *vim25.Client) func(nsx.Segment, bool) {
	return func(s nsx.Segment, deleted bool) {
		if err := vsphere.SegmentPortGroup(ctx, c, s.DisplayName, "dvpg-app-dev-vl310", deleted); err != nil {
			t.Error(err)
		}
	}
}

// TestSegmentProvisioning: a VM built on a segment made for its deployment
// (N1.2): the subnet carved from the block, the segment made on the Tier-1
// and tagged as rvav's, its port group waited for, the VM's address in that
// subnet and its adapter on the segment; all of it deleted with the
// deployment, after the VM.
func TestSegmentProvisioning(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		fakeNSX := nsx.NewFakeServer()
		fakeNSX.RealizeAfter = 2
		fakeNSX.OnSegment = portGroups(t, ctx, c)
		client := useFakeNSX(h, fakeNSX)
		h.acts.NSX, h.acts.NSXTimeout = client, 20*time.Second
		// The development EfficientIP has the nsx-gva-dev block (10.60.0.0/22).
		item, version := segmentItem(h)
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		r := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })]
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Priority: &r.Priority, Clusters: r.Clusters, Datastores: r.Datastores, Networks: r.Networks, Folder: r.Folder,
			ClusterTags: &r.ClusterTags, DatastoreTags: &r.DatastoreTags, Segments: &api.PlacementSegments{Tier1: "t1-gva-dev",
				IpBlock: "nsx-gva-dev", PrefixLength: 26, DnsZone: "corp.example", DnsServers: &[]string{"10.10.0.53"}}}, 200, nil)

		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item, VersionId: version, ProjectId: "prj_web",
			Environment: "dev", Inputs: segmentInputs()}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		req = h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)

		var depID string
		if err := h.store.Pool.QueryRow(ctx, `select id from deployments where created_by_request_id = $1`, req.Id).Scan(&depID); err != nil {
			t.Fatal(err)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+depID, "tnguyen", nil, 200, &dep)
		segID := provision.SegmentName(dep.Id, "app")
		seg, err := client.Segment(ctx, segID)
		if err != nil || seg.ConnectivityPath != "/infra/tier-1s/t1-gva-dev" || seg.Subnets[0].GatewayAddress != "10.60.0.1/26" ||
			!nsx.Owned(seg.Tags, "rvav") || !slices.Contains(seg.Tags, nsx.Tag{Scope: nsx.DeployScope, Tag: dep.Id}) {
			t.Fatalf("segment = %+v, %v", seg, err)
		}
		types := map[string]string{}
		rows, _ := h.store.Pool.Query(ctx, `select type, name from resources where deployment_id = $1`, dep.Id)
		for rows.Next() {
			var typ, name string
			_ = rows.Scan(&typ, &name)
			types[typ] = name
		}
		rows.Close()
		if types["nsx.segment"] != segID || types["ipam.subnet"] != "10.60.0.0/26" || !strings.HasPrefix(types["ipam.address"], "10.60.0.") {
			t.Fatalf("resources = %v", types)
		}
		// The deployment's page lists it.
		if _, raw, _ := h.do("GET", "/api/v1/deployments/"+dep.Id, "tnguyen", nil); !strings.Contains(string(raw), `"type":"nsx.segment"`) ||
			!strings.Contains(string(raw), `"tier1":"t1-gva-dev"`) || !strings.Contains(string(raw), `"type":"ipam.subnet"`) {
			t.Fatalf("deployment = %s", raw)
		}
		vm, err := h.vc.FindVM(ctx, dep.Name)
		if err != nil || len(vm.NICs) == 0 || vm.NICs[0].Network != segID {
			t.Fatalf("vm = %+v, %v", vm, err)
		}
		var mapped string
		_ = h.store.Pool.QueryRow(ctx, `select subnet from network_subnets where network = $1 and source = 'nsx'`, segID).Scan(&mapped)
		if mapped != "10.60.0.0/26" {
			t.Fatalf("mapping = %q", mapped)
		}
		// It goes with the deployment: not remapped by hand.
		if code, raw, _ := h.do("PUT", "/api/v1/admin/network-mappings/"+segID, admin, api.NetworkMappingWrite{Subnet: "10.99.0.0/24",
			Gateway: "10.99.0.1", DnsZone: "corp.example"}); code != 409 {
			t.Fatalf("remapping the segment = %d %s", code, raw)
		}
		var status, system string
		_ = h.store.Pool.QueryRow(ctx, `select status, system from request_steps where request_id = $1 and key = 'nsx.segment:app'`,
			req.Id).Scan(&status, &system)
		if status != "done" || system != "nsx" {
			t.Fatalf("segment step: %q %q", status, system)
		}

		// Deleted with the deployment, after the VM: the segment, its port
		// group, its subnet and its mapping.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+dep.Id+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		if _, err := client.Segment(ctx, segID); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("segment after delete: %v", err)
		}
		if ok, _ := h.vc.NetworkExists(ctx, segID); ok {
			t.Fatal("the port group is still in vCenter")
		}
		subnets, _ := h.acts.IPAM.Subnets(ctx)
		if slices.ContainsFunc(subnets, func(s efficientip.SubnetInfo) bool { return s.Name == segID }) {
			t.Fatal("the subnet is still in EfficientIP")
		}
		var n int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from network_subnets where network = $1`, segID).Scan(&n)
		if n != 0 {
			t.Fatal("the mapping is still there")
		}
	}, simulator.VPX())
}

// groupItem is the seeded RHEL item whose VM is in a security group made
// for its deployment, with its firewall rules, and in an existing one.
func groupItem(h *harness) (string, string) {
	h.t.Helper()
	return rhelVariant(h, "rhel9-groups", "RHEL 9 in security groups", groupBlueprint("nsx:grp-backup-clients", "SSH"))
}

// groupBlueprint puts the VM in a group of its deployment, with rules into
// it from any on HTTPS and from what ssh names on service, and in
// grp-web-servers.
func groupBlueprint(from, service string) func(bp map[string]any) {
	return func(bp map[string]any) {
		bp["groups"] = []any{"web", "nsx:grp-web-servers"}
		bp["security_groups"] = []any{map[string]any{"key": "web", "title": "Web servers", "rules": []any{
			map[string]any{"from": []any{"any"}, "services": []any{"HTTPS"}},
			map[string]any{"from": []any{from}, "services": []any{service}},
		}}}
	}
}

// TestSecurityGroupPlanning: a request for an item whose VM is in NSX
// security groups (N1.3): refused without an NSX Manager, or when NSX
// lacks a group it names; else planned with its deployment's group, the
// firewall rules into it, and the tags putting the VM in both groups.
func TestSecurityGroupPlanning(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		// Saved published only with an NSX Manager that has what it names.
		gone := rhelYAML(h, "rhel9-groups-gone", "RHEL 9 in groups NSX lacks", groupBlueprint("nsx:grp-gone", "NOPE"))
		refused := func() []string {
			t.Helper()
			var problem api.Problem
			h.json("POST", "/api/v1/admin/catalog/import", "mkeller", rawYAML(gone), 422, &problem, "Content-Type", "application/yaml")
			var out []string
			for _, e := range *problem.Errors {
				out = append(out, e.Field+" "+deref(e.Code))
			}
			slices.Sort(out)
			return out
		}
		if got := refused(); !slices.Equal(got, []string{"/versions/0/blueprint no_nsx"}) {
			t.Fatalf("saved without NSX: %v", got)
		}
		client := useFakeNSX(h, nsx.NewFakeServer())
		if got := refused(); !slices.Equal(got, []string{"/versions/0/blueprint/security_groups/0/rules/1/from/0 unknown",
			"/versions/0/blueprint/security_groups/0/rules/1/services/0 unknown"}) {
			t.Fatalf("saved with what NSX lacks: %v", got)
		}

		item, version := groupItem(h)
		draft := api.RequestDraft{CatalogItemId: item, VersionId: version, ProjectId: "prj_web", Environment: "dev",
			Inputs: segmentInputs()}
		var pv api.RequestPreview
		url := h.svc.Config.NSX.URL
		h.svc.Config.NSX.URL = ""
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "no_nsx" }) {
			t.Fatalf("without NSX: %+v", pv.Errors)
		}
		h.svc.Config.NSX.URL = url
		// A group deleted in NSX since.
		backup, err := client.Group(ctx, "grp-backup-clients")
		if err != nil {
			t.Fatal(err)
		}
		if err := client.DeleteGroup(ctx, "grp-backup-clients"); err != nil {
			t.Fatal(err)
		}
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "unknown_group" }) {
			t.Fatalf("a group NSX lacks: %+v", pv.Errors)
		}
		if err := client.PutGroup(ctx, backup); err != nil {
			t.Fatal(err)
		}

		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if !pv.Valid {
			t.Fatalf("preview: %+v", pv.Errors)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		var raw []byte
		if err := h.store.Pool.QueryRow(ctx, `select plan from requests where id = $1`, req.Id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plan provision.Plan
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		if len(plan.Security) != 1 || len(plan.VMs) != 1 {
			t.Fatalf("plan: %d security units, %d VMs", len(plan.Security), len(plan.VMs))
		}
		sec, vm := plan.Security[0], plan.VMs[0]
		web := provision.GroupName(vm.DeploymentID, "web")
		if sec.DeploymentID != vm.DeploymentID || sec.Policy != provision.PolicyName(vm.DeploymentID) || len(sec.Groups) != 1 ||
			sec.Groups[0].ID != web || sec.Groups[0].Tag != (nsx.Tag{Scope: nsx.GroupScope, Tag: web}) || len(sec.Rules) != 2 {
			t.Fatalf("security = %+v", sec)
		}
		r1, r2 := sec.Rules[0], sec.Rules[1]
		if !slices.Equal(r1.SourceGroups, []string{"ANY"}) || !slices.Equal(r1.Services, []string{nsx.ServicePath + "HTTPS"}) ||
			!slices.Equal(r1.DestinationGroups, []string{nsx.GroupPath + web}) || r1.Action != "ALLOW" ||
			!slices.Equal(r2.SourceGroups, []string{nsx.GroupPath + "grp-backup-clients"}) || r2.ID != "web-2" {
			t.Fatalf("rules = %+v", sec.Rules)
		}
		if len(vm.NSXGroups) != 2 || vm.NSXGroups[0].ID != web || vm.NSXGroups[1].ID != "grp-web-servers" ||
			!slices.Equal(vm.NSXGroups[1].Tags, []nsx.Tag{{Scope: "role", Tag: "web"}}) {
			t.Fatalf("vm groups = %+v", vm.NSXGroups)
		}
		for _, key := range []string{"nsx.security", "nsx.tags"} {
			var system string
			if err := h.store.Pool.QueryRow(ctx, `select system from request_steps where request_id = $1 and key = $2`, req.Id, key).
				Scan(&system); err != nil || system != "nsx" {
				t.Fatalf("step %s: %q %v", key, system, err)
			}
		}

		// Groups the form chose, through the groups binding.
		bound, boundVersion := rhelVariant(h, "rhel9-groups-bound", "RHEL 9 in chosen groups", func(bp map[string]any) {
			groupBlueprint("any", "SSH")(bp)
			bp["bindings"].(map[string]any)["groups"] = `inputs.role == "web" ? ["nsx:grp-backup-clients", "web"] : []`
		})
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: bound, VersionId: boundVersion,
			ProjectId: "prj_web", Environment: "dev", Inputs: segmentInputs()}, 202, &req, "Idempotency-Key", uuid.NewString())
		_ = h.store.Pool.QueryRow(ctx, `select plan from requests where id = $1`, req.Id).Scan(&raw)
		plan = provision.Plan{}
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		var ids, offered []string
		for _, g := range plan.VMs[0].NSXGroups {
			ids = append(ids, g.ID)
		}
		for _, g := range plan.VMs[0].NSXOffered {
			offered = append(offered, g.ID)
		}
		web = provision.GroupName(plan.VMs[0].DeploymentID, "web")
		if !slices.Equal(ids, []string{web, "grp-web-servers", "grp-backup-clients"}) ||
			!slices.Equal(offered, []string{web, "grp-web-servers", "grp-backup-clients"}) {
			t.Fatalf("bound groups = %v, offered %v", ids, offered)
		}

		// A rule from a group of addresses: its members needn't be by tag.
		if err := client.PutGroup(ctx, nsx.Group{ID: "grp-mgmt-ips", DisplayName: "Management addresses",
			Expression: []nsx.Condition{{ResourceType: "IPAddressExpression"}}}); err != nil {
			t.Fatal(err)
		}
		ips, ipsVersion := rhelVariant(h, "rhel9-groups-ips", "RHEL 9 reached from management", groupBlueprint("nsx:grp-mgmt-ips", "SSH"))
		h.json("POST", "/api/v1/requests/preview", "tnguyen", api.RequestDraft{CatalogItemId: ips, VersionId: ipsVersion,
			ProjectId: "prj_web", Environment: "dev", Inputs: segmentInputs()}, 200, &pv)
		if !pv.Valid {
			t.Fatalf("a rule from a group of addresses: %+v", pv.Errors)
		}
		// Another deployment's group, through a groups binding: refused.
		if err := client.PutGroup(ctx, nsx.Group{ID: "othergrp", Tags: []nsx.Tag{{Scope: nsx.OwnerScope, Tag: "rvav"},
			{Scope: nsx.DeployScope, Tag: "dep_other"}}, Expression: []nsx.Condition{nsx.TagCondition(provision.GroupTag("othergrp"))}}); err != nil {
			t.Fatal(err)
		}
		from, fromVersion := rhelVariant(h, "rhel9-groups-input", "RHEL 9 in a group from its role", func(bp map[string]any) {
			groupBlueprint("any", "SSH")(bp)
			bp["bindings"].(map[string]any)["groups"] = `["nsx:" + inputs.role]`
		})
		other := segmentInputs()
		other["role"] = "othergrp"
		h.json("POST", "/api/v1/requests/preview", "tnguyen", api.RequestDraft{CatalogItemId: from, VersionId: fromVersion,
			ProjectId: "prj_web", Environment: "dev", Inputs: other}, 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "rvav_group" }) {
			t.Fatalf("another deployment's group: %+v", pv.Errors)
		}
		// A segment an input names, on an item that has none: refused.
		named := segmentInputs()
		named["network"] = "segment:app"
		h.json("POST", "/api/v1/requests/preview", "tnguyen", api.RequestDraft{CatalogItemId: item, VersionId: version,
			ProjectId: "prj_web", Environment: "dev", Inputs: named}, 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "unknown_segment" }) {
			t.Fatalf("a segment from an input: %+v", pv.Errors)
		}

		// The form designer's NSX pickers: neither rvav's groups nor one of
		// addresses are offered.
		if _, err := h.store.Pool.Exec(ctx, `update catalog_item_versions set ui_schema = jsonb_set(jsonb_set(jsonb_set(jsonb_set(ui_schema,
			'{fields,pick_segment}', '{"widget": "select", "label": "Segment", "value_source": {"kind": "builtin", "name": "nsx.segments", "params": {"tier1": "t1-gva-dev"}}}'),
			'{fields,pick_tier1}', '{"widget": "select", "label": "Tier-1", "value_source": {"kind": "builtin", "name": "nsx.tier1s"}}'),
			'{fields,pick_group}', '{"widget": "multiselect", "label": "Groups", "value_source": {"kind": "builtin", "name": "nsx.groups"}}'),
			'{fields,pick_service}', '{"widget": "select", "label": "Service", "value_source": {"kind": "builtin", "name": "nsx.services"}}')
			where id = $1`, version); err != nil {
			t.Fatal(err)
		}
		dev := api.EnvironmentKey("dev")
		pick := func(field string) []string {
			t.Helper()
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", "tnguyen", api.FieldValuesQuery{VersionId: version, ProjectId: "prj_web",
				Environment: &dev, Field: field, Inputs: map[string]any{}}, 200, &out)
			var values []string
			for _, o := range *out.Options {
				values = append(values, o.Value)
			}
			return values
		}
		// Segments on the Tier-1 asked for; groups VMs can join by tag.
		if got := pick("pick_segment"); !slices.Equal(got, []string{"seg-gva-dev-app"}) {
			t.Fatalf("nsx.segments = %v", got)
		}
		if got := pick("pick_tier1"); len(got) != 3 || !slices.Contains(got, "t1-gva-dev") {
			t.Fatalf("nsx.tier1s = %v", got)
		}
		if got := pick("pick_group"); !slices.Equal(got, []string{"nsx:grp-backup-clients", "nsx:grp-web-servers"}) {
			t.Fatalf("nsx.groups = %v", got)
		}
		if got := pick("pick_service"); len(got) != 8 || !slices.Contains(got, "HTTPS") {
			t.Fatalf("nsx.services = %v", got)
		}
	})
}

// TestSecurityGroupProvisioning: a VM built in NSX security groups (N1.3):
// its deployment's group made, tagged as rvav's and selecting the VMs of
// its tag, the firewall rules into it in a policy of their own; the VM
// tagged once NSX sees it, joining that group and an existing one; all of
// it deleted with the deployment, the VM's tags first.
func TestSecurityGroupProvisioning(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		fakeNSX := nsx.NewFakeServer()
		fakeNSX.AnyVM, fakeNSX.SeeAfter = true, 2
		// A CMDB tagged it role|web before rvav: grp-web-servers' tag.
		fakeNSX.NewVMTags = []nsx.Tag{{Scope: "role", Tag: "web"}}
		client := useFakeNSX(h, fakeNSX)
		h.acts.NSX, h.acts.NSXTimeout = client, 20*time.Second
		item, version := groupItem(h)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item, VersionId: version, ProjectId: "prj_web",
			Environment: "dev", Inputs: segmentInputs()}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		req = h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)

		var depID, uuidVM string
		if err := h.store.Pool.QueryRow(ctx, `select d.id, r.external_id from deployments d join resources r on r.deployment_id = d.id
			where d.created_by_request_id = $1 and r.type = 'vsphere.vm'`, req.Id).Scan(&depID, &uuidVM); err != nil {
			t.Fatal(err)
		}
		web := provision.GroupName(depID, "web")
		owner := []nsx.Tag{{Scope: nsx.OwnerScope, Tag: "rvav"}, {Scope: nsx.DeployScope, Tag: depID}}
		g, err := client.Group(ctx, web)
		if err != nil || !slices.Equal(g.Tags, owner) || len(g.Expression) != 1 ||
			g.Expression[0] != nsx.TagCondition(nsx.Tag{Scope: nsx.GroupScope, Tag: web}) {
			t.Fatalf("group = %+v, %v", g, err)
		}
		pol, err := client.SecurityPolicy(ctx, provision.PolicyName(depID))
		if err != nil || !slices.Equal(pol.Tags, owner) || len(pol.Rules) != 2 || pol.Rules[0].Services[0] != "/infra/services/HTTPS" ||
			!slices.Equal(pol.Scope, []string{nsx.GroupPath + web}) {
			t.Fatalf("policy = %+v, %v", pol, err)
		}
		vm, _ := fakeNSX.VM(uuidVM)
		want := append(slices.Clone(owner), nsx.Tag{Scope: nsx.GroupScope, Tag: web}, nsx.Tag{Scope: "role", Tag: "web"})
		if !sameTags(vm.Tags, want) {
			t.Fatalf("VM tags = %+v, want %+v", vm.Tags, want)
		}
		types := map[string]int{}
		rows, _ := h.store.Pool.Query(ctx, `select type from resources where deployment_id = $1`, depID)
		for rows.Next() {
			var typ string
			_ = rows.Scan(&typ)
			types[typ]++
		}
		rows.Close()
		if types["nsx.group"] != 1 || types["nsx.policy"] != 1 || types["nsx.membership"] != 1 {
			t.Fatalf("resources = %v", types)
		}
		for _, key := range []string{"nsx.security", "nsx.tags"} {
			var status string
			_ = h.store.Pool.QueryRow(ctx, `select status from request_steps where request_id = $1 and key = $2`, req.Id, key).Scan(&status)
			if status != "done" {
				t.Fatalf("step %s: %q", key, status)
			}
		}

		// Day-2: out of grp-web-servers, and back in; only the groups its
		// item offers.
		var vmRes string
		_ = h.store.Pool.QueryRow(ctx, `select id from resources where deployment_id = $1 and type = 'vsphere.vm'`, depID).Scan(&vmRes)
		var actions struct{ Items []api.Day2Action }
		h.json("GET", "/api/v1/deployments/"+depID+"/actions", "tnguyen", nil, 200, &actions)
		offered := func(key string) (bool, string) {
			for _, a := range actions.Items {
				if a.Key == key && deref(a.ResourceId) == vmRes {
					return a.Enabled, deref(a.DisabledReason)
				}
			}
			return false, "not listed"
		}
		if ok, why := offered("nsx_group_add"); ok || why != "The VM is in every security group its item offers." {
			t.Fatalf("nsx_group_add: %v %q", ok, why)
		}
		change := func(action, group string) {
			t.Helper()
			var r api.Request
			h.json("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: action, ResourceId: &vmRes,
				Inputs: map[string]any{"group": group}}, 202, &r, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(r.Id, nil)
			h.waitStatus(r.Id, "tnguyen", api.RequestStatusSucceeded)
		}
		change("nsx_group_remove", "grp-web-servers")
		var groups int
		_ = h.store.Pool.QueryRow(ctx, `select jsonb_array_length(properties->'groups') from resources
			where deployment_id = $1 and type = 'nsx.membership'`, depID).Scan(&groups)
		// role|web was the CMDB's, not rvav's: it stays.
		if vm, _ := fakeNSX.VM(uuidVM); !sameTags(vm.Tags, want) || groups != 1 {
			t.Fatalf("VM tags out of grp-web-servers = %+v, %d groups", vm.Tags, groups)
		}
		h.json("GET", "/api/v1/deployments/"+depID+"/actions", "tnguyen", nil, 200, &actions)
		if ok, why := offered("nsx_group_add"); !ok {
			t.Fatalf("nsx_group_add: %q", why)
		}
		change("nsx_group_add", "grp-web-servers")
		if vm, _ := fakeNSX.VM(uuidVM); !sameTags(vm.Tags, want) {
			t.Fatalf("VM tags back in grp-web-servers = %+v", vm.Tags)
		}
		code, raw, _ := h.do("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: "nsx_group_add",
			ResourceId: &vmRes, Inputs: map[string]any{"group": "grp-backup-clients"}}, "Idempotency-Key", uuid.NewString())
		if code != 409 && code != 422 {
			t.Fatalf("a group the item doesn't offer = %d %s", code, raw)
		}

		// Drift: role|web removed in NSX takes the VM out of grp-web-servers;
		// putting it back makes it rvav's.
		fakeNSX.SetVMTagsDirectly(uuidVM, want[:3])
		tagSync := &inventory.NSXTagSync{Store: h.store, NSX: client, Events: h.acts.Events, Log: h.acts.Log}
		if err := tagSync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var depState string
		_ = h.store.Pool.QueryRow(ctx, `select sync_state from deployments where id = $1`, depID).Scan(&depState)
		if _, raw, _ := h.do("GET", "/api/v1/deployments/"+depID, "tnguyen", nil); depState != "drifted" ||
			!strings.Contains(string(raw), `"missing":["role|web"]`) {
			t.Fatalf("after the tag was removed: %s, %s", depState, raw)
		}
		// Joining it again restores the tag.
		h.json("GET", "/api/v1/deployments/"+depID+"/actions", "tnguyen", nil, 200, &actions)
		if ok, why := offered("nsx_group_add"); !ok {
			t.Fatalf("nsx_group_add while drifted: %q", why)
		}
		change("nsx_group_add", "grp-web-servers")
		if vm, _ := fakeNSX.VM(uuidVM); !sameTags(vm.Tags, want) {
			t.Fatalf("VM tags restored = %+v", vm.Tags)
		}
		if err := tagSync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		_ = h.store.Pool.QueryRow(ctx, `select sync_state from deployments where id = $1`, depID).Scan(&depState)
		if depState != "in_sync" {
			t.Fatalf("after joining again: %s", depState)
		}
		// The page's resources: the group, its rules, the VM's groups.
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+depID, "tnguyen", nil, 200, &dep)
		kinds := map[string]bool{}
		for _, r := range dep.Resources {
			if d, err := r.Discriminator(); err == nil {
				kinds[d] = true
			}
		}
		if !kinds["nsx.group"] || !kinds["nsx.policy"] || !kinds["nsx.membership"] {
			t.Fatalf("deployment resources = %v", kinds)
		}

		// Deleted with the deployment: the VM out of its groups first, the
		// rules, then the group.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		if vm, _ := fakeNSX.VM(uuidVM); len(vm.Tags) != 0 {
			t.Fatalf("VM tags after delete = %+v", vm.Tags)
		}
		if _, err := client.SecurityPolicy(ctx, provision.PolicyName(depID)); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("policy after delete: %v", err)
		}
		if _, err := client.Group(ctx, web); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("group after delete: %v", err)
		}
		if _, err := client.Group(ctx, "grp-web-servers"); err != nil {
			t.Fatalf("the existing group: %v", err)
		}
	}, simulator.VPX())
}

// sameTags says whether two tag lists hold the same tags, in any order.
func sameTags(a, b []nsx.Tag) bool {
	return len(a) == len(b) && !slices.ContainsFunc(a, func(t nsx.Tag) bool { return !slices.Contains(b, t) })
}
