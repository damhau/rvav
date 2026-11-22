package auditlog

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

func TestFormat(t *testing.T) {
	at := time.Date(2026, 11, 19, 9, 30, 0, 123456789, time.FixedZone("CET", 3600))
	e := Event{ID: "42", At: at, Category: "settings", Action: "settings.audit_destination.update.something.long",
		TargetType: "audit_destination", TargetID: "adt_1", Detail: json.RawMessage(`{"name":"SIEM"}`)}
	got := string(Format(Facilities["local0"], "rvav.corp.example", "rvav", e))
	want := `<133>1 2026-11-19T08:30:00.123456Z rvav.corp.example rvav - settings.audit_destination - {"id":"42",`
	if !strings.HasPrefix(got, want) {
		t.Fatalf("got  %s\nwant %s…", got, want)
	}
	e.Action = "auth.sign_in_failed"
	if got := string(Format(Facilities["authpriv"], "", "my app", e)); !strings.HasPrefix(got, "<84>1 2026-11-19T08:30:00.123456Z - my_app - auth.sign_in_failed - {") {
		t.Fatalf("warning = %s", got)
	}
}

func TestUnfold(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	text := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	b, _ := pem.Decode([]byte(Unfold(strings.ReplaceAll(text, "\n", " "))))
	if b == nil || b.Type != "PRIVATE KEY" || string(b.Bytes) != string(der) {
		t.Fatalf("unfolded = %q", Unfold(strings.ReplaceAll(text, "\n", " ")))
	}
	if Unfold(text) != strings.TrimSpace(text) {
		t.Fatal("a PEM with its lines changed")
	}
}

func TestBackoff(t *testing.T) {
	for n, want := range map[int]time.Duration{0: 5 * time.Second, 1: 5 * time.Second, 2: 30 * time.Second, 4: 5 * time.Minute, 40: 5 * time.Minute} {
		if got := Backoff(n); got != want {
			t.Errorf("Backoff(%d) = %s, want %s", n, got, want)
		}
	}
}

func syslogDestination(addr string, transport api.SyslogTransport) store.AuditDestination {
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	b, _ := json.Marshal(api.AuditSyslog{Host: host, Port: &p, Transport: &transport})
	return store.AuditDestination{Kind: "syslog", Settings: b}
}

// TCP and UDP, for labs: TCP frames as TLS does, UDP sends one event per
// datagram, a large one without its detail.
func TestSyslogTCPAndUDP(t *testing.T) {
	f := &Forwarder{Hostname: "rvav.test", Timeout: 2 * time.Second}
	events := []Event{{ID: "1", At: time.Now(), Action: "token.create", Detail: json.RawMessage(`{}`)},
		{ID: "2", At: time.Now(), Action: "token.revoke", Detail: json.RawMessage(`{"name":"` + strings.Repeat("x", 9000) + `"}`)}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	frames := make(chan string, 2)
	// Open until the test ends: the test send counts a server that closes
	// the connection after the events as a failure.
	done := make(chan struct{})
	defer close(done)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		in := bufio.NewReader(c)
		for range 2 {
			n, _ := in.ReadString(' ')
			size, _ := strconv.Atoi(strings.TrimSpace(n))
			msg := make([]byte, size)
			if _, err := io.ReadFull(in, msg); err != nil {
				frames <- err.Error()
				return
			}
			frames <- string(msg)
		}
		<-done
	}()
	msg, err := f.Send(context.Background(), syslogDestination(ln.Addr().String(), api.SyslogTCP), events)
	if err != nil || !strings.HasSuffix(msg, "over TCP.") {
		t.Fatalf("TCP send = %q, %v", msg, err)
	}
	for _, id := range []string{"1", "2"} {
		if m := <-frames; !strings.HasPrefix(m, "<133>1 ") || !strings.Contains(m, `{"id":"`+id+`"`) {
			t.Fatalf("TCP frame %s = %.80s", id, m)
		}
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err := f.Send(context.Background(), syslogDestination(pc.LocalAddr().String(), api.SyslogUDP), events); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64<<10)
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	for _, want := range []string{`"id":"1"`, `"detail":{"truncated":true}`} {
		n, _, err := pc.ReadFrom(buf)
		if err != nil || !strings.HasPrefix(string(buf[:n]), "<133>1 ") || !strings.Contains(string(buf[:n]), want) || n > maxDatagram {
			t.Fatalf("UDP datagram = %.120s (%d bytes), %v; want %s", buf[:n], n, err, want)
		}
	}
}
