package vcsim_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/task"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/vcsim"
)

// TestSerialClones clones from many VMs into one folder at once while
// container views of every VM are read, as rvav's workers and inventory
// watch do. Without SerialClones, vcsim panics with "does not have a Name"
// in four runs out of five (rvav#187).
func TestSerialClones(t *testing.T) {
	m := simulator.VPX()
	m.Machine = 8
	defer m.Remove()
	if err := m.Create(); err != nil {
		t.Fatal(err)
	}
	vcsim.SerialClones(m.Map())
	s := m.Service.NewServer()
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := govmomi.NewClient(ctx, s.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	finder := find.NewFinder(c.Client, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finder.SetDatacenter(dc)
	folders, err := dc.Folders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := finder.VirtualMachineList(ctx, "*")
	if err != nil {
		t.Fatal(err)
	}

	// Each new VM is announced to these views while it has no name yet,
	// which keeps it nameless long enough for the race to show.
	var readers sync.WaitGroup
	var stop atomic.Bool
	defer readers.Wait()
	defer stop.Store(true)
	for range 4 {
		v, err := view.NewManager(c.Client).CreateContainerView(ctx, c.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
		if err != nil {
			t.Fatal(err)
		}
		readers.Go(func() {
			for !stop.Load() {
				var vms []mo.VirtualMachine
				_ = v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"name"}, &vms)
			}
		})
	}

	clone := func(src *object.VirtualMachine, name string) error {
		tk, err := src.Clone(ctx, folders.VmFolder, name, types.VirtualMachineCloneSpec{})
		if err != nil {
			return err
		}
		return tk.Wait(ctx)
	}
	const each = 3
	var wg sync.WaitGroup
	errs := make([]error, len(sources))
	for w, src := range sources {
		wg.Go(func() {
			for i := range each {
				if errs[w] = clone(src, fmt.Sprintf("clone-%02d-%d", w, i)); errs[w] != nil {
					return
				}
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	made, err := finder.VirtualMachineList(ctx, "clone-*")
	if err != nil || len(made) != len(sources)*each {
		t.Fatalf("clones = %d of %d, %v", len(made), len(sources)*each, err)
	}

	// The duplicate check still holds, now even between clones that race.
	errs = make([]error, len(sources))
	for i, src := range sources {
		wg.Go(func() { errs[i] = clone(src, "twin") })
	}
	wg.Wait()
	refused := 0
	for _, err := range errs {
		var te task.Error
		if errors.As(err, &te) {
			if _, ok := te.Fault().(*types.DuplicateName); ok {
				refused++
				continue
			}
		}
		if err != nil {
			t.Fatalf("twin: %v", err)
		}
	}
	if refused != len(sources)-1 {
		t.Fatalf("%d of %d clones named twin refused, want all but one", refused, len(sources))
	}
}
