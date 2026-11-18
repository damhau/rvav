package provision

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

// TestCloudbaseMetadata: the network as cloud-init's, and the user
// cloudbase-init makes, with its key or password (ADR-0039).
func TestCloudbaseMetadata(t *testing.T) {
	nics := []ciNIC{{MAC: "00:50:56:AA:BB:CC", IP: "10.0.0.5", PrefixLength: 24, Gateway: "10.0.0.1", DNSServers: []string{"10.0.0.2"}}, {MAC: "00:50:56:AA:BB:CD"}}
	raw, err := cloudbaseMetadata("dep/win01", "win01", "corp.example", nics, adminUser{username: "ops", keys: []string{testKey}})
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Instance string  `json:"instance-id"`
		Local    string  `json:"local-hostname"`
		User     string  `json:"admin-username"`
		Password *string `json:"admin-password"`
		Keys     string  `json:"public-keys-data"`
		Network  struct {
			Version   int
			Ethernets map[string]struct {
				Match     struct{ Macaddress string }
				Addresses []string
				Routes    []struct{ To, Via string }
				DHCP4     bool `json:"dhcp4"`
			}
		}
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	nic0, nic1 := m.Network.Ethernets["nic0"], m.Network.Ethernets["nic1"]
	if m.Instance != "dep/win01" || m.Local != "win01" || m.User != "ops" || m.Password != nil || m.Keys != testKey ||
		m.Network.Version != 2 || nic0.Match.Macaddress != "00:50:56:aa:bb:cc" || len(nic0.Addresses) != 1 ||
		nic0.Addresses[0] != "10.0.0.5/24" || len(nic0.Routes) != 1 || nic0.Routes[0].To != "default" || !nic1.DHCP4 {
		t.Fatalf("metadata = %s", raw)
	}
	raw, _ = cloudbaseMetadata("dep/win01", "win01", "", nics, adminUser{username: "Administrator", password: "pw"})
	_ = json.Unmarshal(raw, &m)
	if m.User != "Administrator" || m.Password == nil || *m.Password != "pw" {
		t.Fatalf("with a password = %s", raw)
	}
}

// TestCloudbaseUserData: a lone document stays as it is; several go as
// parts named for cloudbase-init to run; with a password, a last part
// writes the metadata again without it.
func TestCloudbaseUserData(t *testing.T) {
	ps := "#ps1_sysnative\nWrite-Host hi"
	if ud, err := cloudbaseUserData("", ps, nil); err != nil || ud != ps {
		t.Fatalf("one document = %q %v", ud, err)
	}
	if ud, _ := cloudbaseUserData("#cloud-config\na: 1", "#cloud-config\nb: 2", nil); !strings.HasPrefix(ud, "#cloud-config\n") ||
		!strings.Contains(ud, "a: 1") || !strings.Contains(ud, "b: 2") {
		t.Fatalf("two cloud-configs = %q", ud)
	}
	cleared := []byte(`{"admin-username":"ops"}`)
	ud, err := cloudbaseUserData("#cloud-config\nset_timezone: UTC", ps, cleared)
	if err != nil {
		t.Fatal(err)
	}
	header, body, _ := strings.Cut(ud, "\r\n\r\n")
	_, params, err := mime.ParseMediaType(strings.TrimPrefix(strings.Split(header, "\r\n")[0], "Content-Type: "))
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(strings.NewReader(body), params["boundary"])
	type got struct{ typ, name, body string }
	var parts []got
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		parts = append(parts, got{strings.Split(p.Header.Get("Content-Type"), ";")[0], p.FileName(), string(b)})
	}
	if len(parts) != 3 || parts[0].typ != "text/cloud-config" || parts[0].name != "" ||
		parts[1].typ != "text/x-shellscript" || parts[1].name != "part-002.ps1" || parts[1].body != ps ||
		parts[2].name != "part-003.ps1" || !strings.HasPrefix(parts[2].body, "#ps1_sysnative\n") {
		t.Fatalf("parts = %+v", parts)
	}
	want := "info-set guestinfo.metadata " + base64.StdEncoding.EncodeToString(cleared)
	if !strings.Contains(parts[2].body, `\VMware\VMware Tools\rpctool.exe" "`+want+`"`) {
		t.Fatalf("the clearing part = %q", parts[2].body)
	}
	for doc, ext := range map[string]string{"rem cmd\necho": ".cmd", "#!/usr/bin/env python3\n": ".py", "#!/bin/bash\n": ".sh", "#cloud-config\n": ""} {
		if got := scriptExt(doc); got != ext {
			t.Errorf("scriptExt(%q) = %q, want %q", doc, got, ext)
		}
	}
}
