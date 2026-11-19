package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/vmprops"
)

func updateVM(name, ip string, cpu, memGB int, disks ...int) existingVM {
	vm := api.VmProperties{Moref: "vm-" + name, CpuCount: cpu, MemoryMb: memGB * 1024, PowerState: api.On,
		GuestState: api.VmPropertiesGuestStateRunning, Fqdn: new(name + ".corp.example"), Cluster: new("GVA-DEV-CL01"),
		Template: new("tpl"), Nics: []api.VmNic{{Network: "dvpg-app", IpAddresses: &[]string{ip}}}}
	for i, gb := range disks {
		file := fmt.Sprintf("[ds] %s/%s_%d.vmdk", name, name, i)
		vm.Disks = append(vm.Disks, api.VmDisk{Label: day2.DiskLabel(i + 1), CapacityGb: gb, Key: new(2000 + i), File: &file, BlueprintDisk: new(i)})
	}
	return existingVM{t: day2.Target{ResourceID: "res-" + name, ResourceName: name, Type: "vsphere.vm", VM: vm}}
}

// recordedAs sets which blueprint disk each of a VM's disks is: -1 for a
// disk that isn't one.
func recordedAs(vm existingVM, which ...int) existingVM {
	vm.t.VM.Disks = slices.Clone(vm.t.VM.Disks)
	for i, j := range which {
		vm.t.VM.Disks[i].BlueprintDisk = nil
		if j >= 0 {
			vm.t.VM.Disks[i].BlueprintDisk = new(j)
		}
	}
	return vm
}

func refusals(u *componentPlan) []string {
	var out []string
	for _, r := range u.refused {
		out = append(out, r.Reason)
	}
	return out
}

func keysOf(ops []day2.Op) []string {
	var out []string
	for _, op := range ops {
		out = append(out, op.Key)
	}
	return out
}

func kindsOf(changes []api.UpdateChange) []string {
	var out []string
	for _, c := range changes {
		out = append(out, string(c.Kind))
	}
	return out
}

func newPlan() *componentPlan {
	return &componentPlan{delta: map[string]int{"vcpu": 0, "memory_gb": 0, "storage_gb": 0, "vm_count": 0}}
}

// An input that resizes the VMs resizes each one not at the new size yet,
// powered off first when it can't take the size while it runs; a disk the
// new version adds is added to each, before.
func TestUpdateInPlace(t *testing.T) {
	plan := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", Cluster: "GVA-DEV-CL01", PostConfig: []string{"baseline"}}
	bigger := plan
	bigger.VCPU, bigger.MemoryGB = 4, 8
	bigger.Disks = []blueprint.DiskPlan{{SizeGB: 20}}
	web01, web02 := updateVM("web01", "10.0.0.11", 2, 4, 50), updateVM("web02", "10.0.0.12", 4, 8, 50)
	u := newPlan()
	c := componentChange{key: "web", title: "Web servers", had: true, old: plan, new: bigger, count: 2,
		vms: []existingVM{web01, web02}, jobs: []provision.Job{{Key: "baseline", Template: "linux-baseline"}}}
	if n := c.plan(u); n != 0 || len(u.refused) > 0 {
		t.Fatalf("adds %d, refused %+v", n, u.refused)
	}
	want := []string{"vsphere.disk_add@web01", "vsphere.shutdown_guest@web01", "vsphere.reconfigure@web01", "vsphere.power_on@web01",
		"vsphere.disk_add@web02"}
	if got := keysOf(u.ops); !slices.Equal(got, want) {
		t.Fatalf("ops = %v, want %v", got, want)
	}
	for _, op := range u.ops {
		if op.Args["moref"] == "" || op.Args["resource"] == "" || !strings.Contains(op.Title, "web0") {
			t.Fatalf("op %+v isn't on its VM", op)
		}
	}
	if got := kindsOf(u.changes); !slices.Equal(got, []string{"disk_add", "resize", "disk_add"}) {
		t.Fatalf("changes = %v", got)
	}
	if d := *u.changes[1].Detail; !strings.Contains(d, "Was 2 vCPU and 4 GB") || !strings.Contains(d, "Restarts the VM") {
		t.Fatalf("resize detail = %q", d)
	}
	// Web02 was resized already, by a day-2 action: only its disk is added.
	if u.delta["vcpu"] != 2 || u.delta["memory_gb"] != 4 || u.delta["storage_gb"] != 40 || u.delta["vm_count"] != 0 {
		t.Fatalf("delta = %v", u.delta)
	}
	// The job ran when they were built: not again.
	if len(u.jobs) != 0 || len(u.kept) != 2 || u.kept[1].Index != 1 || u.kept[0].IP != "10.0.0.11" {
		t.Fatalf("jobs %+v, kept %+v", u.jobs, u.kept)
	}
}

