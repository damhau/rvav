package provision

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vmprops"
)

// Addresses from rvav's own IP ranges, for networks EfficientIP doesn't
// manage (ADR-0063): recorded as ipam.address resources with source rvav.

// rangeSource marks an address from rvav's IP ranges.
const rangeSource = "rvav"

type rangeAddress struct {
	Address    string `json:"address"`
	Hostname   string `json:"hostname"`
	NIC        string `json:"nic"`
	Source     string `json:"source"`
	Allocation string `json:"allocation_id"`
}

func rangeAddressOf(r store.Resource) (rangeAddress, bool) {
	var p rangeAddress
	if r.Type != "ipam.address" || json.Unmarshal(r.Properties, &p) != nil || p.Source != rangeSource {
		return p, false
	}
	return p, true
}

// FromRanges says whether any of the VM's addresses come from rvav's IP
// ranges.
func (vm VMUnit) FromRanges() bool {
	return vm.IPRanges || slices.ContainsFunc(vm.NICs, func(n NICUnit) bool { return n.IPRanges })
}

// allocate gives one of a VM's adapters (nic "" for its first) an address
// from the IP ranges of its network, the one a subscription chose when it
// chose one, and records it.
func (a *Activities) allocate(ctx context.Context, in VMInput, network, fqdn, nic, chosen string) (string, error) {
	al, rg, err := a.Store.AllocateAddress(ctx, store.AllocationRequest{VCenter: in.VM.VCenter, Network: network,
		RequestID: in.RequestID, DeploymentID: in.VM.DeploymentID, Hostname: in.VM.Name, NIC: nic, FQDN: fqdn, Address: chosen})
	switch {
	case errors.Is(err, store.ErrNoRange):
		return "", permanent("%s has no IP range in vCenter %s any more. Ask an administrator to add one.", network, in.VM.VCenter)
	case errors.Is(err, store.ErrRangeFull):
		return "", permanent("The IP ranges of %s have no free address. Ask an administrator to add a range.", network)
	case errors.Is(err, store.ErrAddressOutside):
		return "", permanent("%s, which a subscription chose, is not an address the IP ranges of %s give", chosen, network)
	case errors.Is(err, store.ErrAddressHeld):
		return "", permanent("Another VM holds %s, which a subscription chose", chosen)
	case err != nil:
		return "", err
	}
	ext := rangeSource + ":" + al.ID
	p := map[string]any{"address": al.Address, "subnet": rg.CIDR, "hostname": in.VM.Name, "fqdn": fqdn, "source": rangeSource,
		"range_id": rg.ID, "range": rg.Name, "allocation_id": al.ID}
	if nic != "" {
		p["nic"] = nic
	}
	props, _ := json.Marshal(p)
	res, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: in.VM.DeploymentID,
		RequestID: &in.RequestID, Type: "ipam.address", Name: al.Address, ExternalID: &ext, Properties: props})
	if err != nil {
		return "", err
	}
	a.Events.Resource(ctx, in.ProjectID, res)
	return al.Address, nil
}

// addressSource says where an adapter's address comes from.
func addressSource(ranges bool, network, subnet string) string {
	if ranges {
		return "IP ranges of " + network
	}
	return "subnet " + subnet
}

// sourcesDetail says where the VM's addresses come from, each source once.
func sourcesDetail(from []string) string {
	var list []string
	for _, f := range from {
		if !slices.Contains(list, f) {
			list = append(list, f)
		}
	}
	out := strings.Join(list, ", ")
	if out == "" {
		return ""
	}
	return strings.ToUpper(out[:1]) + out[1:]
}

// release gives an address back to its range and forgets it.
func (a *Activities) release(ctx context.Context, r store.Resource, p rangeAddress) error {
	if err := a.Store.ReleaseAddress(ctx, a.Store.Pool, p.Allocation); err != nil {
		return err
	}
	if err := a.Store.DeleteResource(ctx, a.Store.Pool, r.ID); err != nil {
		return err
	}
	a.Events.Deployment(ctx, r.DeploymentID)
	return nil
}

// releaseUnbuilt gives back the addresses a failed request got from IP
// ranges for VMs it never made: a retry asks for them again.
func (a *Activities) releaseUnbuilt(ctx context.Context, requestID string) error {
	list, err := a.Store.ResourcesForRequest(ctx, requestID)
	if err != nil {
		return err
	}
	built := map[string]bool{}
	for _, r := range list {
		if r.Type == "vsphere.vm" {
			built[r.DeploymentID+"/"+r.Name] = true
		}
	}
	for _, r := range list {
		if p, ok := rangeAddressOf(r); ok && !built[r.DeploymentID+"/"+p.Hostname] {
			if err := a.release(ctx, r, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// releaseNIC gives back the address an added adapter of the VM got from IP
// ranges, once a day-2 action moved the adapter to another network or
// removed it: the adapter no longer uses it.
func (a *Activities) releaseNIC(ctx context.Context, in OpInput) error {
	vm, err := a.Store.GetResource(ctx, in.ResourceID)
	if err != nil {
		return err
	}
	key := int32(argInt(in.Op.Args, "key"))
	if key == 0 {
		for _, n := range day2.VM(vm.Properties).Nics {
			if n.Label != nil && *n.Label == argStr(in.Op.Args, "nic") && n.Key != nil {
				key = int32(*n.Key)
			}
		}
	}
	bp, ok := vmprops.OursOf(vm.Properties).BlueprintNICs[key]
	if !ok || bp == vmprops.FirstNIC {
		return nil
	}
	list, err := a.deploymentResources(ctx, vm.DeploymentID, "ipam.address")
	if err != nil {
		return err
	}
	for _, r := range list {
		if p, ok := rangeAddressOf(r); ok && p.Hostname == vm.Name && p.NIC == bp {
			if err := a.release(ctx, r, p); err != nil {
				return err
			}
		}
	}
	return nil
}
