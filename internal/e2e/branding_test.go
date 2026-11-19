package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/store"
)

func pngLogo(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, h/2, color.RGBA{R: 0x5b, G: 0x2a, B: 0x86, A: 0xff})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// slackTexts are the texts of a message's blocks, one a line.
func slackTexts(t *testing.T, post map[string]string) string {
	t.Helper()
	var blocks any
	if err := json.Unmarshal([]byte(post["blocks"]), &blocks); err != nil {
		t.Fatal(err)
	}
	var texts []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				if s, ok := x.(string); ok && k == "text" {
					texts = append(texts, s)
				}
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(blocks)
	return strings.Join(texts, "\n")
}

func (h *harness) audited(prefix string) int {
	h.t.Helper()
	var n int
	if err := h.store.Pool.QueryRow(h.ctx, `select count(*) from audit_events where action like $1 || '%'`, prefix).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// TestBranding: anyone reads the portal's branding, the sign-in page
// first, revalidated by its ETag; administrators set a name, an accent
// whose text reads in both themes and logos checked to be pictures and
// nothing that runs (O1.5, ADR-0059).
func TestBranding(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"

		var b api.Branding
		hdr := h.json("GET", "/api/v1/branding", "", nil, 200, &b)
		etag := hdr.Get("ETag")
		if b.Name != "rvav" || b.Accent != nil || b.Light.Accent != "#0e6488" || b.Dark.Accent != "#4fb0d8" ||
			b.LogoLightUrl != nil || etag == "" || hdr.Get("Cache-Control") != "no-cache" {
			t.Fatalf("rvav's own branding = %+v %v", b, hdr)
		}
		if code, body, _ := h.do("GET", "/api/v1/branding", "", nil, "If-None-Match", etag); code != 304 || len(body) != 0 {
			t.Fatalf("the same branding again = %d %s", code, body)
		}

		write := api.BrandingWrite{Name: "  Corp   Cloud ", Accent: "#ffcc00"}
		h.json("PUT", "/api/v1/admin/branding", "tnguyen", write, 403, nil)
		h.json("PUT", "/api/v1/admin/branding", "lmartin", write, 403, nil)
		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/branding", admin, write, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/accent" || deref(problem.Errors[0].Code) != "contrast" ||
			!strings.Contains(problem.Errors[0].Message, "4.5:1") || !strings.Contains(problem.Errors[0].Message, "closest darker colour") {
			t.Fatalf("a yellow accent = %+v", problem)
		}
		var check api.AccentCheck
		h.json("POST", "/api/v1/admin/branding/accent-check", admin, api.AccentCheckQuery{Accent: "#ffcc00"}, 200, &check)
		if check.Ok || check.Suggestion == nil || check.Ratio >= 4.5 {
			t.Fatalf("checking yellow = %+v", check)
		}
		suggested := *check.Suggestion
		h.json("POST", "/api/v1/admin/branding/accent-check", admin, api.AccentCheckQuery{Accent: suggested}, 200, &check)
		if !check.Ok || check.Light == nil || check.Light.Accent != suggested || check.Dark == nil || check.Ratio < 4.5 {
			t.Fatalf("checking the suggestion %s = %+v", suggested, check)
		}
		h.json("PUT", "/api/v1/admin/branding", admin, api.BrandingWrite{Name: strings.Repeat("x", 41), Accent: ""}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/name" {
			t.Fatalf("a long name = %+v", problem)
		}

		write.Accent = "#5B2A86"
		h.json("PUT", "/api/v1/admin/branding", admin, write, 200, &b)
		if b.Name != "Corp Cloud" || deref(b.Accent) != "#5b2a86" || b.Light.Accent != "#5b2a86" || b.Dark.Accent == "#5b2a86" {
			t.Fatalf("the saved branding = %+v", b)
		}
		hdr = h.json("GET", "/api/v1/branding", "", nil, 200, &b, "If-None-Match", etag)
		if hdr.Get("ETag") == etag || b.Name != "Corp Cloud" {
			t.Fatalf("the branding after a change = %+v %v", b, hdr)
		}

		upload := func(variant, content string, want int) api.Branding {
			t.Helper()
			var out api.Branding
			var into any = &out
			if want != 200 {
				into = nil
			}
			h.json("PUT", "/api/v1/admin/branding/logos/"+variant, admin, rawYAML(content), want, into,
				"Content-Type", "application/octet-stream")
			return out
		}
		logo := pngLogo(t, 240, 64)
		upload("light", logo, 200)
		h.json("PUT", "/api/v1/admin/branding/logos/light", "tnguyen", rawYAML(logo), 403, nil, "Content-Type", "application/octet-stream")
		upload("dark", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`, 422)
		upload("dark", `<svg xmlns="http://www.w3.org/2000/svg"><image href="https://evil.example/x.png"/></svg>`, 422)
		upload("dark", "<!DOCTYPE html><p>hi</p>", 415)
		upload("dark", "GIF89a\x01\x00\x01\x00", 415)
		upload("dark", logo+strings.Repeat("\x00", 300<<10), 413)
		upload("dark", pngLogo(t, 4000, 10), 422)
		b = upload("dark", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 32"><rect width="120" height="32" fill="#4fb0d8"/></svg>`, 200)
		if b.LogoLightUrl == nil || b.LogoDarkUrl == nil || !strings.Contains(*b.LogoLightUrl, "?v=") {
			t.Fatalf("logos = %+v", b)
		}

		code, body, hdr := h.do("GET", *b.LogoLightUrl, "", nil)
		if code != 200 || string(body) != logo || hdr.Get("Content-Type") != "image/png" || hdr.Get("X-Content-Type-Options") != "nosniff" ||
			!strings.Contains(hdr.Get("Content-Security-Policy"), "sandbox") || !strings.Contains(hdr.Get("Cache-Control"), "immutable") {
			t.Fatalf("GET the light logo = %d %v", code, hdr)
		}
		if code, _, _ := h.do("GET", *b.LogoLightUrl, "", nil, "If-None-Match", hdr.Get("ETag")); code != 304 {
			t.Fatalf("GET the same logo again = %d", code)
		}
		if code, _, hdr := h.do("GET", "/api/v1/branding/logos/light?v=0000", "", nil); code != 200 || hdr.Get("Cache-Control") != "no-cache" {
			t.Fatalf("GET a logo by an old version = %d %v", code, hdr)
		}
		if code, _, hdr := h.do("GET", *b.LogoDarkUrl, "", nil); code != 200 || hdr.Get("Content-Type") != "image/svg+xml" {
			t.Fatalf("GET the dark logo = %d %v", code, hdr)
		}

		h.json("DELETE", "/api/v1/admin/branding/logos/dark", "tnguyen", nil, 403, nil)
		b = api.Branding{}
		h.json("DELETE", "/api/v1/admin/branding/logos/dark", admin, nil, 200, &b)
		if b.LogoDarkUrl != nil || b.LogoLightUrl == nil {
			t.Fatalf("after removing the dark logo = %+v", b)
		}
		if code, _, _ := h.do("GET", "/api/v1/branding/logos/dark", "", nil); code != 404 {
			t.Fatalf("GET a removed logo = %d", code)
		}

		if n := h.audited("settings.branding.update"); n != 1 {
			t.Errorf("%d branding updates audited", n)
		}
		if n := h.audited("settings.branding_logo."); n != 3 {
			t.Errorf("%d logo changes audited", n)
		}
		var category string
		if err := h.store.Pool.QueryRow(ctx, `select action from audit_events where action like 'settings.branding%' limit 1`).Scan(&category); err != nil ||
			store.AuditCategory(category) != store.AuditSettings {
			t.Errorf("a branding change's category = %s %v", store.AuditCategory(category), err)
		}
	})
}

