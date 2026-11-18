package inventory

import (
	"maps"
	"net/url"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/integrations/efficientip"
)

func TestMappings(t *testing.T) {
	subnet := func(cidr, network, gateway, zone string) efficientip.SubnetInfo {
		return efficientip.SubnetInfo{ID: cidr, CIDR: cidr, Name: cidr, Params: url.Values{efficientip.ParamNetwork: {network},
			efficientip.ParamGateway: {gateway}, efficientip.ParamZone: {zone}}}
	}
	found, held, problems := Mappings([]efficientip.SubnetInfo{
		subnet("10.0.1.0/24", "net-a, net-b", "10.0.1.1", "corp.example"),
		subnet("10.0.2.0/24", "net-b", "10.0.2.1", "corp.example"), // a second claim on net-b
		subnet("10.0.3.0/24", "net-c", "", "corp.example"),         // no gateway
		subnet("10.0.4.0/24", "net-d", "10.0.4.1", ""),             // no zone
		subnet("10.0.5.0/24", "", "10.0.5.1", "corp.example"),      // not on a vSphere network
	})
	if len(found) != 1 || found[0].Network != "net-a" || found[0].Subnet != "10.0.1.0/24" || found[0].Gateway != "10.0.1.1" {
		t.Fatalf("found = %+v", found)
	}
	slices.Sort(held)
	if !slices.Equal(held, []string{"net-b", "net-c", "net-d"}) || len(problems) != 3 {
		t.Fatalf("held = %v, problems = %v", held, problems)
	}
}

// An IPv6 subnet gives a mapped network its IPv6 subnet; one that can't
// is said why, and changes nothing else (ADR-0039).
func TestMappingsIPv6(t *testing.T) {
	subnet := func(cidr, network, gateway string) efficientip.SubnetInfo {
		return efficientip.SubnetInfo{ID: "id " + cidr, CIDR: cidr, Name: cidr, Params: url.Values{efficientip.ParamNetwork: {network},
			efficientip.ParamGateway: {gateway}, efficientip.ParamZone: {"corp.example"}}}
	}
	found, held, problems := Mappings([]efficientip.SubnetInfo{
		subnet("10.0.1.0/24", "net-a", "10.0.1.1"),
		subnet("2001:db8:1::/64", "net-a", "2001:db8:1::1"),
		subnet("10.0.2.0/24", "net-b", "10.0.2.1"),
		subnet("2001:db8:2::/64", "net-b", "2001:db8:2::1"),
		subnet("2001:db8:3::/64", "net-b", "2001:db8:3::1"), // a second IPv6 claim on net-b
		subnet("2001:db8:4::/64", "net-c", "2001:db8:4::1"), // net-c has no IPv4 subnet
		subnet("10.0.5.0/24", "net-e", "10.0.5.1"),
		subnet("2001:db8:5::/64", "net-e", ""), // no gateway
	})
	got := map[string]string{}
	for _, f := range found {
		got[f.Network] = f.Subnet + " " + f.Subnet6 + " " + f.Gateway6 + " " + f.EfficientIPSubnet6ID
	}
	want := map[string]string{"net-a": "10.0.1.0/24 2001:db8:1::/64 2001:db8:1::1 id 2001:db8:1::/64", "net-b": "10.0.2.0/24   ",
		"net-e": "10.0.5.0/24   "}
	if !maps.Equal(got, want) || len(held) != 0 || len(problems) != 3 {
		t.Fatalf("found = %v, held = %v, problems = %v", got, held, problems)
	}
}