// Changes rvav can't make in place are refused, each with its reason.
func TestUpdateRefused(t *testing.T) {
	plan := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl-a", Cluster: "CL01", Network: "net-a",
		Disks: []blueprint.DiskPlan{{SizeGB: 20}}}
	other := plan
	other.Template, other.Cluster, other.DiskGB = "tpl-b", "CL02", 40
	other.Disks = []blueprint.DiskPlan{{SizeGB: 10}}
	other.NICs = []blueprint.NICPlan{{Key: "backup", Network: "net-b", IPAM: blueprint.IPAMEfficientIP}}
	u := newPlan()
	c := componentChange{key: "db", title: "Database", had: true, old: plan, new: other, count: 1,
		vms: []existingVM{updateVM("db01", "10.0.0.21", 2, 4, 50, 20)}}
	c.plan(u)
	var settings []string
	for _, r := range u.refused {
		settings = append(settings, *r.Setting)
		if !strings.HasPrefix(r.Reason, "Database: ") {
			t.Fatalf("reason %q doesn't name its component", r.Reason)
		}
	}
	for _, s := range []string{"template", "cluster", "disk_gb", "disks", "nics"} {
		if !slices.Contains(settings, s) {
			t.Fatalf("refused %v, want %s", settings, s)
		}
	}
}

