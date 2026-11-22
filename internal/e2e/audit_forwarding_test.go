package e2e

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auditlog"
	"github.com/damhau/rvav/internal/dispatch"
)

// testPKI is a CA with a server certificate for 127.0.0.1 and a client
// certificate, as PEM.
type testPKI struct {
	caPEM, clientPEM, clientKeyPEM string
	server                         tls.Certificate
	pool                           *x509.CertPool
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	key := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	caKey := key()
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "e2e syslog CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	issue := func(serial int64, cn string, usage x509.ExtKeyUsage, ip net.IP) ([]byte, *ecdsa.PrivateKey) {
		k := key()
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{usage},
			KeyUsage: x509.KeyUsageDigitalSignature}
		if ip != nil {
			tmpl.IPAddresses = []net.IP{ip}
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &k.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return der, k
	}
	pemOf := func(typ string, b []byte) string { return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b})) }
	serverDER, serverKey := issue(2, "syslog.e2e", x509.ExtKeyUsageServerAuth, net.ParseIP("127.0.0.1"))
	clientDER, clientKey := issue(3, "rvav", x509.ExtKeyUsageClientAuth, nil)
	serverKeyDER, _ := x509.MarshalPKCS8PrivateKey(serverKey)
	clientKeyDER, _ := x509.MarshalPKCS8PrivateKey(clientKey)
	server, err := tls.X509KeyPair([]byte(pemOf("CERTIFICATE", serverDER)), []byte(pemOf("PRIVATE KEY", serverKeyDER)))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return testPKI{caPEM: pemOf("CERTIFICATE", caDER), clientPEM: pemOf("CERTIFICATE", clientDER),
		clientKeyPEM: pemOf("PRIVATE KEY", clientKeyDER), server: server, pool: pool}
}

// syslogReceiver is an RFC 5425 server: TLS, a client certificate
// required, octet-counted frames. It can stop and start again on its port.
type syslogReceiver struct {
	t    *testing.T
	pki  testPKI
	addr string
	mu   sync.Mutex
	ln   net.Listener
	msgs chan string
	// peers are the client certificates' subjects.
	peers chan string
}

func newSyslogReceiver(t *testing.T, pki testPKI) *syslogReceiver {
	r := &syslogReceiver{t: t, pki: pki, msgs: make(chan string, 100), peers: make(chan string, 100)}
	r.start("127.0.0.1:0")
	t.Cleanup(r.stop)
	return r
}

func (r *syslogReceiver) start(addr string) {
	r.t.Helper()
	ln, err := tls.Listen("tcp", addr, &tls.Config{Certificates: []tls.Certificate{r.pki.server}, ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs: r.pki.pool, MinVersion: tls.VersionTLS12})
	if err != nil {
		r.t.Fatal(err)
	}
	r.mu.Lock()
	r.ln, r.addr = ln, ln.Addr().String()
	r.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(c.(*tls.Conn))
		}
	}()
}

func (r *syslogReceiver) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ln != nil {
		_ = r.ln.Close()
		r.ln = nil
	}
}

// serve reads "MSG-LEN SP SYSLOG-MSG" frames until the client closes.
func (r *syslogReceiver) serve(c *tls.Conn) {
	defer c.Close()
	if err := c.Handshake(); err != nil {
		return
	}
	if certs := c.ConnectionState().PeerCertificates; len(certs) > 0 {
		r.peers <- certs[0].Subject.CommonName
	}
	in := bufio.NewReader(c)
	for {
		n, err := in.ReadString(' ')
		if err != nil {
			return
		}
		size, err := strconv.Atoi(strings.TrimSuffix(n, " "))
		if err != nil || size <= 0 {
			r.msgs <- "bad frame length " + strconv.Quote(n)
			return
		}
		msg := make([]byte, size)
		if _, err := io.ReadFull(in, msg); err != nil {
			r.msgs <- "short frame: " + err.Error()
			return
		}
		r.msgs <- string(msg)
	}
}

func (r *syslogReceiver) next(t *testing.T) string {
	t.Helper()
	select {
	case m := <-r.msgs:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no syslog message")
		return ""
	}
}

func (r *syslogReceiver) none(t *testing.T) {
	t.Helper()
	select {
	case m := <-r.msgs:
		t.Fatalf("unexpected syslog message %s", m)
	default:
	}
}

// rfc5424 is the header RFC 5424 gives a message, and its MSG, after a
// byte order mark when it isn't all ASCII.
var rfc5424 = regexp.MustCompile(`^<(\d{1,3})>1 (\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{6}Z) (\S{1,255}) (\S{1,48}) - (\S{1,32}) - (?:\x{FEFF})?(\{.*\})$`)

