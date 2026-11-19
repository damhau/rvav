package vmprops

import (
	"encoding/json"
	"maps"
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

// TestTagDrift: a VM's tags are taken as expected the first time they are
// read; properties built without them keep the last read; a change in
// vCenter is flagged per category, and an action that set the tags takes
// them as they are (X1.3).
func TestTagDrift(t *testing.T) {
	// No tags: not read.
	tagged := func(tags ...string) json.RawMessage {
		return Build("vc01", vsphere.VM{Moref: "vm-1", InstanceUUID: "4219ab00-0000-4000-8000-000000000001", PowerState: "on",
			GuestState: "unknown", CPUCount: 2, MemoryMB: 4096, Tags: tags}, Ours{}, nil, 0)
	}
	tagsOf := func(props json.RawMessage) []string {
		var p struct {
			VM struct {
				Tags *[]string `json:"tags"`
			} `json:"vm"`
		}
		_ = json.Unmarshal(props, &p)
		if p.VM.Tags == nil {
			return nil
		}
		return *p.VM.Tags
	}

	built, _ := Settle(nil, tagged(), All...)
	first, drifted := Compare(built, tagged("app/shop"))
	if drifted {
		t.Fatalf("tags read the first time drift: %v", drift(t, first))
	}
	unread, drifted := Compare(first, tagged())
	if drifted || !slices.Equal(tagsOf(unread), []string{"app/shop"}) {
		t.Fatalf("properties without tags = %v, %v", tagsOf(unread), drift(t, unread))
	}

	changed, drifted := Compare(unread, tagged("app/shop", "backup/daily", "backup/weekly"))
	want := []string{"backup tags: - → daily, weekly"}
	if !drifted || !slices.Equal(drift(t, changed), want) {
		t.Fatalf("drift = %v, want %v", drift(t, changed), want)
	}
	if gone, _ := Compare(changed, tagged("backup/daily", "backup/weekly")); !slices.Equal(drift(t, gone),
		[]string{"app tags: shop → -", "backup tags: - → daily, weekly"}) {
		t.Fatalf("a category's tags removed = %v", drift(t, gone))
	}
	if kept, _ := Settle(changed, tagged(), Hardware...); !slices.Equal(drift(t, kept), want) {
		t.Fatalf("settling the hardware = %v", drift(t, kept))
	}
	if settled, drifted := Settle(changed, tagged("app/shop", "backup/daily", "backup/weekly"), Tags); drifted {
		t.Fatalf("after setting the tags = %v", drift(t, settled))
	}
}

// What rvav built a VM with comes back from its stored properties, so the
// inventory sync keeps it: custom properties, which adapters are on DHCP
// (X1.1), and which disks (X1.6) and adapters (#192) are its blueprint's;
// a template's adapter after its first, and its disk after its first, stay
// unknown.
func TestOursRoundTrip(t *testing.T) {
	v := vsphere.VM{Moref: "vm-1", NICs: []vsphere.NIC{{Key: 4000, Network: "a"}, {Key: 4001, Network: "b"}, {Key: 4002, Network: "c"}},
		BaseDisks: 2, Disks: []vsphere.Disk{{Key: 2000}, {Key: 2001}, {Key: 2002}, {Key: 2003}}}
	dhcp := AdapterDHCP(v, false, []bool{true})
	ours := Ours{Template: "tpl", Properties: map[string]string{"app": "shop"}, DHCPAdapters: dhcp, BlueprintDisks: BuiltDisks(v, 2),
		BlueprintNICs: BuiltNICs(v, []string{"backup"})}
	back := OursOf(Build("vc01", v, ours, nil, 0))
	if back.Properties["app"] != "shop" || len(back.DHCPAdapters) != 2 || back.DHCPAdapters["Network adapter 1"] ||
		!back.DHCPAdapters["Network adapter 3"] {
		t.Fatalf("ours = %+v", back)
	}
	if want := map[int32]int{2000: 0, 2002: 1, 2003: 2}; !maps.Equal(back.BlueprintDisks, want) {
		t.Fatalf("blueprint disks = %v, want %v", back.BlueprintDisks, want)
	}
	if want := map[int32]string{4000: FirstNIC, 4002: "backup"}; !maps.Equal(back.BlueprintNICs, want) {
		t.Fatalf("blueprint adapters = %v, want %v", back.BlueprintNICs, want)
	}
	// Before rvav kept its template's disks on it: the last ones it added.
	v.BaseDisks = 0
	if got := BuiltDisks(v, 1); !maps.Equal(got, map[int32]int{2000: 0, 2003: 1}) {
		t.Fatalf("without the template's count = %v", got)
	}
	if _, ok := back.DHCPAdapters["Network adapter 2"]; ok {
		t.Fatal("the template's second adapter is known")
	}
	if !slices.Equal([]byte(Build("vc01", v, back, nil, 0)), []byte(Build("vc01", v, ours, nil, 0))) {
		t.Fatal("rebuilt from what was stored, the properties differ")
	}
}
