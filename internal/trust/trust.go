// Package trust is how rvav checks the certificates of the servers it
// connects to (ADR-0030): the system's CAs, those of the CA bundle set in
// Administration, and for one connection a certificate pinned by its
// SHA-256, trusted besides those the CAs vouch for. Inspect reads the
// certificate a server presents, for a connection's test.
package trust

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Options are how one connection checks its server's certificate.
type Options struct {
	// Insecure checks nothing. For labs only.
	Insecure bool
	// Pin is the SHA-256 of a certificate trusted for this connection
	// besides those the CAs vouch for, as Fingerprint gives it.
	Pin string
	// Roots are the CAs; nil means the process's (Roots).
	Roots *x509.CertPool
}

// RootCAs are the CAs the connection trusts: Roots unless given.
func (o Options) RootCAs() *x509.CertPool {
	if o.Roots != nil {
		return o.Roots
	}
	return Roots()
}

var (
	bundle atomic.Pointer[string]
	pool   atomic.Pointer[x509.CertPool]
)

// SetBundle trusts the CAs of a PEM bundle besides the system's, for the
// connections made after. A process sets it when it starts: a new bundle
// applies as connections do, each process starting again with it.
func SetBundle(pemText string) error {
	p, err := Pool(pemText)
	if err != nil {
		return err
	}
	bundle.Store(&pemText)
	pool.Store(p)
	return nil
}

var installed atomic.Pointer[x509.CertPool]

// Install puts the process's roots on http.DefaultTransport, for the
// libraries that use it rather than a client of rvav's (OAuth's token
// requests). A process calls it as it starts, before it connects anywhere;
// it changes nothing while the roots are those installed (the system's,
// with no bundle).
func Install() {
	p := Roots()
	if installed.Load() == p {
		return
	}
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return
	}
	cfg := tr.TLSClientConfig.Clone()
	if cfg == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cfg.RootCAs = p
	tr.TLSClientConfig = cfg
	installed.Store(p)
}

// Bundle is the PEM bundle set, for clients that take one (go-git).
func Bundle() string {
	if b := bundle.Load(); b != nil {
		return *b
	}
	return ""
}

// Roots are the system's CAs and the bundle's; nil, meaning the system's,
// when no bundle is set.
func Roots() *x509.CertPool { return pool.Load() }

// Pool is the system's CAs with those of a PEM bundle; nil for an empty
// bundle, meaning the system's.
func Pool(pemText string) (*x509.CertPool, error) {
	certs, err := Parse(pemText)
	if err != nil || len(certs) == 0 {
		return nil, err
	}
	p, err := x509.SystemCertPool()
	if err != nil || p == nil {
		p = x509.NewCertPool()
	}
	for _, c := range certs {
		p.AddCert(c)
	}
	return p, nil
}

// Parse reads the certificates of a PEM bundle, refusing anything else in
// it: a private key has no place there.
func Parse(pemText string) ([]*x509.Certificate, error) {
	rest := []byte(strings.TrimSpace(pemText))
	var out []*x509.Certificate
	for len(rest) > 0 {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			return nil, errors.New("not PEM: certificates between -----BEGIN CERTIFICATE----- and -----END CERTIFICATE-----")
		}
		if b.Type != "CERTIFICATE" {
			if strings.Contains(b.Type, "PRIVATE KEY") {
				return nil, errors.New("a private key: the bundle holds the CAs' certificates only")
			}
			return nil, fmt.Errorf("a %s, not a certificate", b.Type)
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return nil, fmt.Errorf("certificate %d: %w", len(out)+1, err)
		}
		out = append(out, c)
		rest = []byte(strings.TrimSpace(string(rest)))
	}
	return out, nil
}

// Fingerprint is a certificate's SHA-256, as AB:CD:… (as govmomi's
// thumbprints and most browsers show it).
func Fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return colons(sum[:])
}

