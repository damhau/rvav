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
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vra"
	"github.com/damhau/rvav/internal/vraimport"
)

// TestVRADeployments: vRA's deployments adopted through onboarding, with
// their owner, project (by name, or mapped), environment, item and what
// remains of their lease; those that can't be are left in vRA with the
// reason; running it again adopts nothing twice (P2.3).
func TestVRADeployments(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		e := vra.SampleEstate(time.Now())
		items := importVRAItems(h, &e)
		publishVRAItem(h, items["bp-rhel9"])
		publishVRAItem(h, items["bp-db"])
		// The VMs vRA built are in vCenter (vra-gone-001 isn't).
		for i, name := range []string{"vra-web-001", "vra-api-001", "vra-api-002", "vra-db-001", "vra-leg-001"} {
			existingVM(t, ctx, h, c, model, name, "10.31.10."+string(rune('0'+i))+"1")
		}
		if err := (&inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}).SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		admin, err := auth.ForUser(ctx, h.store, "usr_mkeller")
		if err != nil {
			t.Fatal(err)
		}
		// rvav's Data Engineering is vRA's Data Services.
		projectMap := map[string]string{"vra-prj-data": "prj_data"}
		if _, err := h.svc.AdoptVRADeployments(ctx, &auth.Principal{User: store.User{ID: "usr_tnguyen"}}, &e, projectMap, false); err == nil {
			t.Fatal("a member planned an adoption")
		}
		status := func(plans []vraimport.DeploymentPlan) map[string]vraimport.DeploymentPlan {
			out := map[string]vraimport.DeploymentPlan{}
			for _, p := range plans {
				out[p.Name] = p
			}
			return out
		}
		note := func(p vraimport.DeploymentPlan, text string) bool {
			return slices.ContainsFunc(p.Notes, func(n vraimport.Note) bool { return strings.Contains(n.Text, text) })
		}

		// The dry plan, then adopting.
		dry, err := h.svc.AdoptVRADeployments(ctx, admin, &e, projectMap, false)
		if err != nil {
			t.Fatal(err)
		}
		if s := status(dry); s["vra-web-001"].Status != vraimport.Adopt || len(s["vra-web-pair"].VMs) != 2 {
			t.Fatalf("plan = %+v", dry)
		}
		var n int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from deployments where name like 'vra-%'`).Scan(&n)
		if n != 0 {
			t.Fatal("a plan adopted something")
		}
		plans, err := h.svc.AdoptVRADeployments(ctx, admin, &e, projectMap, true)
		if err != nil {
			t.Fatal(err)
		}
		s := status(plans)
		for name, want := range map[string]string{"vra-web-001": vraimport.Adopted, "vra-web-pair": vraimport.Adopted,
			"vra-db-001": vraimport.Skipped, "vra-gone": vraimport.Skipped, "vra-legacy": vraimport.Skipped} {
			if s[name].Status != want {
				t.Fatalf("%s: %s, want %s: %+v", name, s[name].Status, want, s[name].Notes)
			}
		}
		if !note(s["vra-db-001"], "isn't a member of Data Engineering") || !note(s["vra-gone"], "vra-gone-001") ||
			!note(s["vra-legacy"], "Legacy Apps has no rvav project") {
			t.Fatalf("reasons: %+v %+v %+v", s["vra-db-001"].Notes, s["vra-gone"].Notes, s["vra-legacy"].Notes)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+s["vra-web-pair"].Deployments[0], "lmartin", nil, 200, &dep)
		if dep.Name != "vra-web-pair" || dep.Owner.Id != "usr_lmartin" || dep.Project.Id != "prj_web" || dep.Environment != "dev" ||
			dep.VmCount != 2 || dep.Lease.ExpiresAt == nil ||
			dep.Lease.ExpiresAt.Sub(time.Now()) < 44*24*time.Hour || dep.Lease.ExpiresAt.Sub(time.Now()) > 46*24*time.Hour {
			t.Fatalf("vra-web-pair = %+v (lease %+v)", dep, dep.Lease)
		}

		// Léa joins Data Engineering: run again, her database is adopted,
		// and nothing is adopted twice.
		h.json("PUT", "/api/v1/projects/prj_data/members/usr_lmartin", "mkeller", api.ProjectMemberWrite{Role: api.Member}, 200, nil)
		again, err := h.svc.AdoptVRADeployments(ctx, admin, &e, projectMap, true)
		if err != nil {
			t.Fatal(err)
		}
		first := s["vra-web-001"].Deployments
		s = status(again)
		if s["vra-db-001"].Status != vraimport.Adopted || len(s["vra-db-001"].Deployments) != 1 ||
			s["vra-web-001"].Status != vraimport.Adopted || !slices.Equal(s["vra-web-001"].Deployments, first) {
			t.Fatalf("again: db %+v, web %+v", s["vra-db-001"], s["vra-web-001"])
		}
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from deployments where name like 'vra-%' and status <> 'deleted'`).Scan(&n)
		if n != 3 {
			t.Fatalf("%d vra deployments in rvav, want 3", n)
		}
	}, model)
}

