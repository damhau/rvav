package vraimport

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/vra"
)

const accessKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl ops@example"

// accessPlan maps a one-machine template whose machine has the given
// properties, indented as a machine's.
func accessPlan(t *testing.T, image, properties string) (ItemPlan, blueprint.Blueprint) {
	t.Helper()
	content := `formatVersion: 1
inputs:
  sshKey:
    type: string
  dbPassword:
    type: string
    encrypted: true
  plain:
    type: string
resources:
  vm:
    type: Cloud.vSphere.Machine
    properties:
      image: ` + image + `
      flavor: small
` + properties
	e := vra.SampleEstate(time.Now())
	p := MapTemplate(&e, vra.Blueprint{ID: "bp-access", Name: "Access", Versions: []vra.BlueprintVersion{
		{Version: "1", Status: "RELEASED", Content: content}}})
	if p.Doc == nil {
		t.Fatalf("plan = %s %+v", p.Status, p.Notes)
	}
	raw, _ := json.Marshal(p.Doc.Versions[0].Blueprint)
	b, err := blueprint.Parse(raw)
	if err != nil {
		t.Fatalf("blueprint %s: %v", raw, err)
	}
	return p, b
}

// TestMapRemoteAccess: vRA's three kinds of remote access are rvav's
// (ADR-0039); what rvav can't take is left out and said, and a password
// written in the template is never repeated.
func TestMapRemoteAccess(t *testing.T) {
	const where = "resources.vm.remoteAccess"
	for _, c := range []struct {
		name, image, props string
		want               *blueprint.RemoteAccess
		level, at, text    string
	}{
		{name: "a key from an input", image: "rhel9", props: `
      remoteAccess:
        authentication: publicPrivateKey
        username: ops
        sshKey: ${input.sshKey}`,
			want: &blueprint.RemoteAccess{Kind: blueprint.AccessSSHKey, Username: "ops", PublicKey: "${inputs.sshKey}"},
			// Exact: "with" is in every Info note's text.
			level: Info, at: where, text: "sign in as ops with the public key given"},
		{name: "a key written in the template", image: "rhel9", props: `
      remoteAccess:
        authentication: publicPrivateKey
        username: ops
        sshKey: ` + accessKey,
			want: &blueprint.RemoteAccess{Kind: blueprint.AccessSSHKey, Username: "ops", PublicKey: accessKey}},
		{name: "a key from an input the form hasn't", image: "rhel9", props: `
      remoteAccess:
        authentication: publicPrivateKey
        username: ops
        sshKey: ${input.missing}`,
			level: Unmapped, at: where + ".sshKey", text: "or an input that holds it"},
		{name: "a key pair made for each deployment", image: "rhel9", props: `
      remoteAccess:
        authentication: generatedPublicPrivateKey
        username: ops`,
			want:  &blueprint.RemoteAccess{Kind: blueprint.AccessGeneratedKey, Username: "ops"},
			level: Info, at: where, text: "a key pair rvav makes"},
		{name: "a password from an encrypted input", image: "rhel9", props: `
      remoteAccess:
        authentication: usernamePassword
        username: ops
        password: ${input.dbPassword}`,
			want: &blueprint.RemoteAccess{Kind: blueprint.AccessPassword, Username: "ops", Password: "${inputs.dbPassword}"}},
		{name: "a password from a plain input", image: "rhel9", props: `
      remoteAccess:
        authentication: usernamePassword
        username: ops
        password: ${input.plain}`,
			level: Unmapped, at: where + ".password", text: "from a secret input"},
		{name: "a password written in the template", image: "rhel9", props: `
      remoteAccess:
        authentication: usernamePassword
        username: ops
        password: Hunter2-Hunter2`,
			level: Unmapped, at: where + ".password", text: "stored secret"},
		{name: "a key pair named in the cloud account", image: "rhel9", props: `
      remoteAccess:
        authentication: keyPairName
        keyPair: ops-key`,
			level: Unmapped, at: where + ".authentication", text: "keyPairName"},
		{name: "root", image: "rhel9", props: `
      remoteAccess:
        authentication: generatedPublicPrivateKey
        username: root`,
			level: Unmapped, at: where + ".username", text: "User root"},
		{name: "a user name from an input", image: "rhel9", props: `
      remoteAccess:
        authentication: generatedPublicPrivateKey
        username: ${input.plain}`,
			level: Unmapped, at: where + ".username", text: "by hand"},
		{name: "Linux with a customization specification", image: "rhel9", props: `
      customizationSpec: linux-spec
      remoteAccess:
        authentication: generatedPublicPrivateKey
        username: ops`,
			level: Unmapped, at: where, text: "customization specification linux-spec"},
		{name: "Windows' Administrator with a customization specification", image: "win2022", props: `
      customizationSpec: win-spec
      remoteAccess:
        authentication: usernamePassword
        username: Administrator
        password: ${input.dbPassword}`,
			want: &blueprint.RemoteAccess{Kind: blueprint.AccessPassword, Username: "Administrator", Password: "${inputs.dbPassword}"}},
		{name: "a key on Windows", image: "win2022", props: `
      remoteAccess:
        authentication: generatedPublicPrivateKey
        username: ops`,
			want:  &blueprint.RemoteAccess{Kind: blueprint.AccessGeneratedKey, Username: "ops"},
			level: Warning, at: where, text: "cloudbase-init"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, b := accessPlan(t, c.image, c.props)
			got := b.Guest.RemoteAccess
			switch {
			case c.want == nil && got != nil:
				t.Fatalf("remote access = %+v, want none", got)
			case c.want != nil && (got == nil || *got != *c.want):
				t.Fatalf("remote access = %+v, want %+v; notes %+v", got, c.want, p.Notes)
			}
			if c.text != "" && !has(p, c.level, c.at, c.text) {
				t.Errorf("no %s note at %s with %q in %+v", c.level, c.at, c.text, p.Notes)
			}
			if c.want == nil && p.Status == Complete {
				t.Error("left out, and the item is complete")
			}
			for _, n := range p.Notes {
				if strings.Contains(n.Text, "Hunter2") {
					t.Errorf("a note repeats the password: %+v", n)
				}
			}
		})
	}
}

