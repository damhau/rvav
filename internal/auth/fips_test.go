package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
)

// An argon2id hash in the form rvav made them before FIPS 140 mode, its
// salt and key made up.
const argon2idHash = "$argon2id$v=19$m=65536,t=1,p=2$c2FsdHNhbHRzYWx0c2FsdA$Zv2uM0sSpdYGy5Yp3N8Jkh7cJ7Q2vB1R2m3n4o5p6q8"

// In FIPS 140 mode the local administrator's password is hashed with
// PBKDF2-HMAC-SHA-512, and an argon2id hash that matches is made again.
func TestPasswordsInFIPSMode(t *testing.T) {
	if !fipstest.In(t, fips.On) {
		return
	}
	ctx := context.Background()
	h, err := HashPassword(ctx, "correct horse battery")
	if err != nil || !strings.HasPrefix(h, "$pbkdf2-sha512$i=210000$") {
		t.Fatalf("hash %q, %v", h, err)
	}
	if !check(t, "correct horse battery", h) || check(t, "correct horse batterY", h) {
		t.Fatal("the hash doesn't check its password")
	}
	if NeedsRehash(h) || !NeedsRehash(argon2idHash) {
		t.Fatal("NeedsRehash")
	}
	if err := CheckHash(argon2idHash); err != nil {
		t.Fatalf("FIPS 140 mode, not only: %v", err)
	}
	for _, bad := range []string{"$pbkdf2-sha512$i=0$c2FsdA$a2V5", "$pbkdf2-sha512$i=x$c2FsdA$a2V5", "$pbkdf2-sha512$c2FsdA$a2V5",
		h[:len(h)-4], "$pbkdf2-sha512$i=99999999$" + strings.SplitN(h, "$", 4)[3]} {
		if check(t, "correct horse battery", bad) {
			t.Errorf("%q matched", bad)
		}
	}
}

// In FIPS 140-only mode: argon2id hashes are refused, PBKDF2's checked;
// tokens are checked with keys published with their certificates (x5c),
// whose SHA-1 thumbprint go-jose would otherwise check, and panic.
func TestFIPSOnlyMode(t *testing.T) {
	if !fipstest.In(t, fips.Only) {
		return
	}
	ctx := context.Background()
	if _, err := CheckPassword(ctx, "correct horse battery", argon2idHash); !errors.Is(err, ErrArgon2idRefused) {
		t.Fatalf("an argon2id hash: %v", err)
	}
	if !strings.Contains(ErrArgon2idRefused.Error(), "rvav admin reset-password") {
		t.Fatalf("the refusal says no way out: %v", ErrArgon2idRefused)
	}
	h, err := HashPassword(ctx, "correct horse battery")
	if err != nil || !check(t, "correct horse battery", h) {
		t.Fatalf("PBKDF2: %q %v", h, err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "idp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	cert, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	jwk, err := json.Marshal(jose.JSONWebKey{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"})
	if err != nil {
		t.Fatal(err)
	}
	var withX5C map[string]any
	_ = json.Unmarshal(jwk, &withX5C)
	withX5C["x5c"] = []any{cert}
	withX5C["x5t"] = "MDEyMzQ1Njc4OTAxMjM0NTY3ODk"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": srv.URL, "jwks_uri": srv.URL + "/certs",
				"authorization_endpoint": srv.URL + "/auth", "token_endpoint": srv.URL + "/token",
				"id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/certs":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{withX5C}})
		}
	}))
	defer srv.Close()

	ps := ProviderSettings{Issuer: srv.URL, ClientID: "rvav"}.normal()
	verifier, _, err := newVerifier(ctx, ps)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.Signed(sig).Claims(map[string]any{"iss": srv.URL, "sub": "u1", "aud": "rvav",
		"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(ctx, tok); err != nil {
		t.Fatalf("a token signed with a key published with x5c: %v", err)
	}
	if check := CheckProvider(ctx, ps); check.Discovery != nil || check.KeysErr != nil || len(check.Keys) != 1 {
		t.Fatalf("the provider's check: %+v", check)
	}
}
