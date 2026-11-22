package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/vra"
)

// TestVRADeleteProtection: read from vRA (the fake), vra-web-pair has one
// VM vRA keeps from deletion (its resource's preventDelete) and one it
// doesn't; vra-web-001 is kept by the template version it was requested
// from. The plan shows it, adoption keeps it, and the deployment's Delete
// is refused, naming the VM, until an administrator allows it (#190).
func TestVRADeleteProtection(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		e := vra.SampleEstate(time.Now())
		rhel := &e.Blueprints[0]
		rhel.Versions[1].Content = strings.Replace(rhel.Versions[1].Content, "    type: Cloud.vSphere.Machine\n",
			"    type: Cloud.vSphere.Machine\n    preventDelete: true\n", 1)
		rhel.Content = rhel.Versions[1].Content
		publishVRAItem(h, importVRAItems(h, &e)["bp-rhel9"])
		for i, name := range []string{"vra-web-001", "vra-api-001", "vra-api-002"} {
			existingVM(t, ctx, h, c, model, name, fmt.Sprintf("10.31.10.%d", 141+i))
		}
		if err := (&inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}).SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		fake := httptest.NewServer(vra.NewFakeServer(e))
		defer fake.Close()
		h.svc.Config.VRA.URL, h.svc.Config.VRA.Token = fake.URL, vra.FakeRefreshToken
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, nil, 201, nil)

		plan := func() api.VraPlan {
			t.Helper()
			var p api.VraPlan
			h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
			return p
		}
		dep := func(p api.VraPlan, vraID string) api.VraDeploymentPlan {
			t.Helper()
			return p.Deployments[slices.IndexFunc(p.Deployments, func(d api.VraDeploymentPlan) bool { return d.VraId == vraID })]
		}
		kept := func(d api.VraDeploymentPlan) []string {
			var out []string
			if d.Vms == nil {
				return nil
			}
			for _, vm := range *d.Vms {
				if vm.PreventDelete != nil && *vm.PreventDelete {
					out = append(out, vm.Name)
				}
			}
			return out
		}
		note := func(d api.VraDeploymentPlan, text string) bool {
			return slices.ContainsFunc(d.Notes, func(n api.VraNote) bool { return strings.Contains(n.Text, text) })
		}

		// Before adoption: vRA's protection.
		p := plan()
		pair, web := dep(p, "dep-vra-2"), dep(p, "dep-vra-1")
		if pair.Status != "adopt" || pair.Vms == nil || len(*pair.Vms) != 2 || !slices.Equal(kept(pair), []string{"vra-api-001"}) ||
			!note(pair, "vRA keeps vra-api-001 from deletion (preventDelete)") {
			t.Fatalf("vra-web-pair planned = %+v %+v", pair.Vms, pair.Notes)
		}
		if web.Status != "adopt" || !slices.Equal(kept(web), []string{"vra-web-001"}) {
			t.Fatalf("vra-web-001 planned = %+v %+v", web.Vms, web.Notes)
		}

		// Adopted: rvav's.
		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{Catalog: ptr(false), Deployments: ptr(true),
			Projects: &[]string{"vra-prj-web"}}, 200, &res)
		if !slices.Equal(res.Adopted, []string{"vra-web-001", "vra-web-pair"}) || len(res.Failed) != 0 {
			t.Fatalf("import = %+v", res)
		}
		pair = dep(res.Plan, "dep-vra-2")
		if pair.Status != "adopted" || !slices.Equal(kept(pair), []string{"vra-api-001"}) || !note(pair, "Kept from deletion, as in vRA: vra-api-001.") {
			t.Fatalf("vra-web-pair adopted = %+v %+v", pair.Vms, pair.Notes)
		}
		var audit struct{ Items []api.AuditEvent }
		h.json("GET", "/api/v1/audit?action=deployment.adopted", admin, nil, 200, &audit)
		protected := map[string]string{}
		for _, a := range audit.Items {
			protected[fmt.Sprint(a.Detail["name"])] = fmt.Sprint(a.Detail["prevent_delete"])
		}
		if protected["vra-web-pair"] != "[vra-api-001]" || protected["vra-web-001"] != "[vra-web-001]" {
			t.Fatalf("audited = %v", protected)
		}

		d := &vmDeployment{h: h, id: (*pair.Deployments)[0]}
		del := d.actions("lmartin")["delete"]
		if del.Enabled || !strings.Contains(deref(del.DisabledReason), "keeps vra-api-001 from being deleted") ||
			strings.Contains(deref(del.DisabledReason), "vra-api-002") {
			t.Fatalf("owner's delete = %+v", del)
		}
		w := &vmDeployment{h: h, id: (*dep(res.Plan, "dep-vra-1").Deployments)[0]}
		if del := w.actions("tnguyen")["delete"]; del.Enabled || !strings.Contains(deref(del.DisabledReason), "keeps vra-web-001 from being deleted") {
			t.Fatalf("vra-web-001's delete = %+v", del)
		}
		action := func(user, key string, want int) []byte {
			t.Helper()
			code, body, _ := h.do("POST", "/api/v1/deployments/"+d.id+"/actions", user, api.Day2Draft{Action: key, Inputs: map[string]any{}},
				"Idempotency-Key", uuid.NewString())
			if code != want {
				t.Fatalf("%s %s: %d %s, want %d", user, key, code, body, want)
			}
			return body
		}
		if body := action("lmartin", "delete", 409); !strings.Contains(string(body), "keeps vra-api-001 from being deleted") {
			t.Fatalf("delete refused with %s", body)
		}

		// Allowed: the plan says rvav no longer keeps it; the deployment is
		// deleted, both its VMs.
		var r api.Request
		if err := json.Unmarshal(action(admin, "allow_delete", 202), &r); err != nil {
			t.Fatal(err)
		}
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, admin, api.RequestStatusSucceeded)
		if pair = dep(plan(), "dep-vra-2"); len(kept(pair)) != 0 || !note(pair, "vRA keeps vra-api-001 from deletion, rvav doesn't") {
			t.Fatalf("vra-web-pair allowed = %+v %+v", pair.Vms, pair.Notes)
		}
		if err := json.Unmarshal(action("lmartin", "delete", 202), &r); err != nil {
			t.Fatal(err)
		}
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, "lmartin", api.RequestStatusSucceeded)
		if vmExists(ctx, c, "vra-api-001") || vmExists(ctx, c, "vra-api-002") || !vmExists(ctx, c, "vra-web-001") {
			t.Fatal("deleting vra-web-pair: its VMs are still in vCenter, or vra-web-001 isn't")
		}
	}, model)
}
