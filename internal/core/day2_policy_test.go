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
		global, project, want string
	}{
		{"deny", "allow", "global"},
		{"deny", "approval", "global"},
		{"approval", "allow", "global"},
		{"deny", "soft deny", "global"},
		{"approval", "soft approval", "global"},
		{"approval", "soft deny", "global"},
		{"approval", "approval", "both"},
		{"soft approval", "approval", "project"},
		{"allow", "deny", "project"},
		{"allow", "approval", "project"},
		{"approval", "deny", "project"},
		{"deny", "deny", "project"},
		{"allow", "allow", "project"},
		{"soft deny", "allow", "project"},
		{"soft approval", "allow", "project"},
	} {
		if got := decides(policy(c.global), policy(c.project)); got != c.want {
			t.Errorf("a global %s with a project's %s: %s decides, want %s", c.global, c.project, got, c.want)
		}
	}
}
