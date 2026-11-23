package vcsim_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/vcsim"
)

// TestNonBlockingFilters builds two VMs at once, each cloned then
// reconfigured, while the sync's live watch waits for updates: a request
// with count: 2 with the sync running. Without NonBlockingFilters, vcsim's
// locks wait on each other for ever within a few rounds (rvav#206).
func TestNonBlockingFilters(t *testing.T) {
	// Each clone keeps its source VM locked for 20 ms before it makes the
	// VM. Without that, the overlap that hangs is a few microseconds wide
	// and shows only now and then.
	delays := simulator.TaskDelay.MethodDelay
	simulator.TaskDelay.MethodDelay = map[string]int{"CloneVm": 20}
	defer func() { simulator.TaskDelay.MethodDelay = delays }()
	m := simulator.VPX()
	defer m.Remove()
	if err := m.Create(); err != nil {
		t.Fatal(err)
	}
	vcsim.SerialClones(m.Map())
	vcsim.NonBlockingFilters(m.Map())
	s := m.Service.NewServer()
	hung := false
	defer func() {
		// Close waits for the calls in progress, which a hung vcsim never
		// answers.
		if !hung {
			s.Close()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connect := func() *vim25.Client {
		t.Helper()
		c, err := govmomi.NewClient(ctx, s.URL, true)
		if err != nil {
			t.Fatal(err)
		}
		return c.Client
	}
	c := connect()
	finder := find.NewFinder(c, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finder.SetDatacenter(dc)
	folders, err := dc.Folders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	template, err := finder.VirtualMachine(ctx, "DC0_H0_VM0")
	if err != nil {
		t.Fatal(err)
	}

	// The sync's watch, in its own session as the sync process has.
	watcher := vsphere.NewFromVim("vc", connect())
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	var mu sync.Mutex
	seen := map[string]bool{}
	following := make(chan struct{})
	watched := make(chan error, 1)
	go func() {
		var once sync.Once
		watched <- watcher.Watch(watchCtx, func(cs []vsphere.Change) {
			mu.Lock()
			for _, ch := range cs {
				seen[ch.VM] = true
			}
			mu.Unlock()
			once.Do(func() { close(following) })
		})
	}()
	select {
	case <-following:
	case err := <-watched:
		t.Fatalf("watch: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the watch has not started within 10s")
	}

	reconfigure := func(vm *object.VirtualMachine, memoryMB int64) error {
		task, err := vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{MemoryMB: memoryMB})
		if err != nil {
			return err
		}
		return task.Wait(ctx)
	}
	// As rvav's CloneVM does: the clone, then the VM's own settings.
	build := func(name string) (types.ManagedObjectReference, error) {
		task, err := template.Clone(ctx, folders.VmFolder, name, types.VirtualMachineCloneSpec{})
		if err != nil {
			return types.ManagedObjectReference{}, err
		}
		info, err := task.WaitForResult(ctx)
		if err != nil {
			return types.ManagedObjectReference{}, err
		}
		ref := info.Result.(types.ManagedObjectReference)
		return ref, reconfigure(object.NewVirtualMachine(c, ref), 2048)
	}
	var made []types.ManagedObjectReference
	for round := range 20 {
		refs := make([]types.ManagedObjectReference, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range refs {
			wg.Go(func() { refs[i], errs[i] = build(fmt.Sprintf("vm-%02d-%d", round, i)) })
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			hung = true
			t.Fatalf("round %d: the two VMs are not built within 10s; vcsim's waiting goroutines:\n%s", round, waiting())
		}
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		made = append(made, refs...)
	}

	// The watch still reports each VM made. vcsim reads the whole view
	// again for each change before it answers, which takes a while under
	// the race detector.
	deadline := time.Now().Add(60 * time.Second)
	for {
		mu.Lock()
		var missing []string
		for _, ref := range made {
			if !seen[ref.Value] {
				missing = append(missing, ref.Value)
			}
		}
		mu.Unlock()
		if len(missing) == 0 {
			break
		}
		select {
		case err := <-watched:
			t.Fatalf("the watch has ended (%v) without reporting %v", err, missing)
		default:
		}
		if time.Now().After(deadline) {
			hung = true
			t.Fatalf("the watch has not reported %v; vcsim's waiting goroutines:\n%s", missing, waiting())
		}
		time.Sleep(20 * time.Millisecond)
	}

	// vcsim ends a wait for updates only once something changes, and the
	// watch destroys its collector after the wait.
	stopWatch()
	vm := object.NewVirtualMachine(c, made[0])
	for i := 0; ; i++ {
		select {
		case err := <-watched:
			if err != nil {
				t.Fatalf("watch: %v", err)
			}
			return
		case <-time.After(50 * time.Millisecond):
			if i == 200 {
				t.Fatal("the watch has not stopped within 10s")
			}
			_ = reconfigure(vm, int64(1024+i))
		}
	}
}

// waiting returns the stacks of the simulator's goroutines that wait for a
// lock or a task.
func waiting() string {
	buf := make([]byte, 1<<24)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(g, "govmomi/simulator") && (strings.Contains(g, "sync.Mutex.Lock") ||
			strings.Contains(g, "(*Task).Wait") || strings.Contains(g, "rvav/internal/vcsim.")) {
			out = append(out, g)
		}
	}
	return strings.Join(out, "\n\n")
}
