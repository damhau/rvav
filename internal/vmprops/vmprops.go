// Package vmprops builds the properties stored on vsphere.vm resources from
// what vCenter reports, so the workflow and the sync loop write the same shape.
package vmprops

import (
	"encoding/json"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/google/uuid"

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

// Build returns the resource properties for a VM. fqdn and template come
// from rvav because vCenter does not always know them.
func Build(vcenter string, vm vsphere.VM, fqdn, template string, usage *Usage, snapshots int) json.RawMessage {
	p := api.VmProperties{
		Moref:      vm.Moref,
		Vcenter:    vcenter,
		PowerState: api.VmPropertiesPowerState(vm.PowerState),
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
	p.Fqdn, p.Template = set(fqdn), set(template)
	p.SnapshotCount = present.Ptr(snapshots)
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
		Mac         *string   `json:"mac,omitempty"`
		Network     string    `json:"network"`
	}, len(vm.NICs))
	for i, n := range vm.NICs {
		p.Nics[i].Network, p.Nics[i].Mac = n.Network, set(n.MAC)
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
