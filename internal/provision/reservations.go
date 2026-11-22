package provision

import (
	"context"
	"slices"

	"github.com/damhau/rvav/internal/store"
)

// Reservations are what the plan's VMs hold on the clusters they go to
// until they exist (ADR-0036). VMs of plans made before a vCenter was
// named in them hold nothing.
func (p Plan) Reservations() []store.Reservation {
	vms := slices.Clone(p.VMs)
	for _, c := range p.Composite {
		vms = append(vms, c.VMs...)
	}
	var out []store.Reservation
	for _, vm := range vms {
		if vm.VCenter == "" || vm.Cluster == "" {
			continue
		}
		out = append(out, store.Reservation{VMName: vm.Name, DeploymentID: vm.DeploymentID, VCenter: vm.VCenter,
			Cluster: vm.Cluster, ResourcePool: vm.ResourcePool, MemoryMB: int64(vm.MemoryGB) * 1024, VCPU: vm.VCPU})
	}
	return out
}

// SweepReservations deletes the placement reservations that no longer
// count: their VMs exist, their requests ended, or they expired.
func (a *Activities) SweepReservations(ctx context.Context) (int64, error) {
	if a.Store == nil {
		return 0, nil
	}
	n, err := a.Store.SweepReservations(ctx)
	if n > 0 {
		a.Log.Info("swept placement reservations", "rows", n)
	}
	return n, err
}
