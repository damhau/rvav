package auth

import (
	"strings"
	"testing"
)

// An origin goes into the page's Content-Security-Policy, through a header:
// only a scheme, host and port pass.
func TestOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://login.microsoftonline.com/0f1e/v2.0":   "https://login.microsoftonline.com",
		"http://localhost:8081/realms/rvav":             "http://localhost:8081",
		"https://SSO.Corp.Example:8443/realms/rvav?x=1": "https://sso.corp.example:8443",
		"https://[::1]:8443/":                           "https://[::1]:8443",
		"https://corp.okta.com/oauth2/default":          "https://corp.okta.com",
		"javascript:alert(1)":                           "",
		"https://user:pw@sso.corp.example/":             "",
		"https://sso.corp.example;script-src *":         "",
		"https://sso.corp.example'/":                    "",
		"ftp://sso.corp.example":                        "",
		"sso.corp.example":                              "",
		"":                                              "",
	} {
		if got := Origin(in); got != want {
			t.Errorf("Origin(%q) = %q, want %q", in, got, want)
		}
	}
}

// A trial's token is read without its signature when it is refused, to
// show its claims; an opaque one says why it can't be read.
func TestUnverifiedClaims(t *testing.T) {
	// {"aud":"account","sub":"u1"}
	claims, err := unverifiedClaims("eyJhbGciOiJSUzI1NiJ9.eyJhdWQiOiJhY2NvdW50Iiwic3ViIjoidTEifQ.c2ln")
	if err != nil || claims["aud"] != "account" || claims["sub"] != "u1" {
		t.Fatalf("claims = %v, %v", claims, err)
	}
	if _, err := unverifiedClaims("opaque"); err == nil || !strings.Contains(err.Error(), "not a JWT") {
		t.Fatalf("opaque token: %v", err)
	}
	if _, err := unverifiedClaims("a.!!!.c"); err == nil {
		t.Fatal("bad claims accepted")
	}
	if g := groupsIn(map[string]any{"roles": []any{"/rvav-admins", "ops", 3}}, "roles"); len(g) != 2 || g[0] != "rvav-admins" || g[1] != "ops" {
		t.Fatalf("groups = %v", g)
	}
}
