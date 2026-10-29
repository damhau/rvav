package secretinput

import "testing"

// TestOpenMissing: a marker with nothing sealed for it is an error, never
// passed on as it is; nothing to open needs no key (S14 review).
func TestOpenMissing(t *testing.T) {
	if err := Open(nil, "req_1", nil, map[string]any{"role": "app"}); err != nil {
		t.Fatalf("nothing to open: %v", err)
	}
	if err := Open(nil, "req_1", nil, map[string]any{"password": Marker("password")}); err == nil {
		t.Fatal("a marker with nothing sealed was passed on")
	}
}
