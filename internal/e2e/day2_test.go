package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
)

// vmDeployment is a dev VM built through the catalog, to run actions on.
type vmDeployment struct {
	h        *harness
	model    *simulator.Model
	id       string
	built    api.Request
	resource string
	viewer   string // who reads the deployment in vm()
}

func buildVM(h *harness, model *simulator.Model) *vmDeployment {
	h.t.Helper()
	const web = "prj_web"
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
	d := &vmDeployment{h: h, model: model, id: (*built.DeploymentIds)[0], built: built, viewer: "tnguyen"}
	d.resource = *d.actions("tnguyen")["power_off"].ResourceId
	return d
}

func (d *vmDeployment) actions(user string) map[string]api.Day2Action {
	var out struct{ Items []api.Day2Action }
	d.h.json("GET", "/api/v1/deployments/"+d.id+"/actions", user, nil, 200, &out)
	m := map[string]api.Day2Action{}
	for _, a := range out.Items {
		m[a.Key] = a
	}
	return m
}

func (d *vmDeployment) vm() api.VmProperties {
	d.h.t.Helper()
	var dep struct {
		Resources []struct {
			Type string
			VM   api.VmProperties `json:"vm"`
		}
	}
	d.h.json("GET", "/api/v1/deployments/"+d.id, d.viewer, nil, 200, &dep)
	for _, r := range dep.Resources {
		if r.Type == "vsphere.vm" {
			return r.VM
		}
	}
	d.h.t.Fatal("no VM resource")
	return api.VmProperties{}
}

// run submits an action on the VM and expects the status want.
func (d *vmDeployment) run(user, action string, inputs map[string]any, want int) api.Request {
	d.h.t.Helper()
	if inputs == nil {
		inputs = map[string]any{}
	}
	draft := api.Day2Draft{Action: action, ResourceId: &d.resource, Inputs: inputs}
	var r api.Request
	var out any
	if want == 202 {
		out = &r
	}
	d.h.json("POST", "/api/v1/deployments/"+d.id+"/actions", user, draft, want, out, "Idempotency-Key", uuid.NewString())
	return r
}

// succeed runs an action through its workflow and expects it to succeed.
func (d *vmDeployment) succeed(user, action string, inputs map[string]any) api.Request {
	d.h.t.Helper()
	r := d.run(user, action, inputs, 202)
	d.h.runWorkflow(r.Id, nil)
	return d.h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
}

// toolsRunning makes vcsim report VMware Tools running in the guest; rvav
// sees it when an action next refreshes the VM.
func (d *vmDeployment) toolsRunning() {
	sim := d.model.Map().Get(types.ManagedObjectReference{Type: "VirtualMachine", Value: d.vm().Moref}).(*simulator.VirtualMachine)
	sim.Guest.ToolsRunningStatus = string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)
}

