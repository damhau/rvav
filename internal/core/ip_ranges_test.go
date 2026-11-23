package core

import (
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// TestRangeInUse: a range whose gateway changes while VMs hold its
// addresses is refused on the gateway, saying how many they hold.
func TestRangeInUse(t *testing.T) {
	old := store.IPRange{ID: "ipr_a", Name: "Lab", VCenter: "vc01", Network: "dvpg-lab", CIDR: "10.0.0.0/24",
		StartAddress: "10.0.0.10", EndAddress: "10.0.0.20", Gateway: "10.0.0.1"}
	moved := old
	moved.Gateway = "10.0.0.2"
	for given, want := range map[int]string{
		1: "A VM holds 1 address from this range: its vCenter, network, subnet and gateway can't change.",
		2: "VMs hold 2 addresses from this range: its vCenter, network, subnet and gateway can't change.",
	} {
		errs := checkIPRange(moved, moved.Range(), &old, nil, []string{"10.0.0.10", "10.0.0.11"}[:given], nil)
		if !slices.ContainsFunc(errs, func(e api.FieldError) bool { return e.Field == "/gateway" && e.Message == want }) {
			t.Errorf("%d held: %+v", given, errs)
		}
	}
}
