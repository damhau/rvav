// Package vmprops builds the properties stored on vsphere.vm resources from
// what vCenter reports, so the workflow and the sync loop write the same shape.
package vmprops

import (
	"encoding/json"
	"maps"
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
				Label string `json:"label"`
				DHCP  *bool  `json:"dhcp"`
			} `json:"nics"`
		} `json:"vm"`
	}
	_ = json.Unmarshal(props, &p)
	out := p.VM.Ours
	for _, n := range p.VM.Nics {
		if n.DHCP != nil {
			if out.DHCPAdapters == nil {
				out.DHCPAdapters = map[string]bool{}
			}
			out.DHCPAdapters[n.Label] = *n.DHCP
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
		p.Disks[i].CapacityGb, p.Disks[i].Datastore, p.Disks[i].Label = d.CapacityGB, set(d.Datastore), label
		if d.Provisioning != "" {
			p.Disks[i].Provisioning = present.Ptr(api.DiskProvisioning(d.Provisioning))
		}
		if d.Linked {
			p.Disks[i].Linked = present.Ptr(true)
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
