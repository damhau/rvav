package inventory

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/store"
)

// SubnetSync links vSphere networks to EfficientIP subnets from the
// subnets' class parameters (ADR-0015), every interval.
type SubnetSync struct {
	Store    *store.Store
	IPAM     efficientip.Client
	Log      *slog.Logger
	Interval time.Duration
	// IntervalNow, when set, gives it as saved now (ADR-0030).
	IntervalNow func(context.Context) time.Duration
}

func (s *SubnetSync) Run(ctx context.Context) error {
	for {
		// Every sync replica runs it: one at a time (S14.1).
		err := s.Store.WithLock(ctx, store.LockSubnetSync, func() error { return s.SyncOnce(ctx) })
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("EfficientIP subnet sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every(ctx, s.Interval, s.IntervalNow)):
		}
	}
}

// SyncOnce reads the subnets and replaces the synced network mappings.
// Networks whose subnets have a problem keep their mapping until it is
// fixed. An answer that maps no network changes nothing: losing every
// mapping at once is more likely a permission change than the network.
func (s *SubnetSync) SyncOnce(ctx context.Context) error {
	subnets, err := s.IPAM.Subnets(ctx)
	if err != nil {
		return err
	}
	found, held, problems := Mappings(subnets)
	for _, p := range problems {
		s.Log.Warn("EfficientIP subnet not mapped", "reason", p)
	}
	if len(found) == 0 {
		s.Log.Warn("EfficientIP subnets map no network; network mappings left as they are", "subnets", len(subnets))
		return nil
	}
	synced, removed, err := s.Store.SyncNetworkSubnets(ctx, found, held)
	if err != nil {
		return err
	}
	s.Log.Info("EfficientIP subnets synced", "subnets", len(subnets), "networks", synced, "removed", removed)
	return nil
}

// Mappings are the network mappings the subnets' class parameters
// describe: vsphere_network (one network, or several separated by commas),
// gateway, domain and dns_servers. A network claimed by two subnets is left
// out, and so is a subnet without a gateway or a DNS zone; those networks
// are held: their mapping stays as it is. An IPv6 subnet gives a mapped
// network its IPv6 subnet (ADR-0039); one that can't is said why.
func Mappings(subnets []efficientip.SubnetInfo) (found []store.NetworkSubnet, held, problems []string) {
	byNetwork := map[string][]store.NetworkSubnet{}
	six := map[string][]efficientip.SubnetInfo{}
	var order []string
	for _, sn := range subnets {
		networks := splitList(sn.Params.Get(efficientip.ParamNetwork))
		if len(networks) == 0 {
			continue
		}
		if p, err := netip.ParsePrefix(sn.CIDR); err == nil && p.Addr().Is6() {
			for _, n := range networks {
				six[n] = append(six[n], sn)
			}
			continue
		}
		gateway, zone := sn.Params.Get(efficientip.ParamGateway), sn.Params.Get(efficientip.ParamZone)
		switch {
		case gateway == "":
			problems = append(problems, fmt.Sprintf("subnet %s (%s) has no %s class parameter", sn.CIDR, sn.Name, efficientip.ParamGateway))
			held = append(held, networks...)
			continue
		case zone == "":
			problems = append(problems, fmt.Sprintf("subnet %s (%s) has no %s class parameter", sn.CIDR, sn.Name, efficientip.ParamZone))
			held = append(held, networks...)
			continue
		}
		for _, n := range networks {
			if _, seen := byNetwork[n]; !seen {
				order = append(order, n)
			}
			byNetwork[n] = append(byNetwork[n], store.NetworkSubnet{Network: n, Subnet: sn.CIDR, Gateway: gateway, DNSZone: zone,
				DNSServers: splitList(sn.Params.Get(efficientip.ParamDNSServers)), EfficientIPSubnetID: sn.ID, SubnetName: sn.Name})
		}
	}
	for _, n := range order {
		if claims := byNetwork[n]; len(claims) > 1 {
			cidrs := make([]string, len(claims))
			for i, c := range claims {
				cidrs[i] = c.Subnet
			}
			problems = append(problems, fmt.Sprintf("network %s is claimed by several subnets (%s)", n, strings.Join(cidrs, ", ")))
			held = append(held, n)
			continue
		}
		found = append(found, byNetwork[n][0])
	}
	found = slices.DeleteFunc(found, func(n store.NetworkSubnet) bool { return slices.Contains(held, n.Network) })
	for _, n := range slices.Sorted(maps.Keys(six)) {
		claims := six[n]
		i := slices.IndexFunc(found, func(m store.NetworkSubnet) bool { return m.Network == n })
		switch {
		case i < 0:
			problems = append(problems, fmt.Sprintf("IPv6 subnet %s (%s): network %s has no IPv4 subnet in EfficientIP", claims[0].CIDR, claims[0].Name, n))
		case len(claims) > 1:
			problems = append(problems, fmt.Sprintf("network %s is claimed by several IPv6 subnets (%s, %s)", n, claims[0].CIDR, claims[1].CIDR))
		case claims[0].Params.Get(efficientip.ParamGateway) == "":
			problems = append(problems, fmt.Sprintf("IPv6 subnet %s (%s) has no %s class parameter", claims[0].CIDR, claims[0].Name, efficientip.ParamGateway))
		default:
			found[i].Subnet6, found[i].Gateway6, found[i].EfficientIPSubnet6ID = claims[0].CIDR, claims[0].Params.Get(efficientip.ParamGateway), claims[0].ID
		}
	}
	return found, held, problems
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
