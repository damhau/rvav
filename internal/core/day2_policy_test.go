package core

import (
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/store"
)

func TestGlobalOverridesProjectPolicy(t *testing.T) {
	policy := func(s string) store.Day2Policy {
		effect, soft := strings.CutPrefix(s, "soft ")
		p := store.Day2Policy{Effect: effect, Enforcement: store.EnforcementHard}
		if soft {
			p.Enforcement = store.EnforcementSoft
		}
		return p
	}
	for _, c := range []struct {
		global, project string
		want            bool
	}{
		{"deny", "allow", true},
		{"deny", "approval", true},
		{"approval", "allow", true},
		{"deny", "soft deny", true},
		{"approval", "soft approval", true},
		{"allow", "deny", false},
		{"allow", "approval", false},
		{"approval", "deny", false},
		{"deny", "deny", false},
		{"allow", "allow", false},
		{"soft deny", "allow", false},
		{"soft approval", "allow", false},
	} {
		if got := overrides(policy(c.global), policy(c.project)); got != c.want {
			t.Errorf("a global %s over a project's %s = %v, want %v", c.global, c.project, got, c.want)
		}
	}
}
