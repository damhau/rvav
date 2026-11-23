package e2e

import (
	"github.com/vmware/govmomi/simulator"

	"github.com/damhau/rvav/internal/vcsim"
)

// Every vcsim a test serves, through simulator.Test or with
// RegisterEndpoints, runs its clones into a folder one at a time: a
// multi-VM blueprint's concurrent clones would otherwise crash the test
// binary (rvav#187). Its property filters don't block the VMs it makes,
// which would otherwise hang with the sync's live watch open (rvav#206).
func init() {
	simulator.RegisterEndpoint(func(_ *simulator.Service, r *simulator.Registry) {
		vcsim.SerialClones(r)
		vcsim.NonBlockingFilters(r)
	})
}
