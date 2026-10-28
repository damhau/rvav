package e2e

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/store"
)

// sentMail is a message the fake SMTP server received.
type sentMail struct {
	To                  []string
	Header              mail.Header
	Subject, Text, HTML string
}

// fakeSMTP is a mail server that keeps what it receives; it refuses the
// next fail messages.
type fakeSMTP struct {
	mu   sync.Mutex
	mail []sentMail
	fail int
	addr *net.TCPAddr
}

func startSMTP(t *testing.T) *fakeSMTP {
	f := &fakeSMTP{}
	s := smtp.NewServer(smtp.BackendFunc(func(*smtp.Conn) (smtp.Session, error) { return &smtpSession{f: f}, nil }))
	s.Domain, s.AllowInsecureAuth = "localhost", true
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.addr = l.Addr().(*net.TCPAddr)
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })
	return f
}

// to are the messages sent to an address so far.
func (f *fakeSMTP) to(addr string) []sentMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sentMail
	for _, m := range f.mail {
		for _, rcpt := range m.To {
			if rcpt == addr {
				out = append(out, m)
			}
		}
	}
	return out
}

type smtpSession struct {
	f  *fakeSMTP
	to []string
}

func (s *smtpSession) Reset()        { s.to = nil }
func (s *smtpSession) Logout() error { return nil }
func (s *smtpSession) Mail(string, *smtp.MailOptions) error {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if s.f.fail > 0 {
		s.f.fail--
		return &smtp.SMTPError{Code: 451, Message: "Try again later"}
	}
	return nil
}
func (s *smtpSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}
func (s *smtpSession) Data(r io.Reader) error {
	msg, err := mail.ReadMessage(r)
	if err != nil {
		return err
	}
	m := sentMail{To: s.to, Header: msg.Header}
	m.Subject, _ = new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		return err
	}
	parts := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		b, _ := io.ReadAll(p)
		switch {
		case strings.HasPrefix(p.Header.Get("Content-Type"), "text/plain"):
			m.Text = string(b)
		case strings.HasPrefix(p.Header.Get("Content-Type"), "text/html"):
			m.HTML = string(b)
		}
	}
	s.f.mu.Lock()
	s.f.mail = append(s.f.mail, m)
	s.f.mu.Unlock()
	return nil
}

