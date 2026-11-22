package vcsim_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/vcsim"
)

// TestCloudInitGuests: VMs set up through guestinfo, as rvav sets up those
// with cloud-init, get the addresses their metadata gives once powered on,
// in each encoding cloud-init reads; a VM without metadata gets none.
func TestCloudInitGuests(t *testing.T) {
	m := simulator.VPX()
	defer m.Remove()
	if err := m.Create(); err != nil {
		t.Fatal(err)
	}
	vcsim.CloudInitGuests(m.Map())
	s := m.Service.NewServer()
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := govmomi.NewClient(ctx, s.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	vms, err := find.NewFinder(c.Client, true).VirtualMachineList(ctx, "/DC0/vm/*")
	if err != nil || len(vms) < 4 {
		t.Fatalf("vms = %d, %v", len(vms), err)
	}
	static, dhcp, fallback, bare := vms[0], vms[1], vms[2], vms[3]

	guest := func(vm *object.VirtualMachine) *types.GuestInfo {
		t.Helper()
		var o mo.VirtualMachine
		if err := vm.Properties(ctx, vm.Reference(), []string{"guest"}, &o); err != nil {
			t.Fatal(err)
		}
		return o.Guest
	}
	do := func(tk *object.Task, err error) {
		t.Helper()
		if err == nil {
			err = tk.Wait(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	setMetadata := func(vm *object.VirtualMachine, encoding, doc string) {
		t.Helper()
		do(vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{ExtraConfig: []types.BaseOptionValue{
			&types.OptionValue{Key: "guestinfo.metadata", Value: doc},
			&types.OptionValue{Key: "guestinfo.metadata.encoding", Value: encoding},
		}}))
	}
	waitIP := func(vm *object.VirtualMachine) string {
		t.Helper()
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		ip, err := vm.WaitForIP(wctx, true)
		if err != nil {
			t.Fatalf("%s: no guest IP: %v", vm.Name(), err)
		}
		return ip
	}
	restart := func(vm *object.VirtualMachine) {
		t.Helper()
		do(vm.PowerOff(ctx))
		do(vm.PowerOn(ctx))
	}
	boot := func(vm *object.VirtualMachine) string {
		t.Helper()
		restart(vm)
		return waitIP(vm)
	}
	for _, vm := range vms[:4] {
		if ip := guest(vm).IpAddress; ip != "" {
			t.Fatalf("%s has the guest IP %s already", vm.Name(), ip)
		}
	}

	// The bare VM boots first; nothing ever gives it an address.
	restart(bare)
	restart(fallback)

	// rvav's metadata: JSON, base64, each adapter matched by MAC address.
	mac := guest(static).Net[0].MacAddress
	setMetadata(static, "base64", base64.StdEncoding.EncodeToString(fmt.Appendf(nil,
		`{"instance-id":"vm-1","network":{"version":2,"ethernets":{"nic0":{"match":{"macaddress":%q},"set-name":"eth0",`+
			`"addresses":["2001:db8:310::5/64","10.31.10.5/24"]}}}}`, mac)))
	if ip := boot(static); ip != "10.31.10.5" {
		t.Errorf("static: guest IP %s, want the IPv4 address its metadata gives", ip)
	}
	if net := guest(static).Net[0]; !slices.Equal(net.IpAddress, []string{"2001:db8:310::5", "10.31.10.5"}) {
		t.Errorf("static: adapter addresses %v", net.IpAddress)
	}

	// YAML, gzip: DHCP with an IPv6 address beside it, found by name.
	var z bytes.Buffer
	w := gzip.NewWriter(&z)
	fmt.Fprint(w, "network:\n  version: 2\n  ethernets:\n    nic0:\n      dhcp4: true\n      addresses: [\"2001:db8:320::9/64\"]\n")
	_ = w.Close()
	setMetadata(dhcp, "gzip+base64", base64.StdEncoding.EncodeToString(z.Bytes()))
	lease := boot(dhcp)
	if a, err := netip.ParseAddr(lease); err != nil || !vcsim.DHCPLeases.Contains(a) {
		t.Errorf("dhcp: guest IP %s, want one of %s", lease, vcsim.DHCPLeases)
	}
	if net := guest(dhcp).Net[0]; !slices.Equal(net.IpAddress, []string{lease, "2001:db8:320::9"}) {
		t.Errorf("dhcp: adapter addresses %v", net.IpAddress)
	}

	if ip := guest(bare).IpAddress; ip != "" {
		t.Errorf("bare: guest IP %s without metadata", ip)
	}

	// Metadata without a network, given while the VM runs: cloud-init's
	// fallback, DHCP on the first adapter.
	setMetadata(fallback, "", `{"instance-id": "vm-3", "local-hostname": "vm-3"}`)
	ip := waitIP(fallback)
	if a, err := netip.ParseAddr(ip); err != nil || !vcsim.DHCPLeases.Contains(a) || ip == lease {
		t.Errorf("fallback: guest IP %s, want another lease than %s", ip, lease)
	}
}
