package vsphere

import (
	"testing"

	"github.com/vmware/govmomi/vim25/types"
)

// A Windows VM joins the domain with the account given, in its OU, with its
// local administrator's password; a vCenter specification is the base, and
// rvav sets the name and adapters on it (ADR-0024).
func TestCustomizationSpecWindows(t *testing.T) {
	base := &types.CustomizationSpec{
		Identity: &types.CustomizationSysprep{
			UserData:       types.CustomizationUserData{FullName: "Ops", OrgName: "Corp", ProductId: "KEY"},
			GuiUnattended:  types.CustomizationGuiUnattended{TimeZone: 4},
			Identification: types.CustomizationIdentification{JoinWorkgroup: "WORKGROUP"},
		},
		NicSettingMap: []types.CustomizationAdapterMapping{{Adapter: types.CustomizationIPSettings{Ip: &types.CustomizationDhcpIpGenerator{}}}},
	}
	cu := Customization{OSFamily: "windows", Hostname: "web-prd-db01", Domain: "corp.example", IP: "10.21.30.5", PrefixLength: 24,
		Gateway: "10.21.30.1", DNSServers: []string{"10.0.0.53"}, AdminPassword: "local-secret", JoinDomain: "corp.example",
		DomainOU: "OU=Servers,DC=corp,DC=example", DomainUser: "svc-join", DomainPassword: "join-secret",
		Extra: []NICSetting{{IP: "10.90.0.5", PrefixLength: 16}, {DHCP: true}}, SearchDomains: []string{"svc.corp.example", "corp.example"}}
	spec := CustomizationSpec(cu, base)
	if got := spec.GlobalIPSettings.DnsSuffixList; len(got) != 2 || got[0] != "corp.example" || got[1] != "svc.corp.example" {
		t.Fatalf("DNS suffixes = %v", got)
	}
	sp := spec.Identity.(*types.CustomizationSysprep)
	if sp.UserData.ComputerName.(*types.CustomizationFixedName).Name != "web-prd-db01" || sp.UserData.ProductId != "KEY" ||
		sp.GuiUnattended.TimeZone != 4 || sp.GuiUnattended.Password == nil || sp.GuiUnattended.Password.Value != "local-secret" {
		t.Fatalf("sysprep = %+v", sp)
	}
	id := sp.Identification
	if id.JoinDomain != "corp.example" || id.JoinWorkgroup != "" || id.DomainAdmin != "svc-join" || id.DomainAdminPassword == nil ||
		id.DomainAdminPassword.Value != "join-secret" || !id.DomainAdminPassword.PlainText || id.DomainOU != "OU=Servers,DC=corp,DC=example" {
		t.Fatalf("identification = %+v", id)
	}
	if len(spec.NicSettingMap) != 3 || spec.NicSettingMap[0].Adapter.Gateway[0] != "10.21.30.1" ||
		spec.NicSettingMap[1].Adapter.Ip.(*types.CustomizationFixedIp).IpAddress != "10.90.0.5" || len(spec.NicSettingMap[1].Adapter.Gateway) != 0 {
		t.Fatalf("adapters = %+v", spec.NicSettingMap)
	}
	if _, ok := spec.NicSettingMap[2].Adapter.Ip.(*types.CustomizationDhcpIpGenerator); !ok {
		t.Fatal("the DHCP adapter has a fixed address")
	}
	// The base itself is left as it was.
	if base.Identity.(*types.CustomizationSysprep).UserData.ComputerName != nil {
		t.Fatal("the base specification was changed")
	}

	// Without a base: rvav's own, in a workgroup unless it joins.
	plain := CustomizationSpec(Customization{OSFamily: "windows", Hostname: "x", TimeZone: "85"}, nil).Identity.(*types.CustomizationSysprep)
	if plain.Identification.JoinWorkgroup != "WORKGROUP" || plain.GuiUnattended.TimeZone != 85 || plain.GuiUnattended.Password != nil {
		t.Fatalf("own sysprep = %+v", plain)
	}
}

// A first adapter on DHCP gets no address, gateway or DNS servers: DHCP
// gives them (ADR-0039).
func TestCustomizationSpecDHCP(t *testing.T) {
	spec := CustomizationSpec(Customization{OSFamily: "linux", Hostname: "web-dev-app01", Domain: "corp.example", DHCP: true,
		Extra: []NICSetting{{IP: "10.9.0.4", PrefixLength: 24}}}, nil)
	first := spec.NicSettingMap[0].Adapter
	if _, ok := first.Ip.(*types.CustomizationDhcpIpGenerator); !ok || first.Gateway != nil || first.SubnetMask != "" ||
		first.DnsDomain != "corp.example" || len(spec.GlobalIPSettings.DnsServerList) != 0 {
		t.Fatalf("first adapter = %+v, global = %+v", first, spec.GlobalIPSettings)
	}
	if ip, ok := spec.NicSettingMap[1].Adapter.Ip.(*types.CustomizationFixedIp); !ok || ip.IpAddress != "10.9.0.4" {
		t.Fatalf("added adapter = %+v", spec.NicSettingMap[1].Adapter)
	}
}

// A first adapter with an IPv6 address gets it beside its IPv4 one, with
// its own gateway (ADR-0039).
func TestCustomizationSpecIPv6(t *testing.T) {
	spec := CustomizationSpec(Customization{OSFamily: "linux", Hostname: "web-dev-app01", Domain: "corp.example",
		IP: "10.31.10.5", PrefixLength: 24, Gateway: "10.31.10.1", IP6: "2001:db8:310::5", PrefixLength6: 64, Gateway6: "2001:db8:310::1"}, nil)
	first := spec.NicSettingMap[0].Adapter
	if ip, ok := first.Ip.(*types.CustomizationFixedIp); !ok || ip.IpAddress != "10.31.10.5" || first.IpV6Spec == nil {
		t.Fatalf("first adapter = %+v", first)
	}
	v6, ok := first.IpV6Spec.Ip[0].(*types.CustomizationFixedIpV6)
	if !ok || v6.IpAddress != "2001:db8:310::5" || v6.SubnetMask != 64 || first.IpV6Spec.Gateway[0] != "2001:db8:310::1" {
		t.Fatalf("IPv6 = %+v", first.IpV6Spec)
	}
	if spec := CustomizationSpec(Customization{OSFamily: "linux", Hostname: "web-dev-app01", IP: "10.31.10.5", PrefixLength: 24}, nil); spec.NicSettingMap[0].Adapter.IpV6Spec != nil {
		t.Fatal("an IPv4 adapter has IPv6")
	}
}

func TestCustomizationSpecLinux(t *testing.T) {
	base := &types.CustomizationSpec{Identity: &types.CustomizationLinuxPrep{TimeZone: "UTC", Domain: "old.example"}}
	lp := CustomizationSpec(Customization{OSFamily: "linux", Hostname: "web-dev-app01", Domain: "corp.example", IP: "10.0.0.2"}, base).
		Identity.(*types.CustomizationLinuxPrep)
	if lp.HostName.(*types.CustomizationFixedName).Name != "web-dev-app01" || lp.Domain != "corp.example" || lp.TimeZone != "UTC" {
		t.Fatalf("linux prep = %+v", lp)
	}
}
