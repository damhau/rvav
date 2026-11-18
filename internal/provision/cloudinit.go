package provision

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/textproto"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

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

// mergeUserData merges a catalogue template's rendered user data with the
// item's (ADR-0037). Two #cloud-config documents become one: maps merged
// key by key, lists the template's entries then the item's, any other
// value the item's. Otherwise both go as parts of a MIME multipart
// document, the template's first, which cloud-init handles in turn.
func mergeUserData(image, own string) (string, error) {
	switch {
	case image == "":
		return own, nil
	case own == "":
		return image, nil
	case isCloudConfig(image) && isCloudConfig(own):
		var a, b map[string]any
		if err := yaml.Unmarshal([]byte(image), &a); err != nil {
			return "", fmt.Errorf("the template's is not a YAML map: %v", err)
		}
		if err := yaml.Unmarshal([]byte(own), &b); err != nil {
			return "", fmt.Errorf("the item's is not a YAML map: %v", err)
		}
		out, err := yaml.Marshal(mergeConfig(a, b))
		if err != nil {
			return "", err
		}
		return "#cloud-config\n" + string(out), nil
	}
	return multipartUserData(image, own)
}

func isCloudConfig(s string) bool {
	return strings.HasPrefix(strings.TrimLeft(s, " \t\r\n"), "#cloud-config")
}

func mergeConfig(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	maps.Copy(out, a)
	for k, v := range b {
		switch x := out[k].(type) {
		case map[string]any:
			if y, ok := v.(map[string]any); ok {
				out[k] = mergeConfig(x, y)
				continue
			}
		case []any:
			if y, ok := v.([]any); ok {
				out[k] = append(slices.Clip(x), y...)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// multipartUserData puts each document in a part of its own; cloud-init
// names them part-001, part-002 in order.
func multipartUserData(docs ...string) (string, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, d := range docs {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", userDataType(d)+`; charset="utf-8"`)
		h.Set("MIME-Version", "1.0")
		p, err := w.CreatePart(h)
		if err != nil {
			return "", err
		}
		if _, err := io.WriteString(p, d); err != nil {
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("Content-Type: multipart/mixed; boundary=%q\r\nMIME-Version: 1.0\r\n\r\n", w.Boundary()) + b.String(), nil
}

// userDataType is a document's MIME type; cloud-init works out a
// text/plain one's from its first line.
func userDataType(doc string) string {
	switch doc = strings.TrimLeft(doc, " \t\r\n"); {
	case strings.HasPrefix(doc, "#cloud-config"):
		return "text/cloud-config"
	case strings.HasPrefix(doc, "#!"):
		return "text/x-shellscript"
	}
	return "text/plain"
}

// guestInfo is what cloud-init reads: both documents, base64.
func guestInfo(metadata []byte, userData string) map[string]string {
	enc := base64.StdEncoding.EncodeToString
	return map[string]string{
		"guestinfo.metadata": enc(metadata), "guestinfo.metadata.encoding": "base64",
		"guestinfo.userdata": enc([]byte(userData)), "guestinfo.userdata.encoding": "base64",
	}
}
