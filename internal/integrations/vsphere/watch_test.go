package vsphere

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
)

// Watch says the inventory changed once it follows, then each VM that
// changes (powered off, reconfigured), and a VM deleted as gone.
func TestWatch(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		d := NewFromVim("vc01.corp.example", c)
		var mu sync.Mutex
		var got [][]Change
		wctx, stop := context.WithCancel(ctx)
		done := make(chan error)
		go func() {
			done <- d.Watch(wctx, func(cs []Change) {
				mu.Lock()
				got = append(got, cs)
				mu.Unlock()
			})
		}()
		seen := func(ok func(Change) bool) {
			t.Helper()
			for start := time.Now(); time.Since(start) < 5*time.Second; time.Sleep(20 * time.Millisecond) {
				mu.Lock()
				found := slices.ContainsFunc(got, func(cs []Change) bool { return slices.ContainsFunc(cs, ok) })
				mu.Unlock()
				if found {
					return
				}
			}
			t.Fatalf("change not seen in %+v", got)
		}
		seen(func(c Change) bool { return c.Inventory })
		mu.Lock()
		if len(got) != 1 || len(got[0]) != 1 {
			t.Fatalf("what is there when following = %+v, want the inventory changed once", got)
		}
		mu.Unlock()

		vm, err := find.NewFinder(c).VirtualMachine(ctx, "/DC0/vm/DC0_H0_VM0")
		if err != nil {
			t.Fatal(err)
		}
		task, _ := vm.PowerOff(ctx)
		if err := task.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		seen(func(c Change) bool { return c.VM == vm.Reference().Value && !c.Gone })
		// A reconfiguration (here more memory) is a change too.
		mu.Lock()
		got = nil
		mu.Unlock()
		task, _ = vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{MemoryMB: 64})
		if err := task.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		seen(func(c Change) bool { return c.VM == vm.Reference().Value && !c.Gone })
		task, _ = vm.Destroy(ctx)
		if err := task.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		seen(func(c Change) bool { return c.VM == vm.Reference().Value && c.Gone })

		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("stopped watch = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the watch didn't stop")
		}
	})
}
