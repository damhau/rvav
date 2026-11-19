package notify

import (
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
		a := &fipsOnlyAuth{user: "rvav", pass: "s3cret", host: "smtp.corp.example"}
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
