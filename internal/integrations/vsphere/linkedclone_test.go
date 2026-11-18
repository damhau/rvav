package vsphere

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// cloneSpy keeps the clone specs sent to vCenter: vcsim ignores a clone's
// snapshot and disk move type.
type cloneSpy struct {
	soap.RoundTripper
	specs []types.VirtualMachineCloneSpec
}

func (s *cloneSpy) RoundTrip(ctx context.Context, req, res soap.HasFault) error {
	if b, ok := req.(*methods.CloneVM_TaskBody); ok {
		s.specs = append(s.specs, b.Req.Spec)
	}
	return s.RoundTripper.RoundTrip(ctx, req, res)
}

// TestLinkedClone: a linked clone is made from the template's current
// snapshot, its disks children of the template's; without a snapshot, with
// a disk larger than the template's, or from a content library, it is
// refused before any clone (ADR-0037).
func TestLinkedClone(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		spy := &cloneSpy{RoundTripper: c.RoundTripper}
		c.RoundTripper = spy
		d := NewFromVim("vc01.corp.example", c)
		spec := VMSpec{Name: "web-dev-linked01", Template: "tpl-rhel9.4-2026.10", Cluster: "GVA-DEV-CL01", Datastore: "vsan-gva-dev-cl01",
			Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/rvav/web/dev", VCPU: 2, MemoryMB: 2048, DiskGB: 40,
			Provisioning: ProvisionThickEager, LinkedClone: true}

		_, err := d.CloneVM(ctx, spec)
		if !errors.Is(err, ErrLinkedClone) || !strings.Contains(err.Error(), "template tpl-rhel9.4-2026.10 has no snapshot") {
			t.Fatalf("without a snapshot: %v", err)
		}
		tplRef, err := findRef(ctx, c, "VirtualMachine", spec.Template)
		if err != nil {
			t.Fatal(err)
		}
		tpl := object.NewVirtualMachine(c, tplRef)
		task, err := tpl.CreateSnapshot(ctx, "base", "", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := task.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		snapshot, err := tpl.FindSnapshot(ctx, "base")
		if err != nil {
			t.Fatal(err)
		}

		bigger := spec
		bigger.DiskGB = 60
		if _, err := d.CloneVM(ctx, bigger); !errors.Is(err, ErrLinkedClone) || !strings.Contains(err.Error(), "keeps the 40 GB disk") {
			t.Fatalf("a larger disk: %v", err)
		}
		library := spec
		library.Library, library.Template = "rvav-templates", "rhel9-library"
		if _, err := d.CloneVM(ctx, library); !errors.Is(err, ErrLinkedClone) {
			t.Fatalf("a content library item: %v", err)
		}
		if len(spy.specs) != 0 {
			t.Fatalf("cloned anyway: %+v", spy.specs)
		}

		vm, err := d.CloneVM(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		if len(spy.specs) != 1 {
			t.Fatalf("%d clones", len(spy.specs))
		}
		cs := spy.specs[0]
		if cs.Snapshot == nil || *cs.Snapshot != *snapshot ||
			cs.Location.DiskMoveType != string(types.VirtualMachineRelocateDiskMoveOptionsCreateNewChildDiskBacking) || len(cs.Location.Disk) != 0 {
			t.Fatalf("clone spec = %+v, snapshot %v", cs, snapshot)
		}
		if vm.Name != spec.Name {
			t.Fatalf("VM = %+v", vm)
		}

		// A full clone has neither.
		full := spec
		full.Name, full.LinkedClone = "web-dev-full01", false
		if _, err := d.CloneVM(ctx, full); err != nil {
			t.Fatal(err)
		}
		if cs := spy.specs[1]; cs.Snapshot != nil || cs.Location.DiskMoveType != "" || len(cs.Location.Disk) == 0 {
			t.Fatalf("full clone spec = %+v", cs)
		}
	})
}
