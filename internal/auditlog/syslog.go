package auditlog

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/trust"
)

// Facilities are RFC 5424's facility codes by name.
var Facilities = map[string]int{
	"kern": 0, "user": 1, "mail": 2, "daemon": 3, "auth": 4, "syslog": 5, "lpr": 6, "news": 7, "uucp": 8, "cron": 9,
	"authpriv": 10, "ftp": 11, "ntp": 12, "audit": 13, "alert": 14, "clock": 15,
	"local0": 16, "local1": 17, "local2": 18, "local3": 19, "local4": 20, "local5": 21, "local6": 22, "local7": 23,
}

// Severities: a refused sign-in is a warning, every other event a notice.
const (
	severityWarning = 4
	severityNotice  = 5
)

// maxDatagram is the largest UDP message sent whole; a larger event goes
// with its detail left out, rather than cut in a receiver's buffer.
const maxDatagram = 8 << 10

// MaxFrame is the longest message sent over TCP and TLS: syslog-ng's
// default log-msg-size, past which it closes the connection. A longer
// event is set aside.
const MaxFrame = 64 << 10

// SyslogDefaults fills what a syslog destination leaves out.
func SyslogDefaults(c api.AuditSyslog) api.AuditSyslog {
	if c.Transport == nil {
		t := api.SyslogTLS
		c.Transport = &t
	}
	if c.Port == nil {
		p := 514
		if *c.Transport == api.SyslogTLS {
			p = 6514
		}
		c.Port = &p
	}
	if c.Facility == nil {
		f := api.FacilityLocal0
		c.Facility = &f
	}
	if c.AppName == nil {
		a := "rvav"
		c.AppName = &a
	}
	return c
}

// Format is an event as an RFC 5424 message, its MSG the event's JSON.
func Format(facility int, hostname, appName string, e Event) []byte {
	severity := severityNotice
	if e.Action == "auth.sign_in_failed" {
		severity = severityWarning
	}
	body, _ := json.Marshal(e)
	return fmt.Appendf(nil, "<%d>1 %s %s %s - %s - %s", facility*8+severity, e.At.UTC().Format("2006-01-02T15:04:05.000000Z"),
		field(hostname, 255), field(appName, 48), field(e.Action, 32), body)
}

// field is a header field: printable US-ASCII, at most n characters, "-"
// when empty.
func field(s string, n int) string {
	b := make([]byte, 0, min(len(s), n))
	for i := 0; i < len(s) && len(b) < n; i++ {
		c := s[i]
		if c < 33 || c > 126 {
			c = '_'
		}
		b = append(b, c)
	}
	if len(b) == 0 {
		return "-"
	}
	return string(b)
}

type syslogSender struct {
	conn     net.Conn
	udp      bool
	facility int
	hostname string
	appName  string
	where    string
	timeout  time.Duration
}

func (f *Forwarder) dialSyslog(ctx context.Context, c api.AuditSyslog) (sender, error) {
	c = SyslogDefaults(c)
	addr := net.JoinHostPort(c.Host, strconv.Itoa(*c.Port))
	ctx, cancel := context.WithTimeout(ctx, f.timeout())
	defer cancel()
	d := &net.Dialer{}
	var conn net.Conn
	var err error
	switch *c.Transport {
	case api.SyslogTLS:
		var cfg *tls.Config
		if cfg, err = f.tlsConfig(ctx, c); err != nil {
			return nil, err
		}
		conn, err = (&tls.Dialer{NetDialer: d, Config: cfg}).DialContext(ctx, "tcp", addr)
	case api.SyslogTCP:
		conn, err = d.DialContext(ctx, "tcp", addr)
	case api.SyslogUDP:
		conn, err = d.DialContext(ctx, "udp", addr)
	default:
		return nil, fmt.Errorf("no transport %s", *c.Transport)
	}
	if err != nil {
		return nil, err
	}
	if *c.Transport != api.SyslogUDP {
		if err := refused(conn); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return &syslogSender{conn: conn, udp: *c.Transport == api.SyslogUDP, facility: Facilities[string(*c.Facility)],
		hostname: f.Hostname, appName: *c.AppName, where: addr + " over " + strings.ToUpper(string(*c.Transport)),
		timeout: f.timeout()}, nil
}

// refused waits a moment for the server to close the connection it just
// took. A syslog server never writes, so whatever comes back is a refusal:
// in TLS 1.3 a client certificate the server refuses is only told after
// the handshake, and events written before would be counted as sent.
func refused(conn net.Conn) error {
	if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		return err
	}
	_, err := conn.Read(make([]byte, 1))
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return conn.SetReadDeadline(time.Time{})
	}
	if err == nil || errors.Is(err, io.EOF) {
		return errors.New("the server closed the connection")
	}
	return fmt.Errorf("the server refused the connection: %w", err)
}

