package inventory

import (
	"context"
	"fmt"
	"log/slog"
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
}

func (s *SubnetSync) Run(ctx context.Context) error {
	for {
		if err := s.SyncOnce(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("EfficientIP subnet sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Interval):
		}
	}
}

// SyncOnce reads the subnets and replaces the synced network mappings. An
// empty answer changes nothing: losing every mapping at once is more likely
// a permission change than the network.
func (s *SubnetSync) SyncOnce(ctx context.Context) error {
	subnets, err := s.IPAM.Subnets(ctx)
	if err != nil {
		return err
	}
	if len(subnets) == 0 {
		s.Log.Warn("EfficientIP returned no subnets; network mappings left as they are")
		return nil
	}
	found, problems := Mappings(subnets)
	for _, p := range problems {
		s.Log.Warn("EfficientIP subnet not mapped", "reason", p)
	}
	synced, removed, err := s.Store.SyncNetworkSubnets(ctx, found)
	if err != nil {
		return err
	}
	s.Log.Info("EfficientIP subnets synced", "subnets", len(subnets), "networks", synced, "removed", removed)
	return nil
}

// Mappings are the network mappings the subnets' class parameters
// describe: vsphere_network (one network, or several separated by commas),
// gateway, domain and dns_servers. A network claimed by two subnets is left
// out, and so is a subnet without a gateway or a DNS zone.
func Mappings(subnets []efficientip.SubnetInfo) (found []store.NetworkSubnet, problems []string) {
	byNetwork := map[string][]store.NetworkSubnet{}
	var order []string
	for _, sn := range subnets {
		networks := splitList(sn.Params.Get(efficientip.ParamNetwork))
		if len(networks) == 0 {
			continue
		}
		gateway, zone := sn.Params.Get(efficientip.ParamGateway), sn.Params.Get(efficientip.ParamZone)
		switch {
		case gateway == "":
			problems = append(problems, fmt.Sprintf("subnet %s (%s) has no %s class parameter", sn.CIDR, sn.Name, efficientip.ParamGateway))
			continue
		case zone == "":
			problems = append(problems, fmt.Sprintf("subnet %s (%s) has no %s class parameter", sn.CIDR, sn.Name, efficientip.ParamZone))
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
			continue
		}
		found = append(found, byNetwork[n][0])
	}
	return found, problems
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
