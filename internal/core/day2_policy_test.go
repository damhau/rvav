package core

import (
	"testing"

	"github.com/damhau/rvav/internal/store"
)

func TestGlobalOverridesProjectPolicy(t *testing.T) {
	for _, c := range []struct {
		global, project string
		want            bool
	}{
		{"deny", "allow", true},
		{"deny", "approval", true},
		{"approval", "allow", true},
		{"allow", "deny", false},
		{"allow", "approval", false},
		{"approval", "deny", false},
		{"deny", "deny", false},
		{"allow", "allow", false},
	} {
		if got := overrides(store.Day2Policy{Effect: c.global}, store.Day2Policy{Effect: c.project}); got != c.want {
			t.Errorf("a global %s over a project's %s = %v, want %v", c.global, c.project, got, c.want)
		}
	}
}
