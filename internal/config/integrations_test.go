package config

import (
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
