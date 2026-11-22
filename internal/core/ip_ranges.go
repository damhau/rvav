package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/iprange"
	"github.com/damhau/rvav/internal/store"
)

// IP ranges rvav hands addresses from itself, for networks EfficientIP
// doesn't manage (ADR-0063).

func presentIPRange(r store.IPRange) api.IpRange {
	size := r.Range().Size()
	out := api.IpRange{Id: r.ID, Name: r.Name, Vcenter: r.VCenter, Network: r.Network, Cidr: r.CIDR,
		StartAddress: r.StartAddress, EndAddress: r.EndAddress, Gateway: r.Gateway, DnsServers: nonNilStrings(r.DNSServers),
		DnsSearchDomains: nonNilStrings(r.DNSSearchDomains), DnsDomain: r.DNSDomain, Excluded: nonNilStrings(r.Excluded),
		Size: size, Used: r.Used, Taken: len(r.Taken), Free: max(0, size-r.Used-len(r.Taken)), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.Description != "" {
		out.Description = &r.Description
	}
	return out
}

func (s *Service) IPRanges(ctx context.Context, p *auth.Principal) ([]api.IpRange, error) {
	if !p.Everywhere(auth.InfrastructureRead) {
		return nil, ErrForbidden
	}
	list, err := s.Store.IPRanges(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.IpRange, len(list))
	for i, r := range list {
		out[i] = presentIPRange(r)
	}
	return out, nil
}

func (s *Service) IPRange(ctx context.Context, p *auth.Principal, id string) (api.IpRange, error) {
	if !p.Everywhere(auth.InfrastructureRead) {
		return api.IpRange{}, ErrForbidden
	}
	r, err := s.Store.IPRange(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.IpRange{}, ErrNotFound
	}
	if err != nil {
		return api.IpRange{}, err
	}
	return presentIPRange(r), nil
}

func (s *Service) IPAllocations(ctx context.Context, p *auth.Principal, id string) ([]api.IpAllocation, error) {
	if _, err := s.IPRange(ctx, p, id); err != nil {
		return nil, err
	}
	list, err := s.Store.IPAllocations(ctx, id)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(list, func(a, b store.IPAllocation) int {
		x, _ := netip.ParseAddr(a.Address)
		y, _ := netip.ParseAddr(b.Address)
		return x.Compare(y)
	})
	out := make([]api.IpAllocation, len(list))
	for i, a := range list {
		out[i] = api.IpAllocation{Address: a.Address, Hostname: a.Hostname, AllocatedAt: a.CreatedAt,
			DeploymentId: &a.DeploymentID, RequestId: &a.RequestID, DeploymentName: a.DeploymentName}
		if a.FQDN != "" {
			out[i].Fqdn = &a.FQDN
		}
		if a.NIC != "" {
			out[i].Adapter = &a.NIC
		}
	}
	return out, nil
}

var domainName = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

func trimAll(list []string) []string {
	out := []string{}
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ipRangeOf reads a range's write, the errors of each field apart.
func ipRangeOf(w api.IpRangeWrite) (store.IPRange, iprange.Range, []api.FieldError) {
	r := store.IPRange{Name: strings.TrimSpace(w.Name), Description: strings.TrimSpace(deref(w.Description)),
		VCenter: strings.TrimSpace(w.Vcenter), Network: strings.TrimSpace(w.Network),
		DNSServers: trimAll(derefSlice(w.DnsServers)), DNSSearchDomains: trimAll(derefSlice(w.DnsSearchDomains)),
		DNSDomain: strings.TrimSpace(deref(w.DnsDomain)), Excluded: trimAll(derefSlice(w.Excluded))}
	var errs []api.FieldError
	bad := func(field, code, format string, args ...any) {
		errs = append(errs, fieldErr(field, code, fmt.Sprintf(format, args...)))
	}
	if r.Name == "" {
		bad("/name", "required", "Name the range.")
	}
	if r.VCenter == "" {
		bad("/vcenter", "required", "Name the vCenter the network is in.")
	}
	if r.Network == "" {
		bad("/network", "required", "Name the port group or NSX segment.")
	}
	var rg iprange.Range
	prefix, err := iprange.ParsePrefix(w.Cidr)
	if err != nil {
		bad("/cidr", "invalid", "An IPv4 subnet such as 10.51.0.0/24.")
	} else {
		rg.Prefix, r.CIDR = prefix, prefix.String()
	}
	addr := func(field, value, what string) netip.Addr {
		a, err := iprange.ParseAddr(value)
		switch {
		case err != nil:
			bad(field, "invalid", "An IPv4 address.")
		case prefix.IsValid() && !prefix.Contains(a):
			bad(field, "outside", "The %s must be in the subnet %s.", what, prefix)
		}
		return a
	}
	rg.Start, rg.End, rg.Gateway = addr("/start_address", w.StartAddress, "start"), addr("/end_address", w.EndAddress, "end"),
		addr("/gateway", w.Gateway, "gateway")
	r.StartAddress, r.EndAddress, r.Gateway = rg.Start.String(), rg.End.String(), rg.Gateway.String()
	if rg.Start.IsValid() && rg.End.IsValid() && rg.End.Less(rg.Start) {
		bad("/end_address", "before_start", "The end comes before the start, %s.", rg.Start)
	}
	if rg.Gateway.IsValid() && prefix.IsValid() && prefix.Bits() <= 30 &&
		(rg.Gateway == prefix.Addr() || rg.Gateway == lastAddress(prefix)) {
		bad("/gateway", "invalid", "The gateway can't be the subnet's network or broadcast address.")
	}
	for i, d := range r.DNSServers {
		if _, err := netip.ParseAddr(d); err != nil {
			bad(fmt.Sprintf("/dns_servers/%d", i), "invalid", "An IP address.")
		}
	}
	for i, d := range r.DNSSearchDomains {
		if !domainName.MatchString(strings.TrimSuffix(d, ".")) {
			bad(fmt.Sprintf("/dns_search_domains/%d", i), "invalid", "A domain such as lab.corp.example.")
		}
	}
	// What is saved is what is given, so that a client that compares them,
	// as Terraform does, finds them equal.
	switch d := strings.TrimSuffix(r.DNSDomain, "."); {
	case r.DNSDomain != "" && (d == "" || !domainName.MatchString(d)):
		bad("/dns_domain", "invalid", "A domain such as lab.corp.example, or empty for the blueprint's.")
	case d != r.DNSDomain:
		bad("/dns_domain", "not_canonical", "Without the final dot: %s.", d)
	}
	for i, e := range r.Excluded {
		sp, err := iprange.ParseSpan(e)
		switch {
		case err != nil:
			bad(fmt.Sprintf("/excluded/%d", i), "invalid", "An IPv4 address, or two joined by a dash such as 10.51.0.50-10.51.0.59.")
		case prefix.IsValid() && (!prefix.Contains(sp.From) || !prefix.Contains(sp.To)):
			bad(fmt.Sprintf("/excluded/%d", i), "outside", "The addresses must be in the subnet %s.", prefix)
		case sp.String() != e:
			bad(fmt.Sprintf("/excluded/%d", i), "not_canonical", "Write it %s.", sp)
		default:
			rg.Excluded = append(rg.Excluded, sp)
		}
	}
	if len(errs) == 0 {
		for i, d := range r.DNSServers {
			if a, _ := netip.ParseAddr(d); rg.Usable(a) {
				bad(fmt.Sprintf("/dns_servers/%d", i), "in_range", "The range would give %s, a DNS server, to a VM: exclude it.", d)
			}
		}
	}
	if len(errs) == 0 && rg.Size() == 0 {
		bad("/end_address", "no_address", "The range gives no address: its exclusions and the gateway take them all.")
	}
	return r, rg, errs
}

// checkIPRange checks a range against the others, against the addresses
// VMs hold otherwise (taken) and, when it gave addresses, against what it
// was.
func checkIPRange(r store.IPRange, rg iprange.Range, old *store.IPRange, others []store.IPRange, given []string,
	taken []store.HeldAddress) []api.FieldError {
	var errs []api.FieldError
	bad := func(field, code, format string, args ...any) {
		errs = append(errs, fieldErr(field, code, fmt.Sprintf(format, args...)))
	}
	if held := newlyTaken(rg, old, taken); len(held) == 1 {
		bad("/excluded", "held", "A VM holds an address the range would give: %s. Exclude it, or keep the range apart from it.", heldList(held))
	} else if len(held) > 1 {
		bad("/excluded", "held", "VMs hold addresses the range would give: %s. Exclude them, or keep the range apart from them.", heldList(held))
	}
	for _, o := range others {
		if o.VCenter != r.VCenter {
			continue
		}
		if o.Network == r.Network {
			differs := func(field, what string, same bool) {
				if !same {
					bad(field, "differs", "The range %s of %s says otherwise: ranges of one network share its %s.", o.Name, r.Network, what)
				}
			}
			differs("/cidr", "subnet", o.CIDR == r.CIDR)
			differs("/gateway", "gateway", o.Gateway == r.Gateway)
			differs("/dns_servers", "DNS servers", slices.Equal(o.DNSServers, r.DNSServers))
			differs("/dns_search_domains", "search domains", slices.Equal(o.DNSSearchDomains, r.DNSSearchDomains))
			differs("/dns_domain", "DNS domain", o.DNSDomain == r.DNSDomain)
		}
		if or := o.Range(); or.Prefix.Overlaps(rg.Prefix) && or.Overlaps(rg) {
			bad("/start_address", "overlaps", "The range overlaps %s (%s to %s, on %s).", o.Name, o.StartAddress, o.EndAddress, o.Network)
		}
	}
	if old == nil || len(given) == 0 {
		return errs
	}
	if old.VCenter != r.VCenter || old.Network != r.Network || old.CIDR != r.CIDR || old.Gateway != r.Gateway {
		bad("/network", "in_use", "VMs hold %d addresses from this range: its vCenter, network, subnet and gateway can't change.", len(given))
	}
	for _, g := range given {
		a, err := netip.ParseAddr(g)
		if err != nil || rg.Usable(a) {
			continue
		}
		field := "/excluded"
		switch {
		case a.Less(rg.Start):
			field = "/start_address"
		case rg.End.Less(a):
			field = "/end_address"
		}
		bad(field, "in_use", "A VM holds %s: the range must keep it.", g)
	}
	return errs
}

// newlyTaken are the addresses VMs hold otherwise that the range gives
// and didn't before: those it had stay, skipped, so that a range still
// saves once EfficientIP gave one of its addresses.
func newlyTaken(rg iprange.Range, old *store.IPRange, taken []store.HeldAddress) []store.HeldAddress {
	var was iprange.Range
	if old != nil {
		was = old.Range()
	}
	var out []store.HeldAddress
	for _, h := range taken {
		if h.From != "dns" && rg.Usable(h.Address) && (old == nil || !was.Usable(h.Address)) {
			out = append(out, h)
		}
	}
	return out
}

// heldList says which addresses which VMs hold, five at most.
func heldList(held []store.HeldAddress) string {
	var parts []string
	for i, h := range held {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("and %d more", len(held)-5))
			break
		}
		how := "a deployment's address"
		if h.From == "vcenter" {
			how = "as vCenter reports"
		}
		parts = append(parts, fmt.Sprintf("%s (%s, %s)", h.Address, h.Holder, how))
	}
	return strings.Join(parts, ", ")
}

