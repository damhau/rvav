package config

import (
	"encoding/json"
	"reflect"
	"slices"
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