// Scaling out adds VMs after those kept; scaling in removes the last ones
// with their own records, the DRS rule first, and frees what they use.
func TestUpdateScale(t *testing.T) {
	plan := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", Cluster: "GVA-DEV-CL01"}
	vms := []existingVM{updateVM("web01", "10.0.0.11", 2, 4, 50), updateVM("web02", "10.0.0.12", 2, 4, 50)}

	u := newPlan()
	c := componentChange{key: "web", title: "Web servers", had: true, old: plan, new: plan, count: 3, vms: vms,
		antiAffinity: true, cluster: "GVA-DEV-CL01"}
	if n := c.plan(u); n != 1 || len(u.refused) > 0 || len(u.added) != 1 || u.delta["vm_count"] != 1 || u.delta["vcpu"] != 2 {
		t.Fatalf("scale out: adds %d, %+v", n, u)
	}
	c.cluster = "GVA-DEV-CL02"
	u = newPlan()
	if c.plan(u); len(u.refused) != 1 || *u.refused[0].Setting != "cluster" {
		t.Fatalf("anti-affinity onto another cluster: %+v", u.refused)
	}

	three := append(slices.Clone(vms), updateVM("web03", "10.0.0.13", 2, 4, 50))
	addr := func(id, ip, host string) day2.Resource {
		props, _ := json.Marshal(map[string]any{"address": ip, "hostname": host})
		return day2.Resource{ID: id, Type: "ipam.address", Name: ip, Properties: props}
	}
	dns := func(id, fqdn, ip string) day2.Resource {
		props, _ := json.Marshal(map[string]any{"fqdn": fqdn, "value": ip})
		return day2.Resource{ID: id, Type: "dns.record", Name: fqdn, Properties: props}
	}
	rule, _ := json.Marshal(map[string]any{"rule": map[string]any{"cluster": "GVA-DEV-CL01", "vcenter": "vc", "component": "web"}})
	resources := []day2.Resource{{ID: "rule", Type: "vsphere.drs_rule", Name: "rvav-dep-web", Properties: rule},
		addr("ip2", "10.0.0.12", "web02"), addr("ip3", "10.0.0.13", "web03"), dns("dns3", "web03.corp.example", "10.0.0.13")}
	for _, vm := range three {
		resources = append(resources, day2.Resource{ID: vm.t.ResourceID, Type: "vsphere.vm", Name: vm.name(), VM: vm.t.VM})
	}
	u = newPlan()
	c = componentChange{key: "web", title: "Web servers", had: true, old: plan, new: plan, count: 2, vms: three, resources: resources}
	c.plan(u)
	want := []string{"vsphere.drs_rule@web", "vsphere.destroy@web03", "dns.delete@web03", "ipam.release@web03"}
	if got := keysOf(u.remove); !slices.Equal(got, want) || len(u.refused) > 0 {
		t.Fatalf("scale in: %v, want %v (refused %+v)", got, want, u.refused)
	}
	if got := u.remove[0].Args["vms"]; !slices.Equal(got.([]string), []string{"web01", "web02"}) {
		t.Fatalf("rule keeps %v", got)
	}
	if got := u.remove[3].Args["resources"]; !slices.Equal(got.([]string), []string{"ip3"}) {
		t.Fatalf("releases %v", got)
	}
	if u.delta["vm_count"] != -1 || u.delta["storage_gb"] != -50 || len(u.removed) != 1 || !u.removed[0].Destructive {
		t.Fatalf("delta %v, removed %+v", u.delta, u.removed)
	}

	// Down to one: the rule goes; a protected VM is refused.
	u = newPlan()
	three[1].preventDelete = true
	c.count, c.vms = 1, three
	c.plan(u)
	if u.remove[0].Op != "delete_resources" || u.remove[0].Args["resource"] != "rule" || len(u.refused) != 1 {
		t.Fatalf("scale to one: %+v, refused %+v", u.remove[0], u.refused)
	}
}

// The VMs rvav built go by their index, 10 after 9, and those adopted
// after them: scaling in removes an adopted VM first.
func TestUpdateScaleInOrder(t *testing.T) {
	legacy := updateVM("legacy-web01", "10.0.0.21", 2, 4, 50)
	legacy.t.VM.Template = nil
	vms := []existingVM{legacy, updateVM("web10", "10.0.0.20", 2, 4, 50), updateVM("web09", "10.0.0.19", 2, 4, 50)}
	slices.SortFunc(vms, byIndex)
	var names []string
	for _, vm := range vms {
		names = append(names, vm.name())
	}
	if want := []string{"web09", "web10", "legacy-web01"}; !slices.Equal(names, want) {
		t.Fatalf("order %v, want %v", names, want)
	}
	plan := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl"}
	u := newPlan()
	c := componentChange{key: "web", title: "Web servers", had: true, old: plan, new: plan, count: 2, vms: vms}
	c.plan(u)
	if got := keysOf(u.remove); !slices.Contains(got, "vsphere.destroy@legacy-web01") || len(u.removed) != 1 {
		t.Fatalf("scale in removes %v", got)
	}
}

// A failed deployment's jobs run again on the VMs kept: they may not all
// have run.
func TestUpdateFailedJobs(t *testing.T) {
	plan := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", PostConfig: []string{"baseline"}}
	jobs := []provision.Job{{Key: "baseline", Template: "linux-baseline"}}
	u := newPlan()
	c := componentChange{key: blueprint.SingleKey, had: true, old: plan, new: plan, count: 1, failed: true, jobs: jobs,
		vms: []existingVM{updateVM("app01", "10.0.0.31", 2, 4, 50)}}
	c.plan(u)
	if len(u.jobs) != 1 || u.jobs[0].Component != blueprint.SingleKey || len(u.ran) != 1 || u.ran[0].Kind != api.Job {
		t.Fatalf("jobs %+v, changes %+v", u.jobs, u.ran)
	}
	// A deployment of one VM whose VM is gone gets it built again.
	u = newPlan()
	c.vms = nil
	if n := c.plan(u); n != 1 || len(u.jobs) != 0 {
		t.Fatalf("adds %d, jobs %+v", n, u.jobs)
	}
}