// TestMapAddressing: DHCP and an IPv6 address on a machine's first adapter
// are rvav's ipam: dhcp and ipv6 (ADR-0039); vRA's other adapter settings,
// and those rvav has on the first adapter only, are left out and said.
func TestMapAddressing(t *testing.T) {
	const where = "resources.vm.networks"
	nets := func(s string) string {
		return "      networks:\n" + s + `
  app:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
  seg:
    type: Cloud.NSX.Network
    properties:
      networkType: routed
`
	}
	for _, c := range []struct {
		name, props     string
		ipam            string
		ipv6            bool
		level, at, text string
	}{
		{name: "DHCP", props: nets(`        - network: ${resource.app.id}
          assignment: dynamic`),
			ipam: blueprint.IPAMDHCP, level: Info, at: where + "[0].assignment", text: "DHCP"},
		{name: "static", props: nets(`        - network: ${resource.app.id}
          assignment: static
          deviceIndex: 0`)},
		{name: "IPv6", props: nets(`        - network: ${resource.app.id}
          assignIPv6Address: true`),
			ipv6: true, level: Info, at: where + "[0].assignIPv6Address", text: "AAAA"},
		{name: "IPv6 with DHCP", props: nets(`        - network: ${resource.app.id}
          assignment: dynamic
          assignIPv6Address: true`),
			ipam: blueprint.IPAMDHCP, level: Unmapped, at: where + "[0].assignIPv6Address", text: "not with DHCP"},
		{name: "IPv6 on a second adapter", props: nets(`        - network: ${resource.app.id}
        - network: ${resource.app.id}
          assignIPv6Address: true`),
			level: Unmapped, at: where + "[1].assignIPv6Address", text: "first adapter only"},
		{name: "DHCP on a second adapter", props: nets(`        - network: ${resource.app.id}
        - network: ${resource.app.id}
          assignment: dynamic`),
			level: Info, at: where + "[1].assignment", text: "ipam: dhcp when you add it to nics"},
		{name: "DHCP on a segment", props: nets(`        - network: ${resource.seg.id}
          assignment: dynamic`),
			level: Unmapped, at: where + "[0].assignment", text: "no DHCP"},
		{name: "IPv6 on a segment", props: nets(`        - network: ${resource.seg.id}
          assignIPv6Address: true`),
			level: Unmapped, at: where + "[0].assignIPv6Address", text: "IPv4 only"},
		{name: "a fixed address", props: nets(`        - network: ${resource.app.id}
          address: 10.31.10.50`),
			level: Unmapped, at: where + "[0].address", text: "10.31.10.50"},
		{name: "a MAC address", props: nets(`        - network: ${resource.app.id}
          macAddress: 00:50:56:01:02:03`),
			level: Unmapped, at: where + "[0].macAddress", text: "no rvav equivalent"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, b := accessPlan(t, "rhel9", c.props)
			if b.IPAM != c.ipam || (b.IPv6 != nil && *b.IPv6) != c.ipv6 {
				t.Fatalf("ipam %q, ipv6 %v; want %q, %v; notes %+v", b.IPAM, b.IPv6, c.ipam, c.ipv6, p.Notes)
			}
			if c.text != "" && !has(p, c.level, c.at, c.text) {
				t.Errorf("no %s note at %s with %q in %+v", c.level, c.at, c.text, p.Notes)
			}
			if c.text == "" {
				for _, n := range p.Notes {
					if strings.HasPrefix(n.Where, where) {
						t.Errorf("a note on the adapters: %+v", n)
					}
				}
			}
		})
	}
}
