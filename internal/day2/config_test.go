package day2

import (
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