// TestDay2Actions runs power actions on a VM built by a dev request: each is
// a request with its own timeline, the VM shows the result, and actions
// that don't fit the VM's state or the caller's role are refused.
func TestDay2Actions(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		if _, err := h.store.Pool.Exec(ctx, `insert into project_members (project_id, user_id, role) values ('prj_web', 'usr_sbrunner', 'viewer')`); err != nil {
			t.Fatal(err)
		}
		d := buildVM(h, model)
		dep, built := d.id, d.built
		actions, vm := d.actions, d.vm
		run := func(user, action string, want int) api.Request { return d.run(user, action, nil, want) }

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
		d.toolsRunning()
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
		// One action at a time on a VM; actions on the whole deployment wait too.
		run("lmartin", "reset", 409)
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", "lmartin", api.Day2Draft{Action: "change_owner",
			Inputs: map[string]any{"owner": "usr_lmartin"}}, 409, nil, "Idempotency-Key", uuid.NewString())
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
		run("tnguyen", "resize", 422)

		// A VM removed behind rvav's back: the action fails, the deployment stays usable.
		if err := h.vc.Destroy(ctx, vm().Moref); err != nil {
			t.Fatal(err)
		}
		lost := run("tnguyen", "power_on", 202)
		h.runWorkflow(lost.Id, nil)
		failed := h.waitStatus(lost.Id, "tnguyen", api.RequestStatusFailed)
		if failed.Error == nil || failed.Error.Message != "The VM is no longer in vCenter." {
			t.Fatalf("failed action = %+v", failed)
		}
		// Retrying applies today's day-2 policies.
		if _, err := h.store.Pool.Exec(ctx, `insert into day2_policies (id, name, reason, priority, actions, effect)
			values ('d2p_no_power', 'No power changes', 'Power changes are frozen.', 1, '{power_*}', 'deny')`); err != nil {
			t.Fatal(err)
		}
		h.json("POST", "/api/v1/requests/"+lost.Id+"/retry", "tnguyen", nil, 403, nil)
		if _, err := h.store.Pool.Exec(ctx, `delete from day2_policies where id = 'd2p_no_power'`); err != nil {
			t.Fatal(err)
		}
		h.json("POST", "/api/v1/requests/"+lost.Id+"/retry", "sbrunner", nil, 403, nil)
		h.json("POST", "/api/v1/requests/"+lost.Id+"/retry", "tnguyen", nil, 202, nil)
		h.runWorkflow(lost.Id, nil)
		h.waitStatus(lost.Id, "tnguyen", api.RequestStatusFailed)
		var after api.Deployment
		h.json("GET", "/api/v1/deployments/"+dep, "tnguyen", nil, 200, &after)
		if after.Status != api.DeploymentStatusReady {
			t.Fatalf("deployment after a failed action = %s", after.Status)
		}

		// The history: the creating request, then every action, newest first.
		var hist api.RequestPage
		h.json("GET", "/api/v1/deployments/"+dep+"/history", "sbrunner", nil, 200, &hist)
		if len(hist.Items) != 7 || hist.Items[6].Id != built.Id || hist.Items[6].Kind != api.Provision {
			t.Fatalf("history = %d items", len(hist.Items))
		}
		var n int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action = 'action.run' and target_id = $1`, dep).Scan(&n); err != nil || n != 6 {
			t.Fatalf("audit has %d action runs (%v)", n, err)
		}
	}, model)
}

// TestDay2Configuration resizes a VM (hot add aware), adds and extends
// disks, and adds, moves and removes network adapters.
func TestDay2Configuration(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		d := buildVM(h, model)
		d.toolsRunning()
		start := d.vm()
		if start.PowerState != api.On || start.CpuCount != 2 || len(start.Disks) != 1 || len(start.Nics) != 1 {
			t.Fatalf("built VM = %+v", start)
		}

		// The resize form starts from the current size; without hot add, a
		// running VM must be restarted for it.
		a := d.actions("tnguyen")["resize"]
		if !a.Enabled || a.Form == nil || a.Form.InitialValues["cpu_count"] != float64(2) || a.Form.UiSchema.Fields["restart"].Label == "" {
			t.Fatalf("resize = %+v", a)
		}
		d.run("tnguyen", "resize", map[string]any{"cpu_count": 2, "memory_gb": start.MemoryMb / 1024}, 422)
		d.run("tnguyen", "resize", map[string]any{"cpu_count": 4, "memory_gb": 16, "restart": false}, 422)
		d.run("tnguyen", "resize", map[string]any{"cpu_count": 128, "memory_gb": 16, "restart": true}, 422)
		r := d.run("tnguyen", "resize", map[string]any{"cpu_count": 4, "memory_gb": 16, "restart": true}, 202)
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+r.Id+"/steps", "tnguyen", nil, 200, &steps)
		if len(steps.Items) != 6 || steps.Items[3].Title != "Set 4 vCPU and 16 GB" {
			t.Fatalf("resize with restart steps = %+v", steps.Items)
		}
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, "tnguyen", api.RequestStatusSucceeded)
		if vm := d.vm(); vm.CpuCount != 4 || vm.MemoryMb != 16384 || vm.PowerState != api.On {
			t.Fatalf("after resize: %d vCPU, %d MB, %s", vm.CpuCount, vm.MemoryMb, vm.PowerState)
		}

		// With hot add on, it resizes in place.
		sim := d.model.Map().Get(types.ManagedObjectReference{Type: "VirtualMachine", Value: start.Moref}).(*simulator.VirtualMachine)
		on := true
		sim.Config.CpuHotAddEnabled, sim.Config.MemoryHotAddEnabled = &on, &on
		d.succeed("tnguyen", "power_off", nil)
		d.succeed("tnguyen", "power_on", nil)
		r = d.succeed("tnguyen", "resize", map[string]any{"cpu_count": 6, "memory_gb": 24, "restart": false})
		if r.Progress.Total != 4 || d.vm().CpuCount != 6 {
			t.Fatalf("hot resize: %+v, VM %d vCPU", r.Progress, d.vm().CpuCount)
		}

		// Disks.
		d.succeed("tnguyen", "disk_add", map[string]any{"size_gb": 100})
		vm := d.vm()
		if len(vm.Disks) != 2 || vm.Disks[1].CapacityGb != 100 {
			t.Fatalf("disks after add = %+v", vm.Disks)
		}
		first := vm.Disks[0]
		d.run("tnguyen", "disk_extend", map[string]any{"disk": first.Label, "size_gb": first.CapacityGb}, 422)
		d.run("tnguyen", "disk_extend", map[string]any{"disk": "Hard disk 9", "size_gb": 500}, 422)
		d.succeed("tnguyen", "disk_extend", map[string]any{"disk": first.Label, "size_gb": first.CapacityGb + 20})
		if got := d.vm().Disks[0].CapacityGb; got != first.CapacityGb+20 {
			t.Fatalf("extended disk is %d GB; before %+v, after %+v", got, vm.Disks, d.vm().Disks)
		}

		// Network adapters: the placement rules decide the networks.
		a = d.actions("tnguyen")["nic_add"]
		if a.Form == nil || len(*a.Form.UiSchema.Fields["network"].Options) != 2 {
			t.Fatalf("nic_add = %+v", a)
		}
		if m := d.actions("tnguyen")["nic_remove"]; m.Enabled {
			t.Fatalf("nic_remove with one adapter = %+v", m)
		}
		d.run("tnguyen", "nic_add", map[string]any{"network": "dvpg-web-prd-vl210"}, 422)
		d.succeed("tnguyen", "nic_add", map[string]any{"network": "dvpg-app-dev-vl310"})
		nics := d.vm().Nics
		if len(nics) != 2 || nics[1].Network != "dvpg-app-dev-vl310" {
			t.Fatalf("nics after add = %+v", nics)
		}
		second := deref(nics[1].Label)
		d.run("tnguyen", "nic_move", map[string]any{"nic": second, "network": "dvpg-app-dev-vl310"}, 422)
		d.run("tnguyen", "nic_move", map[string]any{"nic": deref(nics[0].Label), "network": "dvpg-db-dev-vl320"}, 422)
		d.succeed("tnguyen", "nic_move", map[string]any{"nic": second, "network": "dvpg-db-dev-vl320"})
		if n := d.vm().Nics[1].Network; n != "dvpg-db-dev-vl320" {
			t.Fatalf("moved nic is on %s", n)
		}
		d.succeed("tnguyen", "nic_remove", map[string]any{"nic": second})
		if n := len(d.vm().Nics); n != 1 {
			t.Fatalf("%d nics after remove", n)
		}
	}, model)
}

// TestDay2Snapshots takes, reverts and deletes snapshots within the
// environment's retention, and lets rvav delete the expired ones.
func TestDay2Snapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		d := buildVM(h, model)
		snapshots := func() []string {
			var names []string
			if s := d.vm().Snapshots; s != nil {
				for _, x := range *s {
					names = append(names, x.Name)
				}
			}
			return names
		}

		a := d.actions("tnguyen")
		if a["snapshot_revert"].Enabled || a["snapshot_delete"].Enabled {
			t.Fatalf("revert and delete without snapshots = %+v", a)
		}
		create := a["snapshot_create"]
		if !create.Enabled || create.Form == nil || create.Form.InitialValues["name"] == nil ||
			deref(create.Form.UiSchema.Fields["name"].Help) != "rvav deletes snapshots after 7 days in Development." {
			t.Fatalf("snapshot_create = %+v", create)
		}
		d.succeed("tnguyen", "snapshot_create", map[string]any{"name": "before-patch", "description": "Before the July patches"})
		d.run("tnguyen", "snapshot_create", map[string]any{"name": "before-patch"}, 422)
		if got := snapshots(); len(got) != 1 || got[0] != "before-patch" {
			t.Fatalf("snapshots = %v", got)
		}
		if e := d.actions("tnguyen")["disk_extend"]; e.Enabled {
			t.Fatalf("disk_extend with a snapshot = %+v", e)
		}
		d.succeed("tnguyen", "snapshot_create", map[string]any{"name": "before-upgrade", "memory": true})
		d.succeed("tnguyen", "snapshot_create", map[string]any{"name": "third"})
		// Development keeps at most three.
		if c := d.actions("tnguyen")["snapshot_create"]; c.Enabled || deref(c.DisabledReason) != "The VM has 3 snapshots, the most Development allows. Delete one first." {
			t.Fatalf("a fourth snapshot = %+v", c)
		}
		d.run("tnguyen", "snapshot_create", map[string]any{"name": "fourth"}, 409)

		r := d.succeed("tnguyen", "snapshot_revert", map[string]any{"snapshot": "before-upgrade"})
		if r.Title != "Revert to a snapshot · "+r.Resource.Name {
			t.Fatalf("revert request = %+v", r)
		}
		d.run("tnguyen", "snapshot_delete", map[string]any{"snapshot": "nope"}, 422)
		d.succeed("tnguyen", "snapshot_delete", map[string]any{"snapshot": "third"})
		if got := snapshots(); len(got) != 2 {
			t.Fatalf("snapshots after delete = %v", got)
		}

		// A week later, rvav deletes them, oldest first, one per round. (vcsim
		// can't remove the current snapshot at the root of the tree, so a
		// fresh one comes first.)
		later := time.Now().Add(8 * 24 * time.Hour)
		for i, left := range []string{"before-upgrade", "fresh"} {
			if i == 1 {
				d.succeed("tnguyen", "snapshot_create", map[string]any{"name": "fresh"})
			}
			started, err := h.svc.ExpireSnapshots(ctx, later)
			if err != nil || len(started) != 1 {
				t.Fatalf("expire: %v, %v", started, err)
			}
			var req api.Request
			h.json("GET", "/api/v1/requests/"+started[0], "tnguyen", nil, 200, &req)
			if req.Requester.Name != "rvav" || req.Action.Key != "snapshot_delete" || deref(req.Justification) != "Snapshots are kept 7 days in Development." {
				t.Fatalf("expiry request = %+v", req)
			}
			h.runWorkflow(req.Id, nil)
			h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
			if got := snapshots(); len(got) != 1 || got[0] != left {
				t.Fatalf("snapshots after expiry = %v, want %s", got, left)
			}
		}
		if started, err := h.svc.ExpireSnapshots(ctx, time.Now().Add(6*24*time.Hour)); err != nil || len(started) != 0 {
			t.Fatalf("nothing left to expire: %v, %v", started, err)
		}
	}, model)
}

// TestDay2Lifecycle renames a VM (vCenter and DNS), mounts an ISO image,
// hands the deployment to another owner and project, then deletes it.
func TestDay2Lifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		d := buildVM(h, model)
		type resource struct {
			Type string
			Name string
			VM   api.VmProperties `json:"vm"`
		}
		deployment := func(user string) (api.Deployment, []resource) {
			var dep struct {
				api.Deployment
				Resources []resource
			}
			h.json("GET", "/api/v1/deployments/"+d.id, user, nil, 200, &dep)
			return dep.Deployment, dep.Resources
		}
		deploymentAction := func(user, action string, inputs map[string]any, want int) api.Request {
			var r api.Request
			var out any
			if want == 202 {
				out = &r
			}
			h.json("POST", "/api/v1/deployments/"+d.id+"/actions", user, api.Day2Draft{Action: action, Inputs: inputs},
				want, out, "Idempotency-Key", uuid.NewString())
			if want == 202 {
				h.runWorkflow(r.Id, nil)
				return h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
			}
			return r
		}
		start := d.vm()
		oldName := (*start.Fqdn)[:len("web-dev-app001")]

		// Rename: vCenter, DNS and rvav's records.
		if a := d.actions("tnguyen")["rename"]; a.Form == nil || a.Form.InitialValues["name"] != oldName {
			t.Fatalf("rename = %+v", a)
		}
		d.run("tnguyen", "rename", map[string]any{"name": oldName}, 422)
		d.run("tnguyen", "rename", map[string]any{"name": "Web_Dev"}, 422)
		domain := (*start.Fqdn)[len(oldName):]
		if _, err := h.acts.IPAM.CreateRecord(ctx, efficientip.Record{FQDN: "web-dev-taken" + domain, Type: "A", Value: "10.31.10.99"}); err != nil {
			t.Fatal(err)
		}
		d.run("tnguyen", "rename", map[string]any{"name": "web-dev-taken"}, 422)
		d.succeed("tnguyen", "rename", map[string]any{"name": "web-dev-api001"})
		dep, resources := deployment("tnguyen")
		if dep.Name != "web-dev-api001" {
			t.Fatalf("deployment name = %s", dep.Name)
		}
		var names []string
		for _, r := range resources {
			names = append(names, r.Name)
			if r.Type == "vsphere.vm" && (r.Name != "web-dev-api001" || deref(r.VM.Fqdn) != "web-dev-api001"+domain) {
				t.Fatalf("renamed VM = %+v", r)
			}
		}
		if !contains(names, "A web-dev-api001"+domain) {
			t.Fatalf("resources after rename = %v", names)
		}
		if got, _ := h.acts.IPAM.Reserve(ctx, efficientip.Subnet{CIDR: "10.31.10.0/24"}, "web-dev-api001"+domain, dep.Id); got.IP != dep.PrimaryAddress.Ip {
			t.Fatalf("the address is not named web-dev-api001%s in EfficientIP: %+v", domain, got)
		}
		if ok, _ := h.acts.IPAM.NameExists(ctx, "web-dev-api001"+domain); !ok {
			t.Fatal("the new name is not in DNS")
		}
		if ok, _ := h.acts.IPAM.NameExists(ctx, oldName+domain); ok {
			t.Fatal("the old name is still in DNS")
		}

		// ISO images.
		if a := d.actions("tnguyen")["iso_unmount"]; a.Enabled {
			t.Fatalf("iso_unmount with nothing mounted = %+v", a)
		}
		d.run("tnguyen", "iso_mount", map[string]any{"iso": "rhel.iso"}, 422)
		d.succeed("tnguyen", "iso_mount", map[string]any{"iso": "[nfs-gva-bronze-01] iso/rhel-9.4-x86_64-dvd.iso"})
		if iso := deref(d.vm().Iso); iso != "[nfs-gva-bronze-01] iso/rhel-9.4-x86_64-dvd.iso" {
			t.Fatalf("mounted iso = %q", iso)
		}
		d.succeed("tnguyen", "iso_unmount", nil)
		if d.vm().Iso != nil {
			t.Fatal("the ISO is still mounted")
		}

		// Deployment actions have no resource.
		a := d.actions("tnguyen")
		if del := a["delete"]; del.ResourceId != nil || del.Confirm == nil || deref(del.Confirm.TypeToConfirm) != "web-dev-api001" {
			t.Fatalf("delete = %+v", del)
		}
		d.run("tnguyen", "delete", nil, 422) // with a resource ID

		// Owner: one of the project's members.
		owner := a["change_owner"]
		if owner.Form == nil || len(*owner.Form.UiSchema.Fields["owner"].Options) == 0 {
			t.Fatalf("change_owner = %+v", owner)
		}
		deploymentAction("tnguyen", "change_owner", map[string]any{"owner": "usr_sbrunner"}, 422)
		deploymentAction("tnguyen", "change_owner", map[string]any{"owner": "usr_lmartin"}, 202)
		if dep, _ := deployment("lmartin"); dep.Owner.Id != "usr_lmartin" {
			t.Fatalf("owner = %+v", dep.Owner)
		}

		// Project: Léa can request only in Web Platform; Marc anywhere.
		if m := d.actions("lmartin")["change_project"]; m.Enabled {
			t.Fatalf("change_project for lmartin = %+v", m)
		}
		move := d.actions("mkeller")["change_project"]
		if !move.Enabled || move.Form == nil {
			t.Fatalf("change_project for mkeller = %+v", move)
		}
		deploymentAction("mkeller", "change_project", map[string]any{"project": "prj_data"}, 202)
		d.viewer = "lmartin"
		dep, _ = deployment("lmartin")
		if dep.Project.Id != "prj_data" || deref(d.vm().Folder) != "/GVA/vm/rvav/data/dev" {
			t.Fatalf("after the move: project %s, folder %s", dep.Project.Id, deref(d.vm().Folder))
		}
		// Thomas is no longer in the deployment's project, nor its owner.
		h.json("GET", "/api/v1/deployments/"+d.id, "tnguyen", nil, 404, nil)

		// Delete: the VM, its DNS records and its address.
		r := deploymentAction("lmartin", "delete", map[string]any{}, 202)
		if r.Resource != nil || r.Title != "Delete · web-dev-api001" {
			t.Fatalf("delete request = %+v", r)
		}
		dep, resources = deployment("lmartin")
		if dep.Status != api.DeploymentStatusDeleted || len(resources) != 0 {
			t.Fatalf("after delete: %s, %d resources", dep.Status, len(resources))
		}
		if ok, _ := h.acts.IPAM.NameExists(ctx, "web-dev-api001"+domain); ok {
			t.Fatal("the DNS record is still there")
		}
		if _, err := h.vc.GetVM(ctx, start.Moref); err == nil {
			t.Fatal("the VM is still in vCenter")
		}
		var after struct{ Items []api.Day2Action }
		h.json("GET", "/api/v1/deployments/"+d.id+"/actions", "lmartin", nil, 200, &after)
		for _, x := range after.Items {
			if x.Enabled {
				t.Fatalf("%s is enabled on a deleted deployment", x.Key)
			}
		}
	}, model)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestDay2Policies checks who may run what: viewers nothing, members their
// VMs' operations, owners and project admins deletion and hand-over, and a
// policy that asks for approval.
func TestDay2Policies(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		if _, err := h.store.Pool.Exec(ctx, `insert into project_members (project_id, user_id, role)
			values ('prj_web', 'usr_sbrunner', 'viewer'), ('prj_web', 'usr_afavre', 'member')`); err != nil {
			t.Fatal(err)
		}
		d := buildVM(h, model)

		// Viewers: nothing.
		if a := d.actions("sbrunner")["power_off"]; a.Enabled || deref(a.DisabledReason) != "Your role in Web Platform does not allow Power off." {
			t.Fatalf("viewer's power_off = %+v", a)
		}
		// A member who is not the owner operates the VM but can't delete it.
		a := d.actions("afavre")
		if !a["power_off"].Enabled {
			t.Fatalf("member's power_off = %+v", a["power_off"])
		}
		if del := a["delete"]; del.Enabled || deref(del.DisabledReason) != "Only the owner and the project admins can delete this deployment or hand it over." {
			t.Fatalf("member's delete = %+v", del)
		}
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "afavre", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			403, nil, "Idempotency-Key", uuid.NewString())
		// The owner and the project admin may.
		if !d.actions("tnguyen")["delete"].Enabled || !d.actions("lmartin")["change_owner"].Enabled {
			t.Fatal("owner or project admin may not delete or hand over")
		}

		// A policy that asks the leads before a running VM is reverted.
		if _, err := h.store.Pool.Exec(ctx, `insert into day2_policies (id, name, reason, priority, actions, roles, condition, effect, levels)
			values ('d2p_revert', 'Reverting running VMs', 'Reverting a running VM loses its current state', 15,
			'{snapshot_revert}', '{owner,member}', 'resource.power_state == "on" && environment.key == "dev"', 'approval',
			'[{"name": "Infrastructure leads", "approvers": ["usr_mkeller"]}]')`); err != nil {
			t.Fatal(err)
		}
		d.succeed("tnguyen", "snapshot_create", map[string]any{"name": "base"})
		rv := d.actions("tnguyen")["snapshot_revert"]
		if !rv.Enabled || !rv.Approval.Required || len(rv.Approval.Reasons) != 1 || rv.Approval.Reasons[0] != "Reverting a running VM loses its current state" {
			t.Fatalf("snapshot_revert = %+v", rv)
		}
		r := d.run("tnguyen", "snapshot_revert", map[string]any{"snapshot": "base"}, 202)
		if r.Status != api.RequestStatusPendingApproval {
			t.Fatalf("revert status = %s", r.Status)
		}
		h.runWorkflow(r.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				var inbox api.ApprovalPage
				h.json("GET", "/api/v1/approvals", "mkeller", nil, 200, &inbox)
				if inbox.Total != 1 || inbox.Items[0].Request.Id != r.Id {
					t.Errorf("mkeller's inbox = %+v", inbox)
					return
				}
				var detail api.Approval
				h.json("GET", "/api/v1/approvals/"+inbox.Items[0].Id, "mkeller", nil, 200, &detail)
				if len(detail.EditableFields) != 0 || len(detail.Facts) < 2 {
					t.Errorf("approval detail = %+v", detail)
				}
				h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", "mkeller",
					api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove, Inputs: &map[string]any{"snapshot": "x"}}, 422, nil)
				h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", "mkeller",
					api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove}, 200, nil)
				h.wf.deliver(env)
			}, time.Hour)
		})
		h.waitStatus(r.Id, "tnguyen", api.RequestStatusSucceeded)
		// Powered off, no approval.
		d.succeed("tnguyen", "power_off", nil)
		if rv := d.actions("tnguyen")["snapshot_revert"]; rv.Approval.Required {
			t.Fatalf("revert of a powered-off VM = %+v", rv)
		}
	}, model)
}
