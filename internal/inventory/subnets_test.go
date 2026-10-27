package inventory

import (
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
