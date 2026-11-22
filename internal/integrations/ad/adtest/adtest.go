// Package adtest is an Active Directory for tests: an in-process LDAP
// server (gldap) holding entries in memory, answering as Active Directory
// does where rvav reads its answers. It listens for LDAPS and for LDAP with
// StartTLS, with a self-signed certificate.
package adtest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
	"github.com/hashicorp/go-hclog"
	"github.com/jimlambrt/gldap"
)

// Server is the directory.
type Server struct {
	// LDAPS and StartTLS are its URLs: ldaps://, and ldap:// for StartTLS.
	LDAPS, StartTLS string
	// BindDN and Password are the service account's.
	BindDN, Password string
	// Cert is its certificate.
	Cert *x509.Certificate

	mu      sync.Mutex
	entries map[string]*entry
	bound   map[int]bool
	// Adds counts the computer objects added; FailDelete makes deletes fail.
	adds       int
	failDelete bool
}

type entry struct {
	dn    string
	attrs map[string][]string
}

// BaseDN is the directory's.
const BaseDN = "DC=corp,DC=example"

// Start starts a directory with the base DN, an OU for servers and one
// for each name given, the service account, and the groups given (DNs).
func Start(t testing.TB, ous []string, groups ...string) *Server {
	t.Helper()
	cert, tlsCfg := selfSigned(t)
	s := &Server{BindDN: "CN=svc-rvav,OU=Service accounts," + BaseDN, Password: "e2e-only-ad-password", Cert: cert,
		entries: map[string]*entry{}, bound: map[int]bool{}}
	s.Put(BaseDN, map[string][]string{"objectClass": {"top", "domain"}})
	s.Put("OU=Service accounts,"+BaseDN, map[string][]string{"objectClass": {"organizationalUnit"}})
	s.Put(s.BindDN, map[string][]string{"objectClass": {"user"}, "sAMAccountName": {"svc-rvav"}})
	for _, ou := range ous {
		s.Put(ou, map[string][]string{"objectClass": {"organizationalUnit"}})
	}
	for _, g := range groups {
		s.Put(g, map[string][]string{"objectClass": {"group"}})
	}
	s.LDAPS = "ldaps://" + s.run(t, tlsCfg, true)
	s.StartTLS = "ldap://" + s.run(t, tlsCfg, false)
	return s
}

func (s *Server) run(t testing.TB, tlsCfg *tls.Config, ldaps bool) string {
	t.Helper()
	srv, err := gldap.NewServer(gldap.WithLogger(hclog.NewNullLogger()))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := gldap.NewMux()
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(mux.Bind(s.bind))
	must(mux.ExtendedOperation(s.startTLS(tlsCfg), gldap.ExtendedOperationStartTLS))
	must(mux.Search(s.search))
	must(mux.Add(s.add))
	must(mux.Modify(s.modify))
	must(mux.Delete(s.delete))
	must(mux.DefaultRoute(func(w *gldap.ResponseWriter, r *gldap.Request) {
		_ = w.Write(r.NewResponse(gldap.WithResponseCode(gldap.ResultUnwillingToPerform)))
	}))
	must(srv.Router(mux))
	addr := freeAddr(t)
	var opts []gldap.Option
	if ldaps {
		opts = append(opts, gldap.WithTLSConfig(tlsCfg))
	}
	go func() { _ = srv.Run(addr, opts...) }()
	t.Cleanup(func() { _ = srv.Stop() })
	for deadline := time.Now().Add(5 * time.Second); !srv.Ready(); {
		if time.Now().After(deadline) {
			t.Fatal("the test directory didn't start")
		}
		time.Sleep(time.Millisecond)
	}
	return addr
}

func freeAddr(t testing.TB) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func key(dn string) string {
	d, err := ldap.ParseDN(dn)
	if err != nil {
		return strings.ToLower(dn)
	}
	return strings.ToLower(d.String())
}

// Put sets an entry.
func (s *Server) Put(dn string, attrs map[string][]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key(dn)] = &entry{dn: dn, attrs: attrs}
}

// Entry is an entry's attributes; nil when there is none.
func (s *Server) Entry(dn string) map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[key(dn)]; e != nil {
		return e.attrs
	}
	return nil
}

// Computers are the DNs of the computer objects, in order.
func (s *Server) Computers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.entries {
		if slices.Contains(e.attrs["objectClass"], "computer") {
			out = append(out, e.dn)
		}
	}
	slices.Sort(out)
	return out
}