// TestNotificationTemplates: administrators edit a kind's template per
// channel and language, saved only when it parses and names its kind's
// variables, previewed with sample data, reset to the built-in one; what
// is sent uses it, by email and in Slack, with the portal's branding
// (O1.5, ADR-0059).
func TestNotificationTemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		const path = "/api/v1/admin/notification-templates"

		h.json("GET", path, "tnguyen", nil, 403, nil)
		var catalog api.NotificationTemplateCatalog
		h.json("GET", path, admin, nil, 200, &catalog)
		if len(catalog.Kinds) != len(notify.Kinds) || len(catalog.Functions) == 0 {
			t.Fatalf("the catalog = %+v", catalog)
		}
		for _, k := range catalog.Kinds {
			if len(k.Templates) != 6 || len(k.Variables) == 0 || k.Name == "" {
				t.Errorf("%s: %+v", k.Kind, k)
			}
		}

		var tpl api.NotificationTemplate
		h.json("GET", path+"/request_failed?channel=email&locale=fr", admin, nil, 200, &tpl)
		if tpl.Edited || tpl.Default.Subject == "" || tpl.Html == nil || tpl.Subject != tpl.Default.Subject {
			t.Fatalf("the built-in French email = %+v", tpl)
		}
		h.json("GET", path+"/nope?channel=email&locale=en", admin, nil, 404, nil)

		refused := func(w api.NotificationTemplateWrite, field, says string) {
			t.Helper()
			var problem struct{ Errors []api.FieldError }
			h.json("PUT", path+"/request_failed", admin, w, 422, &problem)
			if len(problem.Errors) == 0 || problem.Errors[0].Field != field || !strings.Contains(problem.Errors[0].Message, says) {
				t.Errorf("%+v: %+v, want %s %q", w, problem, field, says)
			}
		}
		email := api.NotificationTemplateWrite{Channel: api.TemplateEmail, Locale: api.TemplateEnglish,
			Subject: "Broken on {{.Portal}}: {{.Title}}",
			Text:    "{{.User.Name}}, this broke: {{.Body}}",
			Html:    new("<p>This broke: <b>{{.Body}}</b></p>")}
		bad := email
		bad.Subject = "{{.Title"
		refused(bad, "/subject", "Line 1: ")
		bad = email
		bad.Text = "{{.Deployment.Owner}}"
		refused(bad, "/text", ".Deployment is not a variable of this kind")
		bad = email
		bad.Html = new(`{{range .Request.Title}}x{{end}}`)
		refused(bad, "/html", "range")
		slack := api.NotificationTemplateWrite{Channel: api.TemplateSlack, Locale: api.TemplateEnglish,
			Subject: "Failed: {{.Title}}", Text: "*{{.Title}}* failed on {{.Portal}}: {{.Body}}"}
		bad = slack
		bad.Html = new("<p>x</p>")
		refused(bad, "/html", "Only emails have an HTML part.")
		h.json("PUT", path+"/request_failed", "tnguyen", email, 403, nil)

		var preview api.NotificationTemplatePreview
		h.json("POST", path+"/request_failed/preview", admin, email, 200, &preview)
		if !strings.HasPrefix(preview.Subject, "Broken on rvav: ") || preview.Html == nil || !strings.Contains(*preview.Html, "This broke: <b>") ||
			!strings.Contains(preview.Text, "Thomas Nguyen, this broke: ") {
			t.Fatalf("the email's preview = %+v", preview)
		}
		preview = api.NotificationTemplatePreview{}
		h.json("POST", path+"/request_failed/preview", admin, slack, 200, &preview)
		if preview.Blocks == nil || len(*preview.Blocks) < 2 || preview.Html != nil {
			t.Fatalf("the Slack preview = %+v", preview)
		}
		teams := api.NotificationTemplateWrite{Channel: api.TemplateTeams, Locale: api.TemplateFrench, Subject: "Échec : {{.Title}}",
			Text: "**{{.Title}}** : {{.Body}}"}
		preview = api.NotificationTemplatePreview{}
		h.json("POST", path+"/request_failed/preview", admin, teams, 200, &preview)
		if preview.Card == nil || (*preview.Card)["type"] != "AdaptiveCard" {
			t.Fatalf("the Teams preview = %+v", preview)
		}

		h.json("PUT", path+"/request_failed", admin, email, 200, &tpl)
		if !tpl.Edited || tpl.Subject != email.Subject || deref(tpl.UpdatedBy) != "Marc Keller" || tpl.Default.Subject == email.Subject {
			t.Fatalf("the saved email = %+v", tpl)
		}
		h.json("PUT", path+"/request_failed", admin, slack, 200, nil)
		h.json("GET", path, admin, nil, 200, &catalog)
		edited := 0
		for _, k := range catalog.Kinds {
			for _, s := range k.Templates {
				if s.Edited {
					edited++
				}
			}
		}
		if edited != 2 {
			t.Fatalf("%d templates edited", edited)
		}

		// A template edited outside rvav that fails when written: the built-in one is sent.
		if err := h.store.SetNotificationTemplate(ctx, h.store.Pool, store.NotificationTemplate{Kind: "request_rejected",
			Channel: "email", Locale: "en", Subject: "{{index .Approval.Reasons 5}}", Text: "x", HTML: "<p>x</p>"}); err != nil {
			t.Fatal(err)
		}

		h.json("PUT", "/api/v1/admin/branding", admin, api.BrandingWrite{Name: "Corp Cloud", Accent: "#5b2a86"}, 200, nil)
		logo := pngLogo(t, 120, 32)
		h.json("PUT", "/api/v1/admin/branding/logos/light", admin, rawYAML(logo), 200, nil, "Content-Type", "application/octet-stream")

		srv := startSMTP(t)
		sl := startSlack(t)
		cfg := config.SlackConfig{BotToken: "xoxb-test", SigningSecret: slackSigning, APIURL: sl.URL + "/api/"}
		h.svc.Events.Channels = []string{"email", "slack"}
		n := &notify.Notifier{Store: h.store, Log: h.svc.Log, PublicURL: "https://rvav.corp.example",
			Senders: map[string]notify.Sender{"slack": &notify.Slack{Config: cfg, Store: h.store},
				"email": &notify.Email{Config: config.SMTPConfig{Host: "127.0.0.1", Port: srv.addr.Port, TLS: "none",
					From: "rvav <rvav@corp.example>"}}}}
		if err := h.store.SetNotificationPreferences(ctx, seed.UserID("mkeller"), store.NotificationPreferences{Locale: "en",
			Rules: map[string][]string{"request_failed": {"email", "slack"}}}); err != nil {
			t.Fatal(err)
		}
		if err := h.store.SetNotificationPreferences(ctx, seed.UserID("tnguyen"), store.NotificationPreferences{Locale: "fr",
			Rules: map[string][]string{}}); err != nil {
			t.Fatal(err)
		}
		failed := func(user, dedupe string) store.NewNotification {
			return store.NewNotification{UserID: seed.UserID(user), Kind: "request_failed", Title: "REQ-1 <web-01>",
				Body: "vCenter refused the clone & <b>stopped</b>", Link: "/requests/req_1", Level: "error", DedupeKey: dedupe}
		}
		h.svc.Events.Notify(ctx, failed("mkeller", "o15:mkeller"))
		h.svc.Events.Notify(ctx, failed("tnguyen", "o15:tnguyen"))
		h.svc.Events.Notify(ctx, store.NewNotification{UserID: seed.UserID("lmartin"), Kind: "request_rejected", Title: "REQ-2",
			Body: "Too big", Link: "/requests/req_2", DedupeKey: "o15:rejected"})
		if _, err := n.Once(ctx); err != nil {
			t.Fatal(err)
		}

		got := srv.to("marc.keller@corp.example")
		if len(got) != 1 {
			t.Fatalf("%d emails to Marc", len(got))
		}
		m := got[0]
		if m.Subject != "Broken on Corp Cloud: REQ-1 <web-01>" || !strings.Contains(m.Text, "Marc Keller, this broke: vCenter refused the clone & <b>stopped</b>") ||
			!strings.Contains(m.HTML, "This broke: <b>vCenter refused the clone &amp; &lt;b&gt;stopped&lt;/b&gt;</b>") ||
			!strings.Contains(m.HTML, `src="cid:logo@rvav"`) || !strings.Contains(m.HTML, "#5b2a86") {
			t.Fatalf("the edited email = %s\n%s\n%s", m.Subject, m.Text, m.HTML)
		}
		if l, ok := m.Inline["logo@rvav"]; !ok || string(l.Content) != logo || l.ContentType != "image/png" {
			t.Fatalf("the email's logo = %+v", m.Inline)
		}
		fr := srv.to("thomas.nguyen@corp.example")
		if len(fr) != 1 || strings.HasPrefix(fr[0].Subject, "Broken") || !strings.Contains(fr[0].Text, "Bonjour Thomas Nguyen") ||
			!strings.Contains(fr[0].Text, "Corp Cloud") {
			t.Fatalf("the built-in French email = %+v", fr)
		}
		rejected := srv.to("lea.martin@corp.example")
		if len(rejected) != 1 || strings.Contains(rejected[0].Subject, "{{") || rejected[0].Subject == "" {
			t.Fatalf("the email whose template fails = %+v", rejected)
		}

		sl.mu.Lock()
		posts := append([]map[string]string(nil), sl.posts...)
		sl.mu.Unlock()
		if len(posts) != 1 || posts[0]["channel"] != "D-U-MKELLER" ||
			!strings.Contains(slackTexts(t, posts[0]), "*REQ-1 &lt;web-01&gt;* failed on Corp Cloud: vCenter refused the clone &amp; &lt;b&gt;stopped") ||
			!strings.Contains(slackTexts(t, posts[0]), "Open in Corp Cloud") {
			t.Fatalf("the Slack message = %+v", posts)
		}

		var reset api.NotificationTemplate
		h.json("DELETE", path+"/request_failed?channel=email&locale=en", admin, nil, 200, &reset)
		if reset.Edited || reset.Subject != reset.Default.Subject {
			t.Fatalf("after the reset = %+v", reset)
		}
		h.json("DELETE", path+"/request_failed?channel=email&locale=en", admin, nil, 200, nil)
		if n := h.audited("settings.notification_template.update"); n != 2 {
			t.Errorf("%d template edits audited", n)
		}
		if n := h.audited("settings.notification_template.reset"); n != 1 {
			t.Errorf("%d resets audited", n)
		}
	})
}
