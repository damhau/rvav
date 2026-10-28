package notify

import (
	"context"
	"errors"
	"time"

	"github.com/wneessen/go-mail"

	"github.com/damhau/rvav/internal/config"
)

// Email sends notifications by SMTP: plain text and HTML.
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
	if e.Config.Username != "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthPlain), mail.WithUsername(e.Config.Username),
			mail.WithPassword(e.Config.Password))
	}
	return mail.NewClient(e.Config.Host, opts...)
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
