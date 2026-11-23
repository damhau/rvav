package config

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// An environment variable wins over a stored setting and locks its field;
// the rest comes from the stored settings (ADR-0020).
func TestStoredSettings(t *testing.T) {
	t.Setenv("RVAV_VCENTER_URL", "https://vc-env.corp.example/sdk")
	t.Setenv("RVAV_VCENTER_USERNAME", "")
	t.Setenv("RVAV_VCENTER_PASSWORD", "")
	c := Config{VCenter: VCenterConfig{URL: "https://vc-env.corp.example/sdk", Name: "vc01.corp.example"}}
	c.ApplyStored("vcenter", Stored{Settings: map[string]any{"url": "https://vc-ui.corp.example/sdk", "username": "svc-rvav",
		"insecure": true, "name": "vc02"}, Secret: "s3cret"})
	if c.VCenter.URL != "https://vc-env.corp.example/sdk" || c.VCenter.Username != "svc-rvav" || c.VCenter.Password != "s3cret" ||
		!c.VCenter.Insecure || c.VCenter.Name != "vc02" {
		t.Fatalf("vcenter = %+v", c.VCenter)
	}
	if locked := Locked("vcenter"); !slices.Equal(locked, []string{"url"}) {
		t.Fatalf("locked = %v", locked)
	}
	if errs := CheckSettings("awx", map[string]any{"token": "x", "insecure": "yes", "inventory_source_id": 1.5, "url": "ftp://x"}); len(errs) != 4 {
		t.Fatalf("errors = %v", errs)
	}
}

// The identity provider's settings (ADR-0030): URLs, a claim's name, and
// openid among the scopes; an environment variable wins.
func TestProviderSettings(t *testing.T) {
	t.Setenv("RVAV_OIDC_ISSUER", "")
	t.Setenv("RVAV_OIDC_CLIENT_ID", "rvav-env")
	c := Config{Auth: AuthConfig{OIDCClientID: "rvav-env", Scopes: "openid profile email", GroupsClaim: "groups"}}
	c.ApplyStored("oidc", Stored{Settings: map[string]any{"issuer": "https://login.microsoftonline.com/t1/v2.0", "client_id": "rvav-ui",
		"scopes": "openid profile api://rvav/access", "groups_claim": "roles", "discovery_url": ""}})
	if c.Auth.OIDCIssuer != "https://login.microsoftonline.com/t1/v2.0" || c.Auth.OIDCClientID != "rvav-env" ||
		c.Auth.Scopes != "openid profile api://rvav/access" || c.Auth.GroupsClaim != "roles" || c.Auth.OIDCDiscoveryURL != "" {
		t.Fatalf("auth = %+v", c.Auth)
	}
	if locked := Locked("oidc"); !slices.Equal(locked, []string{"client_id"}) {
		t.Fatalf("locked = %v", locked)
	}
	errs := CheckSettings("oidc", map[string]any{"issuer": "login.example", "discovery_url": "ftp://x", "scopes": "profile email",
		"groups_claim": "my groups", "secret": "x"})
	for _, f := range []string{"issuer", "discovery_url", "scopes", "groups_claim", "secret"} {
		if errs[f] == "" {
			t.Errorf("%s: no error in %v", f, errs)
		}
	}
	if errs := CheckSettings("oidc", map[string]any{"issuer": "http://localhost:8081/realms/rvav", "scopes": "",
		"groups_claim": "roles"}); len(errs) != 0 {
		t.Fatalf("good settings: %v", errs)
	}
}