// TestVRADeploymentNSX: a vRA deployment's NSX segment and security group
// adopted with it (N1.4): found by name, tagged as rvav's, recorded on the
// deployment with the group's firewall rules, and deleted with it; a group
// other rules use is left as it is.
func TestVRADeploymentNSX(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		client := useFakeNSX(h, nsx.NewFakeServer())
		h.acts.NSX = client
		e := vra.SampleEstate(time.Now())
		items := importVRAItems(h, &e)
		publishVRAItem(h, items["bp-rhel9"])
		existingVM(t, ctx, h, c, model, "vra-web-001", "10.31.10.41")
		if err := (&inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}).SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		// What vRA's account made in NSX for vra-web-001: a segment, three
		// groups, the rules into the first. The security team's policies
		// use the second (with another group) and the third.
		const vraUser = "svc-vra"
		seg := nsx.Segment{ID: "vra-seg-1", DisplayName: "web-net-mcm81", ConnectivityPath: nsx.Tier1Path + "t1-gva-dev",
			Subnets: []nsx.SegmentSubnet{{GatewayAddress: "10.70.0.1/26", Network: "10.70.0.0/26"}}}
		rule := func(from, to string) nsx.Rule {
			return nsx.Rule{ID: "r1", SourceGroups: []string{from}, DestinationGroups: []string{to}, Services: []string{"ANY"}, Action: "ALLOW"}
		}
		for _, err := range []error{client.PutSegment(ctx, seg),
			// Two segments of one name: which is the deployment's can't be told.
			client.PutSegment(ctx, nsx.Segment{ID: "dup-1", DisplayName: "dup-net-mcm85"}),
			client.PutSegment(ctx, nsx.Segment{ID: "dup-2", DisplayName: "dup-net-mcm85"}),
			client.PutGroup(ctx, nsx.Group{ID: "vra-grp-1", DisplayName: "web-sg-mcm82", CreateUser: vraUser}),
			client.PutGroup(ctx, nsx.Group{ID: "vra-grp-2", DisplayName: "admin-sg-mcm83", CreateUser: vraUser}),
			client.PutGroup(ctx, nsx.Group{ID: "vra-grp-3", DisplayName: "db-sg-mcm84", CreateUser: vraUser}),
			client.PutSecurityPolicy(ctx, nsx.SecurityPolicy{ID: "vra-pol-1", DisplayName: "web-sg-mcm82", CreateUser: vraUser,
				Rules: []nsx.Rule{rule("ANY", nsx.GroupPath+"vra-grp-1")}}),
			client.PutSecurityPolicy(ctx, nsx.SecurityPolicy{ID: "shared", DisplayName: "Shared", CreateUser: "secteam",
				Rules: []nsx.Rule{rule(nsx.GroupPath+"vra-grp-2", nsx.GroupPath+"grp-web-servers")}}),
			client.PutSecurityPolicy(ctx, nsx.SecurityPolicy{ID: "app-team", DisplayName: "App team", CreateUser: "secteam",
				Rules: []nsx.Rule{rule("ANY", nsx.GroupPath+"vra-grp-3")}}),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
		for i := range e.Deployments {
			if e.Deployments[i].Name == "vra-web-001" {
				e.Deployments[i].Resources = append(e.Deployments[i].Resources,
					vra.Resource{ID: "r-net", Name: "web_net", Type: "Cloud.NSX.Network", Properties: map[string]any{
						"resourceName": "web-net-mcm81", "networkType": "outbound"}},
					vra.Resource{ID: "r-sg", Name: "web_sg", Type: "Cloud.SecurityGroup", Properties: map[string]any{
						"resourceName": "web-sg-mcm82", "securityGroupType": "new"}},
					vra.Resource{ID: "r-sg2", Name: "admin_sg", Type: "Cloud.SecurityGroup", Properties: map[string]any{
						"resourceName": "admin-sg-mcm83", "securityGroupType": "new"}},
					vra.Resource{ID: "r-sg3", Name: "db_sg", Type: "Cloud.SecurityGroup", Properties: map[string]any{
						"resourceName": "db-sg-mcm84", "securityGroupType": "new"}},
					vra.Resource{ID: "r-net2", Name: "dup_net", Type: "Cloud.NSX.Network", Properties: map[string]any{
						"resourceName": "dup-net-mcm85", "networkType": "routed"}},
					// Its NSX name not recorded: "app_net" could be any segment's.
					vra.Resource{ID: "r-net3", Name: "app_net", Type: "Cloud.NSX.Network", Properties: map[string]any{
						"networkType": "outbound"}})
			}
		}
		admin, err := auth.ForUser(ctx, h.store, "usr_mkeller")
		if err != nil {
			t.Fatal(err)
		}
		plans, err := h.svc.AdoptVRADeployments(ctx, admin, &e, map[string]string{"vra-prj-data": "prj_data"}, true)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(plans, func(p vraimport.DeploymentPlan) bool { return p.Name == "vra-web-001" })
		p := plans[i]
		if p.Status != vraimport.Adopted || !slices.Equal(p.NSXSegments, []string{"web-net-mcm81", "dup-net-mcm85"}) || len(p.NSXGroups) != 3 {
			t.Fatalf("plan = %+v", p)
		}
		for _, text := range []string{"also used by security policy shared", "also used by security policy app-team",
			"2 segments named dup-net-mcm85", "didn't record its name in NSX"} {
			if !slices.ContainsFunc(p.Notes, func(n vraimport.Note) bool { return strings.Contains(n.Text, text) }) {
				t.Fatalf("no note %q: %+v", text, p.Notes)
			}
		}
		for _, id := range []string{"dup-1", "dup-2"} {
			if s, _ := client.Segment(ctx, id); len(s.Tags) != 0 {
				t.Fatalf("%s taken: %+v", id, s)
			}
		}
		if g, _ := client.Group(ctx, "vra-grp-3"); len(g.Tags) != 0 {
			t.Fatalf("the app team's group taken: %+v", g)
		}
		depID := p.Deployments[0]
		owned := []nsx.Tag{{Scope: nsx.OwnerScope, Tag: "rvav"}, {Scope: nsx.DeployScope, Tag: depID}}
		if s, _ := client.Segment(ctx, "vra-seg-1"); !slices.Equal(s.Tags, owned) || s.ConnectivityPath != seg.ConnectivityPath {
			t.Fatalf("segment = %+v", s)
		}
		if g, _ := client.Group(ctx, "vra-grp-1"); !slices.Equal(g.Tags, owned) {
			t.Fatalf("group = %+v", g)
		}
		if g, _ := client.Group(ctx, "vra-grp-2"); len(g.Tags) != 0 {
			t.Fatalf("the shared group = %+v", g)
		}
		kinds := map[string]string{}
		rows, _ := h.store.Pool.Query(ctx, `select type, name from resources where deployment_id = $1 and type like 'nsx.%'`, depID)
		for rows.Next() {
			var typ, name string
			_ = rows.Scan(&typ, &name)
			kinds[typ] = name
		}
		rows.Close()
		if kinds["nsx.segment"] != "web-net-mcm81" || kinds["nsx.group"] != "vra-grp-1" || kinds["nsx.policy"] != "vra-pol-1" || len(kinds) != 3 {
			t.Fatalf("resources = %v", kinds)
		}

		// Deleted with the deployment; the shared group stays.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", "lmartin", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "lmartin", api.RequestStatusSucceeded)
		if _, err := client.Segment(ctx, "vra-seg-1"); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("segment after delete: %v", err)
		}
		if _, err := client.SecurityPolicy(ctx, "vra-pol-1"); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("policy after delete: %v", err)
		}
		if _, err := client.Group(ctx, "vra-grp-1"); !errors.Is(err, nsx.ErrNotFound) {
			t.Fatalf("group after delete: %v", err)
		}
		if _, err := client.Group(ctx, "vra-grp-2"); err != nil {
			t.Fatalf("the shared group: %v", err)
		}
	}, model)
}
