package trust

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type issued struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
}

// issue makes a certificate for 127.0.0.1 (or a CA), signed by parent, or
// self-signed.
func issue(t *testing.T, cn string, ca bool, parent *issued, notAfter time.Time) *issued {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, BasicConstraintsValid: true, IsCA: ca,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if !ca {
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		tmpl.DNSNames = []string{"vc01.corp.example"}
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &issued{cert: c, key: key, pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

func serve(t *testing.T, chain ...*issued) *httptest.Server {
	t.Helper()
	cert := tls.Certificate{PrivateKey: chain[0].key, Leaf: chain[0].cert}
	for _, c := range chain {
		cert.Certificate = append(cert.Certificate, c.cert.Raw)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func get(srv *httptest.Server, o Options) error {
	resp, err := (&http.Client{Transport: Transport(o), Timeout: 5 * time.Second}).Get(srv.URL)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

func TestTrust(t *testing.T) {
	ctx := context.Background()
	year := time.Now().Add(365 * 24 * time.Hour)
	root := issue(t, "Corp Root CA", true, nil, year)
	inter := issue(t, "Corp Issuing CA", true, root, year)
	leaf := issue(t, "vc01.corp.example", false, inter, year)
	signed := serve(t, leaf, inter)
	self := issue(t, "esx-lab", false, nil, year)
	selfSigned := serve(t, self)

	// Signed by a CA rvav doesn't know: shown, not trusted; its anchor is
	// the issuing CA the server sent.
	c, err := Inspect(ctx, signed.URL, Options{})
	if err != nil || c.Trusted || c.Pinned || c.Problem != "no CA rvav trusts signed it" || c.Subject != "vc01.corp.example" ||
		c.Issuer != "Corp Issuing CA" || c.SHA256 != Fingerprint(leaf.cert) || c.AnchorSubject != "Corp Issuing CA" ||
		c.AnchorIsLeaf || c.Anchor != inter.pem || !strings.Contains(strings.Join(c.Names, " "), "127.0.0.1") {
		t.Fatalf("unknown CA: %+v, %v", c, err)
	}
	if err := get(signed, Options{}); err == nil {
		t.Fatal("a connection trusted an unknown CA")
	}
	// The root in the bundle: trusted, and connections work.
	roots, err := Pool(root.pem)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := Inspect(ctx, signed.URL, Options{Roots: roots}); !c.Trusted || c.Problem != "" {
		t.Fatalf("with the root: %+v", c)
	}
	if err := get(signed, Options{Roots: roots}); err != nil {
		t.Fatalf("with the root: %v", err)
	}
	// Or the anchor the test showed, an intermediate: Go trusts it as a root.
	anchored, _ := Pool(c.Anchor)
	if err := get(signed, Options{Roots: anchored}); err != nil {
		t.Fatalf("with the anchor: %v", err)
	}
	// The process's bundle, when no roots are given.
	if err := SetBundle(root.pem); err != nil {
		t.Fatal(err)
	}
	if err := get(signed, Options{}); err != nil || Bundle() != root.pem {
		t.Fatalf("with the process's bundle: %v", err)
	}
	_ = SetBundle("")
	if Roots() != nil {
		t.Fatal("an empty bundle should mean the system's CAs")
	}

	// Self-signed: pinned by its SHA-256, given in any common form.
	pin := strings.ToLower(strings.ReplaceAll(Fingerprint(self.cert), ":", ""))
	if c, _ := Inspect(ctx, selfSigned.URL, Options{Pin: pin}); c.Trusted || !c.Pinned || !c.AnchorIsLeaf || c.Anchor != self.pem {
		t.Fatalf("self-signed, pinned: %+v", c)
	}
	if err := get(selfSigned, Options{Pin: pin}); err != nil {
		t.Fatalf("pinned: %v", err)
	}
	if err := get(selfSigned, Options{Pin: Fingerprint(leaf.cert)}); err == nil {
		t.Fatal("another certificate's pin was accepted")
	}
	// A pin is besides the CAs: a server they vouch for still works.
	if err := get(signed, Options{Pin: Fingerprint(self.cert), Roots: roots, Host: "127.0.0.1"}); err != nil {
		t.Fatalf("pinned elsewhere, CA-signed: %v", err)
	}
	if err := get(selfSigned, Options{Insecure: true}); err != nil {
		t.Fatalf("insecure: %v", err)
	}

	// Another host's certificate, an expired one.
	if c, _ := Inspect(ctx, "https://localhost:"+strings.Split(signed.URL, ":")[2], Options{Roots: roots}); c.Trusted ||
		!strings.HasPrefix(c.Problem, "it isn't for this host") {
		t.Fatalf("another host: %+v", c)
	}
	old := issue(t, "vc-old", false, root, time.Now().Add(-time.Minute))
	if c, _ := Inspect(ctx, serve(t, old).URL, Options{Roots: roots}); c.Trusted || c.Problem != "it has expired, or isn't valid yet" {
		t.Fatalf("expired: %+v", c)
	}
}

func TestFingerprintsAndBundles(t *testing.T) {
	want := "AB:" + strings.Repeat("01:", 30) + "FF"
	for _, s := range []string{want, strings.ToLower(want), strings.ReplaceAll(want, ":", ""), "sha256:" + strings.ReplaceAll(want, ":", " ")} {
		if got, ok := NormalFingerprint(s); !ok || got != want {
			t.Errorf("%q: %q %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "AB:CD", strings.Repeat("ZZ", 32), strings.Repeat("AB", 20)} {
		if _, ok := NormalFingerprint(s); ok {
			t.Errorf("%q accepted", s)
		}
	}
	ca := issue(t, "Corp Root CA", true, nil, time.Now().Add(time.Hour))
	if certs, err := Parse(ca.pem + "\n" + ca.pem); err != nil || len(certs) != 2 {
		t.Fatalf("two certificates: %d, %v", len(certs), err)
	}
	der, _ := x509.MarshalECPrivateKey(ca.key)
	key := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
	for in, want := range map[string]string{
		ca.pem + key:   "a private key",
		"not a bundle": "not PEM",
		"-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n": "certificate 1",
	} {
		if _, err := Parse(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.20q: %v, want %q", in, err, want)
		}
	}
	if p, err := Pool("  "); p != nil || err != nil {
		t.Fatalf("empty bundle: %v %v", p, err)
	}
}

// TestPinAtAnIPAddress: a pinned connection trusts besides its pin only a
// certificate the CAs vouch for, for the host its URL names. At an IP
// address, which is never the handshake's server name, a certificate the
// CA issued for another name was let through: pinning was weaker than not.
func TestPinAtAnIPAddress(t *testing.T) {
	year := time.Now().Add(365 * 24 * time.Hour)
	root := issue(t, "Corp Root CA", true, nil, year)
	roots, err := Pool(root.pem)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "evil.example"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: year, DNSNames: []string{"evil.example"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root.cert, &key.PublicKey, root.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	evil := serve(t, &issued{cert: cert, key: key})
	pinned := Fingerprint(issue(t, "awx01", false, nil, year).cert) // the server's own, elsewhere
	if err := get(evil, Options{Roots: roots}); err == nil {
		t.Fatal("without a pin, a certificate for evil.example was trusted at 127.0.0.1")
	}
	if err := get(evil, Options{Roots: roots, Pin: pinned, Host: "127.0.0.1"}); err == nil {
		t.Fatal("with a pin, a certificate for evil.example was trusted at 127.0.0.1")
	}
	if err := get(evil, Options{Roots: roots, Pin: pinned}); err == nil || !strings.Contains(err.Error(), "no host name") {
		t.Fatalf("with a pin and no host: %v", err)
	}
	// One the CA issued for the address, as a renewed certificate is, is
	// still trusted besides the pin.
	renewed := serve(t, issue(t, "awx01", false, root, year))
	if err := get(renewed, Options{Roots: roots, Pin: pinned, Host: "127.0.0.1"}); err != nil {
		t.Fatalf("a certificate the CA issued for 127.0.0.1, with a pin: %v", err)
	}
}
