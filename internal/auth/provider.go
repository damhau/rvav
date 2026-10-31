package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// Checks of a provider's settings before they are saved (ADR-0030): its
// discovery document and keys, then a token from a trial sign-in.

// ProviderCheck is what the test of a provider's settings found.
type ProviderCheck struct {
	Info      ProviderInfo
	Discovery error
	// Keys are the signing keys published, as "kid (alg)".
	Keys    []string
	KeysErr error
}

// CheckProvider reads the provider's discovery document and its keys, as
// the token check does.
func CheckProvider(ctx context.Context, ps ProviderSettings) ProviderCheck {
	ps = ps.normal()
	var out ProviderCheck
	_, out.Info, out.Discovery = newVerifier(ctx, ps)
	if out.Discovery != nil {
		return out
	}
	switch {
	case out.Info.AuthorizationEndpoint == "" || out.Info.TokenEndpoint == "":
		out.Discovery = errors.New("the discovery document has no authorization or token endpoint")
	case ps.DiscoveryURL == ps.Issuer && Origin(out.Info.TokenEndpoint) != Origin(ps.Issuer):
		// The browser fetches tokens there, and the page may reach the
		// issuer's origin only (Content-Security-Policy).
		out.Discovery = fmt.Errorf("the token endpoint, %s, is not at the issuer's origin (%s): the browser may only reach that one",
			out.Info.TokenEndpoint, Origin(ps.Issuer))
	}
	out.Keys, out.KeysErr = signingKeys(ctx, out.Info.JWKSURL)
	return out
}

func signingKeys(ctx context.Context, jwksURL string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("the keys' address, %q: %v", jwksURL, err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching the keys: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching the keys: %s answered %s", jwksURL, res.Status)
	}
	var set jose.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("reading the keys: %v", err)
	}
	var out []string
	for _, k := range set.Keys {
		if (k.Use == "" || k.Use == "sig") && k.Valid() && k.IsPublic() {
			alg := k.Algorithm
			if alg == "" {
				alg = "any algorithm"
			}
			out = append(out, fmt.Sprintf("%s (%s)", k.KeyID, alg))
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the provider publishes no signing key")
	}
	return out, nil
}

// Trial is what a token from a trial sign-in carries.
type Trial struct {
	Subject, Name, Email string
	Claims               map[string]any
	Groups               []string
	// Refused says why the token check would refuse it; nil: accepted.
	Refused error
}

// TrialToken checks a token as a request's would be, with the settings
// given, and reads its claims and groups. The claims are read even when the
// token is refused, to show why.
func TrialToken(ctx context.Context, ps ProviderSettings, token string) Trial {
	ps = ps.normal()
	var t Trial
	t.Claims, t.Refused = unverifiedClaims(token)
	if t.Refused == nil {
		v, _, err := newVerifier(ctx, ps)
		if err != nil {
			t.Refused = err
		} else if idt, err := v.Verify(ctx, token); err != nil {
			t.Refused = err
		} else if err := idt.Claims(&t.Claims); err != nil {
			t.Refused = err
		}
	}
	str := func(k string) string { s, _ := t.Claims[k].(string); return s }
	t.Subject, t.Name, t.Email = str("sub"), str("name"), str("email")
	if t.Name == "" {
		t.Name = str("preferred_username")
	}
	t.Groups = groupsIn(t.Claims, ps.GroupsClaim)
	return t
}

// unverifiedClaims reads a JWT's claims without checking it.
func unverifiedClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return map[string]any{}, errors.New("the access token is not a JWT: the provider gave an opaque token, for itself (ask for a scope of rvav's API)")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return map[string]any{}, fmt.Errorf("the access token's claims can't be read: %v", err)
	}
	claims := map[string]any{}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return map[string]any{}, fmt.Errorf("the access token's claims can't be read: %v", err)
	}
	return claims, nil
}

// Origin is an http or https URL's scheme, host and port, as the
// Content-Security-Policy names it; "" for anything else.
func Origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return ""
	}
	// Only what a host and port may hold: the origin goes in a header.
	for _, c := range u.Host {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(".-:[]", c)) {
			return ""
		}
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}
