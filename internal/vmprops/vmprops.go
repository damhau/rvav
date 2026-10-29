// Package vmprops builds the properties stored on vsphere.vm resources from
// what vCenter reports, so the workflow and the sync loop write the same shape.
package vmprops

import (
	"encoding/json"
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
}

// OursOf reads them back from stored properties.
func OursOf(props json.RawMessage) Ours {
	var p struct {
		VM Ours `json:"vm"`
	}
	_ = json.Unmarshal(props, &p)
	return p.VM
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
	p.Fqdn, p.Template, p.Component = set(ours.FQDN), set(ours.Template), set(ours.Component)
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
	p.Disks = make([]struct {
		CapacityGb int     `json:"capacity_gb"`
		Datastore  *string `json:"datastore,omitempty"`
		Label      string  `json:"label"`
	}, len(vm.Disks))
	for i, d := range vm.Disks {
		label := d.Label
		if label == "" {
			label = "Hard disk " + strconv.Itoa(i+1)
		}
		p.Disks[i].CapacityGb, p.Disks[i].Datastore, p.Disks[i].Label = d.CapacityGB, set(d.Datastore), label
	}
	p.Nics = make([]struct {
		IpAddresses *[]string `json:"ip_addresses,omitempty"`
		Label       *string   `json:"label,omitempty"`
		Mac         *string   `json:"mac,omitempty"`
		Network     string    `json:"network"`
	}, len(vm.NICs))
	for i, n := range vm.NICs {
		label := n.Label
		if label == "" {
			label = "Network adapter " + strconv.Itoa(i+1)
		}
		p.Nics[i].Network, p.Nics[i].Mac, p.Nics[i].Label = n.Network, set(n.MAC), &label
		if len(n.IPs) > 0 {
			ips := append([]string{}, n.IPs...)
			p.Nics[i].IpAddresses = &ips
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
