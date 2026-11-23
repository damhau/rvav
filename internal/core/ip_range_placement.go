package core

import (
	"context"
	"fmt"
	"net/netip"
	"slices"

	"github.com/damhau/rvav/internal/store"
)

// sourceRanges is the source of a network rvav's own IP ranges address in
// a vCenter (ADR-0063): they come before the subnet linked to it.
const sourceRanges = "rvav"

// rangesOf are a network's IP ranges in the vCenter, in any when vcenter
// is "" (the cluster isn't known yet).
func (pl *placement) rangesOf(vcenter, network string) []store.IPRange {
	var out []store.IPRange
	for _, r := range pl.ranges {
		if r.Network == network && (vcenter == "" || r.VCenter == vcenter) {
			out = append(out, r)
		}
	}
	return out
}

// subnetOf is what addresses a network of the vCenter: its IP ranges
// there, else the subnet linked to it.
func (pl *placement) subnetOf(vcenter, network string) (store.NetworkSubnet, bool) {
	if rs := pl.rangesOf(vcenter, network); len(rs) > 0 {
		return rangeSubnet(rs), true
	}
	sn, ok := pl.subnets[network]
	return sn, ok
}

// rangeSubnet is the subnet a network's ranges share: they agree on it.
func rangeSubnet(rs []store.IPRange) store.NetworkSubnet {
	r := rs[0]
	return store.NetworkSubnet{Network: r.Network, Subnet: r.CIDR, Gateway: r.Gateway, DNSZone: r.DNSDomain,
		DNSServers: r.DNSServers, SubnetName: r.Name, Source: sourceRanges}
}

// fromRanges says whether a subnet is a network's IP ranges.
func fromRanges(sn *store.NetworkSubnet) bool { return sn != nil && sn.Source == sourceRanges }

// rangeSearch are the search domains of the network's ranges.
func (pl *placement) rangeSearch(vcenter, network string) []string {
	for _, r := range pl.rangesOf(vcenter, network) {
		if len(r.DNSSearchDomains) > 0 {
			return r.DNSSearchDomains
		}
	}
	return nil
}

// freeIn is how many addresses ranges have left.
func freeIn(rs []store.IPRange) int {
	n := 0
	for _, r := range rs {
		n += max(0, r.Range().Size()-r.Used-len(r.Taken))
	}
	return n
}

// nextInRanges is the address a network's ranges would give next, "" when
// they are full.
func (pl *placement) nextInRanges(rs []store.IPRange) string {
	if next := pl.hintsInRanges(rs, 1); len(next) > 0 {
		return next[0]
	}
	return ""
}

// hintsInRanges are the next n addresses a network's ranges would give,
// after those hinted to the request's VMs so far, fewer when they are
// full. A hint: rvav gives them as the VMs are built, in the order they
// are, so which VM gets which isn't known yet.
func (pl *placement) hintsInRanges(rs []store.IPRange, n int) []string {
	taken := map[netip.Addr]bool{}
	for _, a := range pl.held {
		ip, err := netip.ParseAddr(a.Address)
		if err == nil && slices.ContainsFunc(rs, func(r store.IPRange) bool { return r.ID == a.RangeID }) {
			taken[ip] = true
		}
	}
	for _, r := range rs {
		for _, h := range r.Taken {
			taken[h.Address] = true
		}
	}
	if pl.hinted == nil {
		pl.hinted = map[string]bool{}
	}
	var out []string
	for _, r := range rs {
		rg := r.Range()
		for len(out) < n {
			ip, ok := rg.First(func(a netip.Addr) bool { return taken[a] || pl.hinted[r.ID+" "+a.String()] })
			if !ok {
				break
			}
			pl.hinted[r.ID+" "+ip.String()] = true
			out = append(out, ip.String())
		}
	}
	return out
}

// nextInRanges is the address a network's IP ranges, in any vCenter,
// would give next, and whether it has any.
func (s *Service) nextInRanges(ctx context.Context, network string) (string, bool, error) {
	ranges, err := s.Store.IPRanges(ctx)
	if err != nil {
		return "", false, err
	}
	pl := &placement{ranges: ranges}
	rs := pl.rangesOf("", network)
	if len(rs) == 0 {
		return "", false, nil
	}
	if pl.held, err = s.Store.IPAllocations(ctx, ""); err != nil {
		return "", true, err
	}
	return pl.nextInRanges(rs), true, nil
}

// takeFromRanges counts the addresses a component's VMs need from the IP
// ranges of a network for one of their adapters, and refuses, on the
// component that goes past them, more than the ranges have left. The VMs
// an update keeps hold theirs already: only those it adds need one.
func (pl *placement) takeFromRanges(p vmPart, network, at string, add func(field, code, msg string)) {
	rs := pl.rangesOf(p.vcenter, network)
	if len(rs) == 0 {
		return
	}
	if pl.rangeNeed == nil {
		pl.rangeNeed = map[string]int{}
	}
	key := p.vcenter + "/" + network
	before := pl.rangeNeed[key]
	pl.rangeNeed[key] += max(0, p.count-p.kept)
	free, need := freeIn(rs), pl.rangeNeed[key]
	switch {
	case need <= free || before > free:
	case free == 0:
		add(at, "no_free_address", fmt.Sprintf("The IP ranges of %s have no free address. Pick another network, or ask an administrator to add a range.", network))
	case free == 1:
		add(at, "no_free_address", fmt.Sprintf("The IP ranges of %s have 1 free address; this request needs %d. Pick another network, or ask an administrator to add a range.",
			network, need))
	default:
		add(at, "no_free_address", fmt.Sprintf("The IP ranges of %s have %d free addresses; this request needs %d. Pick another network, or ask an administrator to add a range.",
			network, free, need))
	}
}
