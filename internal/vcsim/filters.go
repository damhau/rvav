package vcsim

import (
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

var filterGuardRef = types.ManagedObjectReference{Type: "RvavFilterGuard", Value: "filter-guard"}

// NonBlockingFilters keeps the objects a property filter follows from
// waiting for the filter's lock. Call it before the simulator serves
// requests.
//
// vcsim's WaitForUpdatesEx holds a filter's lock while it locks, one by one,
// each object of the container view the filter traverses. A VM made in that
// view holds the view's lock while it waits for the filter's, to mark the
// filter for a new traversal, and a call holding a watched VM (a clone's
// source, or ReconfigVM_Task's VM) waits for the view's lock to register its
// task. With the sync's live watch open, two clones and a reconfigure then
// wait on each other for ever (rvav#206). Here the filter is marked from a
// goroutine of its own, so the view's lock is never held waiting for it.
func NonBlockingFilters(r *simulator.Registry) {
	r.Put(&filterGuard{mo.ExtensibleManagedObject{Self: filterGuardRef}})
	next := r.Handler
	r.Handler = func(ctx *simulator.Context, m *simulator.Method) (mo.Reference, types.BaseMethodFault) {
		var h mo.Reference
		if next != nil {
			var fault types.BaseMethodFault
			if h, fault = next(ctx, m); fault != nil {
				return nil, fault
			}
		}
		if m.Name == "CreateFilter" {
			m.This = filterGuardRef
		}
		return h, nil
	}
}

// filterGuard runs the CreateFilter calls NonBlockingFilters redirects to
// it. It opts out of vcsim's per-object lock, which would serialise them.
type filterGuard struct {
	mo.ExtensibleManagedObject
}

func (*filterGuard) Lock()   {}
func (*filterGuard) Unlock() {}

func (*filterGuard) CreateFilter(ctx *simulator.Context, req *types.CreateFilter) soap.HasFault {
	pc, ok := ctx.Session.Get(req.This).(*simulator.PropertyCollector)
	if !ok {
		return &methods.CreateFilterBody{Fault_: simulator.Fault("", &types.ManagedObjectNotFound{Obj: req.This})}
	}
	ctx.Caller = &req.This
	var res soap.HasFault
	ctx.WithLock(pc, func() {
		res = pc.CreateFilter(ctx, req)
		body, ok := res.(*methods.CreateFilterBody)
		if !ok || body.Res == nil {
			return
		}
		if f, ok := ctx.Session.Get(body.Res.Returnval).(*simulator.PropertyFilter); ok {
			// Replaces the filter among the registry's handlers, which are
			// keyed by reference.
			ctx.Map.AddHandler(liveFilter{f})
		}
	})
	return res
}

// liveFilter is a property filter as the registry's handlers see it.
type liveFilter struct {
	*simulator.PropertyFilter
}

func (f liveFilter) UpdateObject(ctx *simulator.Context, o mo.Reference, changes []types.PropertyChange) {
	ref := o.Reference()
	for _, set := range f.Spec.ObjectSet {
		if set.Obj == ref && len(set.SelectSet) != 0 {
			go f.PropertyFilter.UpdateObject(&simulator.Context{Map: ctx.Map}, ref, changes)
			return
		}
	}
}