// TestEmailNotifications: notifications are kept, and emailed on the
// channels each user chose, in their language, once per occasion, retried
// when the mail server refuses them (S11.2, ADR-0023).
func TestEmailNotifications(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		srv := startSMTP(t)
		h.svc.Events.Channels = []string{"email"}
		// The notifier's clock runs ahead by skip.
		var skip atomic.Int64
		n := &notify.Notifier{Store: h.store, Log: h.svc.Log, PublicURL: "https://rvav.corp.example",
			Now: func() time.Time { return time.Now().Add(time.Duration(skip.Load())) },
			Senders: map[string]notify.Sender{"email": &notify.Email{Config: config.SMTPConfig{Host: "127.0.0.1",
				Port: srv.addr.Port, TLS: "none", From: "rvav <rvav@corp.example>"}}}}
		send := func() {
			t.Helper()
			if _, err := n.Once(ctx); err != nil {
				t.Fatal(err)
			}
		}
		const tom, leads = "thomas.nguyen@corp.example", "marc.keller@corp.example"
		// Tom also wants to hear about what he submits.
		if err := h.store.SetNotificationPreferences(ctx, "usr_tnguyen", store.NotificationPreferences{Locale: "en",
			Rules: map[string][]string{"request_submitted": {"email"}}}); err != nil {
			t.Fatal(err)
		}

		// A production VM: submitted, waiting for the infrastructure leads, approved, ready.
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", "tnguyen", nil, 200, &item)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
			ProjectId: "prj_web", Environment: "prd", Inputs: map[string]any{"role": "front", "size": "XL",
				"cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02", "network": "dvpg-web-prd-vl210",
				"post_config": []string{"linux-baseline"}}}, 202, &req, "Idempotency-Key", uuid.NewString())
		var approvalID string
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				send()
				asked := srv.to(leads)
				if len(asked) != 1 || !strings.HasPrefix(asked[0].Subject, "Approval needed: ") {
					t.Errorf("the leads got %+v", asked)
					return
				}
				inbox := h.waitInbox("mkeller", 1)
				approvalID = inbox.Items[0].Id
				if !strings.Contains(asked[0].Text, "https://rvav.corp.example/approvals/"+approvalID) ||
					!strings.Contains(asked[0].HTML, `href="https://rvav.corp.example/approvals/`+approvalID+`"`) ||
					!strings.Contains(asked[0].Text, req.Number) || asked[0].Header.Get("X-Rvav-Kind") != "approval_needed" {
					t.Errorf("the approval email = %+v", asked[0])
				}
				h.json("POST", "/api/v1/approvals/"+approvalID+"/decision", "mkeller",
					api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove, Comment: new("Fine for Black Friday")}, 200, nil)
				h.wf.deliver(env)
			}, time.Minute)
		})
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("request = %s", req.Status)
		}
		send()
		var subjects []string
		for _, m := range srv.to(tom) {
			subjects = append(subjects, m.Subject)
		}
		// Approved isn't emailed by default; submitted, as Tom chose, and ready are.
		if len(subjects) != 2 || !strings.HasPrefix(subjects[0], "Submitted: ") || !strings.HasPrefix(subjects[1], "Ready: ") {
			t.Fatalf("Tom got %q", subjects)
		}
		var kept []string
		rows, _ := h.store.Pool.Query(ctx, `select kind from notifications where user_id = 'usr_tnguyen' order by created_at`)
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			kept = append(kept, k)
		}
		rows.Close()
		if strings.Join(kept, ",") != "request_submitted,request_approved,request_succeeded" {
			t.Fatalf("Tom's notifications = %v", kept)
		}

		// Lease reminders in French, a week then a day before; once each,
		// however often housekeeping runs; the mail server refusing one first.
		if err := h.store.SetNotificationPreferences(ctx, "usr_tnguyen", store.NotificationPreferences{Locale: "fr"}); err != nil {
			t.Fatal(err)
		}
		d := leasedVM(h, 10)
		now := time.Now()
		srv.mu.Lock()
		srv.fail = 1
		srv.mu.Unlock()
		for range 2 {
			if _, err := h.svc.ExpireLeases(ctx, now.Add(4*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
		send()
		var refused int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from notification_deliveries where status = 'queued' and error like '%451%'`).Scan(&refused)
		if refused != 1 {
			t.Fatalf("%d deliveries waiting after the server refused one", refused)
		}
		skip.Store(int64(time.Minute)) // past the first back-off
		send()
		if _, err := h.svc.ExpireLeases(ctx, now.Add(9*24*time.Hour+12*time.Hour)); err != nil {
			t.Fatal(err)
		}
		send()
		var lease []sentMail
		for _, m := range srv.to(tom) {
			if strings.HasPrefix(m.Subject, "Le bail de ") {
				lease = append(lease, m)
			}
		}
		if len(lease) != 2 || !strings.HasSuffix(lease[0].Subject, "dans 6 jours") ||
			!strings.HasSuffix(lease[1].Subject, "d'ici un jour") || !strings.Contains(lease[0].Text, "Bonjour Thomas Nguyen") ||
			!strings.Contains(lease[0].Text, "https://rvav.corp.example/deployments/"+d.id) {
			for _, m := range lease {
				t.Logf("%s\n%s", m.Subject, m.Text)
			}
			t.Fatalf("%d lease emails", len(lease))
		}

		// One notification per occasion, whoever sends it again.
		again := store.NewNotification{UserID: "usr_tnguyen", Kind: "owner_changed", Title: "x", DedupeKey: "test:once"}
		h.svc.Events.Notify(ctx, again)
		h.svc.Events.Notify(ctx, again)
		var once int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from notifications where dedupe_key = 'test:once'`).Scan(&once)
		if once != 1 {
			t.Fatalf("%d notifications for one occasion", once)
		}

		// Someone without an address: kept, not retried.
		if _, err := h.store.Pool.Exec(ctx, `update users set email = '' where id = 'usr_lmartin'`); err != nil {
			t.Fatal(err)
		}
		h.svc.Events.Notify(ctx, store.NewNotification{UserID: "usr_lmartin", Kind: "request_failed", Title: "x", DedupeKey: "test:noaddr"})
		send()
		var status, why string
		_ = h.store.Pool.QueryRow(ctx, `select d.status, d.error from notification_deliveries d join notifications n
			on n.id = d.notification_id where n.dedupe_key = 'test:noaddr'`).Scan(&status, &why)
		if status != "failed" || !strings.Contains(why, "no email address") {
			t.Fatalf("a delivery without an address = %s %s", status, why)
		}
		var html bytes.Buffer
		html.WriteString(lease[0].HTML)
		if !strings.Contains(html.String(), `lang="fr"`) {
			t.Fatalf("the French email's HTML = %s", html.String())
		}
	})
}
