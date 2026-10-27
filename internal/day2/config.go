package day2

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/damhau/rvav/internal/api"
)

// Configuration actions: CPU and memory, disks, network adapters.

const (
	maxCPU      = 64
	maxMemoryGB = 512
	maxDiskGB   = 4096
)

func init() {
	register(&Action{
		Key: "resize", Name: "Resize", Group: GroupConfiguration, Types: []string{vmType},
		Form: func(t Target, _ Choices) *api.ActionForm {
			fields := []field{
				number("cpu_count", "vCPU", hotAddHelp("CPUs", t.VM.CpuHotAdd), 1, maxCPU, t.VM.CpuCount),
				number("memory_gb", "Memory (GB)", hotAddHelp("Memory", t.VM.MemoryHotAdd), 1, maxMemoryGB, t.VM.MemoryMb/1024),
			}
			if t.VM.PowerState == api.On {
				fields = append(fields, toggle("restart", "Restart the VM if needed",
					"Shut it down, resize it and power it on again when the change can't be made while it runs."))
			}
			return form("New size", fields...)
		},
		Validate: func(t Target, in map[string]any) []api.FieldError {
			cpu, mem := intInput(in, "cpu_count"), intInput(in, "memory_gb")
			if cpu == t.VM.CpuCount && mem*1024 == t.VM.MemoryMb {
				return invalid("cpu_count", "no_change", "This is the VM's current size.")
			}
			if reason := coldChange(t, cpu, mem); reason != "" && !boolInput(in, "restart") {
				return invalid("restart", "needs_power_off", "%s Allow the restart, or power the VM off first.", reason)
			}
			return nil
		},
		Ops: func(t Target, in map[string]any) []Op {
			cpu, mem := intInput(in, "cpu_count"), intInput(in, "memory_gb")
			set := vcenter("vsphere.reconfigure", fmt.Sprintf("Set %d vCPU and %d GB", cpu, mem), "reconfigure",
				map[string]any{"cpu_count": cpu, "memory_mb": mem * 1024})
			set.Detail = fmt.Sprintf("Was %d vCPU and %d GB", t.VM.CpuCount, t.VM.MemoryMb/1024)
			if coldChange(t, cpu, mem) == "" {
				return []Op{set}
			}
			stop := vcenter("vsphere.shutdown_guest", "Shut down the guest OS of "+t.ResourceName, "shutdown_guest", nil)
			if t.VM.GuestState != api.VmPropertiesGuestStateRunning {
				stop = vcenter("vsphere.power_off", "Power off "+t.ResourceName, "power_off", nil)
			}
			return []Op{stop, set, vcenter("vsphere.power_on", "Power on "+t.ResourceName, "power_on", nil)}
		},
	})
	register(&Action{
		Key: "disk_add", Name: "Add a disk", Group: GroupConfiguration, Types: []string{vmType},
		Form: func(Target, Choices) *api.ActionForm {
			return form("New disk", number("size_gb", "Size (GB)", "Thin provisioned, on the VM's datastore.", 1, maxDiskGB, 50))
		},
		Ops: func(_ Target, in map[string]any) []Op {
			size := intInput(in, "size_gb")
			return []Op{vcenter("vsphere.disk_add", fmt.Sprintf("Add a %d GB disk", size), "add_disk", map[string]any{"size_gb": size})}
		},
	})
	register(&Action{
		Key: "disk_extend", Name: "Extend a disk", Group: GroupConfiguration, Types: []string{vmType},
		Unavailable: func(t Target, _ Choices) string {
			if len(t.VM.Disks) == 0 {
				return "The VM has no disk."
			}
			if t.VM.SnapshotCount != nil && *t.VM.SnapshotCount > 0 {
				return "Disks of a VM with snapshots can't be extended. Delete its snapshots first."
			}
			return ""
		},
		Form: func(t Target, _ Choices) *api.ActionForm {
			opts := make([]api.Option, len(t.VM.Disks))
			for i, d := range t.VM.Disks {
				meta := strconv.Itoa(d.CapacityGb) + " GB"
				opts[i] = api.Option{Value: d.Label, Label: d.Label, Meta: &meta}
			}
			first := t.VM.Disks[0]
			return form("Disk", choice("disk", "Disk", opts, first.Label),
				number("size_gb", "New size (GB)", "Disks only grow. Grow the file system in the guest to use the space.", 1, maxDiskGB, first.CapacityGb))
		},
		Validate: func(t Target, in map[string]any) []api.FieldError {
			d := disk(t, strInput(in, "disk"))
			if size := intInput(in, "size_gb"); d != nil && size <= d.CapacityGb {
				return invalid("size_gb", "too_small", "%s is %d GB; choose a larger size.", d.Label, d.CapacityGb)
			}
			return nil
		},
		Ops: func(t Target, in map[string]any) []Op {
			label, size := strInput(in, "disk"), intInput(in, "size_gb")
			op := vcenter("vsphere.disk_extend", fmt.Sprintf("Extend %s to %d GB", label, size), "extend_disk",
				map[string]any{"disk": label, "size_gb": size})
			if d := disk(t, label); d != nil {
				op.Detail = fmt.Sprintf("Was %d GB", d.CapacityGb)
			}
			return []Op{op}
		},
	})
	register(&Action{
		Key: "nic_add", Name: "Add a network adapter", Group: GroupConfiguration, Types: []string{vmType},
		Unavailable: func(_ Target, c Choices) string {
			if len(c.Networks) == 0 {
				return "The project's placement rules allow no network for this VM's cluster."
			}
			return ""
		},
		Form: func(_ Target, c Choices) *api.ActionForm {
			return form("Network adapter", choice("network", "Network", networkOptions(c), ""))
		},
		Ops: func(_ Target, in map[string]any) []Op {
			network := strInput(in, "network")
			return []Op{vcenter("vsphere.nic_add", "Add a network adapter on "+network, "add_nic", map[string]any{"network": network})}
		},
	})
	register(&Action{
		Key: "nic_move", Name: "Change a network adapter's network", Group: GroupConfiguration, Types: []string{vmType},
		Unavailable: func(t Target, c Choices) string {
			if reason := extraNICs(t); reason != "" {
				return reason
			}
			if len(c.Networks) < 2 {
				return "The project's placement rules allow only one network for this VM's cluster."
			}
			return ""
		},
		Form: func(t Target, c Choices) *api.ActionForm {
			return form("Network adapter", choice("nic", "Adapter", nicOptions(t), ""), choice("network", "Network", networkOptions(c), ""))
		},
		Validate: func(t Target, in map[string]any) []api.FieldError {
			if n := nic(t, strInput(in, "nic")); n != nil && n.Network == strInput(in, "network") {
				return invalid("network", "no_change", "%s is already on %s.", strInput(in, "nic"), n.Network)
			}
			return nil
		},
		Ops: func(t Target, in map[string]any) []Op {
			label, network := strInput(in, "nic"), strInput(in, "network")
			op := vcenter("vsphere.nic_move", "Move "+label+" to "+network, "set_nic_network", map[string]any{"nic": label, "network": network})
			if n := nic(t, label); n != nil {
				op.Detail = "Was on " + n.Network
			}
			return []Op{op}
		},
	})
	register(&Action{
		Key: "nic_remove", Name: "Remove a network adapter", Group: GroupConfiguration, Types: []string{vmType}, Destructive: true,
		Confirm:     "The guest loses the connection on this adapter at once.",
		Unavailable: func(t Target, _ Choices) string { return extraNICs(t) },
		Form: func(t Target, _ Choices) *api.ActionForm {
			return form("Network adapter", choice("nic", "Adapter", nicOptions(t), ""))
		},
		Ops: func(_ Target, in map[string]any) []Op {
			label := strInput(in, "nic")
			return []Op{vcenter("vsphere.nic_remove", "Remove "+label, "remove_nic", map[string]any{"nic": label})}
		},
	})
}

