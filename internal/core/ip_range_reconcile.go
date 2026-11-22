package core

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/iprange"
	"github.com/damhau/rvav/internal/store"
)

// reconcileRanges compares rvav's IP ranges with what VMs hold
// (ADR-0063): addresses given to VMs no deployment has any more, and VMs
// of the inventory whose address is in a range that didn't give it to
// them.
func (s *Service) reconcileRanges(ctx context.Context, report *api.IpamReconcile) error {
	ranges, err := s.Store.IPRanges(ctx)
	if err != nil || len(ranges) == 0 {
		return err
	}
	byID := map[string]store.IPRange{}
	var vcenters []string
	for _, r := range ranges {
		byID[r.ID] = r
		if !slices.Contains(vcenters, r.VCenter) {
			vcenters = append(vcenters, r.VCenter)
		}
	}
	orphans, err := s.Store.OrphanIPAllocations(ctx)
	if err != nil {
		return err
	}
	for _, a := range orphans {
		rg := byID[a.RangeID]
		o := api.IpamOrphanAllocation{RangeId: rg.ID, RangeName: rg.Name, Vcenter: rg.VCenter, Network: rg.Network,
			Address: a.Address, Hostname: a.Hostname, AllocatedAt: a.CreatedAt, DeploymentId: &a.DeploymentID,
			DeploymentName: a.DeploymentName, RequestId: &a.RequestID}
		if a.NIC != "" {
			o.Adapter = &a.NIC
		}
		report.OrphanAllocations = append(report.OrphanAllocations, o)
	}

	held, err := s.Store.IPAllocations(ctx, "")
	if err != nil {
		return err
	}
	holder := map[string]string{}
	for _, a := range held {
		holder[a.RangeID+"/"+a.Address] = a.Hostname
	}
	vms, err := s.Store.AddressedVMs(ctx, vcenters)
	if err != nil {
		return err
	}
	spans := make([]iprange.Range, len(ranges))
	for i, r := range ranges {
		spans[i] = r.Range()
	}
	for _, vm := range vms {
		ip, err := netip.ParseAddr(vm.IPAddress)
		if err != nil || !ip.Is4() {
			continue
		}
		i := -1
		for j := range ranges {
			if ranges[j].VCenter == vm.VCenter && spans[j].Usable(ip) {
				i = j
				break
			}
		}
		if i < 0 {
			continue
		}
		rg := ranges[i]
		h, ok := holder[rg.ID+"/"+ip.String()]
		if ok && strings.EqualFold(h, vm.Name) {
			continue
		}
		u := api.IpamUnallocated{Vm: vm.Name, Vcenter: vm.VCenter, Address: ip.String(), RangeId: rg.ID, RangeName: rg.Name,
			Network: rg.Network, DeploymentId: vm.DeploymentID, DeploymentName: vm.DeploymentName}
		if ok {
			u.HeldBy = &h
		}
		report.Unallocated = append(report.Unallocated, u)
	}
	return nil
}

// ReleaseIPAllocation gives back an address a range gave a VM no
// deployment has any more: an orphan of the reconcile report.
func (s *Service) ReleaseIPAllocation(ctx context.Context, p *auth.Principal, id, address string) error {
	if !p.Everywhere(auth.InfrastructureManage) {
		return ErrForbidden
	}
	r, err := s.IPRange(ctx, p, id)
	if err != nil {
		return err
	}
	al, err := s.Store.ReleaseOrphanAllocation(ctx, id, address)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrInUse):
		return &ConflictError{Msg: fmt.Sprintf("%s still holds %s: delete the VM, or move its adapter, and rvav gives the address back.",
			al.Hostname, al.Address)}
	case err != nil:
		return err
	}
	s.audit(ctx, p, "ip_range.release", "ip_range", id, nil, map[string]string{"name": r.Name, "address": al.Address,
		"hostname": al.Hostname})
	return nil
}