// tlsConfig trusts the destination's CAs besides the process's, and
// presents its client certificate.
func (f *Forwarder) tlsConfig(ctx context.Context, c api.AuditSyslog) (*tls.Config, error) {
	o := trust.Options{Host: c.Host}
	if ca := strings.TrimSpace(deref(c.CaCertificate)); ca != "" {
		certs, err := trust.Parse(Unfold(ca))
		if err != nil {
			return nil, fmt.Errorf("the CA certificate: %w", err)
		}
		pool := trust.Roots()
		if pool == nil {
			if pool, err = x509.SystemCertPool(); err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
		} else {
			pool = pool.Clone()
		}
		for _, cert := range certs {
			pool.AddCert(cert)
		}
		o.Roots = pool
	}
	cfg := trust.Config(o)
	cfg.ServerName = c.Host
	if strings.TrimSpace(deref(c.ClientCertificate)) != "" {
		pair, err := f.ClientCertificate(ctx, deref(c.ClientCertificate), deref(c.ClientKeySecret))
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

// ClientCertificate pairs a certificate with the private key a stored
// secret holds.
func (f *Forwarder) ClientCertificate(ctx context.Context, certPEM, keySecret string) (tls.Certificate, error) {
	if f.Secret == nil {
		return tls.Certificate{}, errors.New("this rvav can't read stored secrets")
	}
	key, err := f.Secret(ctx, keySecret)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("the client key can't be read: %w", err)
	}
	pair, err := tls.X509KeyPair([]byte(Unfold(certPEM)), []byte(Unfold(key)))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("the client certificate and key: %w", err)
	}
	return pair, nil
}

// Unfold puts back the line breaks of a PEM text pasted on one line, as a
// password field keeps it: the blocks' base64 is broken into lines again.
func Unfold(text string) string {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "\n") {
		return text
	}
	var out strings.Builder
	for text != "" {
		begin := strings.Index(text, "-----BEGIN ")
		if begin < 0 {
			break
		}
		headEnd := strings.Index(text[begin+11:], "-----")
		if headEnd < 0 {
			break
		}
		label := text[begin+11 : begin+11+headEnd]
		rest := text[begin+11+headEnd+5:]
		end := strings.Index(rest, "-----END "+label+"-----")
		if end < 0 {
			break
		}
		body := strings.Join(strings.Fields(rest[:end]), "")
		out.WriteString("-----BEGIN " + label + "-----\n")
		for len(body) > 64 {
			out.WriteString(body[:64] + "\n")
			body = body[64:]
		}
		out.WriteString(body + "\n-----END " + label + "-----\n")
		text = strings.TrimSpace(rest[end+len("-----END "+label+"-----"):])
	}
	return out.String()
}

func (s *syslogSender) send(_ context.Context, events []Event) (string, error) {
	if err := s.conn.SetWriteDeadline(time.Now().Add(s.timeout)); err != nil {
		return "", err
	}
	if s.udp {
		for _, e := range events {
			msg := Format(s.facility, s.hostname, s.appName, e)
			if len(msg) > maxDatagram {
				e.Detail = json.RawMessage(`{"truncated":true}`)
				msg = Format(s.facility, s.hostname, s.appName, e)
			}
			if _, err := s.conn.Write(msg); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("Sent %d to %s.", len(events), s.where), nil
	}
	// RFC 5425's octet counting: each message after its length and a space.
	var buf bytes.Buffer
	for _, e := range events {
		msg := Format(s.facility, s.hostname, s.appName, e)
		buf.WriteString(strconv.Itoa(len(msg)))
		buf.WriteByte(' ')
		buf.Write(msg)
	}
	if _, err := s.conn.Write(buf.Bytes()); err != nil {
		return "", err
	}
	return fmt.Sprintf("Sent %d to %s.", len(events), s.where), nil
}

func (s *syslogSender) oversize(e Event) string {
	if s.udp {
		return ""
	}
	if n := len(Format(s.facility, s.hostname, s.appName, e)); n > MaxFrame {
		return fmt.Sprintf("The event is %d KB written for syslog: over TCP and TLS, rvav sends %d KB at most", (n+1023)>>10, MaxFrame>>10)
	}
	return ""
}

func (s *syslogSender) close() error { return s.conn.Close() }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
