package provision

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/blueprint"
)

// cloud-init through guestinfo (ADR-0024): cloud-init's VMware data source
// reads the metadata (host name and network) and the user data from the
// VM's guestinfo keys.

// ciNIC is one of the VM's adapters as cloud-init sets it up: by its MAC
// address, with a fixed address (the first with the gateway) or DHCP.
type ciNIC struct {
	MAC          string
	IP           string
	PrefixLength int
	Gateway      string
	DNSServers   []string
}

// cloudInitMetadata is the metadata document: the instance, its host name
// and its network, version 2.
func cloudInitMetadata(instanceID, hostname, domain string, nics []ciNIC) ([]byte, error) {
	ethernets := map[string]any{}
	for i, n := range nics {
		eth := map[string]any{"match": map[string]any{"macaddress": strings.ToLower(n.MAC)}, "set-name": fmt.Sprintf("eth%d", i)}
		if n.IP == "" {
			eth["dhcp4"] = true
		} else {
			eth["addresses"] = []string{fmt.Sprintf("%s/%d", n.IP, n.PrefixLength)}
			if n.Gateway != "" {
				eth["routes"] = []any{map[string]any{"to": "default", "via": n.Gateway}}
			}
			ns := map[string]any{"addresses": nonNil(n.DNSServers)}
			if domain != "" {
				ns["search"] = []string{domain}
			}
			eth["nameservers"] = ns
		}
		ethernets[fmt.Sprintf("nic%d", i)] = eth
	}
	fqdn := hostname
	if domain != "" {
		fqdn += "." + domain
	}
	return json.Marshal(map[string]any{
		"instance-id": instanceID, "local-hostname": hostname, "hostname": fqdn,
		"network": map[string]any{"version": 2, "ethernets": ethernets},
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// renderUserData renders a blueprint's user data template over what the
// VM and its request are; a value it names that isn't there is an error.
func renderUserData(text string, vars map[string]any) (string, error) {
	t, err := blueprint.ParseUserData(text)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, vars); err != nil {
		return "", err
	}
	return b.String(), nil
}

// guestInfo is what cloud-init reads: both documents, base64.
func guestInfo(metadata []byte, userData string) map[string]string {
	enc := base64.StdEncoding.EncodeToString
	return map[string]string{
		"guestinfo.metadata": enc(metadata), "guestinfo.metadata.encoding": "base64",
		"guestinfo.userdata": enc([]byte(userData)), "guestinfo.userdata.encoding": "base64",
	}
}
