// Package vmprops builds the properties stored on vsphere.vm resources from
// what vCenter reports, so the workflow and the sync loop write the same shape.
package vmprops

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/present"
)

// Usage is optional quick stats.
type Usage struct {
	CPUPct    *float32
	MemoryPct *float32
	SampledAt time.Time
}

// Ours is what rvav knows of a VM that vCenter does not always: its FQDN,
// its template and its blueprint component (ADR-0024).
type Ours struct {
	FQDN      string `json:"fqdn"`
	Template  string `json:"template"`
	Component string `json:"component"`
	// DHCP: its first adapter's address comes from DHCP (ADR-0039).
	DHCP bool `json:"dhcp"`
	// Properties are the custom properties it was built with (ADR-0038).
	Properties map[string]string `json:"custom_properties,omitempty"`
	// DHCPAdapters say, by adapter label, whether rvav left an adapter's
	// address to DHCP; an adapter it doesn't know about is missing.
	DHCPAdapters map[string]bool `json:"-"`
	// BlueprintDisks say, by device key, which of its blueprint's disks a
	// disk is (ADR-0042); a disk rvav didn't make from it is missing.
	BlueprintDisks map[int32]int `json:"-"`
	// BlueprintDiskKeys say, by device key, the key its blueprint gives a
	// disk BlueprintDisks marks, if any (ADR-0052).
	BlueprintDiskKeys map[int32]string `json:"-"`
	// BlueprintNICs say, by device key, which of its blueprint's adapters
	// an adapter is: FirstNIC, or the key of one added (ADR-0042).
	BlueprintNICs map[int32]string `json:"-"`
}

// FirstNIC marks a VM's first adapter, its template's, as the blueprint's.
const FirstNIC = "nic1"

// BuiltNICs are the blueprint adapters of a VM rvav just built: its first
// adapter, and the adapters it added, the last ones, by their keys.
func BuiltNICs(vm vsphere.VM, keys []string) map[int32]string {
	out := map[int32]string{}
	for i, n := range vm.NICs {
		switch j := i - (len(vm.NICs) - len(keys)); {
		case n.Key == 0:
		case i == 0:
			out[n.Key] = FirstNIC
		case j >= 0:
			out[n.Key] = keys[j]
		}
	}
	return out
}

// BuiltDisks are the blueprint disks of a VM rvav just built: its first
// disk, and the extra disks it added after its template's.
func BuiltDisks(vm vsphere.VM, extra int) map[int32]int {
	base := vm.BaseDisks
	if base == 0 {
		base = len(vm.Disks) - extra
	}
	out := map[int32]int{}
	for i, d := range vm.Disks {
		switch j := i - base + 1; {
		case d.Key == 0:
		case i == 0 && base > 0:
			out[d.Key] = 0
		case j >= 1 && j <= extra:
			out[d.Key] = j
		}
	}
	return out
}

// BuiltDiskKeys are the keys the blueprint gives, in order, to the extra
// disks marks names (BuiltDisks).
func BuiltDiskKeys(marks map[int32]int, keys []string) map[int32]string {
	out := map[int32]string{}
	for dev, j := range marks {
		if j >= 1 && j <= len(keys) && keys[j-1] != "" {
			out[dev] = keys[j-1]
		}
	}
	return out
}

// DiskMark is which of its new version's disks a disk an update kept is,
// recorded once the update succeeds (ADR-0052).
type DiskMark struct {
	// Device is vCenter's device key.
	Device int32  `json:"device"`
	Disk   int    `json:"disk"`
	Key    string `json:"key,omitempty"`
}

// Remark records marks on the disks of a VM's stored properties, leaving
// the rest as it is.
func Remark(props json.RawMessage, marks []DiskMark) (json.RawMessage, error) {
	var top, vm map[string]json.RawMessage
	var disks []map[string]json.RawMessage
	if err := json.Unmarshal(props, &top); err != nil {
		return nil, err
	}
	if len(top["vm"]) == 0 {
		return props, nil
	}
	if err := json.Unmarshal(top["vm"], &vm); err != nil {
		return nil, err
	}
	if len(vm["disks"]) == 0 {
		return props, nil
	}
	if err := json.Unmarshal(vm["disks"], &disks); err != nil {
		return nil, err
	}
	for _, d := range disks {
		var dev int32
		if json.Unmarshal(d["key"], &dev) != nil {
			continue
		}
		for _, m := range marks {
			if m.Device != dev {
				continue
			}
			d["blueprint_disk"], _ = json.Marshal(m.Disk)
			delete(d, "blueprint_disk_key")
			if m.Key != "" {
				d["blueprint_disk_key"], _ = json.Marshal(m.Key)
			}
		}
	}
	var err error
	if vm["disks"], err = json.Marshal(disks); err != nil {
		return nil, err
	}
	if top["vm"], err = json.Marshal(vm); err != nil {
		return nil, err
	}
	return json.Marshal(top)
}