// Each field's variable locks it (ADR-0030): set, the field is listed as
// locked and a saved value for it changes nothing.
func TestEveryFieldIsLockedByItsVariable(t *testing.T) {
	sample := map[string]any{"string": "https://saved.example", "bool": true, "int": float64(7),
		"list": []any{"saved.example"}, "duration": "7m"}
	for kind, fields := range Fields {
		for _, f := range fields {
			for _, env := range append([]string{f.Env}, alsoLockedBy[kind][f.Name]...) {
				t.Run(kind+"/"+f.Name+"/"+env, func(t *testing.T) {
					for _, g := range Fields[kind] {
						t.Setenv(g.Env, "")
						for _, e := range alsoLockedBy[kind][g.Name] {
							t.Setenv(e, "")
						}
					}
					t.Setenv(env, "from-env")
					if got := Locked(kind); !slices.Equal(got, []string{f.Name}) {
						t.Fatalf("locked = %v", got)
					}
					saved := Stored{Settings: map[string]any{}}
					switch {
					case f.Secret && kind == "slack":
						b, _ := json.Marshal(map[string]string{f.Name: "saved-secret"})
						saved.Secret = string(b)
					case f.Secret:
						saved.Secret = "saved-secret"
					default:
						saved.Settings[f.Name] = sample[f.Type]
					}
					var with, without Config
					with.ApplyStored(kind, saved)
					without.ApplyStored(kind, Stored{Settings: map[string]any{}})
					if !reflect.DeepEqual(with, without) {
						t.Errorf("a saved %s is used with %s set", f.Name, env)
					}
				})
			}
		}
	}
}

// The console's saved password goes only to the vCenter connection it was
// given for (ADR-0030): not to another address, nor with TLS checks off.
func TestConsolePasswordStaysWithItsVCenter(t *testing.T) {
	for _, k := range []string{"RVAV_CONSOLE_VCENTER_USERNAME", "RVAV_CONSOLE_VCENTER_PASSWORD", "RVAV_WMKS_SDK_URLS"} {
		t.Setenv(k, "")
	}
	given := VCenterConfig{URL: "https://vc01.corp.example/sdk"}
	for name, vc := range map[string]VCenterConfig{
		"the same":   given,
		"another":    {URL: "https://vc02.corp.example/sdk"},
		"TLS off":    {URL: given.URL, Insecure: true},
		"pinned":     {URL: given.URL, CertSHA256: strings.Repeat("CD:", 31) + "CD"},
		"no vCenter": {},
	} {
		c := Config{VCenter: vc}
		c.ApplyStored("console", Stored{Settings: map[string]any{"username": "console", ConsoleForKey: ConsoleFor(given)},
			Secret: "s3cret"})
		if want := name == "the same"; (c.ConsolePassword == "s3cret") != want || c.ConsoleUsername != "console" {
			t.Errorf("%s vCenter: password used = %v", name, c.ConsolePassword != "")
		}
	}
}

// The console's password set by RVAV_CONSOLE_VCENTER_PASSWORD goes only to
// the vCenter set with it (ADR-0030), as vCenter's own does: not to another
// address saved in Administration, nor with TLS checks off or a pin.
func TestConsolePasswordFromTheEnvironment(t *testing.T) {
	for _, k := range []string{"RVAV_VCENTER_URL", "RVAV_VCENTER_INSECURE", "RVAV_VCENTER_CERT_SHA256", "RVAV_VCENTER_USERNAME",
		"RVAV_VCENTER_PASSWORD", "RVAV_VCENTER_NAME", "RVAV_CONSOLE_VCENTER_USERNAME", "RVAV_WMKS_SDK_URLS"} {
		t.Setenv(k, "")
	}
	t.Setenv("RVAV_CONSOLE_VCENTER_PASSWORD", "from-the-env")
	env := VCenterConfig{URL: "https://vc01.corp.example/sdk"}
	pin := strings.Repeat("AB:", 31) + "AB"
	for name, saved := range map[string]map[string]any{
		"the same":    {"url": env.URL, "username": "svc-rvav"},
		"another":     {"url": "https://vc02.corp.example/sdk"},
		"TLS off":     {"insecure": true},
		"a pin":       {"cert_sha256": pin},
		"no settings": {},
	} {
		c := Config{VCenter: env, ConsolePassword: "from-the-env"}
		c.ApplyStored("vcenter", Stored{Settings: saved, Secret: "vcenter's"})
		c.ApplyStored("console", Stored{Settings: map[string]any{ConsoleForKey: ConsoleFor(c.VCenter)}, Secret: "saved"})
		want := map[bool]string{true: "from-the-env", false: ""}[name == "the same" || name == "no settings"]
		if c.ConsolePassword != want {
			t.Errorf("%s vCenter: the console's password = %q, want %q", name, c.ConsolePassword, want)
		}
	}
	// With vCenter's address set by the environment too, a saved one
	// doesn't apply, and the password goes with it.
	t.Setenv("RVAV_VCENTER_URL", env.URL)
	c := Config{VCenter: env, ConsolePassword: "from-the-env"}
	c.ApplyStored("vcenter", Stored{Settings: map[string]any{"url": "https://vc02.corp.example/sdk"}})
	if c.VCenter.URL != env.URL || c.ConsolePassword != "from-the-env" {
		t.Errorf("both set by the environment: %s, %q", c.VCenter.URL, c.ConsolePassword)
	}
}

