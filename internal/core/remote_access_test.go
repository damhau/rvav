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

	// Windows: any kind, through cloudbase-init, or Administrator's
	// password on a customization specification; never with
	// admin_password. A Linux VM on a specification has none.
	windows := func(ra map[string]any, more ...string) map[string]any {
		g := map[string]any{"os_family": "windows", "remote_access": ra}
		for i := 0; i < len(more); i += 2 {
			g[more[i]] = more[i+1]
		}
		return g
	}
	for _, c := range []struct {
		guest map[string]any
		want  []string
	}{
		{windows(map[string]any{"kind": "generated_key", "username": "Ops.Admin"}), nil},
		{windows(map[string]any{"kind": "password", "username": "ops", "secret": "pw"}), nil},
		{windows(map[string]any{"kind": "password", "username": "administrator", "secret": "pw"}, "customization_spec", "win"), nil},
		{windows(map[string]any{"kind": "password", "username": "ops", "secret": "pw"}, "customization_spec", "win"),
			[]string{"/blueprint/guest/remote_access/username invalid"}},
		{windows(map[string]any{"kind": "ssh_key", "username": "ops", "secret": "k"}, "customization_spec", "win"),
			[]string{"/blueprint/guest/remote_access/kind invalid"}},
		{windows(map[string]any{"kind": "generated_key", "username": "ops"}, "admin_password", "win-admin"),
			[]string{"/blueprint/guest/remote_access invalid"}},
		{windows(map[string]any{"kind": "generated_key", "username": "a-name-of-21-letters!"}),
			[]string{"/blueprint/guest/remote_access/username invalid"}},
		{map[string]any{"os_family": "linux", "customization_spec": "lin", "remote_access": map[string]any{"kind": "generated_key", "username": "ops"}},
			[]string{"/blueprint/guest/remote_access invalid"}},
	} {
		if got := check(c.guest); !slices.Equal(got, c.want) {
			t.Errorf("%v = %v, want %v", c.guest, got, c.want)
		}
	}

	// A component's own, or the blueprint's said once for all.
	got := check(map[string]any{"os_family": "linux", "remote_access": map[string]any{"kind": "password", "username": "ops"}},
		map[string]any{"key": "web"}, map[string]any{"key": "app"},
		map[string]any{"key": "db", "guest": map[string]any{"remote_access": map[string]any{"kind": "generated_key", "username": "-x"}}})
	if !slices.Equal(got, []string{"/blueprint/components/2/guest/remote_access/username invalid", "/blueprint/guest/remote_access/password required"}) {
		t.Errorf("components = %v", got)
	}

	// The template the form chose decides at request time: whether it uses
	// cloudbase-init, and its customization specification.
	cbi := &store.VMTemplate{Label: "Windows Server 2025 (cloudbase-init)", CloudbaseInit: true}
	sysprep := &store.VMTemplate{Label: "Windows Server 2025"}
	genKey := &blueprint.RemoteAccess{Kind: "generated_key", Username: "ops"}
	admin := &blueprint.RemoteAccess{Kind: "password", Username: "Administrator", Secret: "pw"}
	for _, c := range []struct {
		g     blueprint.Guest
		image *store.VMTemplate
		want  string
	}{
		{blueprint.Guest{OSFamily: "windows", RemoteAccess: genKey}, cbi, ""},
		{blueprint.Guest{OSFamily: "windows", RemoteAccess: admin}, sysprep, ""},
		{blueprint.Guest{OSFamily: "windows", RemoteAccess: admin}, nil, ""},
		{blueprint.Guest{OSFamily: "windows", RemoteAccess: genKey}, sysprep, "Windows Server 2025 doesn't use cloudbase-init, which a Windows VM needs to be given an SSH key"},
		{blueprint.Guest{OSFamily: "windows", RemoteAccess: &blueprint.RemoteAccess{Kind: "password", Username: "ops"}}, nil,
			"The template doesn't use cloudbase-init: the only user vCenter's customization sets a password for is Administrator, not ops"},
		{blueprint.Guest{OSFamily: "windows", CloudInit: &blueprint.CloudInit{UserData: "#ps1"}}, sysprep, "Windows Server 2025 doesn't use cloudbase-init, and this item gives its VMs user data"},
		{blueprint.Guest{OSFamily: "windows", CloudInit: &blueprint.CloudInit{UserData: "#ps1"}}, cbi, ""},
		{blueprint.Guest{OSFamily: "windows", CustomizationSpec: "win"}, cbi, "configures its VMs with cloudbase-init, and this item names the customization specification win"},
		{blueprint.Guest{OSFamily: "windows", DomainJoin: &blueprint.DomainJoin{Domain: "corp.example"}}, cbi, "which doesn't join them to a domain"},
		{blueprint.Guest{OSFamily: "windows", RemoteAccess: &blueprint.RemoteAccess{Kind: "generated_key", Username: "a-very-long-windows-user"}}, cbi,
			"can't have the user a-very-long-windows-user"},
		{blueprint.Guest{OSFamily: "linux", CustomizationSpec: "rhel-spec", RemoteAccess: genKey}, nil, "customization specification rhel-spec"},
		{blueprint.Guest{OSFamily: "linux", RemoteAccess: genKey}, nil, ""},
	} {
		msg := accessProblem(c.g, c.image)
		if c.want == "" && msg != "" || !strings.Contains(msg, c.want) {
			t.Errorf("%+v on %v: %q, want %q", c.g, c.image, msg, c.want)
		}
	}
}
