package vcsim

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"sync"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"go.yaml.in/yaml/v3"
)

// DHCPLeases is where CloudInitGuests takes the address of an adapter on
// DHCP.
var DHCPLeases = netip.MustParsePrefix("10.99.0.0/16")

var guestsRef = types.ManagedObjectReference{Type: "RvavCloudInitGuests", Value: "cloud-init-guests"}

// CloudInitGuests has each VM's guest report the addresses cloud-init (or
// cloudbase-init) gives it, as VMware Tools does on a vCenter. vcsim sets
// a guest IP only through guest customization, which a VM set up through
// guestinfo doesn't get, so rvav would wait for its IP forever (rvav#188).
//
// A powered-on VM with guestinfo.metadata and no guest IP gets, on each
// adapter, the static addresses the metadata's network configuration
// gives it, or an address from DHCPLeases when the adapter is on DHCP. Its
// guest IP is the first IPv4 address. Call it before the simulator serves
// requests.
func CloudInitGuests(r *simulator.Registry) {
	r.AddHandler(&guests{r: r, leases: map[string]netip.Addr{}})
}

type guests struct {
	r      *simulator.Registry
	mu     sync.Mutex
	leases map[string]netip.Addr
}

func (*guests) Reference() types.ManagedObjectReference                       { return guestsRef }
func (*guests) PutObject(*simulator.Context, mo.Reference)                    {}
func (*guests) RemoveObject(*simulator.Context, types.ManagedObjectReference) {}

// UpdateObject runs while the VM's lock is held, so the guest boots in a
// goroutine of its own once the change is done.
func (g *guests) UpdateObject(_ *simulator.Context, o mo.Reference, changes []types.PropertyChange) {
	ref := o.Reference()
	if ref.Type != "VirtualMachine" {
		return
	}
	for _, c := range changes {
		if c.Name == "runtime.powerState" || c.Name == "config.extraConfig" {
			go g.boot(ref)
			return
		}
	}
}

func (g *guests) boot(ref types.ManagedObjectReference) {
	vm, ok := g.r.Get(ref).(*simulator.VirtualMachine)
	if !ok {
		return
	}
	ctx := &simulator.Context{Map: g.r}
	g.r.WithLock(ctx, vm, func() {
		if changes := g.report(vm); changes != nil {
			ctx.Update(vm, changes)
		}
	})
}

// report sets the guest's addresses and returns the changes to publish,
// or nil when the guest has nothing to report.
func (g *guests) report(vm *simulator.VirtualMachine) []types.PropertyChange {
	if vm.Runtime.PowerState != types.VirtualMachinePowerStatePoweredOn || vm.Config == nil || vm.Config.Template ||
		vm.Guest == nil || vm.Guest.IpAddress != "" {
		return nil
	}
	meta, ok := readMetadata(vm.Config.ExtraConfig)
	if !ok {
		return nil
	}
	var first, firstV4 string
	for i := range vm.Guest.Net {
		nic := &vm.Guest.Net[i]
		eth, ok := meta.adapter(i, nic.MacAddress)
		if !ok {
			continue
		}
		addrs := eth.static()
		if eth.DHCP4 && !hasV4(addrs) {
			addrs = append([]netip.Addr{g.lease(nic.MacAddress)}, addrs...)
		}
		if len(addrs) == 0 {
			continue
		}
		nic.IpAddress = nil
		for _, a := range addrs {
			nic.IpAddress = append(nic.IpAddress, a.String())
			if first == "" {
				first = a.String()
			}
			if firstV4 == "" && a.Is4() {
				firstV4 = a.String()
			}
		}
	}
	ip := firstV4
	if ip == "" {
		ip = first
	}
	if ip == "" {
		return nil
	}
	return []types.PropertyChange{
		{Name: "guest.ipAddress", Val: ip},
		{Name: "summary.guest.ipAddress", Val: ip},
		{Name: "guest.net", Val: vm.Guest.Net},
	}
}

// lease is the adapter's address from DHCPLeases, the same each time it
// asks, as a DHCP server's lease.
func (g *guests) lease(mac string) netip.Addr {
	g.mu.Lock()
	defer g.mu.Unlock()
	mac = strings.ToLower(mac)
	if a, ok := g.leases[mac]; ok {
		return a
	}
	base := DHCPLeases.Masked().Addr().As4()
	size := uint32(1) << (32 - DHCPLeases.Bits())
	// From .10, as a DHCP pool leaves the first addresses to the network.
	n := (uint32(len(g.leases)) + 10) % size
	var a [4]byte
	binary.BigEndian.PutUint32(a[:], binary.BigEndian.Uint32(base[:])+n)
	g.leases[mac] = netip.AddrFrom4(a)
	return g.leases[mac]
}

// cloudMetadata is the part of cloud-init's metadata the guest's network
// comes from: a network configuration, version 2.
type cloudMetadata struct {
	Network struct {
		Ethernets map[string]ethernet `yaml:"ethernets"`
	} `yaml:"network"`
}

type ethernet struct {
	Match struct {
		MACAddress string `yaml:"macaddress"`
	} `yaml:"match"`
	Addresses []string `yaml:"addresses"`
	DHCP4     bool     `yaml:"dhcp4"`
}

// adapter is the configuration of the VM's adapter i: the one matching
// its MAC address, or else the one rvav names nic<i>. Without any, cloud-
// init puts the first adapter on DHCP.
func (m cloudMetadata) adapter(i int, mac string) (ethernet, bool) {
	if len(m.Network.Ethernets) == 0 {
		return ethernet{DHCP4: true}, i == 0
	}
	for _, e := range m.Network.Ethernets {
		if e.Match.MACAddress != "" && strings.EqualFold(e.Match.MACAddress, mac) {
			return e, true
		}
	}
	e, ok := m.Network.Ethernets[fmt.Sprintf("nic%d", i)]
	return e, ok && e.Match.MACAddress == ""
}

func (e ethernet) static() []netip.Addr {
	var out []netip.Addr
	for _, s := range e.Addresses {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Addr())
		} else if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a)
		}
	}
	return out
}

func hasV4(addrs []netip.Addr) bool {
	for _, a := range addrs {
		if a.Is4() {
			return true
		}
	}
	return false
}

// readMetadata reads guestinfo.metadata as cloud-init's VMware data source
// does, in the encodings it accepts.
func readMetadata(extra []types.BaseOptionValue) (cloudMetadata, bool) {
	info := map[string]string{}
	for _, o := range extra {
		if ov := o.GetOptionValue(); ov != nil {
			if s, ok := ov.Value.(string); ok {
				info[ov.Key] = s
			}
		}
	}
	raw := info["guestinfo.metadata"]
	if raw == "" {
		return cloudMetadata{}, false
	}
	b, err := decode(raw, info["guestinfo.metadata.encoding"])
	if err != nil {
		return cloudMetadata{}, false
	}
	var m cloudMetadata
	if err := yaml.Unmarshal(b, &m); err != nil {
		return cloudMetadata{}, false
	}
	return m, true
}

func decode(s, encoding string) ([]byte, error) {
	switch encoding {
	case "":
		return []byte(s), nil
	case "base64", "b64":
		return base64.StdEncoding.DecodeString(s)
	case "gzip+base64", "gz+b64":
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, err
		}
		z, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(z)
	}
	return nil, fmt.Errorf("unknown encoding %q", encoding)
}
