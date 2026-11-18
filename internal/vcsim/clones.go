// Package vcsim adjusts the govmomi vCenter simulator for rvav's tests and
// cmd/rvav-vcsim. Only test code and the simulator command import it.
package vcsim

import (
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

var guardRef = types.ManagedObjectReference{Type: "RvavCloneGuard", Value: "clone-guard"}

// SerialClones makes each CloneVM_Task hold its destination folder's lock
// until the clone has finished, so that clones into one folder run one at a
// time. Call it before the simulator serves requests.
//
// vcsim's clone looks for a duplicate name among the folder's children
// without that lock, while a VM that another clone or CreateVM_Task is
// creating there is already a child but has no name yet. The lookup then
// panics in the simulator's own goroutine, which ends the process (rvav#187).
func SerialClones(r *simulator.Registry) {
	r.Put(&cloneGuard{mo.ExtensibleManagedObject{Self: guardRef}})
	next := r.Handler
	r.Handler = func(ctx *simulator.Context, m *simulator.Method) (mo.Reference, types.BaseMethodFault) {
		var h mo.Reference
		if next != nil {
			var fault types.BaseMethodFault
			if h, fault = next(ctx, m); fault != nil {
				return nil, fault
			}
		}
		if m.Name == "CloneVM_Task" {
			if _, ok := ctx.Map.Get(m.This).(*simulator.VirtualMachine); ok {
				// vcsim replaces the handler returned here with the object
				// m.This names whenever the call has a session.
				m.This = guardRef
			}
		}
		return h, nil
	}
}

// cloneGuard runs the CloneVM_Task calls SerialClones redirects to it. It
// opts out of vcsim's per-object lock, which would serialise every clone.
type cloneGuard struct {
	mo.ExtensibleManagedObject
}

func (*cloneGuard) Lock()   {}
func (*cloneGuard) Unlock() {}

func (*cloneGuard) CloneVMTask(ctx *simulator.Context, req *types.CloneVM_Task) soap.HasFault {
	vm, ok := ctx.Map.Get(req.This).(*simulator.VirtualMachine)
	if !ok {
		return &methods.CloneVM_TaskBody{Fault_: simulator.Fault("", &types.ManagedObjectNotFound{Obj: req.This})}
	}
	ctx.Caller = &req.This
	var res soap.HasFault
	// The source VM first, then the folder: the order of vcsim's own clone.
	ctx.WithLock(vm, func() {
		ctx.WithLock(req.Folder, func() {
			res = vm.CloneVMTask(ctx, req)
			body, ok := res.(*methods.CloneVM_TaskBody)
			if !ok || body.Res == nil {
				return
			}
			if task, ok := ctx.Map.Get(body.Res.Returnval).(*simulator.Task); ok {
				wait(ctx.Map, task)
			}
		})
	})
	return res
}

// wait is Task.Wait polling every millisecond rather than every ten: the
// folder stays locked meanwhile.
func wait(r *simulator.Registry, task *simulator.Task) {
	own := &simulator.Context{}
	for {
		var done bool
		r.WithLock(own, task, func() {
			done = task.Info.State == types.TaskInfoStateSuccess || task.Info.State == types.TaskInfoStateError
		})
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
