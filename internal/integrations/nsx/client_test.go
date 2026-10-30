package nsx

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fake(t *testing.T) (*FakeServer, *Client) {
	t.Helper()
	f := NewFakeServer()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(Options{URL: srv.URL, Username: FakeUser, Password: FakePassword})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

// TestSignIn: a user and password, refused credentials, and a client
// certificate (a principal identity) over TLS.
func TestSignIn(t *testing.T) {
	ctx := context.Background()
	_, c := fake(t)
	if v, err := c.Version(ctx); err != nil || v == "" {
		t.Fatalf("version = %q, %v", v, err)
	}
	bad := *c
	bad.opts.Password = "wrong"
	var apiErr *APIError
	if _, err := bad.Tier1s(ctx); !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden || apiErr.Code != 403 {
		t.Fatalf("a wrong password: %v", err)
	}
	if _, err := New(Options{URL: "https://nsx.corp.example"}); err == nil {
		t.Fatal("no credentials taken")
	}

	// A principal identity: the server asks for a client certificate.
	srv := httptest.NewUnstartedServer(NewFakeServer())
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()
	certFile, keyFile := clientCert(t)
	pi, err := New(Options{URL: srv.URL, CertFile: certFile, KeyFile: keyFile, Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if ts, err := pi.Tier1s(ctx); err != nil || len(ts) != 3 {
		t.Fatalf("with a certificate: %d, %v", len(ts), err)
	}
	none, _ := New(Options{URL: srv.URL, Username: "x", Password: "y", Insecure: true})
	if _, err := none.Tier1s(ctx); err == nil {
		t.Fatal("no certificate, yet signed in")
	}
}

func clientCert(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "rvav"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "rvav.crt"), filepath.Join(dir, "rvav.key")
	_ = os.WriteFile(cf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(kf, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600)
	return cf, kf
}

// TestLists: the sample's gateways, zones, services, segments and groups;
// more than a page, by cursor.
func TestLists(t *testing.T) {
	ctx := context.Background()
	f, c := fake(t)
	for i := range 2500 {
		id := fmt.Sprintf("grp-%04d", i)
		f.groups[id] = Group{ID: id, DisplayName: id, Path: GroupPath + id}
	}
	ts, err := c.Tier1s(ctx)
	if err != nil || len(ts) != 3 || ts[0].Path != "/infra/tier-1s/t1-gva-dev" {
		t.Fatalf("tier-1s: %+v, %v", ts, err)
	}
	zs, _ := c.TransportZones(ctx)
	ss, _ := c.Services(ctx)
	segs, _ := c.Segments(ctx)
	if len(zs) != 1 || zs[0].Type != "OVERLAY_STANDARD" || len(ss) != 8 || len(segs) != 2 {
		t.Fatalf("zones %d, services %d, segments %d", len(zs), len(ss), len(segs))
	}
	gs, err := c.Groups(ctx)
	if err != nil || len(gs) != 2502 {
		t.Fatalf("groups across pages: %d, %v", len(gs), err)
	}
	if n := f.Calls["GET /policy/api/v1/infra/domains"]; n != 3 {
		t.Fatalf("%d pages read, want 3", n)
	}
}

// TestSegment: made on a Tier-1, realised, updated, deleted twice; a
// Tier-1 that doesn't exist is refused as NSX does.
func TestSegment(t *testing.T) {
	ctx := context.Background()
	f, c := fake(t)
	f.RealizeAfter = 2
	var made, gone []string
	f.OnSegment = func(s Segment, deleted bool) {
		if deleted {
			gone = append(gone, s.ID)
		} else {
			made = append(made, s.ID)
		}
	}
	tags := []Tag{{Scope: OwnerScope, Tag: "rvav"}, {Scope: DeployScope, Tag: "dep_1"}}
	s := Segment{ID: "rvav-dep-1-app", DisplayName: "rvav-dep-1-app", ConnectivityPath: Tier1Path + "t1-nope",
		Subnets: []SegmentSubnet{{GatewayAddress: "10.31.41.1/24"}}, Tags: tags}
	var apiErr *APIError
	if err := c.PutSegment(ctx, s); !errors.As(err, &apiErr) || apiErr.Status != 400 || apiErr.Code != 503040 {
		t.Fatalf("an unknown Tier-1: %v", err)
	}
	s.ConnectivityPath = Tier1Path + "t1-gva-dev"
	if err := c.PutSegment(ctx, s); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"in_progress", "in_progress", "success"} {
		if st, err := c.SegmentState(ctx, s.ID); err != nil || st != want {
			t.Fatalf("state read %d = %q, %v; want %q", i, st, err, want)
		}
	}
	s.Subnets = []SegmentSubnet{{GatewayAddress: "10.31.42.1/24"}}
	if err := c.PutSegment(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := c.Segment(ctx, s.ID)
	if err != nil || got.Subnets[0].GatewayAddress != "10.31.42.1/24" || got.Path != SegmentPath+s.ID || !Owned(got.Tags, "rvav") || Owned(got.Tags, "other") {
		t.Fatalf("segment = %+v, %v", got, err)
	}
	if err := c.DeleteSegment(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSegment(ctx, s.ID); err != nil {
		t.Fatalf("deleted twice: %v", err)
	}
	if _, err := c.Segment(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if len(made) != 1 || len(gone) != 1 {
		t.Fatalf("port groups made %v, removed %v", made, gone)
	}
}

// TestGroupsAndRules: a group by tag, a policy whose rules refer to it
// and to services; references that don't exist refused; PATCH adds rules
// and keeps the others; a group a rule uses can't be deleted.
func TestGroupsAndRules(t *testing.T) {
	ctx := context.Background()
	_, c := fake(t)
	web := Group{ID: "rvav-dep-1-web", Expression: []Condition{TagCondition(Tag{Scope: DeployScope, Tag: "dep_1/web"})}}
	if err := c.PutGroup(ctx, web); err != nil {
		t.Fatal(err)
	}
	rule := Rule{ID: "web-in", SourceGroups: []string{"ANY"}, DestinationGroups: []string{GroupPath + web.ID},
		Services: []string{ServicePath + "HTTPS"}, Scope: []string{GroupPath + web.ID}, Action: "ALLOW"}
	bad := rule
	bad.Services = []string{ServicePath + "Gopher"}
	var apiErr *APIError
	if err := c.PutSecurityPolicy(ctx, SecurityPolicy{ID: "rvav-dep-1", Category: "Application", Rules: []Rule{bad}}); !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Fatalf("an unknown service: %v", err)
	}
	if err := c.PutSecurityPolicy(ctx, SecurityPolicy{ID: "rvav-dep-1", Category: "Application", Rules: []Rule{rule}}); err != nil {
		t.Fatal(err)
	}
	ssh := Rule{ID: "ssh-in", SourceGroups: []string{GroupPath + "grp-web-servers"}, DestinationGroups: []string{GroupPath + web.ID},
		Services: []string{ServicePath + "SSH"}, Action: "ALLOW"}
	if err := c.PutSecurityPolicy(ctx, SecurityPolicy{ID: "rvav-dep-1", Category: "Application", Rules: []Rule{ssh}}); err != nil {
		t.Fatal(err)
	}
	pol, err := c.SecurityPolicy(ctx, "rvav-dep-1")
	if err != nil || len(pol.Rules) != 2 || pol.Rules[0].ID != "web-in" {
		t.Fatalf("policy = %+v, %v", pol, err)
	}
	if err := c.DeleteGroup(ctx, web.ID); !errors.As(err, &apiErr) || apiErr.Code != 500030 {
		t.Fatalf("a group in use deleted: %v", err)
	}
	if err := c.DeleteSecurityPolicy(ctx, "rvav-dep-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteGroup(ctx, web.ID); err != nil {
		t.Fatal(err)
	}
	if gs, _ := c.Groups(ctx); len(gs) != 2 {
		t.Fatalf("%d groups left", len(gs))
	}
}

// TestVMTags: a VM NSX hasn't seen yet can't be tagged; once it has, its
// tags are set whole.
func TestVMTags(t *testing.T) {
	ctx := context.Background()
	f, c := fake(t)
	tags := []Tag{{Scope: OwnerScope, Tag: "rvav"}, {Scope: DeployScope, Tag: "dep_1/web"}}
	if err := c.SetVMTags(ctx, "4210-uuid", tags); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown VM: %v", err)
	}
	f.AddVM("4210-uuid", "web-dev-001")
	if err := c.SetVMTags(ctx, "4210-uuid", tags); err != nil {
		t.Fatal(err)
	}
	if err := c.SetVMTags(ctx, "4210-uuid", tags[:1]); err != nil {
		t.Fatal(err)
	}
	vs, err := c.VMs(ctx)
	if err != nil || len(vs) != 1 || len(vs[0].Tags) != 1 || vs[0].DisplayName != "web-dev-001" {
		t.Fatalf("vms = %+v, %v", vs, err)
	}
}
