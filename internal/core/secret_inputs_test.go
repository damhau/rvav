package core

import (
	"testing"

	"github.com/damhau/rvav/internal/secretinput"
)

// TestMarkSecretValues: a composite step's copy of a sealed value gets its
// marker; other inputs are left alone (S14 review).
func TestMarkSecretValues(t *testing.T) {
	in := map[string]any{"admin_password": "Hunt3r", "role": "db", "port": float64(5432)}
	markSecretValues(in, map[string]any{"password": "Hunt3r"})
	if !secretinput.IsMarker(in["admin_password"]) || in["role"] != "db" || in["port"] != float64(5432) {
		t.Fatalf("inputs = %v", in)
	}
}