// AdapterDHCP says which of a VM rvav just built has its address from
// DHCP: its first adapter as first says, and the adapters it added, the
// last ones, as added says. A template's other adapters are left out.
func AdapterDHCP(vm vsphere.VM, first bool, added []bool) map[string]bool {
	out := map[string]bool{}
	for i, n := range vm.NICs {
		switch j := i - (len(vm.NICs) - len(added)); {
		case j >= 0 && i > 0:
			out[nicLabel(&n.Label, i)] = added[j]
		case i == 0:
			out[nicLabel(&n.Label, i)] = first
		}
	}
	return out
}

// OursOf reads them back from stored properties.
func OursOf(props json.RawMessage) Ours {
	var p struct {
		VM struct {
			Ours
			Nics []struct {
				Label        string  `json:"label"`
				DHCP         *bool   `json:"dhcp"`
				Key          *int32  `json:"key"`
				BlueprintNIC *string `json:"blueprint_nic"`
			} `json:"nics"`
			Disks []struct {
				Key              *int32  `json:"key"`
				BlueprintDisk    *int    `json:"blueprint_disk"`
				BlueprintDiskKey *string `json:"blueprint_disk_key"`
			} `json:"disks"`
		} `json:"vm"`
	}
	_ = json.Unmarshal(props, &p)
	out := p.VM.Ours
	for _, d := range p.VM.Disks {
		if d.Key != nil && d.BlueprintDisk != nil {
			if out.BlueprintDisks == nil {
				out.BlueprintDisks = map[int32]int{}
			}
			out.BlueprintDisks[*d.Key] = *d.BlueprintDisk
		}
		if d.Key != nil && d.BlueprintDiskKey != nil {
			if out.BlueprintDiskKeys == nil {
				out.BlueprintDiskKeys = map[int32]string{}
			}
			out.BlueprintDiskKeys[*d.Key] = *d.BlueprintDiskKey
		}
	}
	for _, n := range p.VM.Nics {
		if n.DHCP != nil {
			if out.DHCPAdapters == nil {
				out.DHCPAdapters = map[string]bool{}
			}
			out.DHCPAdapters[n.Label] = *n.DHCP
		}
		if n.Key != nil && n.BlueprintNIC != nil {
			if out.BlueprintNICs == nil {
				out.BlueprintNICs = map[int32]string{}
			}
			out.BlueprintNICs[*n.Key] = *n.BlueprintNIC
		}
	}
	return out
}

// UsageOf is the quick stats stored with a VM, nil when there are none.
func UsageOf(props json.RawMessage) *Usage {
	var p struct {
		VM struct {
			Usage *struct {
				CPUPct    *float32   `json:"cpu_pct"`
				MemoryPct *float32   `json:"memory_pct"`
				SampledAt *time.Time `json:"sampled_at"`
			} `json:"usage"`
		} `json:"vm"`
	}
	if json.Unmarshal(props, &p) != nil || p.VM.Usage == nil {
		return nil
	}
	u := &Usage{CPUPct: p.VM.Usage.CPUPct, MemoryPct: p.VM.Usage.MemoryPct}
	if p.VM.Usage.SampledAt != nil {
		u.SampledAt = *p.VM.Usage.SampledAt
	}
	return u
}

// VCenterOf is the vCenter a VM's stored properties say it is in.
func VCenterOf(props json.RawMessage) string {
	var p struct {
		VM struct {
			VCenter string `json:"vcenter"`
		} `json:"vm"`
	}
	_ = json.Unmarshal(props, &p)
	return p.VM.VCenter
}

