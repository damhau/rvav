package efficientip

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
)

// family is an address family's services and fields: SOLIDserver keeps
// IPv6 subnets and addresses apart from IPv4 ones, under names of their
// own (ADR-0039).
type family struct {
	v6                                                                    bool
	subnetList, freeAddress, addressList, addAddress, deleteAddress       string
	subnetID, subnetName, subnetPrefix, subnetParams                      string
	addressID, hostaddr, name, params, addressSubnetName, addressSubnetID string
}

var (
	ipv4 = family{subnetList: "ip_block_subnet_list", freeAddress: "ip_find_free_address", addressList: "ip_address_list",
		addAddress: "ip_add", deleteAddress: "ip_delete",
		subnetID: "subnet_id", subnetName: "subnet_name", subnetPrefix: "subnet_prefix", subnetParams: "subnet_class_parameters",
		addressID: "ip_id", hostaddr: "hostaddr", name: "name", params: "ip_class_parameters",
		addressSubnetName: "subnet_name", addressSubnetID: "subnet_id"}
	ipv6 = family{v6: true, subnetList: "ip6_block6_subnet6_list", freeAddress: "ip6_find_free_address6",
		addressList: "ip6_address6_list", addAddress: "ip6_address6_add", deleteAddress: "ip6_address6_delete",
		subnetID: "subnet6_id", subnetName: "subnet6_name", subnetPrefix: "subnet6_prefix", subnetParams: "subnet6_class_parameters",
		addressID: "ip6_id", hostaddr: "hostaddr6", name: "ip6_name", params: "ip6_class_parameters",
		addressSubnetName: "subnet6_name", addressSubnetID: "subnet6_id"}
)

// Key tells an address's ID from the same one in the other family:
// SOLIDserver numbers IPv4 and IPv6 addresses apart.
func (a Address) Key() string { return familyOf(a.IP).key(a.ID) }

func (f family) key(id string) string {
	if f.v6 {
		return "ip6:" + id
	}
	return "ip:" + id
}

// familyOf is the family of an address or a CIDR (IPv4 when it is neither).
func familyOf(s string) family {
	s, _, _ = strings.Cut(s, "/")
	if a, err := netip.ParseAddr(s); err == nil && a.Is6() && !a.Is4In6() {
		return ipv6
	}
	return ipv4
}

// canonical writes an address as rvav stores it; SOLIDserver writes IPv6
// ones in full.
func canonical(ip string) string {
	if a, err := netip.ParseAddr(ip); err == nil {
		return a.String()
	}
	return ip
}

// asStored writes an address as SOLIDserver stores it, to compare in a
// WHERE clause.
func (f family) asStored(ip string) string {
	if a, err := netip.ParseAddr(ip); err == nil && f.v6 {
		return a.StringExpanded()
	}
	return ip
}

// prefixLen is a subnet's prefix length; an IPv4 one may give only its
// size in addresses.
func (f family) prefixLen(r row) int {
	if !f.v6 {
		return prefixLen(r)
	}
	if p, err := strconv.Atoi(r[f.subnetPrefix]); err == nil {
		return p
	}
	return -1
}

// cidr is a subnet row's range.
func (f family) cidr(r row) string {
	return canonical(r["start_hostaddr"]) + "/" + strconv.Itoa(f.prefixLen(r))
}

// noIPv6 reports an IPv6 service SOLIDserver turned down (no IPv6 module,
// or no right to it): rvav's IPv4 work goes on without IPv6.
func noIPv6(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status < 500
}
