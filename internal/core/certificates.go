package core

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/trust"
)

// Certificates (ADR-0030): a connection's test shows the certificate its
// server presents and whether rvav trusts it; an administrator trusts it by
// pinning it for that connection, or adds its CA to the CA bundle
// (Integrations › Trusted certificates), which every connection trusts.

// pendingRoots are the CAs rvav's processes trust once they have applied
// the CA bundle saved, with the bundle.
func (s *Service) pendingRoots(ctx context.Context, stored map[string]store.IntegrationSettings) (*x509.CertPool, string) {
	var row *store.IntegrationSettings
	if r, ok := stored["trust"]; ok {
		row = &r
	}
	bundle, _ := s.pendingSettings(ctx, "trust", row)["ca_bundle"].(string)
	return rootsOf(bundle), bundle
}

// rootsOf are the system's CAs and a bundle's.
func rootsOf(bundle string) *x509.CertPool {
	if p, err := trust.Pool(bundle); err == nil && p != nil {
		return p
	}
	p, _ := x509.SystemCertPool()
	return p
}

// certificateOf is the HTTPS address whose certificate a test of the
// integration shows, how the connection checks it, and whether it takes a
// pin; "" when there is none (a fake, plain HTTP, or no server of its own).
func certificateOf(kind string, cfg config.Config, settings map[string]any) (string, trust.Options, bool) {
	var u string
	var o trust.Options
	pinnable := true
	switch kind {
	case "vcenter":
		u, o = cfg.VCenter.URL, cfg.VCenter.TLS()
	case "awx":
		if !cfg.AWX.Fake {
			u, o = cfg.AWX.URL, cfg.AWX.TLS()
		}
	case "efficientip":
		if !cfg.IPAM.Fake {
			u, o = cfg.IPAM.URL, cfg.IPAM.TLS()
		}
	case "nsx":
		u, o = cfg.NSX.URL, cfg.NSX.TLS()
	case "vra":
		u, o = cfg.VRA.URL, cfg.VRA.TLS()
	case "oidc":
		ps := providerFrom(settings)
		u, pinnable = ps.DiscoveryURL, false
		if u == "" {
			u = ps.Issuer
		}
	case "git":
		u, o, pinnable = cfg.Git.URL, trust.Options{Insecure: cfg.Git.Insecure}, false
	}
	if p, err := url.Parse(u); err != nil || p.Scheme != "https" || p.Host == "" {
		return "", o, false
	}
	return u, o, pinnable
}

// checkCertificate reads the server's certificate and checks it as the
// connection would; false when the connection would refuse it.
func (s *Service) checkCertificate(ctx context.Context, rawURL string, o trust.Options, pinnable bool,
	check checkFunc) (*api.ServerCertificate, bool) {
	u, _ := url.Parse(rawURL)
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "443")
	}
	c, err := trust.Inspect(ctx, rawURL, o)
	if err != nil {
		check("Certificate", fmt.Errorf("%s gave none: %v", addr, err), "")
		return nil, false
	}
	out := &api.ServerCertificate{Address: addr, Subject: c.Subject, Issuer: c.Issuer, Names: c.Names, NotBefore: c.NotBefore,
		NotAfter: c.NotAfter, Sha256: c.SHA256, Trusted: c.Trusted, Pinned: c.Pinned, Checked: !o.Insecure, Pinnable: pinnable,
		AnchorPem: c.Anchor, AnchorSubject: c.AnchorSubject, AnchorIsLeaf: c.AnchorIsLeaf}
	if out.Names == nil {
		out.Names = []string{}
	}
	if c.Problem != "" {
		out.Problem = &c.Problem
	}
	about := fmt.Sprintf("%s, issued by %s, until %s", c.Subject, c.Issuer, c.NotAfter.Format("2 January 2006"))
	switch {
	case o.Insecure:
		check("Certificate", nil, "Not checked: TLS checks are off. "+about+".")
	case c.Trusted:
		check("Certificate", nil, about+": a CA rvav trusts vouches for it.")
	case c.Pinned:
		check("Certificate", nil, about+": pinned for this connection.")
	default:
		fix := "Trust this certificate, or add its CA to the CA bundle."
		if !pinnable {
			fix = "Add its CA to the CA bundle (Trusted certificates)."
		}
		check("Certificate", fmt.Errorf("%s: %s. %s", about, c.Problem, fix), "")
		return out, false
	}
	return out, true
}

// testTrust lists the certificates of the CA bundle.
func testTrust(bundle string, check checkFunc) {
	certs, err := trust.Parse(bundle)
	if err != nil {
		check("CA bundle", err, "")
		return
	}
	if len(certs) == 0 {
		check("CA bundle", nil, "Empty: only the system's CAs are trusted.")
		return
	}
	for _, c := range certs {
		name := c.Subject.CommonName
		if name == "" {
			name = c.Subject.String()
		}
		what := "A CA"
		if !c.IsCA {
			what = "A server's own certificate"
		}
		until := c.NotAfter.Format("2 January 2006")
		var err error
		if time.Now().After(c.NotAfter) {
			err = fmt.Errorf("expired on %s: it vouches for nothing now; SHA-256 %s", until, trust.Fingerprint(c))
		}
		check(name, err, fmt.Sprintf("%s, until %s; SHA-256 %s.", what, until, trust.Fingerprint(c)))
	}
}
