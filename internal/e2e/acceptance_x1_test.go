package e2e

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
)

// x1VM is what the acceptance expects of one VM of the farm.
type x1VM struct {
	disks []string
	vcpu  int
	tags  []string
}

// TestX1Acceptance: X1's acceptance, on the Temporal dev server against
// vcsim. A web farm of two is updated to a new version with one more VM
// and a bigger disk; a disk is removed and a VM's tags edited by day-2
// actions; a VM is unregistered and onboarded again by a plan into the
// same deployment; a last update resizes the three and changes nothing
// else. After each step, vCenter, EfficientIP's addresses and DNS
// records, and the deployment's resources and inputs are as expected.
func TestX1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web, queue = "mkeller", "tnguyen", "prj_web", "rvav-x1-e2e"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		h.acts.Onboarder = h.svc
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		resync := func() {
			t.Helper()
			if err := sync.SyncOnce(ctx); err != nil {
				t.Fatal(err)
			}
		}

		var item api.AdminCatalogItem
		v1def := farmWith(20)
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-farm", Name: "Web farm",
			Summary: "Web servers.", Category: "compute", Type: api.VmBlueprint, Definition: &v1def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-farm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		v1 := item.DraftVersion.Id
		inputs := map[string]any{"app": "farm", "web_nodes": 2, "size": "S", "cluster": "GVA-DEV-CL01", "network": "dvpg-app-dev-vl310"}
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: v1, ProjectId: web,
			Environment: "dev", Inputs: inputs}, 202, &built, "Idempotency-Key", uuid.NewString())
		built = h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		settle(t, ctx, tc, built.Id)
		depID := (*built.DeploymentIds)[0]
		const web01, web02, web03 = "web-dev-farmweb01", "web-dev-farmweb02", "web-dev-farmweb03"

		var morefs map[string]string
		ips := map[string]string{}
		// check compares the deployment, vCenter and EfficientIP with what
		// the step leaves, and returns the deployment's VMs.
		check := func(step string, version string, want map[string]x1VM, wantInputs map[string]any) map[string]api.VmResource {
			t.Helper()
			var dep api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
			if dep.Status != api.DeploymentStatusReady || dep.Version.Id != version {
				t.Fatalf("%s: the deployment is %s, of version %s", step, dep.Status, dep.Version.Number)
			}
			for k, v := range wantInputs {
				if fmt.Sprint(dep.Inputs[k]) != fmt.Sprint(v) {
					t.Fatalf("%s: input %s = %v, want %v (inputs %v)", step, k, dep.Inputs[k], v, dep.Inputs)
				}
			}
			vms := map[string]api.VmResource{}
			var addrs, fqdns, records []string
			for _, r := range dep.Resources {
				switch v, _ := r.ValueByDiscriminator(); v := v.(type) {
				case api.VmResource:
					vms[v.Name] = v
				case api.IpAddressResource:
					addrs = append(addrs, v.Address)
				case api.DnsRecordResource:
					records = append(records, string(v.RecordType)+" "+v.Fqdn)
					if v.RecordType == "A" {
						fqdns = append(fqdns, v.Fqdn)
					}
				}
			}
			if got := slices.Sorted(maps.Keys(vms)); !slices.Equal(got, slices.Sorted(maps.Keys(want))) {
				t.Fatalf("%s: the deployment's VMs = %v", step, got)
			}
			// Each VM has its address and its A and PTR records, and no other.
			if len(addrs) != len(want) || len(records) != 2*len(want) || len(fqdns) != len(want) {
				t.Fatalf("%s: addresses %v, records %v", step, addrs, records)
			}
			for name, wv := range want {
				vm := vms[name]
				if morefs != nil && morefs[name] != "" && morefs[name] != vm.Vm.Moref {
					t.Fatalf("%s: %s is another VM: %s, was %s", step, name, vm.Vm.Moref, morefs[name])
				}
				if got := diskList(vm.Vm); !slices.Equal(got, wv.disks) {
					t.Fatalf("%s: %s's disks = %v, want %v", step, name, got, wv.disks)
				}
				inVC, err := h.vc.GetVM(ctx, vm.Vm.Moref)
				if err != nil {
					t.Fatalf("%s: %s in vCenter: %v", step, name, err)
				}
				var sizes []int
				for _, d := range inVC.Disks {
					sizes = append(sizes, d.CapacityGB)
				}
				var recorded []int
				for _, d := range vm.Vm.Disks {
					recorded = append(recorded, d.CapacityGb)
				}
				// Both sizes have 2 GB of memory a vCPU.
				if !slices.Equal(sizes, recorded) || inVC.CPUCount != wv.vcpu || vm.Vm.CpuCount != wv.vcpu || inVC.MemoryMB != wv.vcpu*2048 {
					t.Fatalf("%s: %s in vCenter: disks %v, %d vCPU, %d MB; rvav has %v, %d vCPU", step, name, sizes, inVC.CPUCount, inVC.MemoryMB, recorded,
						vm.Vm.CpuCount)
				}
				tags, err := h.vc.VMTags(ctx, vm.Vm.Moref)
				if err != nil || !slices.Equal(tags, wv.tags) {
					t.Fatalf("%s: %s's tags in vCenter = %v (%v), want %v", step, name, tags, err, wv.tags)
				}
				fqdn := name + ".corp.example"
				if !slices.Contains(fqdns, fqdn) {
					t.Fatalf("%s: no A record of %s in %v", step, fqdn, records)
				}
				if ip := ips[name]; ip != "" && !slices.Contains(addrs, ip) {
					t.Fatalf("%s: %s's address %s is not the deployment's: %v", step, name, ip, addrs)
				}
				var onNIC []string
				if p := vm.Vm.Nics[0].IpAddresses; p != nil {
					onNIC = *p
				}
				a, r, err := h.acts.IPAM.Find(ctx, onNIC, []string{fqdn})
				if err != nil || len(onNIC) != 1 || len(a) != 1 || len(r) != 2 {
					t.Fatalf("%s: %s in EfficientIP: %v %v %v", step, name, a, r, err)
				}
				ips[name] = onNIC[0]
			}
			return vms
		}
		publish := func(def api.CatalogVersionDefinition) string {
			var draft api.CatalogItemVersionDetail
			h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions", admin, api.CatalogItemVersionCreate{Definition: &def}, 201, &draft)
			h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+draft.Id+"/publish", admin, nil, 200, nil)
			return draft.Id
		}
		titles := func(pv api.UpdatePreview) []string {
			var out []string
			for _, ch := range pv.Changes {
				out = append(out, string(ch.Kind)+" "+ch.Title)
			}
			return out
		}
		update := func(version string, in map[string]any, want []string, history string) api.Request {
			t.Helper()
			var pv api.UpdatePreview
			h.json("POST", "/api/v1/deployments/"+depID+"/update/preview", owner, api.UpdateDraft{VersionId: version, Inputs: in}, 200, &pv)
			if !pv.Allowed || pv.Unchanged || len(pv.Refused) != 0 || !slices.Equal(titles(pv), want) {
				t.Fatalf("update preview: %v, refused %+v, errors %+v; want %v", titles(pv), pv.Refused, pv.Errors, want)
			}
			var req api.Request
			h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: version, Inputs: in}, 202, &req,
				"Idempotency-Key", uuid.NewString())
			req = h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
			settle(t, ctx, tc, req.Id)
			if history != "" {
				recordHistory(t, ctx, tc, req.Id, history)
			}
			return req
		}
		act := func(user, action string, resource string, in map[string]any, history string) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/deployments/"+depID+"/actions", user, api.Day2Draft{Action: action, ResourceId: &resource,
				Inputs: in}, 202, &req, "Idempotency-Key", uuid.NewString())
			req = h.waitStatus(req.Id, user, api.RequestStatusSucceeded)
			settle(t, ctx, tc, req.Id)
			if history != "" {
				recordHistory(t, ctx, tc, req.Id, history)
			}
			return req
		}

		vms := check("built", v1, map[string]x1VM{
			web01: {[]string{"50:0", "20:1"}, 2, nil},
			web02: {[]string{"50:0", "20:1"}, 2, nil},
		}, inputs)
		morefs = map[string]string{web01: vms[web01].Vm.Moref, web02: vms[web02].Vm.Moref}

		// 1. Version 2's extra disk is 40 GB, and the farm has a third VM.
		v2 := publish(farmWith(40))
		three := maps.Clone(inputs)
		three["web_nodes"] = 3
		update(v2, three, []string{
			"disk_extend Grow " + vms[web01].Vm.Disks[1].Label + " of " + web01 + " to 40 GB",
			"disk_extend Grow " + vms[web02].Vm.Disks[1].Label + " of " + web02 + " to 40 GB",
			"vm_add Add a VM to Web servers",
		}, "update")
		vms = check("updated", v2, map[string]x1VM{
			web01: {[]string{"50:0", "40:1"}, 2, nil},
			web02: {[]string{"50:0", "40:1"}, 2, nil},
			web03: {[]string{"50:0", "40:1"}, 2, nil},
		}, three)
		morefs[web03] = vms[web03].Vm.Moref

		// 2. The owner removes web01's 40 GB disk.
		r := act(owner, "disk_remove", vms[web01].Id, map[string]any{"disk": vms[web01].Vm.Disks[1].Label}, "day2-disk-remove")
		if steps := timelineOf(h, owner, r.Id); steps["vsphere.disk_remove"].Status != api.StepStatusDone {
			t.Fatalf("disk_remove steps = %v", keysOf(steps))
		}
		check("disk removed", v2, map[string]x1VM{
			web01: {[]string{"50:0"}, 2, nil},
			web02: {[]string{"50:0", "40:1"}, 2, nil},
			web03: {[]string{"50:0", "40:1"}, 2, nil},
		}, three)

		// 3. And tags web02 for daily backups, once the sync read its tags.
		resync()
		act(owner, "tags_edit", vms[web02].Id, map[string]any{"tags": []string{"backup/daily"}}, "")
		daily := []string{"backup/daily"}
		check("tagged", v2, map[string]x1VM{
			web01: {[]string{"50:0"}, 2, nil},
			web02: {[]string{"50:0", "40:1"}, 2, daily},
			web03: {[]string{"50:0", "40:1"}, 2, nil},
		}, three)

		// 4. An administrator unregisters web03: it stays in vCenter, its
		// address and records in EfficientIP, and the farm counts two.
		r = act(admin, "vm_unregister", vms[web03].Id, map[string]any{}, "day2-unregister")
		if s := timelineOf(h, admin, r.Id)["rvav.unregister"]; !strings.HasSuffix(deref(s.Detail), "; web_nodes is now 2") {
			t.Fatalf("the unregister step = %+v", s)
		}
		two := maps.Clone(three)
		two["web_nodes"] = 2
		check("unregistered", v2, map[string]x1VM{
			web01: {[]string{"50:0"}, 2, nil},
			web02: {[]string{"50:0", "40:1"}, 2, daily},
		}, two)
		if a, recs, err := h.acts.IPAM.Find(ctx, []string{ips[web03]}, []string{web03 + ".corp.example"}); err != nil || len(a) != 1 ||
			len(recs) != 2 || !vmExists(ctx, c, web03) {
			t.Fatalf("web03 after unregistering: %v %v %v", a, recs, err)
		}

		// 5. A plan onboards it again into the farm, as a web server.
		resync()
		var plan api.OnboardingPlan
		h.json("POST", "/api/v1/admin/onboarding/plans", admin, api.OnboardingPlanWrite{Name: "Farm web servers",
			Filter: api.OnboardingFilter{Q: present.Ptr("farmweb")},
			Target: api.OnboardingTarget{ProjectId: web, DeploymentId: &depID, Component: present.Ptr("web")}}, 201, &plan)
		if plan.Matching != 1 {
			t.Fatalf("the plan matches %d VMs", plan.Matching)
		}
		var run api.OnboardingRun
		h.json("POST", "/api/v1/admin/onboarding/plans/"+plan.Id+"/runs", admin, nil, 202, &run)
		for deadline := time.Now().Add(time.Minute); run.Status == api.OnboardingRunStatusRunning; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the run is still running: %+v", run)
			}
			h.json("GET", "/api/v1/admin/onboarding/runs/"+run.Id, admin, nil, 200, &run)
		}
		var outcomes []string
		h.json("GET", "/api/v1/admin/onboarding/runs/"+run.Id, admin, nil, 200, &run)
		if run.Vms == nil {
			t.Fatalf("the run has no VMs: %+v", run)
		}
		for _, v := range *run.Vms {
			outcomes = append(outcomes, v.Name+" "+string(v.Outcome)+" "+v.Code)
		}
		slices.Sort(outcomes)
		// It matches the farm's other VMs too, and skips them: the farm has them.
		if run.Status != api.OnboardingRunStatusSucceeded || !slices.Equal(outcomes, []string{web01 + " skipped managed",
			web02 + " skipped managed", web03 + " adopted "}) {
			t.Fatalf("the run = %s, %v", run.Status, outcomes)
		}
		recordWorkflow(t, ctx, tc, provision.OnboardingWorkflowID(run.Id), "onboarding-run")
		// Onboarded, its disks aren't recorded as the blueprint's.
		vms = check("onboarded again", v2, map[string]x1VM{
			web01: {[]string{"50:0"}, 2, nil},
			web02: {[]string{"50:0", "40:1"}, 2, daily},
			web03: {[]string{"50:-", "40:-"}, 2, nil},
		}, three)
		if deref(vms[web03].Vm.Component) != "web" {
			t.Fatalf("web03 is of component %q", deref(vms[web03].Vm.Component))
		}

		// 6. The last update resizes the three VMs, and nothing else: web01
		// doesn't get its disk back, web03 keeps its own, and no VM is
		// added, removed or built again.
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
		bigger := maps.Clone(dep.Inputs)
		bigger["size"] = "M"
		update(v2, bigger, []string{
			"resize Set 4 vCPU and 8 GB on " + web01,
			"resize Set 4 vCPU and 8 GB on " + web02,
			"resize Set 4 vCPU and 8 GB on " + web03,
		}, "")
		want := maps.Clone(three)
		want["size"] = "M"
		check("resized", v2, map[string]x1VM{
			web01: {[]string{"50:0"}, 4, nil},
			web02: {[]string{"50:0", "40:1"}, 4, daily},
			web03: {[]string{"50:-", "40:-"}, 4, nil},
		}, want)
	})
}
