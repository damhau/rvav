package auth

import (
	"slices"
	"testing"
)

func TestSigningAlgs(t *testing.T) {
	got := signingAlgs([]string{"HS256", "RS256", "none", "ES256", "HS512", "EdDSA"})
	if !slices.Equal(got, []string{"RS256", "ES256", "EdDSA"}) {
		t.Fatalf("accepted %v", got)
	}
	if got := signingAlgs([]string{"HS256"}); got != nil {
		t.Fatalf("accepted %v from a provider with shared secrets only", got)
	}
}
