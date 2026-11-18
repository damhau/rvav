package provision

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCloudInit(t *testing.T) {
	raw, err := cloudInitMetadata("dep_1/web01", "web01", "corp.example", []ciNIC{
		{MAC: "00:50:56:AA:00:01", IP: "10.31.10.5", PrefixLength: 24, Gateway: "10.31.10.1", DNSServers: []string{"10.0.0.53"}},
		{MAC: "00:50:56:aa:00:02"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Hostname string `json:"hostname"`
		Local    string `json:"local-hostname"`
		Network  struct {
			Version   int `json:"version"`
			Ethernets map[string]struct {
				Match       map[string]string
				Addresses   []string
				DHCP4       bool `json:"dhcp4"`
				Routes      []map[string]string
				Nameservers struct{ Addresses, Search []string }
			}
		}
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	nic0, nic1 := m.Network.Ethernets["nic0"], m.Network.Ethernets["nic1"]
	if m.Hostname != "web01.corp.example" || m.Local != "web01" || m.Network.Version != 2 ||
		nic0.Match["macaddress"] != "00:50:56:aa:00:01" || nic0.Addresses[0] != "10.31.10.5/24" || nic0.Routes[0]["via"] != "10.31.10.1" ||
		nic0.Nameservers.Search[0] != "corp.example" || !nic1.DHCP4 || len(nic1.Routes) != 0 {
		t.Fatalf("metadata = %s", raw)
	}

	raw, err = cloudInitMetadata("dep_1/web01", "web01", "corp.example", []ciNIC{
		{MAC: "00:50:56:aa:00:01", IP: "10.31.10.5", PrefixLength: 24, Gateway: "10.31.10.1",
			IP6: "2001:db8:310::5", PrefixLength6: 64, Gateway6: "2001:db8:310::1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Network.Ethernets = nil
	_ = json.Unmarshal(raw, &m)
	if nic0 := m.Network.Ethernets["nic0"]; strings.Join(nic0.Addresses, " ") != "10.31.10.5/24 2001:db8:310::5/64" || len(nic0.Routes) != 2 ||
		nic0.Routes[1]["to"] != "::/0" || nic0.Routes[1]["via"] != "2001:db8:310::1" {
		t.Fatalf("dual-stack metadata = %s", raw)
	}

	secret := func(name string) (string, error) {
		if name == "app-pw" {
			return `p"w: 1`, nil
		}
		return "", errors.New("There is no secret " + name)
	}
	user, err := renderUserData("#cloud-config\nfqdn: {{ .vm.fqdn }}\nfinal_message: {{ .inputs.role }}\npassword: {{ secret \"app-pw\" | json }}",
		map[string]any{"vm": map[string]any{"fqdn": "web01.corp.example"}, "inputs": map[string]any{"role": "web"}}, secret)
	if err != nil || user != "#cloud-config\nfqdn: web01.corp.example\nfinal_message: web\npassword: \"p\\\"w: 1\"" {
		t.Fatalf("user data = %q, %v", user, err)
	}
	if _, err := renderUserData("{{ .inputs.missing }}", map[string]any{"inputs": map[string]any{}}, secret); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("a missing input = %v", err)
	}
	if _, err := renderUserData(`{{ secret "gone" }}`, nil, secret); err == nil || !strings.Contains(err.Error(), "There is no secret gone") {
		t.Fatalf("a missing secret = %v", err)
	}
	gi := guestInfo(raw, user)
	if dec, _ := base64.StdEncoding.DecodeString(gi["guestinfo.userdata"]); string(dec) != user || gi["guestinfo.metadata.encoding"] != "base64" {
		t.Fatalf("guestinfo = %v", gi)
	}
}
