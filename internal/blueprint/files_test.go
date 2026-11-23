package blueprint

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestUserDataFiles(t *testing.T) {
	refs, err := UserDataFiles("#cloud-config\nwrite_files:\n  - path: /etc/motd\n    content: {{ file \"linux.motd\" | json }}\n" +
		"{{ if .inputs.ca }}ca: {{ file \"pki.ca_bundle\" }}{{ end }}")
	if err != nil {
		t.Fatal(err)
	}
	if want := []SecretRef{{Name: "linux.motd", Line: 4}, {Name: "pki.ca_bundle", Line: 5}}; !slices.Equal(refs, want) {
		t.Fatalf("refs = %+v", refs)
	}
	for _, text := range []string{
		"a: {{ file .inputs.which }}",
		"a: {{ \"linux.motd\" | file }}",
		"a: {{ file \"motd\" }}",
		"a: {{ file \"Linux.motd\" }}",
		"a: {{ file \"linux.motd.text\" }}",
	} {
		if _, err := UserDataFiles(text); err == nil || !strings.Contains(err.Error(), "name the shared variable in quotes") {
			t.Errorf("%q: err = %v", text, err)
		}
		if err := CheckUserData(text); err == nil {
			t.Errorf("CheckUserData takes %q", text)
		}
	}
	if err := CheckUserData("a: {{ file \"linux.motd\" }}"); err != nil {
		t.Errorf("CheckUserData refuses a file printed as it is: %v", err)
	}
}

func TestBlueprintFiles(t *testing.T) {
	raw := json.RawMessage(`{"kind": "vm", "components": [
		{"key": "web", "guest": {"os_family": "linux", "cloud_init": {"user_data": "#cloud-config\na: {{ file \"linux.motd\" }}"}}},
		{"key": "db", "guest": {"os_family": "linux"}}]}`)
	var b Blueprint
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if refs := b.Files(); len(refs) != 1 || refs[0] != (SecretRef{Name: "linux.motd", Pointer: "/components/0/guest/cloud_init/user_data", Line: 2}) {
		t.Fatalf("files = %+v", refs)
	}
}
