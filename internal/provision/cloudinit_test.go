package provision

import (
	"encoding/base64"
	"encoding/json"
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

	user, err := renderUserData("#cloud-config\nfqdn: {{ .vm.fqdn }}\nfinal_message: {{ .inputs.role }}", map[string]any{
		"vm": map[string]any{"fqdn": "web01.corp.example"}, "inputs": map[string]any{"role": "web"}})
	if err != nil || user != "#cloud-config\nfqdn: web01.corp.example\nfinal_message: web" {
		t.Fatalf("user data = %q, %v", user, err)
	}
	if _, err := renderUserData("{{ .inputs.missing }}", map[string]any{"inputs": map[string]any{}}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("a missing input = %v", err)
	}
	gi := guestInfo(raw, user)
	if dec, _ := base64.StdEncoding.DecodeString(gi["guestinfo.userdata"]); string(dec) != user || gi["guestinfo.metadata.encoding"] != "base64" {
		t.Fatalf("guestinfo = %v", gi)
	}
}
