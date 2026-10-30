package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
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