// An update acts on the disks rvav recorded as the blueprint's (X1.6): not
// on one a day-2 action added between them, wherever it sits.
func TestUpdateDisksByRecord(t *testing.T) {
	base := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", Cluster: "GVA-DEV-CL01"}
	v2, v3 := base, base
	v2.Disks = []blueprint.DiskPlan{{SizeGB: 20}, {SizeGB: 30}}
	v3.Disks = []blueprint.DiskPlan{{SizeGB: 20}}
	v3.DiskGB = 60
	// Its first disk, v2's 20 GB disk, the owner's 100 GB disk, v2's 30 GB disk.
	vm := recordedAs(updateVM("web01", "10.0.0.11", 2, 4, 50, 20, 100, 30), 0, 1, -1, 2)
	u := newPlan()
	c := componentChange{key: "web", title: "Web", had: true, old: v2, new: v3, count: 1, vms: []existingVM{vm}}
	c.plan(u)
	if len(u.refused) > 0 {
		t.Fatalf("refused %v", refusals(u))
	}
	var got []string
	for _, op := range u.ops {
		got = append(got, fmt.Sprintf("%s %v %v", op.Op, op.Args["file"], op.Args["size_gb"]))
	}
	want := []string{"extend_disk <nil> 60", "remove_disk [ds] web01/web01_3.vmdk <nil>"}
	if !slices.Equal(got, want) {
		t.Fatalf("ops = %v, want %v", got, want)
	}
	if u.ops[0].Args["key"] != 2000 || u.ops[1].Args["key"] != 2003 {
		t.Fatalf("by key: %+v", u.ops)
	}
	if u.delta["storage_gb"] != 10-30 || u.changes[1].Title != "Remove Hard disk 4 from web01" {
		t.Fatalf("delta %v, changes %+v", u.delta, u.changes)
	}

	// Its first extra disk grows, wherever it sits.
	v4 := v3
	v4.Disks = []blueprint.DiskPlan{{SizeGB: 40}}
	moved := recordedAs(updateVM("web03", "10.0.0.13", 2, 4, 60, 100, 20), 0, -1, 1)
	u = newPlan()
	c = componentChange{key: "web", title: "Web", had: true, old: v3, new: v4, count: 1, vms: []existingVM{moved}}
	if c.plan(u); len(u.refused) > 0 || len(u.ops) != 1 || u.ops[0].Args["key"] != 2002 || u.ops[0].Args["size_gb"] != 40 {
		t.Fatalf("grow its first extra disk: %+v, refused %v", u.ops, refusals(u))
	}
	c = componentChange{key: "web", title: "Web", had: true, old: v2, new: v3, count: 1}

	// The blueprint's disk the owner removed stays removed.
	gone := recordedAs(updateVM("web02", "10.0.0.12", 2, 4, 50, 100, 30), 0, -1, 2)
	u = newPlan()
	c.vms = []existingVM{gone}
	if c.plan(u); len(u.refused) > 0 || !slices.Equal(keysOf(u.ops), []string{"vsphere.disk_extend@web02", "vsphere.disk_remove@web02"}) ||
		u.ops[1].Args["key"] != 2002 {
		t.Fatalf("without its first extra disk: %v, refused %v", u.ops, refusals(u))
	}
}

