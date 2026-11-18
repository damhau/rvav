package day2

import (
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/api"
)

// TestExtendLinkedClone: a linked clone's template disk isn't offered for
// extending, nor taken; its added disks are (T1.7, ADR-0037).
func TestExtendLinkedClone(t *testing.T) {
	a := Get("disk_extend")
	vm := func(disks ...api.VmDisk) Target {
		return Target{Type: vmType, VM: api.VmProperties{Disks: disks}}
	}
	yes := true
	linked := api.VmDisk{Label: "Hard disk 1", CapacityGb: 40, Linked: &yes}
	added := api.VmDisk{Label: "Hard disk 2", CapacityGb: 10}

	if why := a.Unavailable(vm(linked), Choices{}); why != linkedDisk {
		t.Errorf("only a linked disk: %q", why)
	}
	both := vm(linked, added)
	if why := a.Unavailable(both, Choices{}); why != "" {
		t.Fatalf("with an added disk: %q", why)
	}
	f := a.Form(both, Choices{})
	if opts := *f.UiSchema.Fields["disk"].Options; len(opts) != 1 || opts[0].Value != "Hard disk 2" || f.InitialValues["disk"] != "Hard disk 2" {
		t.Errorf("the disks offered = %+v, %v", opts, f.InitialValues)
	}
	if errs := a.Validate(both, map[string]any{"disk": "Hard disk 1", "size_gb": 80.0}); len(errs) != 1 || errs[0].Field != "/inputs/disk" {
		t.Errorf("extending the linked disk = %+v", errs)
	}
	if errs := a.Validate(both, map[string]any{"disk": "Hard disk 2", "size_gb": 20.0}); len(errs) != 0 {
		t.Errorf("extending the added disk = %+v", errs)
	}
}

// TestRemoveDisk: neither the boot disk nor a linked clone's template disk
// is offered; the step names the disk by its file (X1.3).
func TestRemoveDisk(t *testing.T) {
	a := Get("disk_remove")
	vm := func(disks ...api.VmDisk) Target {
		return Target{Type: vmType, ResourceName: "web-dev-api004", VM: api.VmProperties{Disks: disks}}
	}
	yes, file := true, "[ds1] web-dev-api004/web-dev-api004_1.vmdk"
	linked := api.VmDisk{Label: "Hard disk 1", CapacityGb: 40, Linked: &yes}
	added := api.VmDisk{Label: "Hard disk 2", CapacityGb: 10, File: &file}

	if why := a.Unavailable(vm(linked), Choices{}); why != "The VM has only its boot disk, which can't be removed." {
		t.Errorf("only the boot disk: %q", why)
	}
	both := vm(linked, added)
	if why := a.Unavailable(both, Choices{}); why != "" {
		t.Fatalf("with an added disk: %q", why)
	}
	if opts := *a.Form(both, Choices{}).UiSchema.Fields["disk"].Options; len(opts) != 1 || opts[0].Value != "Hard disk 2" {
		t.Errorf("the disks offered = %+v", opts)
	}
	ops := a.Ops(both, map[string]any{"disk": "Hard disk 2"})
	if len(ops) != 1 || ops[0].Op != "remove_disk" || ops[0].Args["file"] != file || ops[0].Detail != "10 GB" {
		t.Errorf("ops = %+v", ops)
	}
	n := 2
	both.VM.SnapshotCount = &n
	if why := a.Unavailable(both, Choices{}); why != "Disks of a VM with snapshots can't be removed. Delete its snapshots first." {
		t.Errorf("with snapshots: %q", why)
	}
}

// TestAddDiskChoices: a new disk goes with the VM's first by default; the
// placement rule's storage policy is the only one offered when it names
// one; without datastores to choose, it goes on the VM's (X1.3).
func TestAddDiskChoices(t *testing.T) {
	a := Get("disk_add")
	ds := "ds-b"
	vm := Target{Type: vmType, VM: api.VmProperties{Disks: []api.VmDisk{{Label: "Hard disk 1", CapacityGb: 40, Datastore: &ds}}}}
	c := Choices{Datastores: []api.Option{{Value: "ds-a", Label: "ds-a"}, {Value: "ds-b", Label: "ds-b"}},
		StoragePolicies: []string{"Gold", "Silver"}}

	f := a.Form(vm, c)
	if f.InitialValues["datastore"] != "ds-b" || f.InitialValues["storage_policy"] != datastoreDefault ||
		len(*f.UiSchema.Fields["storage_policy"].Options) != 3 {
		t.Errorf("form = %+v, %+v", f.InitialValues, f.UiSchema.Fields["storage_policy"])
	}
	c.RulePolicy, c.StoragePolicies = "Gold", []string{"Gold"}
	f = a.Form(vm, c)
	if opts := *f.UiSchema.Fields["storage_policy"].Options; len(opts) != 1 || f.InitialValues["storage_policy"] != "Gold" {
		t.Errorf("with the rule's policy: %+v", opts)
	}
	if req := f.Schema["required"].([]string); !slices.Equal(req, []string{"size_gb"}) {
		t.Errorf("required = %v: a size alone adds a disk, as before the choices", req)
	}
	ops := a.Ops(vm, map[string]any{"size_gb": 10.0, "datastore": "ds-a", "storage_policy": "Gold", "provisioning": "thin"})
	if len(ops) != 1 || ops[0].Args["datastore"] != "ds-a" || ops[0].Args["storage_policy"] != "Gold" || ops[0].Detail != "Thin · Storage policy Gold" {
		t.Errorf("ops = %+v", ops)
	}
	ops = a.Ops(vm, map[string]any{"size_gb": 10.0})
	if len(ops) != 1 || len(ops[0].Args) != 1 || ops[0].Title != "Add a 10 GB disk" {
		t.Errorf("ops with a size alone = %+v", ops)
	}
	f = a.Form(vm, Choices{})
	if _, ok := f.UiSchema.Fields["datastore"]; ok || deref(f.UiSchema.Fields["size_gb"].Help) != "On the VM's datastore." {
		t.Errorf("without choices: %+v", f.UiSchema.Fields)
	}
}