// Adds is how many computer objects were added.
func (s *Server) Adds() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adds
}

// FailDelete makes deletes fail, as when the service account may not.
func (s *Server) FailDelete(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failDelete = fail
}

func (s *Server) bind(w *gldap.ResponseWriter, r *gldap.Request) {
	resp := r.NewBindResponse(gldap.WithResponseCode(gldap.ResultInvalidCredentials))
	defer func() { _ = w.Write(resp) }()
	m, err := r.GetSimpleBindMessage()
	if err != nil {
		return
	}
	if strings.EqualFold(m.UserName, s.BindDN) && string(m.Password) == s.Password {
		s.mu.Lock()
		s.bound[r.ConnectionID()] = true
		s.mu.Unlock()
		resp.SetResultCode(gldap.ResultSuccess)
	}
}

func (s *Server) startTLS(cfg *tls.Config) gldap.HandlerFunc {
	return func(w *gldap.ResponseWriter, r *gldap.Request) {
		res := r.NewExtendedResponse(gldap.WithResponseCode(gldap.ResultSuccess))
		res.SetResponseName(gldap.ExtendedOperationStartTLS)
		if err := w.Write(res); err != nil {
			return
		}
		_ = r.StartTLS(cfg)
	}
}

func (s *Server) isBound(r *gldap.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bound[r.ConnectionID()]
}

var equality = regexp.MustCompile(`\(([A-Za-z]+)=([^()]*)\)`)

// matches reads the filters rvav sends: (attr=value), (attr=*), and an
// AND of them.
func matches(filter string, e *entry) bool {
	for _, m := range equality.FindAllStringSubmatch(filter, -1) {
		attr, want := m[1], m[2]
		var vals []string
		for k, v := range e.attrs {
			if strings.EqualFold(k, attr) {
				vals = v
			}
		}
		if want == "*" {
			if len(vals) == 0 && !strings.EqualFold(attr, "objectClass") {
				return false
			}
			continue
		}
		if !slices.ContainsFunc(vals, func(v string) bool { return strings.EqualFold(v, unescape(want)) }) {
			return false
		}
	}
	return true
}

func unescape(v string) string {
	return regexp.MustCompile(`\\([0-9a-fA-F]{2})`).ReplaceAllStringFunc(v, func(h string) string {
		var b byte
		_, _ = fmt.Sscanf(h[1:], "%02x", &b)
		return string(b)
	})
}

func under(dn, base string) bool {
	d, err1 := ldap.ParseDN(dn)
	b, err2 := ldap.ParseDN(base)
	return err1 == nil && err2 == nil && (b.EqualFold(d) || b.AncestorOfFold(d))
}

func (s *Server) search(w *gldap.ResponseWriter, r *gldap.Request) {
	done := r.NewSearchDoneResponse(gldap.WithResponseCode(gldap.ResultSuccess))
	defer func() { _ = w.Write(done) }()
	if !s.isBound(r) {
		done.SetResultCode(gldap.ResultInsufficientAccessRights)
		return
	}
	m, err := r.GetSearchMessage()
	if err != nil {
		done.SetResultCode(gldap.ResultOperationsError)
		return
	}
	s.mu.Lock()
	base := s.entries[key(m.BaseDN)]
	var found []*entry
	if base != nil {
		for _, e := range s.entries {
			in := e == base
			if m.Scope != gldap.BaseObject {
				in = under(e.dn, m.BaseDN)
			}
			if in && matches(m.Filter, e) {
				found = append(found, e)
			}
		}
	}
	s.mu.Unlock()
	if base == nil {
		done.SetResultCode(gldap.ResultNoSuchObject)
		return
	}
	for _, e := range found {
		res := r.NewSearchResponseEntry(e.dn)
		for k, v := range e.attrs {
			res.AddAttribute(k, v)
		}
		_ = w.Write(res)
	}
}

func parent(dn string) string {
	d, err := ldap.ParseDN(dn)
	if err != nil || len(d.RDNs) < 2 {
		return ""
	}
	return (&ldap.DN{RDNs: d.RDNs[1:]}).String()
}

