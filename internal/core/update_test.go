package core

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/provision"
)

func updateVM(name, ip string, cpu, memGB int, disks ...int) existingVM {
	vm := api.VmProperties{Moref: "vm-" + name, CpuCount: cpu, MemoryMb: memGB * 1024, PowerState: api.On,
		GuestState: api.VmPropertiesGuestStateRunning, Fqdn: new(name + ".corp.example"), Cluster: new("GVA-DEV-CL01"),
		Nics: []api.VmNic{{Network: "dvpg-app", IpAddresses: &[]string{ip}}}}
	for i, gb := range disks {
		vm.Disks = append(vm.Disks, api.VmDisk{Label: day2.DiskLabel(i + 1), CapacityGb: gb})
	}
	return existingVM{t: day2.Target{ResourceID: "res-" + name, ResourceName: name, Type: "vsphere.vm", VM: vm}}
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
