package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/store"
)

// TestCheckRemoteAccess: a remote access is a user with a key from the
// blueprint, an input or a stored secret, a generated key, or a password
// from a secret input or a stored secret (ADR-0039).
func TestCheckRemoteAccess(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " ops@laptop"
	yes := true
	fields := map[string]api.UiField{"ssh_key": {Widget: api.UiWidgetTextarea}, "pw": {Widget: api.UiWidgetSecret},
		"pw2": {Widget: api.UiWidgetText, Secret: &yes}, "role": {Widget: api.UiWidgetText}}
	check := func(guest map[string]any, components ...map[string]any) []string {
		t.Helper()
		bp := map[string]any{"kind": "vm", "template": "rhel9", "guest": guest}
		if len(components) > 0 {
			bp["components"] = components
		}
		raw, _ := json.Marshal(bp)
		b, err := blueprint.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range checkRemoteAccess(b, fields, "/blueprint") {
			out = append(out, e.Field+" "+*e.Code)
		}
		slices.Sort(out)
		return out
	}
	linux := func(ra map[string]any) map[string]any {
		return map[string]any{"os_family": "linux", "remote_access": ra}
	}
	for _, ra := range []map[string]any{
		{"kind": "ssh_key", "username": "ops", "public_key": key},
		{"kind": "ssh_key", "username": "ops", "public_key": "${inputs.ssh_key}"},
		{"kind": "ssh_key", "username": "ops", "secret": "ops-key"},
		{"kind": "generated_key", "username": "ops"},
		{"kind": "password", "username": "ops", "password": "${inputs.pw}"},
		{"kind": "password", "username": "ops", "password": "${ inputs.pw2 }"},
		{"kind": "password", "username": "ops", "secret": "ops-pw"},
	} {
		if errs := check(linux(ra)); len(errs) > 0 {
			t.Errorf("%v: %v", ra, errs)
		}
	}
	for _, c := range []struct {
		ra   map[string]any
		want []string
	}{
		{map[string]any{"kind": "telnet"}, []string{"/blueprint/guest/remote_access/kind enum", "/blueprint/guest/remote_access/username required"}},
		{map[string]any{"kind": "ssh_key", "username": "root"}, []string{"/blueprint/guest/remote_access/public_key required",
			"/blueprint/guest/remote_access/username invalid"}},
		{map[string]any{"kind": "ssh_key", "username": "Ops", "public_key": "ssh-rsa nope"}, []string{
			"/blueprint/guest/remote_access/public_key invalid", "/blueprint/guest/remote_access/username invalid"}},
		{map[string]any{"kind": "ssh_key", "username": "ops", "public_key": "${inputs.nokey}", "password": "x"}, []string{
			"/blueprint/guest/remote_access/password invalid", "/blueprint/guest/remote_access/public_key unknown_input"}},
		{map[string]any{"kind": "ssh_key", "username": "ops", "public_key": "key ${inputs.role}"}, []string{"/blueprint/guest/remote_access/public_key invalid"}},
		{map[string]any{"kind": "ssh_key", "username": "ops", "public_key": key, "secret": "k"}, []string{"/blueprint/guest/remote_access/secret invalid"}},
		{map[string]any{"kind": "generated_key", "username": "ops", "secret": "k"}, []string{"/blueprint/guest/remote_access/secret invalid"}},
		{map[string]any{"kind": "password", "username": "ops"}, []string{"/blueprint/guest/remote_access/password required"}},
		{map[string]any{"kind": "password", "username": "ops", "password": "${inputs.role}"}, []string{"/blueprint/guest/remote_access/password invalid"}},
		{map[string]any{"kind": "password", "username": "ops", "password": "hunter2"}, []string{"/blueprint/guest/remote_access/password invalid"}},
	} {
		if got := check(linux(c.ra)); !slices.Equal(got, c.want) {
			t.Errorf("%v = %v, want %v", c.ra, got, c.want)
		}
	}

	// Windows, or a Linux VM on a customization specification.
	if got := check(map[string]any{"os_family": "windows", "remote_access": map[string]any{"kind": "password", "username": "ops", "secret": "pw"}}); !slices.Equal(got,
		[]string{"/blueprint/guest/remote_access invalid"}) {
		t.Errorf("Windows = %v", got)
	}
	if got := check(map[string]any{"os_family": "linux", "customization_spec": "lin", "remote_access": map[string]any{"kind": "generated_key", "username": "ops"}}); !slices.Equal(got,
		[]string{"/blueprint/guest/remote_access invalid"}) {
		t.Errorf("a specification = %v", got)
	}

	// A component's own, or the blueprint's said once for all.
	got := check(map[string]any{"os_family": "linux", "remote_access": map[string]any{"kind": "password", "username": "ops"}},
		map[string]any{"key": "web"}, map[string]any{"key": "app"},
		map[string]any{"key": "db", "guest": map[string]any{"remote_access": map[string]any{"kind": "generated_key", "username": "-x"}}})
	if !slices.Equal(got, []string{"/blueprint/components/2/guest/remote_access/username invalid", "/blueprint/guest/remote_access/password required"}) {
		t.Errorf("components = %v", got)
	}

	// The template the form chose decides at request time.
	g := blueprint.Guest{OSFamily: "windows", RemoteAccess: &blueprint.RemoteAccess{Kind: "generated_key", Username: "ops"}}
	if msg := accessProblem(g, &store.VMTemplate{Label: "Windows Server 2025"}); !strings.HasPrefix(msg, "Windows Server 2025 is a Windows template") {
		t.Errorf("Windows template: %q", msg)
	}
	g.OSFamily, g.CustomizationSpec = "linux", "rhel-spec"
	if msg := accessProblem(g, nil); !strings.Contains(msg, "customization specification rhel-spec") {
		t.Errorf("a specification: %q", msg)
	}
	g.CustomizationSpec = ""
	if msg := accessProblem(g, nil); msg != "" {
		t.Errorf("Linux by cloud-init: %q", msg)
	}
}