func (s *Server) add(w *gldap.ResponseWriter, r *gldap.Request) {
	resp := r.NewResponse(gldap.WithApplicationCode(gldap.ApplicationAddResponse), gldap.WithResponseCode(gldap.ResultSuccess))
	defer func() { _ = w.Write(resp) }()
	if !s.isBound(r) {
		resp.SetResultCode(gldap.ResultInsufficientAccessRights)
		return
	}
	m, err := r.GetAddMessage()
	if err != nil {
		resp.SetResultCode(gldap.ResultOperationsError)
		return
	}
	attrs := map[string][]string{}
	for _, a := range m.Attributes {
		attrs[a.Type] = a.Vals
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries[key(parent(m.DN))] == nil {
		resp.SetResultCode(gldap.ResultNoSuchObject)
		return
	}
	if s.entries[key(m.DN)] != nil {
		resp.SetResultCode(gldap.ResultEntryAlreadyExists)
		return
	}
	// Active Directory keeps sAMAccountNames unique in the domain.
	for _, e := range s.entries {
		if len(attrs["sAMAccountName"]) > 0 && slices.ContainsFunc(e.attrs["sAMAccountName"], func(v string) bool {
			return strings.EqualFold(v, attrs["sAMAccountName"][0])
		}) {
			resp.SetResultCode(gldap.ResultEntryAlreadyExists)
			return
		}
	}
	s.entries[key(m.DN)] = &entry{dn: m.DN, attrs: attrs}
	if slices.Contains(attrs["objectClass"], "computer") {
		s.adds++
	}
}

func (s *Server) modify(w *gldap.ResponseWriter, r *gldap.Request) {
	resp := r.NewModifyResponse(gldap.WithResponseCode(gldap.ResultSuccess))
	defer func() { _ = w.Write(resp) }()
	if !s.isBound(r) {
		resp.SetResultCode(gldap.ResultInsufficientAccessRights)
		return
	}
	m, err := r.GetModifyMessage()
	if err != nil {
		resp.SetResultCode(gldap.ResultOperationsError)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[key(m.DN)]
	if e == nil {
		resp.SetResultCode(gldap.ResultNoSuchObject)
		return
	}
	for _, c := range m.Changes {
		name, vals := c.Modification.Type, values(c.Modification.Vals)
		switch c.Operation {
		case gldap.AddAttribute:
			for _, v := range vals {
				if slices.ContainsFunc(e.attrs[name], func(x string) bool { return strings.EqualFold(x, v) }) {
					// As Active Directory answers a member already there.
					resp.SetResultCode(gldap.ResultEntryAlreadyExists)
					return
				}
			}
			e.attrs[name] = append(e.attrs[name], vals...)
		case gldap.ReplaceAttribute:
			e.attrs[name] = vals
		case gldap.DeleteAttribute:
			delete(e.attrs, name)
		}
	}
}

// values reads a change's values: gldap 0.1.14 gives the BER of their set
// as one value.
func values(raw []string) []string {
	var out []string
	for _, v := range raw {
		r := bytes.NewReader([]byte(v))
		for r.Len() > 0 {
			p, err := ber.ReadPacket(r)
			if err != nil {
				return raw
			}
			out = append(out, p.Data.String())
		}
	}
	return out
}

func (s *Server) delete(w *gldap.ResponseWriter, r *gldap.Request) {
	resp := r.NewResponse(gldap.WithApplicationCode(gldap.ApplicationDelResponse), gldap.WithResponseCode(gldap.ResultSuccess))
	defer func() { _ = w.Write(resp) }()
	if !s.isBound(r) {
		resp.SetResultCode(gldap.ResultInsufficientAccessRights)
		return
	}
	m, err := r.GetDeleteMessage()
	if err != nil {
		resp.SetResultCode(gldap.ResultOperationsError)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.failDelete:
		resp.SetResultCode(gldap.ResultInsufficientAccessRights)
	case s.entries[key(m.DN)] == nil:
		resp.SetResultCode(gldap.ResultNoSuchObject)
	default:
		delete(s.entries, key(m.DN))
		// Its groups lose it, as Active Directory's links do.
		for _, e := range s.entries {
			e.attrs["member"] = slices.DeleteFunc(e.attrs["member"], func(v string) bool { return strings.EqualFold(v, m.DN) })
			if len(e.attrs["member"]) == 0 {
				delete(e.attrs, "member")
			}
		}
	}
}

func selfSigned(t testing.TB) (*x509.Certificate, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "dc01.corp.example"},
		DNSNames: []string{"dc01.corp.example", "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert, &tls.Config{MinVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}}}
}