func colons(b []byte) string {
	h := strings.ToUpper(hex.EncodeToString(b))
	parts := make([]string, 0, len(b))
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// NormalFingerprint reads a SHA-256 fingerprint given with or without
// colons or spaces, in either case, as Fingerprint writes it.
func NormalFingerprint(s string) (string, bool) {
	h := strings.NewReplacer(":", "", " ", "", "-", "").Replace(strings.TrimSpace(s))
	if strings.HasPrefix(strings.ToLower(h), "sha256") {
		h = h[len("sha256"):]
	}
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != sha256.Size {
		return "", false
	}
	return colons(b), true
}

// Config is the TLS configuration of a connection with these options.
func Config(o Options) *tls.Config {
	c := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: o.RootCAs()}
	switch {
	case o.Insecure:
		c.InsecureSkipVerify = true //nolint:gosec // opt-in, for labs
	case o.Pin != "":
		// Verified below: the pinned certificate, or one the CAs vouch for.
		c.InsecureSkipVerify = true //nolint:gosec // VerifyConnection checks
		pin, _ := NormalFingerprint(o.Pin)
		roots := c.RootCAs
		c.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the server gave no certificate")
			}
			if Fingerprint(cs.PeerCertificates[0]) == pin {
				return nil
			}
			return verify(cs.PeerCertificates, cs.ServerName, roots)
		}
	}
	return c
}

// Transport is http.DefaultTransport's, with Config's TLS.
func Transport(o Options) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = Config(o)
	return tr
}

func verify(chain []*x509.Certificate, host string, roots *x509.CertPool) error {
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{DNSName: host, Roots: roots, Intermediates: inter})
	return err
}

// Certificate is what a server presents, and whether rvav trusts it.
type Certificate struct {
	Subject   string
	Issuer    string
	Names     []string
	NotBefore time.Time
	NotAfter  time.Time
	SHA256    string
	// Trusted: the system's or the bundle's CAs vouch for it, for the host.
	Trusted bool
	// Pinned: it is the connection's pinned certificate.
	Pinned bool
	// Problem says why it isn't trusted.
	Problem string
	// Anchor is the last certificate the server sent, in PEM: added to the
	// bundle, it makes the server trusted. Its subject, and whether it is
	// the server's own (self-signed, or sent alone).
	Anchor        string
	AnchorSubject string
	AnchorIsLeaf  bool
}

// Inspect connects to the URL's host and reads the certificate it
// presents, checking it as a connection with these options would.
func Inspect(ctx context.Context, rawURL string, o Options) (Certificate, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return Certificate{}, fmt.Errorf("%q is not a URL", rawURL)
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "443"
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}} //nolint:gosec // read, then checked below
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return Certificate{}, err
	}
	defer conn.Close()
	chain := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(chain) == 0 {
		return Certificate{}, errors.New("the server gave no certificate")
	}
	leaf, top := chain[0], chain[len(chain)-1]
	c := Certificate{Subject: name(leaf.Subject.CommonName, leaf.Subject.String()),
		Issuer: name(leaf.Issuer.CommonName, leaf.Issuer.String()), Names: leaf.DNSNames,
		NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter, SHA256: Fingerprint(leaf),
		Anchor:        string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: top.Raw})),
		AnchorSubject: name(top.Subject.CommonName, top.Subject.String()), AnchorIsLeaf: top == leaf}
	for _, ip := range leaf.IPAddresses {
		c.Names = append(c.Names, ip.String())
	}
	if pin, ok := NormalFingerprint(o.Pin); ok && pin == c.SHA256 {
		c.Pinned = true
	}
	if err := verify(chain, host, o.RootCAs()); err != nil {
		c.Problem = problem(err)
	} else {
		c.Trusted = true
	}
	return c, nil
}

func name(cn, full string) string {
	if cn != "" {
		return cn
	}
	return full
}

// problem says in a sentence why a certificate isn't trusted.
func problem(err error) string {
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &unknown):
		return "no CA rvav trusts signed it"
	case errors.As(err, &host):
		return "it isn't for this host: " + host.Error()
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		return "it has expired, or isn't valid yet"
	}
	return err.Error()
}