type syslogMsg struct {
	pri                  int
	at, host, app, msgID string
	event                auditlog.Event
}

func parseSyslog(t *testing.T, raw string) syslogMsg {
	t.Helper()
	m := rfc5424.FindStringSubmatch(raw)
	if m == nil {
		t.Fatalf("not RFC 5424: %q", raw)
	}
	pri, _ := strconv.Atoi(m[1])
	out := syslogMsg{pri: pri, at: m[2], host: m[3], app: m[4], msgID: m[5]}
	if err := json.Unmarshal([]byte(m[6]), &out.event); err != nil {
		t.Fatalf("MSG not JSON: %v: %s", err, m[6])
	}
	return out
}

// TestAuditForwardingSyslog: audit events of the destination's categories
// go to a syslog server over TLS, RFC 5425-framed, rvav presenting its
// client certificate; refused sign-ins are warnings. While the server is
// down, they wait, the destination says why, and they go once it is back.
// The test button sends one now. Auditors see destinations and change
// none.
func TestAuditForwardingSyslog(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		// svc-awx is an auditor for all of rvav.
		const admin, auditor = "mkeller", "svc-awx"
		pki := newTestPKI(t)
		rcv := newSyslogReceiver(t, pki)
		host, port, _ := net.SplitHostPort(rcv.addr)
		portN, _ := strconv.Atoi(port)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "syslog-key", Kind: api.SecretWriteKindToken,
			Value: new(strings.ReplaceAll(pki.clientKeyPEM, "\n", " "))}, 201, nil)

		var problem struct{ Errors []api.FieldError }
		udp, local4 := api.SyslogUDP, api.FacilityLocal4
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM",
			Syslog: &api.AuditSyslog{Host: "not a host", Transport: &udp, CaCertificate: &pki.caPEM}}, 422, &problem)
		if got := fieldsOf(problem.Errors); !slices.Equal(got, []string{"/syslog/host", "/syslog/ca_certificate"}) {
			t.Fatalf("bad syslog = %v", got)
		}
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM",
			Syslog: &api.AuditSyslog{Host: host, ClientCertificate: &pki.clientPEM, ClientKeySecret: new("no-such-secret")}}, 422, &problem)
		if got := fieldsOf(problem.Errors); !slices.Equal(got, []string{"/syslog/client_key_secret"}) {
			t.Fatalf("unknown key = %v", got)
		}
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM"}, 422, &problem)
		if got := fieldsOf(problem.Errors); !slices.Equal(got, []string{"/syslog"}) {
			t.Fatalf("neither = %v", got)
		}

		var dest api.AuditDestination

		getDest := func(user string) api.AuditDestination {
			t.Helper()
			var d api.AuditDestination
			h.json("GET", "/api/v1/admin/audit-destinations/"+dest.Id, user, nil, 200, &d)
			return d
		}
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM",
			Categories: &[]api.AuditCategory{api.AuditCategoryToken, api.AuditCategorySignIn, api.AuditCategoryToken},
			Syslog: &api.AuditSyslog{Host: host, Port: &portN, Facility: &local4, AppName: new("rvav-e2e"), CaCertificate: &pki.caPEM,
				ClientCertificate: &pki.clientPEM, ClientKeySecret: new("syslog-key")}}, 201, &dest)
		if dest.Kind != api.AuditDestinationSyslog || !dest.Enabled || *dest.Syslog.Transport != api.SyslogTLS ||
			!slices.Equal(dest.Categories, []api.AuditCategory{api.AuditCategorySignIn, api.AuditCategoryToken}) || dest.State.Backlog != 0 {
			t.Fatalf("created = %+v", dest)
		}
		fwd := h.svc.AuditLog

		// A token's creation (token) and a refused sign-in (sign_in) go; the
		// destination's own creation (settings) doesn't.
		var tok api.ApiTokenCreated
		h.json("POST", "/api/v1/tokens", "tnguyen", api.ApiTokenCreate{Name: "reports"}, 201, &tok)
		code, _, _ := h.do("POST", "/api/v1/auth/sign-in", "", api.SignInRequest{Username: "nobody", Password: "wrong"},
			"X-Requested-With", "e2e")
		if code != http.StatusUnauthorized {
			t.Fatalf("sign-in = %d", code)
		}
		dest = getDest(auditor)
		if dest.State.Backlog != 2 {
			t.Fatalf("backlog = %+v", dest.State)
		}
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		if peer := <-rcv.peers; peer != "rvav" {
			t.Fatalf("client certificate %q", peer)
		}
		created, refused := parseSyslog(t, rcv.next(t)), parseSyslog(t, rcv.next(t))
		rcv.none(t)
		if created.pri != 20*8+5 || created.host != "rvav.e2e" || created.app != "rvav-e2e" || created.msgID != "token.create" ||
			created.event.Category != "token" || created.event.Actor == nil || created.event.Actor.Name != "Thomas Nguyen" ||
			created.event.TargetID != tok.Id || created.event.TargetType != "api_token" {
			t.Fatalf("token.create = %+v", created)
		}
		if refused.pri != 20*8+4 || refused.msgID != "auth.sign_in_failed" || refused.event.Category != "sign_in" ||
			refused.event.Actor != nil {
			t.Fatalf("auth.sign_in_failed = %+v", refused)
		}
		if at, err := time.Parse(time.RFC3339Nano, created.at); err != nil || time.Since(at) > time.Minute {
			t.Fatalf("timestamp %s: %v", created.at, err)
		}
		dest = getDest(admin)
		if s := dest.State; s.Backlog != 0 || s.Delivered != 2 || s.Failures != 0 || s.LastSuccessAt == nil || s.LastError != nil {
			t.Fatalf("after = %+v", s)
		}

		// Down: the event waits, the destination says why and when it tries again.
		rcv.stop()
		h.json("POST", "/api/v1/tokens", "tnguyen", api.ApiTokenCreate{Name: "while down"}, 201, nil)
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		dest = getDest(admin)
		if s := dest.State; s.Backlog != 1 || s.Failures != 1 || s.LastError == nil || !strings.Contains(*s.LastError, "refused") ||
			s.NextAttemptAt == nil || s.NextAttemptAt.Sub(*s.LastErrorAt) != auditlog.Backoff(1) {
			t.Fatalf("down = %+v", s)
		}
		var test api.AuditDestinationTest
		h.json("POST", "/api/v1/admin/audit-destinations/"+dest.Id+"/test", admin, nil, 200, &test)
		if test.Ok || !strings.Contains(test.Message, "refused") {
			t.Fatalf("test while down = %+v", test)
		}
		// Not due yet: nothing is tried.
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		dest = getDest(admin)
		if dest.State.Failures != 1 {
			t.Fatalf("tried before due: %+v", dest.State)
		}

		// Back: what waited goes, in order, once it is due.
		rcv.start(rcv.addr)
		fwd.Now = func() time.Time { return time.Now().Add(time.Minute) }
		defer func() { fwd.Now = nil }()
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		<-rcv.peers
		if m := parseSyslog(t, rcv.next(t)); m.msgID != "token.create" || m.event.Detail == nil ||
			!strings.Contains(string(m.event.Detail), "while down") {
			t.Fatalf("after recovery = %+v", m)
		}
		rcv.none(t)
		dest = getDest(admin)
		if s := dest.State; s.Backlog != 0 || s.Delivered != 3 || s.Failures != 0 || s.LastError != nil || s.NextAttemptAt != nil {
			t.Fatalf("recovered = %+v", s)
		}

		// The test button: one made-up event, now, not queued.
		h.json("POST", "/api/v1/admin/audit-destinations/"+dest.Id+"/test", admin, nil, 200, &test)
		if !test.Ok || !strings.Contains(test.Message, "over TLS") {
			t.Fatalf("test = %+v", test)
		}
		<-rcv.peers
		if m := parseSyslog(t, rcv.next(t)); m.msgID != "settings.audit_destination.test" || m.event.ID != "test" {
			t.Fatalf("test message = %+v", m)
		}

		// Auditors see it, and change nothing.
		var list api.AuditDestinationList
		h.json("GET", "/api/v1/admin/audit-destinations", auditor, nil, 200, &list)
		if len(list.Items) != 1 || list.Items[0].Id != dest.Id {
			t.Fatalf("auditor's list = %+v", list)
		}
		h.json("POST", "/api/v1/admin/audit-destinations/"+dest.Id+"/test", auditor, nil, 403, nil)
		h.json("DELETE", "/api/v1/admin/audit-destinations/"+dest.Id, auditor, nil, 403, nil)
		h.json("GET", "/api/v1/admin/audit-destinations", "tnguyen", nil, 403, nil)

		// Off: nothing more is queued.
		h.json("PUT", "/api/v1/admin/audit-destinations/"+dest.Id, admin, api.AuditDestinationWrite{Name: "SIEM", Enabled: new(false),
			Syslog: dest.Syslog}, 200, &dest)
		h.json("POST", "/api/v1/tokens", "tnguyen", api.ApiTokenCreate{Name: "while off"}, 201, nil)
		dest = getDest(admin)
		if dest.Enabled || dest.State.Backlog != 0 {
			t.Fatalf("off = %+v", dest)
		}
		h.json("DELETE", "/api/v1/admin/audit-destinations/"+dest.Id, admin, nil, 204, nil)
		h.json("GET", "/api/v1/admin/audit-destinations/"+dest.Id, admin, nil, 404, nil)
		if n := h.auditCount("settings.audit_destination.create") + h.auditCount("settings.audit_destination.update") +
			h.auditCount("settings.audit_destination.test") + h.auditCount("settings.audit_destination.delete"); n != 5 {
			t.Fatalf("%d destination changes audited", n)
		}
	})
}