func hotAddHelp(what string, hotAdd *bool) string {
	if hotAdd != nil && *hotAdd {
		return what + " can be added while the VM runs."
	}
	return what + " can be changed only while the VM is powered off."
}

// coldChange says why the new size needs the VM powered off ("" when it
// can be applied now).
func coldChange(t Target, cpu, memGB int) string {
	if t.VM.PowerState != api.On {
		return ""
	}
	hot := func(b *bool) bool { return b != nil && *b }
	switch {
	case cpu < t.VM.CpuCount:
		return "CPUs can't be removed while the VM runs."
	case cpu > t.VM.CpuCount && !hot(t.VM.CpuHotAdd):
		return "CPU hot add is off on this VM."
	case memGB*1024 < t.VM.MemoryMb:
		return "Memory can't be removed while the VM runs."
	case memGB*1024 > t.VM.MemoryMb && !hot(t.VM.MemoryHotAdd):
		return "Memory hot add is off on this VM."
	}
	return ""
}

type vmDisk = struct {
	CapacityGb int     `json:"capacity_gb"`
	Datastore  *string `json:"datastore,omitempty"`
	Label      string  `json:"label"`
}

func disk(t Target, label string) *vmDisk {
	for i := range t.VM.Disks {
		if t.VM.Disks[i].Label == label {
			return &t.VM.Disks[i]
		}
	}
	return nil
}

type vmNIC = struct {
	IpAddresses *[]string `json:"ip_addresses,omitempty"`
	Label       *string   `json:"label,omitempty"`
	Mac         *string   `json:"mac,omitempty"`
	Network     string    `json:"network"`
}

func nicLabel(n vmNIC, i int) string {
	if n.Label != nil && *n.Label != "" {
		return *n.Label
	}
	return "Network adapter " + strconv.Itoa(i+1)
}

func nic(t Target, label string) *vmNIC {
	for i := range t.VM.Nics {
		if nicLabel(t.VM.Nics[i], i) == label {
			return &t.VM.Nics[i]
		}
	}
	return nil
}

// The first adapter carries the address and DNS records rvav manages, so
// only the others can be moved or removed.
func extraNICs(t Target) string {
	if len(t.VM.Nics) < 2 {
		return "The VM has only its first network adapter, which carries its address and DNS records."
	}
	return ""
}

func nicOptions(t Target) []api.Option {
	var out []api.Option
	for i, n := range t.VM.Nics {
		if i == 0 {
			continue
		}
		meta := n.Network
		out = append(out, api.Option{Value: nicLabel(n, i), Label: nicLabel(n, i), Meta: &meta})
	}
	return out
}

func networkOptions(c Choices) []api.Option {
	nets := slices.Clone(c.Networks)
	slices.Sort(nets)
	out := make([]api.Option, len(nets))
	for i, n := range nets {
		out[i] = api.Option{Value: n, Label: n}
	}
	return out
}
