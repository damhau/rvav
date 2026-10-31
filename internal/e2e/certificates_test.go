package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/vcenters"
)

// TestTrustedCertificates: a connection's test shows the certificate its
// server presents (vcsim's, self-signed); unless TLS checks are off, the
// connection refuses it until it is pinned for that connection or its CA is
// in the CA bundle, which every connection trusts, the identity provider's
// too. A new pin needs the password again, as a new address does (U1.4,
// ADR-0030).
func TestTrustedCertificates(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, vc02 = "mkeller", "vc02.corp.example"
		vcsim := c.URL().String()
		vcenter := func(extra map[string]any) map[string]any {
			m := map[string]any{"url": vcsim, "username": "user", "name": "vc01.corp.example", "insecure": false}
			for k, v := range extra {
				m[k] = v
			}
			return m
		}
		var problem struct{ Errors []api.FieldError }
		var test api.IntegrationTest

		// Checked: shown and refused, and the test stops there.
		h.json("POST", "/api/v1/admin/integrations/vcenter/test", admin, api.IntegrationWrite{Settings: vcenter(nil),
			Secret: new("pass")}, 200, &test)
		if test.Certificate == nil {
			t.Fatalf("no certificate: %+v", test)
		}
		cert := *test.Certificate // test is decoded into again below
		if test.Ok || len(test.Checks) != 1 || test.Checks[0].Name != "Certificate" || cert.Trusted || cert.Pinned ||
			!cert.Checked || !cert.Pinnable || !cert.AnchorIsLeaf || deref(cert.Problem) != "no CA rvav trusts signed it" ||
			!strings.Contains(test.Checks[0].Message, "Trust this certificate, or add its CA to the CA bundle") ||
			!strings.HasPrefix(cert.AnchorPem, "-----BEGIN CERTIFICATE-----") || !slices.Contains(cert.Names, "127.0.0.1") {
			t.Fatalf("untrusted = %+v, %+v", test, cert)
		}

		// Pinned, given in any common form, saved as the test shows it; the
		// client signs in trusting it.
		pin := strings.ToLower(strings.ReplaceAll(cert.Sha256, ":", ""))
		h.json("PUT", "/api/v1/admin/integrations/vcenter", admin, api.IntegrationWrite{Settings: vcenter(map[string]any{
			"cert_sha256": "AB:CD"}), Secret: new("pass")}, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/settings/cert_sha256") {
			t.Fatalf("a bad pin = %v", fieldsOf(problem.Errors))
		}
		var vc api.Integration
		h.json("PUT", "/api/v1/admin/integrations/vcenter", admin, api.IntegrationWrite{Settings: vcenter(map[string]any{
			"cert_sha256": pin}), Secret: new("pass")}, 200, &vc)
		if vc.Settings["cert_sha256"] != cert.Sha256 {
			t.Fatalf("saved pin = %v", vc.Settings["cert_sha256"])
		}
		h.json("POST", "/api/v1/admin/integrations/vcenter/test", admin, nil, 200, &test)
		if !test.Ok || len(test.Checks) != 4 || test.Certificate == nil || !test.Certificate.Pinned ||
			!strings.HasSuffix(test.Checks[0].Message, "pinned for this connection.") {
			t.Fatalf("pinned = %+v", test)
		}
		// Another certificate's pin: refused, and saving it needs the password.
		other := "AB:" + strings.Repeat("01:", 30) + "FF"
		h.json("POST", "/api/v1/admin/integrations/vcenter/test", admin, api.IntegrationWrite{Settings: vcenter(map[string]any{
			"cert_sha256": other}), Secret: new("pass")}, 200, &test)
		if test.Ok || test.Certificate == nil || test.Certificate.Pinned || len(test.Checks) != 1 {
			t.Fatalf("another pin = %+v", test)
		}
		h.json("PUT", "/api/v1/admin/integrations/vcenter", admin, api.IntegrationWrite{Settings: vcenter(map[string]any{
			"cert_sha256": other})}, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/secret") {
			t.Fatalf("a new pin without the password = %v", fieldsOf(problem.Errors))
		}

		// A vCenter added besides the integration's takes a pin too, and
		// the pool connects trusting it.
		write := api.VCenterWrite{Name: vc02, Url: vcsim, Username: "user", Password: present.Ptr("pass")}
		h.json("POST", "/api/v1/admin/vcenters/"+vc02+"/test", admin, write, 200, &test)
		if test.Ok || test.Certificate == nil || test.Certificate.Trusted || len(test.Checks) != 1 {
			t.Fatalf("added, untrusted = %+v", test)
		}
		write.CertSha256 = present.Ptr(pin)
		h.json("POST", "/api/v1/admin/vcenters/"+vc02+"/test", admin, write, 200, &test)
		if !test.Ok || test.Certificate == nil || !test.Certificate.Pinned {
			t.Fatalf("added, pinned = %+v", test)
		}
		var added api.VCenter
		h.json("POST", "/api/v1/admin/vcenters", admin, write, 201, &added)
		if deref(added.CertSha256) != cert.Sha256 {
			t.Fatalf("added = %+v", added)
		}
		set := &vcenters.Set{Store: h.store, Secrets: h.svc.Secrets}
		if _, err := set.Get(ctx, vc02); err != nil {
			t.Fatalf("the pool, pinned: %v", err)
		}
		moved := write
		moved.Password, moved.CertSha256 = nil, present.Ptr(other)
		h.json("PUT", "/api/v1/admin/vcenters/"+vc02, admin, moved, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/password") {
			t.Fatalf("added, a new pin without the password = %v", fieldsOf(problem.Errors))
		}

		// The identity provider over HTTPS with the same certificate: it
		// can't be pinned, only trusted through the bundle.
		idp := startFakeIdP(t, "", true)
		idp.up.Store(true)
		provider := api.IntegrationWrite{Settings: map[string]any{"issuer": idp.srv.URL, "client_id": "rvav"}}
		h.json("POST", "/api/v1/admin/integrations/oidc/test", admin, provider, 200, &test)
		if test.Ok || test.Certificate == nil || test.Certificate.Pinnable ||
			!strings.HasSuffix(test.Checks[0].Message, "Add its CA to the CA bundle (Trusted certificates).") {
			t.Fatalf("provider, untrusted = %+v", test)
		}
		idpCert := *test.Certificate

		// The CA bundle: empty, then the certificates the tests offered (for
		// a self-signed one, the certificate itself), as the UI appends them.
		h.json("POST", "/api/v1/admin/integrations/trust/test", admin, nil, 200, &test)
		if !test.Ok || len(test.Checks) != 1 || test.Checks[0].Message != "Empty: only the system's CAs are trusted." {
			t.Fatalf("empty bundle = %+v", test)
		}
		for bad, want := range map[string]string{"not a bundle": "not PEM", cert.AnchorPem + "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n": "a private key"} {
			h.json("PUT", "/api/v1/admin/integrations/trust", admin, api.IntegrationWrite{Settings: map[string]any{"ca_bundle": bad}}, 422, &problem)
			if len(problem.Errors) != 1 || problem.Errors[0].Field != "/settings/ca_bundle" || !strings.Contains(problem.Errors[0].Message, want) {
				t.Fatalf("bundle %.12q = %+v", bad, problem.Errors)
			}
		}
		bundle := cert.AnchorPem + idpCert.AnchorPem
		h.json("PUT", "/api/v1/admin/integrations/trust", admin, api.IntegrationWrite{Settings: map[string]any{"ca_bundle": bundle}}, 200, nil)
		h.json("POST", "/api/v1/admin/integrations/trust/test", admin, nil, 200, &test)
		if !test.Ok || len(test.Checks) != 2 || !strings.HasPrefix(test.Checks[0].Message, "A CA, until ") ||
			!strings.Contains(test.Checks[0].Message, cert.Sha256) || !strings.Contains(test.Checks[1].Message, idpCert.Sha256) {
			t.Fatalf("bundle = %+v", test)
		}
		// The pin cleared, which needs no password: the bundle's CA vouches.
		h.json("PUT", "/api/v1/admin/integrations/vcenter", admin, api.IntegrationWrite{Settings: vcenter(map[string]any{
			"cert_sha256": ""})}, 200, nil)
		h.json("POST", "/api/v1/admin/integrations/vcenter/test", admin, nil, 200, &test)
		if !test.Ok || len(test.Checks) != 4 || test.Certificate == nil || !test.Certificate.Trusted || test.Certificate.Pinned ||
			!strings.HasSuffix(test.Checks[0].Message, "a CA rvav trusts vouches for it.") {
			t.Fatalf("trusted through the bundle = %+v", test)
		}
		// The provider's discovery and keys are read trusting it too.
		h.json("POST", "/api/v1/admin/integrations/oidc/test", admin, provider, 200, &test)
		if !test.Ok || test.Certificate == nil || !test.Certificate.Trusted || len(test.Checks) != 3 {
			t.Fatalf("provider, trusted = %+v", test)
		}
	})
}