// A version that removes extra disks and changes others is refused: its
// disks have no names, so rvav can't tell which ones go.
func TestUpdateDisksAmbiguous(t *testing.T) {
	base := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", Cluster: "GVA-DEV-CL01"}
	old, next := base, base
	old.Disks = []blueprint.DiskPlan{{SizeGB: 20}, {SizeGB: 50}}
	next.Disks = []blueprint.DiskPlan{{SizeGB: 50}}
	u := newPlan()
	c := componentChange{key: "web", title: "Web", had: true, old: old, new: next, count: 1,
		vms: []existingVM{updateVM("web01", "10.0.0.11", 2, 4, 50, 20, 50)}}
	c.plan(u)
	if len(u.refused) != 1 || *u.refused[0].Setting != "disks" || !strings.Contains(u.refused[0].Reason, "can't tell which ones go") {
		t.Fatalf("refused %v", refusals(u))
	}
	for _, op := range u.ops {
		if op.Op == "remove_disk" {
			t.Fatalf("removes %v", op.Args["file"])
		}
	}

	// With keys (#191), the versions' disks are told apart: the update
	// removes data and grows logs, wherever they are, and records logs's
	// new place once it succeeds. On a VM recorded before its version had
	// keys, by the places the old version gives them.
	old.Disks = []blueprint.DiskPlan{{Key: "data", SizeGB: 20}, {Key: "logs", SizeGB: 50}}
	next.Disks = []blueprint.DiskPlan{{Key: "logs", SizeGB: 60}}
	keyed := keyedAs(updateVM("web01", "10.0.0.11", 2, 4, 50, 20, 50), "", "data", "logs")
	before := updateVM("web02", "10.0.0.12", 2, 4, 50, 20, 50)
	u = newPlan()
	c = componentChange{key: "web", title: "Web", had: true, old: old, new: next, count: 2, vms: []existingVM{keyed, before}}
	c.plan(u)
	var got []string
	for _, op := range u.ops {
		got = append(got, fmt.Sprintf("%s %v %v", op.Key, op.Args["key"], op.Args["size_gb"]))
	}
	want := []string{"vsphere.disk_extend@web01 2002 60", "vsphere.disk_remove@web01 2001 <nil>",
		"vsphere.disk_extend@web02 2002 60", "vsphere.disk_remove@web02 2001 <nil>"}
	if !slices.Equal(got, want) || len(u.refused) > 0 || u.delta["storage_gb"] != -20 {
		t.Fatalf("ops = %v, refused %v, delta %v", got, refusals(u), u.delta)
	}
	logs := []vmprops.DiskMark{{Device: 2002, Disk: 1, Key: "logs"}}
	if !reflect.DeepEqual(u.marks, map[string][]vmprops.DiskMark{"res-web01": logs, "res-web02": logs}) {
		t.Fatalf("marks = %+v", u.marks)
	}
}

// keyedAs records the key of the blueprint disk each of a VM's disks is: ""
// for none.
func keyedAs(vm existingVM, keys ...string) existingVM {
	vm.t.VM.Disks = slices.Clone(vm.t.VM.Disks)
	for i, k := range keys {
		vm.t.VM.Disks[i].BlueprintDiskKey = nil
		if k != "" {
			vm.t.VM.Disks[i].BlueprintDiskKey = new(k)
		}
	}
	return vm
}

