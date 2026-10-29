package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/damhau/rvav/internal/config"
)

func TestDevAuthNeedsExplicitFlag(t *testing.T) {
	a := &App{Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Cfg: config.Config{Auth: config.AuthConfig{Mode: "dev", DevUser: "lmartin"}}}
	if _, err := a.Authenticator(context.Background()); err == nil || !strings.Contains(err.Error(), "RVAV_ALLOW_DEV_AUTH=1") {
		t.Fatalf("dev mode without the flag: err = %v", err)
	}
	a.Cfg.Auth.AllowDev = true
	if _, err := a.Authenticator(context.Background()); err != nil {
		t.Fatalf("dev mode with the flag: %v", err)
	}
}

// Temporal behind TLS, with a private CA and a client certificate, or with
// Temporal Cloud's API key (S14.1).
func TestTemporalSecurity(t *testing.T) {
	var opts client.Options
	if err := TemporalSecurity(config.TemporalConfig{}, &opts); err != nil || opts.ConnectionOptions.TLS != nil {
		t.Fatalf("plain = %v %v", opts.ConnectionOptions.TLS, err)
	}
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "rvav"}, NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	write := func(name, typ string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cert, priv := write("cert.pem", "CERTIFICATE", der), write("key.pem", "EC PRIVATE KEY", keyDER)
	c := config.TemporalConfig{TLS: true, CAFile: cert, CertFile: cert, KeyFile: priv, ServerName: "temporal.corp.example", APIKey: "k"}
	if err := TemporalSecurity(c, &opts); err != nil {
		t.Fatal(err)
	}
	tc := opts.ConnectionOptions.TLS
	if tc == nil || tc.RootCAs == nil || len(tc.Certificates) != 1 || tc.ServerName != "temporal.corp.example" || opts.Credentials == nil {
		t.Fatalf("TLS = %+v, credentials %v", tc, opts.Credentials)
	}
	c.CAFile = priv // not a certificate
	if err := TemporalSecurity(c, &client.Options{}); err == nil {
		t.Fatal("a CA file without a certificate was taken")
	}
}