// A locked field sent back as a client read it is unchanged, however it is
// written; the variable that locks it is named (#211).
func TestUnchangedLockedValue(t *testing.T) {
	pin := "AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89"
	for _, c := range []struct {
		kind, name     string
		given, current any
		want           bool
	}{
		{"nsx", "url", "https://nsx01.corp.example ", "https://nsx01.corp.example", true},
		{"nsx", "url", "https://nsx02.corp.example", "https://nsx01.corp.example", false},
		{"nsx", "url", nil, "", true},
		{"nsx", "url", 1.0, "1", false},
		{"nsx", "insecure", false, false, true},
		{"nsx", "insecure", true, false, false},
		{"nsx", "insecure", "false", false, false},
		{"smtp", "port", 587.0, 587, true},
		{"smtp", "port", 25.0, 587, false},
		{"awx", "catalog_labels", []any{" catalog", ""}, []string{"catalog"}, true},
		{"awx", "catalog_labels", nil, []string(nil), true},
		{"awx", "catalog_labels", []any{}, []string{"catalog"}, false},
		{"awx", "catalog_labels", []any{1.0}, []string{"1"}, false},
		{"vcenter", "sync_interval", "120s", "2m", true},
		{"vcenter", "sync_interval", "5m", "2m", false},
		{"nsx", "cert_sha256", strings.ReplaceAll(strings.ToLower(pin), ":", ""), pin, true},
		{"nsx", "cert_sha256", "", pin, false},
		{"awx", "fake", true, true, true},
		{"awx", "fake", false, true, false},
	} {
		if got := Unchanged(c.kind, c.name, c.given, c.current); got != c.want {
			t.Errorf("%s %s: %#v over %#v unchanged = %v, want %v", c.kind, c.name, c.given, c.current, got, c.want)
		}
	}
	t.Setenv("RVAV_VRA_TOKEN", "")
	t.Setenv("RVAV_VRA_PASSWORD", "s3cret")
	t.Setenv("RVAV_NSX_URL", "https://nsx01.corp.example")
	for _, c := range [][3]string{{"vra", "credential", "RVAV_VRA_PASSWORD"}, {"nsx", "url", "RVAV_NSX_URL"}, {"nsx", "username", ""},
		{"awx", "fake", "RVAV_FAKE_AWX"}} {
		if got := LockedBy(c[0], c[1]); got != c[2] {
			t.Errorf("%s %s locked by %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// Saved text is read trimmed: a page that trims what it sends saves what
// rvav runs with (#211).
func TestStoredTextTrimmed(t *testing.T) {
	t.Setenv("RVAV_CA_BUNDLE", "")
	var a, b Config
	a.ApplyStored("trust", Stored{Settings: map[string]any{"ca_bundle": "-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n\n"}})
	b.ApplyStored("trust", Stored{Settings: map[string]any{"ca_bundle": "-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----"}})
	if a.CABundle != b.CABundle {
		t.Errorf("CA bundles %q and %q", a.CABundle, b.CABundle)
	}
}