// Disks match by key only when every extra disk of both versions has one
// (#191): giving the disks keys changes nothing on the VMs, and records
// the keys once the update succeeds; a disk added is recorded with its
// key; a version that keys only some disks matches them by place, refused
// as before when it removes some and changes others.
func TestUpdateDiskKeys(t *testing.T) {
	base := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", Cluster: "GVA-DEV-CL01"}
	plan := func(old, next []blueprint.DiskPlan, vm existingVM) *componentPlan {
		o, n := base, base
		o.Disks, n.Disks = old, next
		u := newPlan()
		c := componentChange{key: "web", title: "Web", had: true, old: o, new: n, count: 1, vms: []existingVM{vm}}
		c.plan(u)
		return u
	}
	vm := updateVM("web01", "10.0.0.11", 2, 4, 50, 20, 50)
	vm.t.VM.PowerState = api.Suspended
	u := plan([]blueprint.DiskPlan{{SizeGB: 20}, {SizeGB: 50}}, []blueprint.DiskPlan{{Key: "data", SizeGB: 20}, {Key: "logs", SizeGB: 50}}, vm)
	want := map[string][]vmprops.DiskMark{"res-web01": {{Device: 2001, Disk: 1, Key: "data"}, {Device: 2002, Disk: 2, Key: "logs"}}}
	if len(u.ops) > 0 || len(u.refused) > 0 || !reflect.DeepEqual(u.marks, want) {
		t.Fatalf("keys given: ops %v, refused %v, marks %+v", keysOf(u.ops), refusals(u), u.marks)
	}

	keyed := keyedAs(updateVM("web01", "10.0.0.11", 2, 4, 50, 20), "", "logs")
	u = plan([]blueprint.DiskPlan{{Key: "logs", SizeGB: 20}}, []blueprint.DiskPlan{{Key: "data", SizeGB: 30}, {Key: "logs", SizeGB: 20}}, keyed)
	if !slices.Equal(keysOf(u.ops), []string{"vsphere.disk_add@web01"}) || u.ops[0].Args["blueprint_disk"] != 1 ||
		u.ops[0].Args["blueprint_disk_key"] != "data" || !reflect.DeepEqual(u.marks["res-web01"], []vmprops.DiskMark{{Device: 2001, Disk: 2, Key: "logs"}}) {
		t.Fatalf("a disk added before: ops %+v, marks %+v", u.ops, u.marks)
	}
	// Made again after it was added: not added twice.
	again := keyedAs(recordedAs(updateVM("web01", "10.0.0.11", 2, 4, 50, 20, 30), 0, 1, 1), "", "logs", "data")
	u = plan([]blueprint.DiskPlan{{Key: "logs", SizeGB: 20}}, []blueprint.DiskPlan{{Key: "data", SizeGB: 30}, {Key: "logs", SizeGB: 20}}, again)
	if len(u.ops) > 0 || len(u.refused) > 0 {
		t.Fatalf("made again: ops %v, refused %v", keysOf(u.ops), refusals(u))
	}

	u = plan([]blueprint.DiskPlan{{Key: "data", SizeGB: 20}, {SizeGB: 50}}, []blueprint.DiskPlan{{SizeGB: 50}},
		keyedAs(updateVM("web01", "10.0.0.11", 2, 4, 50, 20, 50), "", "data"))
	if len(u.refused) != 1 || !strings.Contains(u.refused[0].Reason, "Give every extra disk a key") || len(u.ops) > 0 {
		t.Fatalf("some keyed: ops %v, refused %v", keysOf(u.ops), refusals(u))
	}
}

// After an update failed part-way, the next adds the disk only to the VMs
// it didn't reach, and records which blueprint disk each new one is.
func TestUpdateAddsOnce(t *testing.T) {
	base := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", Cluster: "GVA-DEV-CL01"}
	next := base
	next.Disks = []blueprint.DiskPlan{{SizeGB: 20}}
	u := newPlan()
	c := componentChange{key: "web", title: "Web", had: true, old: base, new: next, count: 2,
		vms: []existingVM{updateVM("web01", "10.0.0.11", 2, 4, 50, 20), updateVM("web02", "10.0.0.12", 2, 4, 50)}}
	c.plan(u)
	if !slices.Equal(keysOf(u.ops), []string{"vsphere.disk_add@web02"}) || u.ops[0].Args["blueprint_disk"] != 1 || u.delta["storage_gb"] != 20 {
		t.Fatalf("ops = %+v, delta %v", u.ops, u.delta)
	}
}

