package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"slices"
	"strconv"
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

func newNotification(userID, kind, dedupe string) store.NewNotification {
	return store.NewNotification{UserID: userID, Kind: kind, Title: "Test " + kind, DedupeKey: dedupe}
}

// stubSender fails for good, as a channel that doesn't know the user.
type stubSender struct{ err error }

func (s stubSender) Send(context.Context, notify.Message) (string, string, error) {
	return "", "", s.err
}

// TestNotificationCentre: the app keeps each person's notifications, read
// or not; preferences choose the channels per kind, say which can reach
// them and why not, and send a test (S11.5, ADR-0023).
func TestNotificationCentre(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		srv := startSMTP(t)
		h.svc.Notifier = &notify.Notifier{Store: h.store, Log: h.svc.Log, PublicURL: "https://rvav.corp.example",
			Senders: map[string]notify.Sender{
				"email": &notify.Email{Config: config.SMTPConfig{Host: "127.0.0.1", Port: srv.addr.Port, TLS: "none", From: "rvav <rvav@corp.example>"}},
				"teams": stubSender{notify.Permanent(errors.New("thomas.nguyen@corp.example is not in the tenant's directory"))},
			}}
		h.svc.Events.Channels = []string{"email", "teams"}

		// The centre: newest first, unread counted, marked read one by one or all.
		for i := range 3 {
			n := newNotification("usr_tnguyen", "request_succeeded", "centre:"+strconv.Itoa(i))
			n.Link = "/requests/REQ-" + strconv.Itoa(i)
			h.svc.Events.Notify(ctx, n)
		}
		h.svc.Events.Notify(ctx, newNotification("usr_mkeller", "approval_needed", "centre:other"))
		var page api.NotificationPage
		h.json("GET", "/api/v1/me/notifications?limit=2", "tnguyen", nil, 200, &page)
		if len(page.Items) != 2 || page.Unread != 3 || page.NextCursor == nil || *page.Items[0].Link != "/requests/REQ-2" {
			t.Fatalf("Thomas's first page = %+v", page)
		}
		var rest api.NotificationPage
		h.json("GET", "/api/v1/me/notifications?limit=2&cursor="+*page.NextCursor, "tnguyen", nil, 200, &rest)
		if len(rest.Items) != 1 || rest.NextCursor != nil || *rest.Items[0].Link != "/requests/REQ-0" {
			t.Fatalf("the next page = %+v", rest)
		}
		var count api.NotificationCount
		h.json("POST", "/api/v1/me/notifications/read", "tnguyen", api.NotificationsRead{Ids: &[]string{page.Items[0].Id}}, 200, &count)
		h.json("POST", "/api/v1/me/notifications/read", "tnguyen", api.NotificationsRead{Ids: &[]string{"ntf_someone_else"}}, 200, nil)
		if count.Unread != 2 {
			t.Fatalf("unread after one read = %d", count.Unread)
		}
		h.json("GET", "/api/v1/me/notifications?unread=true", "tnguyen", nil, 200, &page)
		if len(page.Items) != 2 {
			t.Fatalf("unread = %d", len(page.Items))
		}
		h.json("POST", "/api/v1/me/notifications/read", "tnguyen", api.NotificationsRead{}, 422, nil)
		h.json("POST", "/api/v1/me/notifications/read", "tnguyen", api.NotificationsRead{All: new(true)}, 200, &count)
		var marc api.NotificationPage
		h.json("GET", "/api/v1/me/notifications", "mkeller", nil, 200, &marc)
		if count.Unread != 0 || marc.Unread != 1 {
			t.Fatalf("after all read: Thomas %d, Marc %d", count.Unread, marc.Unread)
		}

		// Preferences: the defaults, what can reach him, and why not.
		var prefs api.NotificationPreferences
		h.json("GET", "/api/v1/me/notification-preferences", "tnguyen", nil, 200, &prefs)
		rule := func(p api.NotificationPreferences, kind api.NotificationKind) (string, bool) {
			for _, r := range p.Rules {
				if r.Kind == kind {
					return fmt.Sprint(r.Channels), r.Default
				}
			}
			return "", false
		}
		if ch, def := rule(prefs, api.KindApprovalNeeded); ch != "[email teams slack]" || !def || len(prefs.Rules) != 10 || prefs.Locale != "en" {
			t.Fatalf("the defaults = %+v", prefs)
		}
		reach := map[api.NotificationChannel]string{}
		for _, c := range prefs.Channels {
			if c.Available {
				reach[c.Channel] = "yes " + *c.Address
			} else {
				reach[c.Channel] = "no " + *c.Reason
			}
		}
		if reach[api.ChannelEmail] != "yes thomas.nguyen@corp.example" || !strings.HasPrefix(reach[api.ChannelSlack], "no Slack is not set up") ||
			!strings.HasPrefix(reach[api.ChannelTeams], "yes") {
			t.Fatalf("channels = %v", reach)
		}
		// Teams didn't know him: said once a delivery there failed for good.
		h.svc.Events.Notify(ctx, newNotification("usr_tnguyen", "approval_needed", "centre:teams"))
		if _, err := h.svc.Notifier.Once(ctx); err != nil {
			t.Fatal(err)
		}
		h.json("GET", "/api/v1/me/notification-preferences", "tnguyen", nil, 200, &prefs)
		for _, c := range prefs.Channels {
			if c.Channel == api.ChannelTeams && (c.Available || !strings.Contains(*c.Reason, "not in the tenant's directory")) {
				t.Fatalf("Teams after a failure = %+v", c)
			}
		}

		// Choices are checked, then kept; a kind left out keeps the defaults.
		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/me/notification-preferences", "tnguyen", api.NotificationPreferencesWrite{Locale: "fr",
			Rules: []api.NotificationRule{{Kind: api.KindRequestFailed, Channels: []api.NotificationChannel{api.ChannelInApp}},
				{Kind: "nope", Channels: []api.NotificationChannel{}}}}, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Contains(fields, "/rules/0/channels/0") || !slices.Contains(fields, "/rules/1/kind") {
			t.Fatalf("bad preferences = %v", fields)
		}
		h.json("PUT", "/api/v1/me/notification-preferences", "tnguyen", api.NotificationPreferencesWrite{Locale: "fr",
			Rules: []api.NotificationRule{{Kind: api.KindRequestFailed, Channels: []api.NotificationChannel{}},
				{Kind: api.KindRequestSubmitted, Channels: []api.NotificationChannel{api.ChannelEmail, api.ChannelEmail}}}}, 200, &prefs)
		failed, def := rule(prefs, api.KindRequestFailed)
		submitted, _ := rule(prefs, api.KindRequestSubmitted)
		ready, readyDefault := rule(prefs, api.KindRequestSucceeded)
		if failed != "[]" || def || submitted != "[email]" || ready != "[email]" || !readyDefault || prefs.Locale != "fr" {
			t.Fatalf("saved = %+v", prefs)
		}

		// A test, now, in his language.
		var check api.IntegrationCheck
		h.json("POST", "/api/v1/me/notification-preferences/test", "tnguyen", map[string]string{"channel": "email"}, 200, &check)
		var got []sentMail
		for _, m := range srv.to("thomas.nguyen@corp.example") {
			if strings.Contains(m.Subject, "test") {
				got = append(got, m)
			}
		}
		if !check.Ok || check.Message != "Sent to thomas.nguyen@corp.example" || len(got) != 1 || got[0].Subject != "Un test de rvav" {
			t.Fatalf("a test email = %+v, %d sent", check, len(got))
		}
		h.json("POST", "/api/v1/me/notification-preferences/test", "tnguyen", map[string]string{"channel": "slack"}, 200, &check)
		if check.Ok || !strings.Contains(check.Message, "not set up") {
			t.Fatalf("a test on Slack = %+v", check)
		}
	})
}
