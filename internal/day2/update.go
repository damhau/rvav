package day2

import (
	"slices"
	"strconv"

	"github.com/damhau/rvav/internal/api"
)

// KeyUpdate is the update of a deployment to its item's version and new
// inputs (ADR-0042). It is offered as an action so that day-2 policies
// decide who may update, but it is not one: its own endpoint plans it from
// the catalog form, and its request is of kind update.
const KeyUpdate = "update"

func init() {
	register(&Action{Key: KeyUpdate, Name: "Update", Group: GroupLifecycle, Types: []string{DeploymentType}, Direct: true, OnFailed: true,
		Applies: func(t Target) bool { return t.ItemType == string(api.VmBlueprint) }})
}

// Suspended says why a VM's hardware can't change now ("" when it can).
func Suspended(t Target) string { return suspended(t) }

// ColdChange says why resizing the VM to this size needs it powered off
// ("" when the change can be made while it runs).
func ColdChange(t Target, cpu, memGB int) string { return coldChange(t, cpu, memGB) }

// Disk is the VM's disk with this label, or nil.
func Disk(t Target, label string) *api.VmDisk { return disk(t, label) }

// NIC is the VM's network adapter with this label, or nil.
func NIC(t Target, label string) *api.VmNic { return nic(t, label) }

// DiskLabel is the label vCenter gives the n-th disk (1 for the first).
func DiskLabel(n int) string { return "Hard disk " + strconv.Itoa(n) }

// NICLabel is the label vCenter gives the n-th network adapter (1 for the first).
func NICLabel(n int) string { return "Network adapter " + strconv.Itoa(n) }

// HasSnapshots is true when the VM has snapshots, whose disks can't change.
func HasSnapshots(t Target) bool { return t.VM.SnapshotCount != nil && *t.VM.SnapshotCount > 0 }

// OwnResourceIDs are the IDs of a VM's own resources of a type, as
// OwnResources finds them.
func OwnResourceIDs(vm Resource, all []Resource, typ string) []string {
	var ids []string
	for _, r := range OwnResources(vm, all) {
		if r.Type == typ && !slices.Contains(ids, r.ID) {
			ids = append(ids, r.ID)
		}
	}
	return ids
}