// A VM whose disks rvav didn't record, onboarded or built before X1.6:
// growing or removing its disks is refused with the reason, not guessed
// from their places; a disk the new version brings is added.
func TestUpdateDisksUnrecorded(t *testing.T) {
	base := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", Cluster: "GVA-DEV-CL01",
		Disks: []blueprint.DiskPlan{{SizeGB: 20}, {SizeGB: 30}}}
	vm := recordedAs(updateVM("legacy01", "10.0.0.41", 2, 4, 50, 20, 30), -1, -1, -1)
	for _, tc := range []struct {
		name    string
		change  func(*blueprint.VMPlan)
		refused []string
		ops     []string
	}{
		{"grow its first disk", func(p *blueprint.VMPlan) { p.DiskGB = 80 }, []string{"disk_gb"}, nil},
		{"grow an extra disk", func(p *blueprint.VMPlan) { p.Disks[1].SizeGB = 40 }, []string{"disks"}, nil},
		{"remove an extra disk", func(p *blueprint.VMPlan) { p.Disks = p.Disks[:1] }, []string{"disks"}, nil},
		{"add a disk", func(p *blueprint.VMPlan) { p.Disks = append(p.Disks, blueprint.DiskPlan{SizeGB: 10}) }, nil,
			[]string{"vsphere.disk_add@legacy01"}},
	} {
		next := base
		next.Disks = slices.Clone(base.Disks)
		tc.change(&next)
		u := newPlan()
		c := componentChange{key: "web", title: "Web", had: true, old: base, new: next, count: 1, vms: []existingVM{vm}}
		c.plan(u)
		var settings []string
		for _, r := range u.refused {
			settings = append(settings, *r.Setting)
			if !strings.Contains(r.Reason, "legacy01 was onboarded or built before rvav recorded") {
				t.Errorf("%s: reason %q", tc.name, r.Reason)
			}
		}
		if !slices.Equal(settings, tc.refused) || !slices.Equal(keysOf(u.ops), tc.ops) {
			t.Errorf("%s: refused %v, ops %v", tc.name, refusals(u), keysOf(u.ops))
		}
	}
}

// withNICs gives a VM its adapters after its first, each "network" or
// "network:blueprint adapter"; recorded marks its first adapter as rvav
// marks those it builds.
func withNICs(vm existingVM, recorded bool, nics ...string) existingVM {
	first := vm.t.VM.Nics[0]
	first.Label, first.Key = new(day2.NICLabel(1)), new(4000)
	if recorded {
		first.BlueprintNic = new("nic1")
	}
	vm.t.VM.Nics = []api.VmNic{first}
	for i, s := range nics {
		network, key, ok := strings.Cut(s, ":")
		n := api.VmNic{Network: network, Label: new(day2.NICLabel(i + 2)), Key: new(4001 + i)}
		if ok {
			n.BlueprintNic = &key
		}
		vm.t.VM.Nics = append(vm.t.VM.Nics, n)
	}
	return vm
}

// nicOps are the adapter operations planned, as "op label key network".
func nicOps(u *componentPlan) []string {
	var out []string
	for _, op := range u.ops {
		if strings.HasSuffix(op.Op, "_nic") || op.Op == "set_nic_network" {
			out = append(out, strings.TrimSpace(fmt.Sprintf("%s %v %v %v", op.Op, op.Args["nic"], op.Args["key"], op.Args["network"])))
		}
	}
	return out
}

// After an update that added a DHCP adapter failed part-way, the next adds
// it only to the VMs it didn't reach (#192), and records which blueprint
// adapter each new one is. A VM rvav didn't record adapters on gets it as
// before.
func TestUpdateNICsAddsOnce(t *testing.T) {
	base := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", Cluster: "GVA-DEV-CL01"}
	next := base
	next.NICs = []blueprint.NICPlan{{Key: "backup", Network: "dvpg-backup", IPAM: blueprint.IPAMDHCP}}
	web01 := withNICs(updateVM("web01", "10.0.0.11", 2, 4, 50), true, "dvpg-backup:backup")
	web02 := withNICs(updateVM("web02", "10.0.0.12", 2, 4, 50), true)
	legacy := withNICs(updateVM("web03", "10.0.0.13", 2, 4, 50), false)
	u := newPlan()
	c := componentChange{key: "web", title: "Web", had: true, old: base, new: next, count: 3, vms: []existingVM{web01, web02, legacy}}
	c.plan(u)
	if !slices.Equal(keysOf(u.ops), []string{"vsphere.nic_add@web02", "vsphere.nic_add@web03"}) || len(u.refused) > 0 {
		t.Fatalf("ops = %v, refused %v", keysOf(u.ops), refusals(u))
	}
	if u.ops[0].Args["blueprint_nic"] != "backup" || u.ops[0].Args["network"] != "dvpg-backup" {
		t.Fatalf("the adapter added = %+v", u.ops[0].Args)
	}
}

