package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/wneessen/go-mail"
	"github.com/wneessen/go-mail/smtp"

	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/trust"
)

// Email sends notifications by SMTP: plain text and HTML, with the
// portal's logo when it has a PNG or JPEG one.
type Email struct {
	Config config.SMTPConfig
}

func (e *Email) client() (*mail.Client, error) {
	opts := []mail.Option{mail.WithPort(e.Config.Port), mail.WithTimeout(30 * time.Second)}
	switch e.Config.TLS {
	case "tls":
		opts = append(opts, mail.WithSSL())
	case "none":
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	default:
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	}
	if e.Config.TLS != "none" {
		// The CAs of the CA bundle too (package trust).
		opts = append(opts, mail.WithTLSConfig(&tls.Config{ServerName: e.Config.Host, MinVersion: tls.VersionTLS12,
			RootCAs: trust.Roots()}))
	}
	if e.Config.Username != "" {
		// The mechanism the server offers (Microsoft 365 has LOGIN, not PLAIN).
		auth := mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover)
		if fips.Current() == fips.Only {
			auth = mail.WithSMTPAuthCustom(&fipsOnlyAuth{user: e.Config.Username, pass: e.Config.Password, host: e.Config.Host})
		}
		opts = append(opts, auth, mail.WithUsername(e.Config.Username), mail.WithPassword(e.Config.Password))
	}
	return mail.NewClient(e.Config.Host, opts...)
}

// fipsOnlyAuth signs in to the SMTP server with SCRAM-SHA-256, or PLAIN or
// LOGIN over TLS: go-mail would otherwise choose SCRAM-SHA-1, NTLM or
// CRAM-MD5 when offered, whose SHA-1 or MD5 panics in FIPS 140-only mode
// (ADR-0060).
type fipsOnlyAuth struct {
	user, pass, host string
	chosen           smtp.Auth
}

func (a *fipsOnlyAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	switch {
	case slices.Contains(server.Auth, "SCRAM-SHA-256"):
		a.chosen = smtp.ScramSHA256Auth(a.user, a.pass)
	case server.TLS && slices.Contains(server.Auth, "PLAIN"):
		a.chosen = smtp.PlainAuth("", a.user, a.pass, a.host, false)
	case server.TLS && slices.Contains(server.Auth, "LOGIN"):
		a.chosen = smtp.LoginAuth(a.user, a.pass, a.host, false)
	default:
		return "", nil, &fips.NotAllowedError{What: "Every way to sign in that the SMTP server offers (" + strings.Join(server.Auth, ", ") + ")",
			Algorithm: "SHA-1, MD5 or a password sent without TLS", Instead: "offer SCRAM-SHA-256, or PLAIN or LOGIN over TLS"}
	}
	return a.chosen.Start(server)
}

func (a *fipsOnlyAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	return a.chosen.Next(fromServer, more)
}

// Send sends m to its user's address.
func (e *Email) Send(ctx context.Context, m Message) (string, string, error) {
	to := m.Delivery.UserEmail
	if to == "" {
		return "", "", Permanent(errors.New("the user has no email address"))
	}
	msg := mail.NewMsg()
	if err := msg.From(e.Config.From); err != nil {
		return "", "", Permanent(err)
	}
	if err := msg.AddToFormat(m.Delivery.UserName, to); err != nil {
		return "", "", Permanent(err)
	}
	msg.Subject(m.Subject)
	msg.SetBodyString(mail.TypeTextPlain, m.Text)
	msg.AddAlternativeString(mail.TypeTextHTML, m.HTML)
	if m.Logo != nil {
		if err := msg.EmbedReader("logo", bytes.NewReader(m.Logo.Content), mail.WithFileContentID(LogoCID),
			mail.WithFileContentType(mail.ContentType(m.Logo.ContentType))); err != nil {
			return "", "", Permanent(err)
		}
	}
	// One Message-ID per delivery: a retry sent twice is one message to mail clients.
	id := m.Delivery.Notification.ID + ".email@rvav"
	msg.SetMessageIDWithValue(id)
	msg.SetGenHeader(mail.Header("Auto-Submitted"), "auto-generated")
	msg.SetGenHeader(mail.Header("X-Rvav-Notification"), m.Delivery.Notification.ID)
	msg.SetGenHeader(mail.Header("X-Rvav-Kind"), m.Delivery.Notification.Kind)
	c, err := e.client()
	if err != nil {
		return to, "", Permanent(err)
	}
	if err := c.DialAndSendWithContext(ctx, msg); err != nil {
		return to, "", err
	}
	return to, id, nil
}

// Test connects and signs in, without sending.
func (e *Email) Test(ctx context.Context) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	if err := c.DialWithContext(ctx); err != nil {
		return err
	}
	return c.Close()
}
