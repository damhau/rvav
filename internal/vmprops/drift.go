package vmprops

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
)

// Drift (S13.2): rvav keeps each VM as its own actions left it, the
// expected VM, and flags how the VM in vCenter differs from it: its power,
// its size, its disks, its adapters' networks and its vSphere tags. An
// action settles what it changed only, so that a change made outside rvav
// stays flagged until it is changed back or accepted.
//
// Tags are read by the full inventory sync only: properties without them
// keep the last ones read, and a VM's tags are taken as expected the first
// time they are read (X1.3).

// Aspects of a VM an action may settle.
const (
	Power    = "power"
	Size     = "size"
	Disks    = "disks"
	Networks = "networks"
	Tags     = "tags"
)

// All is every aspect: a VM rvav builds or takes as it is. Hardware is
// what a snapshot holds: all but the tags.
var (
	All      = []string{Power, Size, Disks, Networks, Tags}
	Hardware = []string{Power, Size, Disks, Networks}
)

// Expected is a VM as rvav last left it.
type Expected struct {
	PowerState string   `json:"power_state"`
	CPUCount   int      `json:"cpu_count"`
	MemoryMB   int      `json:"memory_mb"`
	Disks      []Device `json:"disks"`
	NICs       []Device `json:"nics"`
	// Tags are nil until the VM's tags were read.
	Tags *[]string `json:"tags,omitempty"`
}

// Device is a disk (its size) or an adapter (its network), by label.
type Device struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func expectedOf(vm api.VmProperties) Expected {
	e := Expected{PowerState: string(vm.PowerState), CPUCount: vm.CpuCount, MemoryMB: vm.MemoryMb}
	for _, d := range vm.Disks {
		e.Disks = append(e.Disks, Device{Label: d.Label, Value: gb(d.CapacityGb)})
	}
	for i, n := range vm.Nics {
		e.NICs = append(e.NICs, Device{Label: nicLabel(n.Label, i), Value: n.Network})
	}
	if vm.Tags != nil {
		e.Tags = present.Ptr(slices.Clone(*vm.Tags))
	}
	return e
}

func nicLabel(label *string, i int) string {
	if label != nil && *label != "" {
		return *label
	}
	return fmt.Sprintf("Network adapter %d", i+1)
}

// Settle records, in properties just built from vCenter, the aspects an
// action of rvav's changed as they are now, keeps the others as rvav last
// left them (from old), and says how the VM differs from them.
func Settle(old, props json.RawMessage, aspects ...string) (json.RawMessage, bool) {
	p, prev := decode(props), decode(old)
	if p.VM.Tags == nil {
		p.VM.Tags = prev.VM.Tags
	}
	now := expectedOf(p.VM)
	e, ok := expectedIn(prev)
	if !ok {
		e = now
	}
	if e.Tags == nil {
		e.Tags = now.Tags
	}
	for _, a := range aspects {
		switch a {
		case Power:
			e.PowerState = now.PowerState
		case Size:
			e.CPUCount, e.MemoryMB = now.CPUCount, now.MemoryMB
		case Disks:
			e.Disks = now.Disks
		case Networks:
			e.NICs = now.NICs
		case Tags:
			if now.Tags != nil {
				e.Tags = now.Tags
			}
		}
	}
	return encode(p, e)
}

// Compare keeps, in properties just built from vCenter, the VM as rvav
// last left it (from old; as it is now for a VM rvav had none of), and
// says how the VM differs from it.
func Compare(old, props json.RawMessage) (json.RawMessage, bool) {
	return Settle(old, props)
}

// Carry keeps, in properties just built from vCenter, the expected VM and
// the differences of old as they were: while rvav changes the VM, what
// changes isn't drift.
func Carry(old, props json.RawMessage) json.RawMessage {
	p, prev := decode(props), decode(old)
	if p.VM.Tags == nil {
		p.VM.Tags = prev.VM.Tags
	}
	p.Expected, p.Drift = prev.Expected, prev.Drift
	b, _ := json.Marshal(p)
	return b
}

func decode(raw json.RawMessage) present.VMResourceProps {
	var p present.VMResourceProps
	_ = json.Unmarshal(raw, &p)
	return p
}

func expectedIn(p present.VMResourceProps) (Expected, bool) {
	var e Expected
	if len(p.Expected) == 0 || json.Unmarshal(p.Expected, &e) != nil {
		return e, false
	}
	return e, true
}

func encode(p present.VMResourceProps, e Expected) (json.RawMessage, bool) {
	p.Expected, _ = json.Marshal(e)
	p.Drift = diff(e, expectedOf(p.VM))
	b, _ := json.Marshal(p)
	return b, len(p.Drift) > 0
}

// diff lists how now differs from what rvav expects.
func diff(want, now Expected) []api.VmDrift {
	var out []api.VmDrift
	add := func(prop api.VmDriftProperty, label, rvav, vcenter string) {
		d := api.VmDrift{Property: prop, Label: label}
		if rvav != "" {
			d.Rvav = &rvav
		}
		if vcenter != "" {
			d.Vcenter = &vcenter
		}
		out = append(out, d)
	}
	if want.PowerState != now.PowerState {
		add(api.PowerState, "Power", want.PowerState, now.PowerState)
	}
	if want.CPUCount != now.CPUCount {
		add(api.CpuCount, "vCPU", strconv.Itoa(want.CPUCount), strconv.Itoa(now.CPUCount))
	}
	if want.MemoryMB != now.MemoryMB {
		add(api.MemoryMb, "Memory", memory(want.MemoryMB), memory(now.MemoryMB))
	}
	devices := func(prop api.VmDriftProperty, want, now []Device) {
		for _, w := range want {
			i := slices.IndexFunc(now, func(d Device) bool { return d.Label == w.Label })
			switch {
			case i < 0:
				add(prop, w.Label, w.Value, "")
			case now[i].Value != w.Value:
				add(prop, w.Label, w.Value, now[i].Value)
			}
		}
		for _, n := range now {
			if !slices.ContainsFunc(want, func(d Device) bool { return d.Label == n.Label }) {
				add(prop, n.Label, "", n.Value)
			}
		}
	}
	devices(api.Disk, want.Disks, now.Disks)
	devices(api.Nic, want.NICs, now.NICs)
	if want.Tags != nil && now.Tags != nil {
		w, n := byCategory(*want.Tags), byCategory(*now.Tags)
		for _, c := range sortedKeys(w, n) {
			if !slices.Equal(w[c], n[c]) {
				add(api.Tag, c+" tags", strings.Join(w[c], ", "), strings.Join(n[c], ", "))
			}
		}
	}
	return out
}

// byCategory groups category/tag names by category, sorted.
func byCategory(tags []string) map[string][]string {
	out := map[string][]string{}
	for _, t := range tags {
		c, name, _ := strings.Cut(t, "/")
		out[c] = append(out[c], name)
	}
	for _, l := range out {
		slices.Sort(l)
	}
	return out
}

func sortedKeys(a, b map[string][]string) []string {
	var keys []string
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

func gb(n int) string { return strconv.Itoa(n) + " GB" }

func memory(mb int) string {
	if mb%1024 == 0 {
		return gb(mb / 1024)
	}
	return strconv.Itoa(mb) + " MB"
}