// Build returns the resource properties for a VM.
func Build(vcenter string, vm vsphere.VM, ours Ours, usage *Usage, snapshots int) json.RawMessage {
	p := api.VmProperties{
		Moref:      vm.Moref,
		Vcenter:    vcenter,
		PowerState: api.VmPowerState(vm.PowerState),
		GuestState: api.VmPropertiesGuestState(vm.GuestState),
		CpuCount:   vm.CPUCount,
		MemoryMb:   vm.MemoryMB,
	}
	if u, err := uuid.Parse(vm.InstanceUUID); err == nil {
		p.InstanceUuid = openapi_types.UUID(u)
	}
	set := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	p.GuestOs, p.Cluster, p.Host, p.Folder = set(vm.GuestOS), set(vm.Cluster), set(vm.Host), set(vm.Folder)
	p.ResourcePool = set(vm.ResourcePool)
	p.Fqdn, p.Template, p.Component = set(ours.FQDN), set(ours.Template), set(ours.Component)
	if len(ours.Properties) > 0 {
		props := maps.Clone(ours.Properties)
		p.CustomProperties = &props
	}
	if ours.DHCP {
		p.Dhcp = &ours.DHCP
	}
	if len(vm.Snapshots) > snapshots {
		snapshots = len(vm.Snapshots)
	}
	p.SnapshotCount = present.Ptr(snapshots)
	p.CpuHotAdd, p.MemoryHotAdd, p.Iso = present.Ptr(vm.CPUHotAdd), present.Ptr(vm.MemoryHotAdd), set(vm.ISO)
	if vm.Tags != nil {
		p.Tags = present.Ptr(slices.Clone(vm.Tags))
	}
	if len(vm.Snapshots) > 0 {
		list := make([]struct {
			CreatedAt   time.Time `json:"created_at"`
			Description *string   `json:"description,omitempty"`
			Name        string    `json:"name"`
		}, len(vm.Snapshots))
		for i, s := range vm.Snapshots {
			list[i].Name, list[i].Description, list[i].CreatedAt = s.Name, set(s.Description), s.Created
		}
		p.Snapshots = &list
	}
	p.Disks = make([]api.VmDisk, len(vm.Disks))
	for i, d := range vm.Disks {
		label := d.Label
		if label == "" {
			label = "Hard disk " + strconv.Itoa(i+1)
		}
		p.Disks[i].CapacityGb, p.Disks[i].Datastore, p.Disks[i].File, p.Disks[i].Label = d.CapacityGB, set(d.Datastore), set(d.File), label
		if d.Provisioning != "" {
			p.Disks[i].Provisioning = present.Ptr(api.DiskProvisioning(d.Provisioning))
		}
		if d.Linked {
			p.Disks[i].Linked = present.Ptr(true)
		}
		if d.Key != 0 {
			p.Disks[i].Key = present.Ptr(int(d.Key))
			if j, ok := ours.BlueprintDisks[d.Key]; ok {
				p.Disks[i].BlueprintDisk = present.Ptr(j)
			}
			if k, ok := ours.BlueprintDiskKeys[d.Key]; ok {
				p.Disks[i].BlueprintDiskKey = &k
			}
		}
	}
	p.Nics = make([]api.VmNic, len(vm.NICs))
	for i, n := range vm.NICs {
		label := nicLabel(&n.Label, i)
		p.Nics[i].Network, p.Nics[i].Mac, p.Nics[i].Label = n.Network, set(n.MAC), &label
		if len(n.IPs) > 0 {
			ips := append([]string{}, n.IPs...)
			p.Nics[i].IpAddresses = &ips
		}
		if d, ok := ours.DHCPAdapters[label]; ok {
			p.Nics[i].Dhcp = &d
		} else if i == 0 && ours.Template != "" {
			p.Nics[i].Dhcp = present.Ptr(ours.DHCP)
		}
		if n.Key != 0 {
			p.Nics[i].Key = present.Ptr(int(n.Key))
			if k, ok := ours.BlueprintNICs[n.Key]; ok {
				p.Nics[i].BlueprintNic = &k
			}
		}
	}
	if usage != nil && (usage.CPUPct != nil || usage.MemoryPct != nil) {
		t := usage.SampledAt
		p.Usage = &struct {
			CpuPct    *float32   `json:"cpu_pct,omitempty"`
			MemoryPct *float32   `json:"memory_pct,omitempty"`
			SampledAt *time.Time `json:"sampled_at,omitempty"`
		}{CpuPct: usage.CPUPct, MemoryPct: usage.MemoryPct, SampledAt: &t}
	}
	b, _ := json.Marshal(present.VMResourceProps{VM: p})
	return b
}
