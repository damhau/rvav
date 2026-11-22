package notify

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/wneessen/go-mail/smtp"

	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
)

// In FIPS 140-only mode rvav signs in to the SMTP server with a mechanism
// that mode allows, and says why when the server offers none; a Slack
// signing secret shorter than 112 bits is refused, not a panic (ADR-0060).
func TestNotificationsInFIPSOnlyMode(t *testing.T) {
	if !fipstest.In(t, fips.Only) {
		return
	}
	for _, c := range []struct {
		tls    bool
		offers []string
		want   string
	}{
		{true, []string{"CRAM-MD5", "SCRAM-SHA-1", "SCRAM-SHA-256", "PLAIN"}, "SCRAM-SHA-256"},
		{true, []string{"CRAM-MD5", "NTLM", "LOGIN", "PLAIN"}, "PLAIN"},
		{true, []string{"NTLM", "LOGIN"}, "LOGIN"},
		{false, []string{"SCRAM-SHA-256", "PLAIN"}, "SCRAM-SHA-256"},
		{false, []string{"PLAIN", "LOGIN"}, ""},
		{true, []string{"CRAM-MD5", "SCRAM-SHA-1", "NTLM"}, ""},
	} {
		a := &fipsAuth{user: "rvav", pass: "s3cret", host: "smtp.corp.example", mode: fips.Only}
		proto, _, err := a.Start(&smtp.ServerInfo{Name: "smtp.corp.example", TLS: c.tls, Auth: c.offers})
		if c.want == "" {
			if err == nil || !strings.Contains(err.Error(), "FIPS 140-only mode") {
				t.Errorf("%v (TLS %v): %q, %v", c.offers, c.tls, proto, err)
			}
			continue
		}
		if proto != c.want || err != nil {
			t.Errorf("%v (TLS %v): %q, %v; want %s", c.offers, c.tls, proto, err, c.want)
		}
	}
	e := &Email{Config: config.SMTPConfig{Host: "smtp.corp.example", Port: 587, Username: "rvav", Password: "s3cret"}}
	if _, err := e.client(); err != nil {
		t.Fatal(err)
	}

	if err := VerifySlack(nil, nil, "short"); err == nil || !strings.Contains(err.Error(), "112 bits") {
		t.Fatalf("a short Slack signing secret: %v", err)
	}
}

// In FIPS 140 mode (on) rvav chooses an approved mechanism too: CRAM-MD5,
// NTLM or SCRAM-SHA-1 only when nothing else is offered, then never.
func TestNotificationsInFIPSOnMode(t *testing.T) {
	if !fipstest.In(t, fips.On) {
		return
	}
	a := &fipsAuth{user: "rvav", pass: "s3cret", host: "smtp.corp.example", mode: fips.On}
	if proto, _, err := a.Start(&smtp.ServerInfo{Name: "smtp.corp.example", TLS: true,
		Auth: []string{"CRAM-MD5", "NTLM", "SCRAM-SHA-1", "PLAIN"}}); proto != "PLAIN" || err != nil {
		t.Fatalf("CRAM-MD5, NTLM, SCRAM-SHA-1 and PLAIN over TLS: %q, %v", proto, err)
	}

	port, next := smtpOffering(t, "CRAM-MD5 PLAIN")
	e := &Email{Config: config.SMTPConfig{Host: "127.0.0.1", Port: port, TLS: "none", Username: "rvav", Password: "s3cret"}}
	err := e.Test(context.Background())
	if cmd := <-next; cmd != "QUIT" || err == nil || !strings.Contains(err.Error(), "RVAV_FIPS=on") {
		t.Fatalf("CRAM-MD5, and PLAIN without TLS: the client sent %q, %v", cmd, err)
	}
}

// smtpOffering is a mail server that offers the mechanisms given, and says
// what the client sent after EHLO: the AUTH it chose, or QUIT.
func smtpOffering(t *testing.T, mechanisms string) (int, <-chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	next := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			next <- err.Error()
			return
		}
		defer c.Close()
		in := bufio.NewReader(c)
		_, _ = fmt.Fprint(c, "220 smtp.test ESMTP\r\n")
		for {
			line, err := in.ReadString('\n')
			if err != nil {
				next <- err.Error()
				return
			}
			if cmd := strings.TrimSpace(line); !strings.HasPrefix(cmd, "EHLO") {
				next <- cmd
				_, _ = fmt.Fprint(c, "221 bye\r\n")
				return
			}
			_, _ = fmt.Fprintf(c, "250-smtp.test\r\n250 AUTH %s\r\n", mechanisms)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, next
}
