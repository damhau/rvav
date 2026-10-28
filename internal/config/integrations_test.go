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