// SaveIPRange creates a range (no id) or changes one.
func (s *Service) SaveIPRange(ctx context.Context, p *auth.Principal, id string, w api.IpRangeWrite) (api.IpRange, error) {
	if !p.Everywhere(auth.InfrastructureManage) {
		return api.IpRange{}, ErrForbidden
	}
	r, rg, errs := ipRangeOf(w)
	if r.VCenter != "" && r.Network != "" {
		nets, err := s.Store.InventoryNetworks(ctx)
		if err != nil {
			return api.IpRange{}, err
		}
		if !slices.ContainsFunc(nets, func(n store.InvNetwork) bool { return n.VCenter == r.VCenter && n.Name == r.Network }) {
			errs = append(errs, fieldErr("/network", "unknown", fmt.Sprintf("The inventory of %s has no network %s.", r.VCenter, r.Network)))
		}
	}
	if len(errs) > 0 {
		return api.IpRange{}, &ValidationError{Errors: errs}
	}
	r.ID = id
	saved, err := s.Store.SaveIPRange(ctx, r, func(old *store.IPRange, others []store.IPRange, given []string,
		taken []store.HeldAddress) error {
		if errs := checkIPRange(r, rg, old, others, given, taken); len(errs) > 0 {
			return &ValidationError{Errors: errs}
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return api.IpRange{}, ErrNotFound
	}
	if err != nil {
		return api.IpRange{}, err
	}
	s.audit(ctx, p, map[bool]string{true: "ip_range.create", false: "ip_range.update"}[id == ""], "ip_range", saved.ID, nil, w)
	return presentIPRange(saved), nil
}

func (s *Service) DeleteIPRange(ctx context.Context, p *auth.Principal, id string) error {
	if !p.Everywhere(auth.InfrastructureManage) {
		return ErrForbidden
	}
	r, err := s.Store.IPRange(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	switch err := s.Store.DeleteIPRange(ctx, id); {
	case errors.Is(err, store.ErrInUse):
		return &ConflictError{Msg: fmt.Sprintf("VMs hold addresses from %s: delete them, or move their adapters, first.", r.Name)}
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return err
	}
	s.audit(ctx, p, "ip_range.delete", "ip_range", id, nil, map[string]string{"name": r.Name, "network": r.Network, "vcenter": r.VCenter})
	return nil
}

// rangeMappings are the networks with ranges, one per vCenter, as the
// network mappings list shows them.
func rangeMappings(ranges []store.IPRange) []api.NetworkMapping {
	var out []api.NetworkMapping
	seen := map[[2]string]bool{}
	for _, r := range ranges {
		k := [2]string{r.VCenter, r.Network}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, api.NetworkMapping{Network: r.Network, Vcenter: &r.VCenter, Subnet: r.CIDR, Gateway: r.Gateway,
			DnsZone: r.DNSDomain, DnsServers: nonNilStrings(r.DNSServers), Source: api.NetworkMappingSourceRvav})
	}
	return out
}

// sortMappings orders mappings by network, a network's rvav rows by vCenter
// after its other one.
func sortMappings(list []api.NetworkMapping) {
	slices.SortStableFunc(list, func(a, b api.NetworkMapping) int {
		return cmp.Or(cmp.Compare(a.Network, b.Network), cmp.Compare(deref(a.Vcenter), deref(b.Vcenter)))
	})
}
