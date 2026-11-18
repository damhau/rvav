package e2e

import (
	"github.com/vmware/govmomi/simulator"

	"github.com/damhau/rvav/internal/vcsim"
)

// Every vcsim a test serves, through simulator.Test or with
// RegisterEndpoints, runs its clones into a folder one at a time: a
// multi-VM blueprint's concurrent clones would otherwise crash the test
// binary (rvav#187).
func init() {
	simulator.RegisterEndpoint(func(_ *simulator.Service, r *simulator.Registry) { vcsim.SerialClones(r) })
}
