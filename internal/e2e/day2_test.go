package e2e

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
)

// TestDay2Actions runs day-2 actions on a VM built by a dev request: each is
// a request with its own timeline, the VM shows the result, and actions
// that don't fit the VM's state or the caller's role are refused.
func TestDay2Actions(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web = "prj_web"
		if _, err := h.store.Pool.Exec(ctx, `insert into project_members (project_id, user_id, role) values ('prj_web', 'usr_sbrunner', 'viewer')`); err != nil {
			t.Fatal(err)
		}

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		var built api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId,
			ProjectId: web, Environment: "dev", Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
			202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, nil)
		dep := (*built.DeploymentIds)[0]

		actions := func(user string) map[string]api.Day2Action {
			var out struct{ Items []api.Day2Action }
			h.json("GET", "/api/v1/deployments/"+dep+"/actions", user, nil, 200, &out)
			m := map[string]api.Day2Action{}
			for _, a := range out.Items {
				m[a.Key] = a
			}
			return m
		}
		vm := func() api.VmProperties {
			var d struct {
				Status    api.DeploymentStatus
				Resources []struct {
					Type string
					VM   api.VmProperties `json:"vm"`
				}
			}
			h.json("GET", "/api/v1/deployments/"+dep, "tnguyen", nil, 200, &d)
			for _, r := range d.Resources {
				if r.Type == "vsphere.vm" {
					return r.VM
				}
			}
			t.Fatal("no VM resource")
			return api.VmProperties{}
		}
		var resourceID string
		run := func(user, action string, want int) api.Request {
			var r api.Request
			if resourceID == "" {
				resourceID = *actions("tnguyen")["power_off"].ResourceId
			}
			d := api.Day2Draft{Action: action, ResourceId: &resourceID, Inputs: map[string]any{}}
			if want != 202 {
				h.json("POST", "/api/v1/deployments/"+dep+"/actions", user, d, want, nil, "Idempotency-Key", uuid.NewString())
				return r
			}
			h.json("POST", "/api/v1/deployments/"+dep+"/actions", user, d, want, &r, "Idempotency-Key", uuid.NewString())
			return r
		}

		// A running VM offers what a running VM can do.
		a := actions("tnguyen")
		if on := a["power_on"]; on.Enabled || deref(on.DisabledReason) != "The VM is already powered on." {
			t.Fatalf("power_on = %+v", on)
		}
		if off := a["power_off"]; !off.Enabled || !off.Destructive || off.Confirm == nil || off.Group != api.Power {
			t.Fatalf("power_off = %+v", off)
		}
		// vcsim VMs have no VMware Tools: the guest can't be asked to shut down.
		if g := a["shutdown_guest"]; g.Enabled || deref(g.DisabledReason) != "VMware Tools is not running, so the guest OS can't be asked to shut down." {
			t.Fatalf("shutdown_guest without tools = %+v", g)
		}
		if a["restart_guest"].Enabled || !a["reset"].Enabled {
			t.Fatalf("actions on a running VM = %+v", a)
		}
		// From now on they run; rvav sees it when an action refreshes the VM.
		sim := model.Map().Get(types.ManagedObjectReference{Type: "VirtualMachine", Value: vm().Moref}).(*simulator.VirtualMachine)
		sim.Guest.ToolsRunningStatus = string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)
		// Viewers see the actions but can't run them; others don't see the deployment.
		if v := actions("sbrunner")["power_off"]; v.Enabled || v.DisabledReason == nil {
			t.Fatalf("viewer's power_off = %+v", v)
		}
		run("sbrunner", "power_off", 403)
		h.json("GET", "/api/v1/deployments/"+dep+"/actions", "afavre", nil, 404, nil)

		// Power off is a request of its own.
		off := run("tnguyen", "power_off", 202)
		if off.Kind != api.Day2 || off.Action == nil || off.Action.Name != "Power off" || deref(off.DeploymentId) != dep ||
			off.Resource == nil || off.Status != api.RequestStatusQueued || off.Title != "Power off · "+off.Resource.Name {
			t.Fatalf("power off request = %+v", off)
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+off.Id+"/steps", "tnguyen", nil, 200, &steps)
		if len(steps.Items) != 4 || steps.Items[2].Title != "Power off "+off.Resource.Name {
			t.Fatalf("power off steps = %+v", steps.Items)
		}
		// One action at a time on a deployment.
		run("lmartin", "reset", 409)
		h.runWorkflow(off.Id, nil)
		done := h.waitStatus(off.Id, "tnguyen", api.RequestStatusSucceeded)
		if done.Progress.Done != 4 {
			t.Fatalf("power off progress = %+v", done.Progress)
		}
		if p := vm().PowerState; p != api.Off {
			t.Fatalf("after power off the VM is %s", p)
		}
		a = actions("tnguyen")
		if !a["power_on"].Enabled || a["shutdown_guest"].Enabled || deref(a["reset"].DisabledReason) != "The VM is powered off." {
			t.Fatalf("actions on a powered-off VM = %+v", a)
		}
		run("tnguyen", "power_off", 409)

		// The others, in turn; guest actions once Tools runs.
		for _, step := range []struct {
			action string
			power  api.VmPropertiesPowerState
		}{{"power_on", api.On}, {"restart_guest", api.On}, {"reset", api.On}, {"shutdown_guest", api.Off}} {
			r := run("lmartin", step.action, 202)
			h.runWorkflow(r.Id, nil)
			h.waitStatus(r.Id, "lmartin", api.RequestStatusSucceeded)
			if p := vm().PowerState; p != step.power {
				t.Fatalf("after %s the VM is %s, want %s", step.action, p, step.power)
			}
		}

		// Bad drafts.
		run("tnguyen", "fly", 422)
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", "tnguyen", api.Day2Draft{Action: "power_on", Inputs: map[string]any{}},
			422, nil, "Idempotency-Key", uuid.NewString())

		// A VM removed behind rvav's back: the action fails, the deployment stays usable.
		if err := h.vc.Destroy(ctx, vm().Moref); err != nil {
			t.Fatal(err)
		}
		lost := run("tnguyen", "power_on", 202)
		h.runWorkflow(lost.Id, nil)
		failed := h.waitStatus(lost.Id, "tnguyen", api.RequestStatusFailed)
		if failed.Error == nil || failed.Error.Message == "" {
			t.Fatalf("failed action = %+v", failed)
		}
		var d api.Deployment
		h.json("GET", "/api/v1/deployments/"+dep, "tnguyen", nil, 200, &d)
		if d.Status != api.DeploymentStatusReady {
			t.Fatalf("deployment after a failed action = %s", d.Status)
		}

		// The history: the creating request, then every action, newest first.
		var hist api.RequestPage
		h.json("GET", "/api/v1/deployments/"+dep+"/history", "sbrunner", nil, 200, &hist)
		if len(hist.Items) != 7 || hist.Items[0].Id != lost.Id || hist.Items[6].Id != built.Id || hist.Items[6].Kind != api.Provision {
			t.Fatalf("history = %d items", len(hist.Items))
		}
		var n int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action = 'action.run' and target_id = $1`, dep).Scan(&n); err != nil || n != 6 {
			t.Fatalf("audit has %d action runs (%v)", n, err)
		}
	}, model)
}
