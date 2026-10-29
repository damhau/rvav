package vmprops

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/integrations/vsphere"
)

func vm(power string, cpu, mb int, disks []vsphere.Disk, nics []vsphere.NIC) json.RawMessage {
	return Build("vc01", vsphere.VM{Moref: "vm-1", InstanceUUID: "4219ab00-0000-4000-8000-000000000001", PowerState: power,
		GuestState: "unknown", CPUCount: cpu, MemoryMB: mb, Disks: disks, NICs: nics}, Ours{}, nil, 0)
}

func drift(t *testing.T, props json.RawMessage) []string {
	t.Helper()
	var p struct {
		Drift []struct {
			Label   string  `json:"label"`
			Rvav    *string `json:"rvav"`
			Vcenter *string `json:"vcenter"`
		} `json:"drift"`
	}
	if err := json.Unmarshal(props, &p); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range p.Drift {
		s := func(v *string) string {
			if v == nil {
				return "-"
			}
			return *v
		}
		out = append(out, d.Label+": "+s(d.Rvav)+" → "+s(d.Vcenter))
	}
	return out
}

func TestDrift(t *testing.T) {
	disks := []vsphere.Disk{{Label: "Hard disk 1", CapacityGB: 40}, {Label: "Hard disk 2", CapacityGB: 100}}
	nics := []vsphere.NIC{{Label: "Network adapter 1", Network: "dvpg-a"}}
	built, drifted := Settle(nil, vm("on", 2, 4096, disks, nics), All...)
	if drifted || len(drift(t, built)) != 0 {
		t.Fatalf("a VM as built drifts: %v", drift(t, built))
	}

	// Changed in vCenter: power, memory, a disk grown, one removed and one
	// added, the adapter moved and another added.
	now := vm("off", 2, 1536, []vsphere.Disk{{Label: "Hard disk 1", CapacityGB: 60}, {Label: "Hard disk 3", CapacityGB: 10}},
		[]vsphere.NIC{{Label: "Network adapter 1", Network: "dvpg-b"}, {Label: "Network adapter 2", Network: "dvpg-c"}})
	got, drifted := Compare(built, now)
	want := []string{"Power: on → off", "Memory: 4 GB → 1536 MB", "Hard disk 1: 40 GB → 60 GB", "Hard disk 2: 100 GB → -",
		"Hard disk 3: - → 10 GB", "Network adapter 1: dvpg-a → dvpg-b", "Network adapter 2: - → dvpg-c"}
	if !drifted || !slices.Equal(drift(t, got), want) {
		t.Fatalf("drift = %v, want %v", drift(t, got), want)
	}

	// An action settles what it changed only.
	settled, drifted := Settle(got, now, Power, Disks)
	want = []string{"Memory: 4 GB → 1536 MB", "Network adapter 1: dvpg-a → dvpg-b", "Network adapter 2: - → dvpg-c"}
	if !drifted || !slices.Equal(drift(t, settled), want) {
		t.Fatalf("after settling power and disks = %v, want %v", drift(t, settled), want)
	}

	// While rvav changes the VM, what was expected and flagged is kept.
	carried := Carry(settled, vm("on", 4, 8192, nil, nil))
	if !slices.Equal(drift(t, carried), want) {
		t.Fatalf("carried = %v", drift(t, carried))
	}

	// Changed back: nothing differs. A VM without an expected one is taken
	// as it is.
	if back, drifted := Compare(built, vm("on", 2, 4096, disks, nics)); drifted || len(drift(t, back)) != 0 {
		t.Fatalf("changed back = %v", drift(t, back))
	}
	if first, drifted := Compare(vm("on", 2, 4096, nil, nil), now); drifted {
		t.Fatalf("no expected VM = %v", drift(t, first))
	}
}