// An update moves and removes the DHCP adapters rvav recorded as the
// blueprint's (#192), not one a day-2 action added before them; on a VM
// without the record, those at their places, as before.
func TestUpdateNICsByRecord(t *testing.T) {
	base := blueprint.VMPlan{VCPU: 2, MemoryGB: 4, DiskGB: 50, Template: "tpl", Cluster: "GVA-DEV-CL01"}
	old, next := base, base
	old.NICs = []blueprint.NICPlan{{Key: "backup", Network: "net-b", IPAM: blueprint.IPAMDHCP}, {Key: "mgmt", Network: "net-m", IPAM: blueprint.IPAMDHCP}}
	next.NICs = []blueprint.NICPlan{{Key: "backup", Network: "net-c", IPAM: blueprint.IPAMDHCP}}
	for _, tc := range []struct {
		name string
		vm   existingVM
		want []string
	}{
		{"the owner's adapter first", withNICs(updateVM("web01", "10.0.0.11", 2, 4, 50), true, "net-x", "net-b:backup", "net-m:mgmt"),
			[]string{"set_nic_network Network adapter 3 4002 net-c", "remove_nic Network adapter 4 4003 <nil>"}},
		{"backup removed by its owner", withNICs(updateVM("web02", "10.0.0.12", 2, 4, 50), true, "net-m:mgmt"),
			[]string{"remove_nic Network adapter 2 4001 <nil>"}},
		{"backup moved by an update that failed", withNICs(updateVM("web03", "10.0.0.13", 2, 4, 50), true, "net-c:backup", "net-m:mgmt"),
			[]string{"remove_nic Network adapter 3 4002 <nil>"}},
		{"not recorded", withNICs(updateVM("web04", "10.0.0.14", 2, 4, 50), false, "net-b", "net-m"),
			[]string{"set_nic_network Network adapter 2 4001 net-c", "remove_nic Network adapter 3 4002 <nil>"}},
	} {
		u := newPlan()
		c := componentChange{key: "web", title: "Web", had: true, old: old, new: next, count: 1, vms: []existingVM{tc.vm}}
		c.plan(u)
		if got := nicOps(u); !slices.Equal(got, tc.want) || len(u.refused) > 0 {
			t.Errorf("%s: ops = %v, want %v (refused %v)", tc.name, got, tc.want, refusals(u))
		}
	}
}

func TestNaturalCompare(t *testing.T) {
	names := []string{"web10", "web9", "web01", "db2"}
	slices.SortFunc(names, naturalCompare)
	if !slices.Equal(names, []string{"db2", "web01", "web9", "web10"}) {
		t.Fatalf("sorted = %v", names)
	}
}

func TestSameInputs(t *testing.T) {
	ui := json.RawMessage(`{"fields":{"token":{"widget":"text","secret":true}}}`)
	had := json.RawMessage(`{"size":"S","token":{"$secret":"token"},"n":2}`)
	if !sameInputs(ui, had, map[string]any{"size": "S", "token": "again", "n": 2}) {
		t.Fatal("a secret given again counts as a change")
	}
	if sameInputs(ui, had, map[string]any{"size": "M", "token": "again", "n": 2}) {
		t.Fatal("a new size counts as no change")
	}
}