// TestAuditForwardingWebhook: a SIEM's webhook gets every category's
// events, signed as subscriptions' are; a 500 keeps them for the next
// attempt, which sends them again.
func TestAuditForwardingWebhook(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, secret = "mkeller", "siem-signing-secret"
		type call struct {
			body      []byte
			signature string
			topic     string
		}
		var mu sync.Mutex
		var calls []call
		siem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			calls = append(calls, call{body: b, signature: r.Header.Get("X-Rvav-Signature"), topic: r.Header.Get("X-Rvav-Topic")})
			first := len(calls) == 1
			mu.Unlock()
			if first {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		}))
		t.Cleanup(siem.Close)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "siem-hmac", Kind: api.SecretWriteKindToken,
			Value: new(secret)}, 201, nil)

		var problem struct{ Errors []api.FieldError }
		hook := api.AuditWebhook{Url: siem.URL + "/rvav/audit", Secret: "siem-hmac"}
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM", Webhook: &hook}, 422, &problem)
		if got := fieldsOf(problem.Errors); !slices.Equal(got, []string{"/webhook/url"}) {
			t.Fatalf("host not allowed = %v", got)
		}
		h.allowWebhooks(siem.URL)
		var dest api.AuditDestination

		getDest := func(user string) api.AuditDestination {
			t.Helper()
			var d api.AuditDestination
			h.json("GET", "/api/v1/admin/audit-destinations/"+dest.Id, user, nil, 200, &d)
			return d
		}
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM", Webhook: &hook}, 201, &dest)
		h.json("POST", "/api/v1/tokens", "tnguyen", api.ApiTokenCreate{Name: "reports"}, 201, nil)

		fwd := h.svc.AuditLog
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		dest = getDest(admin)
		if s := dest.State; s.Backlog != 2 || s.Failures != 1 || s.LastError == nil || !strings.HasSuffix(*s.LastError, "answered 500") {
			t.Fatalf("after a 500 = %+v", s)
		}
		fwd.Now = func() time.Time { return time.Now().Add(time.Minute) }
		defer func() { fwd.Now = nil }()
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		dest = getDest(admin)
		if s := dest.State; s.Backlog != 0 || s.Delivered != 2 || s.Failures != 0 || s.LastError != nil {
			t.Fatalf("after the retry = %+v", s)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(calls) != 2 || string(calls[0].body) != string(calls[1].body) {
			t.Fatalf("%d calls, the retry the same: %v", len(calls), len(calls) == 2 && string(calls[0].body) == string(calls[1].body))
		}
		for _, c := range calls {
			ts, _, _ := strings.Cut(strings.TrimPrefix(c.signature, "t="), ",")
			at, _ := strconv.ParseInt(ts, 10, 64)
			if c.signature != dispatch.Sign(secret, time.Unix(at, 0), c.body) || c.topic != "audit" {
				t.Fatalf("signature %q, topic %q", c.signature, c.topic)
			}
		}
		var body auditlog.Body
		if err := json.Unmarshal(calls[1].body, &body); err != nil {
			t.Fatal(err)
		}
		if body.Contract != "rvav.audit.v1" || len(body.Events) != 2 || body.Events[0].Action != "settings.audit_destination.create" ||
			body.Events[0].Category != "settings" || body.Events[1].Action != "token.create" || body.Events[1].Category != "token" {
			t.Fatalf("body = %s", calls[1].body)
		}
		if id0, _ := strconv.Atoi(body.Events[0].ID); id0 == 0 {
			t.Fatalf("event ids %s", calls[1].body)
		}
	})
}
