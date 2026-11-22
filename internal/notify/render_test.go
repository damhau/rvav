package notify

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"github.com/damhau/rvav/internal/api"
)

var kinds = []api.NotificationKind{api.KindRequestSubmitted, api.KindApprovalNeeded, api.KindRequestApproved,
	api.KindRequestRejected, api.KindRequestSucceeded, api.KindRequestFailed, api.KindRequestRolledBack,
	api.KindLeaseExpiring, api.KindLeaseExpired, api.KindOwnerChanged, api.KindStepMessage}

func builtin(t *testing.T, channel string, d Data) Message {
	t.Helper()
	p, ok := Default(d.Kind, channel, d.Locale)
	if !ok {
		t.Fatalf("no %s template of %s in %s", channel, d.Kind, d.Locale)
	}
	m, err := render(channel, p, d, Brand{}, "")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func sample(kind api.NotificationKind, locale string) Data {
	return Sample(string(kind), locale, "https://rvav.corp.example", "Acme Cloud")
}

func blank(s string) bool {
	return s == "" || strings.Contains(s, "<no value>") || strings.Contains(s, "%!")
}

// Every kind is written in every channel and language, with what it is
// about and nothing missing, by its built-in template and by an edited one.
func TestRenderEveryKind(t *testing.T) {
	if got := len(Kinds); got != len(kinds) {
		t.Fatalf("%d editable kinds, %d kinds", got, len(kinds))
	}
	for _, locale := range Locales {
		for _, kind := range kinds {
			d := sample(kind, locale)
			if _, ok := KindOf(string(kind)); !ok {
				t.Fatalf("%s isn't editable", kind)
			}
			for _, channel := range Channels {
				m := builtin(t, channel, d)
				if blank(m.Subject) || blank(m.Text) || channel == "email" && blank(m.HTML) {
					t.Fatalf("%s %s %s: %+v", locale, kind, channel, m)
				}
				if !strings.Contains(m.Subject, "REQ-10077") && !strings.Contains(m.Subject, "web-prd-front01") &&
					!strings.Contains(m.Subject, "RHEL 9") {
					t.Fatalf("%s %s %s: what is it about? %q", locale, kind, channel, m.Subject)
				}
				switch channel {
				case "email":
					if !strings.Contains(m.Text, d.Link) || !strings.Contains(m.HTML, `href="`+d.Link+`"`) ||
						!strings.Contains(m.Text, "Acme Cloud") || !strings.Contains(m.HTML, "background:#0e6488") {
						t.Fatalf("%s %s: the email has no link, name or accent: %s", locale, kind, m.HTML)
					}
				case "teams":
					card, _ := json.Marshal(Card(m))
					if !strings.Contains(string(card), d.Link) || strings.Contains(string(card), "<no value>") {
						t.Fatalf("%s %s: the card = %s", locale, kind, card)
					}
				case "slack":
					blocks, _ := json.Marshal(Blocks(m))
					if !strings.Contains(string(blocks), d.Link) {
						t.Fatalf("%s %s: the blocks = %s", locale, kind, blocks)
					}
				}

				edited := Parts{Subject: "[{{.Portal}}] " + kindProbe(kind), Text: "*Hi* {{.User.Name}}: " + kindProbe(kind)}
				if channel == "email" {
					edited.HTML = "<h2>{{.User.Name}}</h2><p>" + kindProbe(kind) + "</p>"
				}
				if problems := Check(string(kind), channel, locale, edited); len(problems) > 0 {
					t.Fatalf("%s %s %s: %v", locale, kind, channel, problems)
				}
				e, err := render(channel, edited, d, Brand{}, "")
				if err != nil || !strings.HasPrefix(e.Subject, "[Acme Cloud] ") || !strings.Contains(e.Text, "Thomas Nguyen") ||
					channel == "email" && !strings.Contains(e.HTML, "<h2>Thomas Nguyen</h2>") {
					t.Fatalf("%s %s %s edited: %+v %v", locale, kind, channel, e, err)
				}
			}
		}
	}
}

// kindProbe uses what is particular to a kind.
func kindProbe(kind api.NotificationKind) string {
	switch kind {
	case api.KindApprovalNeeded:
		return "{{.Request.Number}} {{range .Approval.Reasons}}{{.}} {{end}}{{.Approval.Name}}"
	case api.KindLeaseExpiring:
		return "{{.Deployment.Name}} {{date .Lease.ExpiresAt}} {{.Lease.Days}}"
	case api.KindLeaseExpired:
		return "{{.Deployment.Name}} {{.Lease.GraceDays}}"
	case api.KindOwnerChanged:
		return "{{.Deployment.Name}} from {{.PreviousOwner}}"
	case api.KindRequestSucceeded:
		return "{{.Request.Number}}{{if .Ready}} ready{{end}}"
	}
	return "{{.Request.Number}} {{.Request.Title}}"
}

func TestRenderDetails(t *testing.T) {
	ends := time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)
	d := Data{Kind: string(api.KindLeaseExpiring), Locale: "fr", Deployment: Deployment{Name: "web01"},
		Lease: Lease{ExpiresAt: &ends, Days: 1}, Link: "x"}
	fr := builtin(t, "email", d)
	if fr.Subject != "Le bail de web01 se termine d'ici un jour" || !strings.Contains(fr.Text, "4 novembre 2026 à 09:30 UTC") ||
		!strings.Contains(fr.Text, "Prolonger le bail : x") {
		t.Fatalf("in French: %q / %q", fr.Subject, fr.Text)
	}
	// Names written into HTML are escaped.
	esc := builtin(t, "email", Data{Kind: string(api.KindOwnerChanged), Locale: "en", User: Person{"<b>x</b>"},
		Deployment: Deployment{Name: "a<script>"}, Portal: "<i>Acme</i>"})
	if strings.Contains(esc.HTML, "<script>") || strings.Contains(esc.HTML, "<b>x</b>") || strings.Contains(esc.HTML, "<i>") {
		t.Fatalf("not escaped: %s", esc.HTML)
	}
	// A language without templates is written in English.
	if m := builtin(t, "email", Data{Kind: string(api.KindOwnerChanged), Locale: Locale("de")}); !strings.HasPrefix(m.Subject, "You now own") {
		t.Fatalf("fallback = %q", m.Subject)
	}
	// The branding: an accent and a logo the email carries.
	logo := &Logo{ContentType: "image/png", Content: []byte("png")}
	p, _ := Default("test", "email", "en")
	m, err := render("email", p, Data{Kind: "test", Locale: "en", Portal: "Acme Cloud"}, Brand{Name: "Acme Cloud",
		Accent: "#5b2a86", AccentFg: "#ffffff", Logo: logo}, "cid:"+LogoCID)
	if err != nil || m.Subject != "A test from Acme Cloud" || m.Logo != logo || !strings.Contains(m.HTML, `src="cid:logo@rvav" alt="Acme Cloud"`) ||
		!strings.Contains(m.HTML, "background:#5b2a86;color:#ffffff") {
		t.Fatalf("branded: %v %s", err, m.HTML)
	}
}

// The preview writes the sample as it would be sent: the email's HTML with
// its logo inline, the card and the blocks.
func TestPreview(t *testing.T) {
	b := Brand{Name: "Acme Cloud", Logo: &Logo{ContentType: "image/png", Content: []byte{0x89, 'P', 'N', 'G'}}}
	p, _ := Default("approval_needed", "email", "fr")
	pv, problems := PreviewOf("approval_needed", "email", "fr", p, b, "https://rvav.corp.example")
	if len(problems) > 0 || !strings.Contains(pv.Message.HTML, `src="data:image/png;base64,iVBORw=="`) ||
		!strings.Contains(pv.Message.HTML, "Décider dans Acme Cloud") || pv.Message.Logo != nil {
		t.Fatalf("email: %v %s", problems, pv.Message.HTML)
	}
	p, _ = Default("approval_needed", "teams", "en")
	pv, problems = PreviewOf("approval_needed", "teams", "en", p, b, "https://rvav.corp.example")
	card, _ := json.Marshal(pv.Card)
	if len(problems) > 0 || !strings.Contains(string(card), `"verb":"approve"`) || !strings.Contains(string(card), "Open in Acme Cloud") {
		t.Fatalf("teams: %v %s", problems, card)
	}
	pv, problems = PreviewOf("lease_expiring", "slack", "en", Parts{Subject: "{{.Deployment.Name}}", Text: "*{{.Deployment.Name}}*"}, b, "")
	blocks, _ := json.Marshal(pv.Blocks)
	if len(problems) > 0 || !strings.Contains(string(blocks), `"text":"*web-prd-front01*"`) {
		t.Fatalf("slack: %v %s", problems, blocks)
	}
	if _, problems := PreviewOf("lease_expiring", "slack", "en", Parts{Subject: "{{.Nope}}", Text: "x"}, b, ""); len(problems) != 1 {
		t.Fatalf("a wrong template's preview: %v", problems)
	}
}

func TestCheck(t *testing.T) {
	for _, locale := range Locales {
		for _, k := range Kinds {
			for _, channel := range Channels {
				p, _ := Default(k.Kind, channel, locale)
				if problems := Check(k.Kind, channel, locale, p); len(problems) > 0 {
					t.Fatalf("the built-in %s template of %s in %s: %v", channel, k.Kind, locale, problems)
				}
			}
		}
	}
	refused := []struct {
		kind, channel string
		p             Parts
		part, message string
	}{
		{"request_failed", "teams", Parts{Subject: "{{.Request.Number", Text: "x"}, "subject", "line 1: unclosed action"},
		{"request_failed", "teams", Parts{Subject: "x", Text: "a\n{{if .Body}}b"}, "text", "line 2: unexpected EOF"},
		{"request_failed", "email", Parts{Subject: "x", Text: "x", HTML: "<p>{{.Request.Nmber}}</p>"}, "html",
			".Request has no field Nmber: .Request.Nmber is not a variable of this kind"},
		{"request_submitted", "slack", Parts{Subject: "{{.Deployment.Name}}", Text: "x"}, "subject",
			".Deployment is not a variable of this kind"},
		{"request_failed", "slack", Parts{Subject: "x", Text: "{{if .Body}}ok{{else}}\n{{.Error}}{{end}}"}, "text",
			"line 2: .Error is not a variable of this kind"},
		{"request_failed", "slack", Parts{Subject: "x", Text: "{{with .Request}}{{.Number}} {{.Owner}}{{end}}"}, "text",
			".Request has no field Owner"},
		{"request_failed", "slack", Parts{Subject: "x", Text: "{{$r := .Request}}{{$r.Owner}}"}, "text", ".Request has no field Owner"},
		{"approval_needed", "slack", Parts{Subject: "x", Text: "{{range .Approval.Reasons}}{{.Text}}{{end}}"}, "text",
			"the items of .Approval.Reasons are text: they have no field Text"},
		{"request_failed", "slack", Parts{Subject: "x", Text: "{{range .Request}}a{{end}}"}, "text", "range goes over a list"},
		{"request_failed", "slack", Parts{Subject: "x", Text: "{{range 100000000}}a{{end}}"}, "text", "range goes over a list"},
		{"approval_needed", "slack", Parts{Subject: "x", Text: "{{range .Approval.Reasons}}{{range $.Approval.Reasons}}a{{end}}{{end}}"},
			"text", "a range can't be inside another"},
		{"request_failed", "slack", Parts{Subject: `{{printf "%999999d" 1}}`, Text: "x"}, "subject", "printf pads to 100 characters at most"},
		{"request_failed", "slack", Parts{Subject: `{{printf "%.101s" .Request.Title}}`, Text: "x"}, "subject", "printf pads to 100"},
		{"request_failed", "slack", Parts{Subject: `{{printf "%[1]*d" 1000 1}}`, Text: "x"}, "subject", "not from *"},
		{"request_failed", "slack", Parts{Subject: "x", Text: `{{define "a"}}b{{end}}c`}, "text", "can't define others"},
		{"request_failed", "slack", Parts{Subject: "x", Text: `{{template "subject" .}}`}, "text", "can't call another"},
		{"request_failed", "slack", Parts{Subject: "x", Text: "x", HTML: "<p>x</p>"}, "html", "only emails"},
		{"request_failed", "email", Parts{Subject: " ", Text: "x", HTML: "x"}, "subject", "it is empty"},
		{"request_failed", "email", Parts{Subject: "{{if false}}x{{end}}", Text: "x", HTML: "x"}, "subject", "it writes nothing"},
		{"request_failed", "teams", Parts{Subject: "x", Text: "{{.Body.Length}}"}, "text", ".Body has no field Length"},
		{"lease_expiring", "teams", Parts{Subject: "x", Text: "{{date .Lease.Days}}"}, "text", "with the sample data,"},
		{"request_failed", "teams", Parts{Subject: strings.Repeat("x", MaxSubject+1), Text: "x"}, "subject", "longer than 1 KB"},
	}
	for _, r := range refused {
		problems := Check(r.kind, r.channel, "en", r.p)
		i := slices.IndexFunc(problems, func(p Problem) bool { return p.Part == r.part })
		if i < 0 || !strings.Contains(problems[i].Message, r.message) {
			t.Errorf("%+v: %v", r.p, problems)
		}
	}
	// Functions and variables are fine wherever they are.
	ok := Parts{Subject: `{{$.Request.Number}} {{printf "%s" .Request.Title | len}} {{printf "%-100s|%5.2f%%" "a" 1.5}}`,
		Text: `{{with $p := .Project}}{{$p.Name}} {{.Environment}}{{end}}{{range $i, $r := .Approval.Reasons}}{{$i}} {{$r}}{{end}}`}
	if problems := Check("approval_needed", "teams", "fr", ok); len(problems) > 0 {
		t.Fatalf("a good template: %v", problems)
	}
}

// A template within its size can still write far more over a long list:
// it stops at the limit, and the built-in template is sent (ADR-0059).
func TestTemplateWritesAtMost(t *testing.T) {
	d := Data{Kind: string(api.KindApprovalNeeded), Locale: "en", Portal: "rvav",
		Approval: Approval{Name: "Managers", Reasons: []string{"a", "b", "c", "d"}}}
	each := func(n int) string { return "{{range .Approval.Reasons}}" + strings.Repeat("x", n) + "{{end}}" }
	for _, c := range []struct {
		channel string
		p       Parts
		part    string
	}{
		{"slack", Parts{Subject: "x", Text: each(MaxText - 100)}, "text: it writes more than 64 KB"},
		{"teams", Parts{Subject: each(MaxSubject - 100), Text: "x"}, ""},
		{"email", Parts{Subject: "x", Text: "x", HTML: each(MaxHTML - 100)}, "html: it writes more than 512 KB"},
	} {
		_, err := render(c.channel, c.p, d, Brand{}, "")
		switch {
		case c.part == "" && err != nil:
			t.Errorf("%s: %v", c.channel, err)
		case c.part != "" && (err == nil || !strings.HasSuffix(err.Error(), c.part)):
			t.Errorf("%s: %v; want %s", c.channel, err, c.part)
		}
	}
}

// A step's message holds what a requester typed, and it reaches other people
// (ADR-0033): to each channel it is data, not markup.
func TestStepMessageEscapes(t *testing.T) {
	const typed = `<img src=x onerror=alert(1)> <script>x</script> *bold* <@U123> <!channel> <https://evil.example|click> [x](https://evil.example) & done`
	for _, locale := range Locales {
		d := Data{Kind: string(api.KindStepMessage), Locale: locale, Body: typed, Link: "https://rvav.corp.example/requests/REQ-1",
			Request: Request{Number: "REQ-1", Title: "Web\nBcc: x@evil.example"}}
		// Mail: its text as it is, its HTML escaped; a subject of one line.
		m := builtin(t, "email", d)
		if !strings.Contains(m.Text, typed) {
			t.Errorf("%s: the text = %s", locale, m.Text)
		}
		for _, raw := range []string{"<img", "<script>x", "<@U123>", "<!channel>"} {
			if strings.Contains(m.HTML, raw) {
				t.Errorf("%s: %s in the HTML: %s", locale, raw, m.HTML)
			}
		}
		if !strings.Contains(m.HTML, "&lt;img") {
			t.Errorf("%s: the HTML = %s", locale, m.HTML)
		}
		if strings.ContainsAny(m.Subject, "\r\n") {
			t.Errorf("%s: the subject = %q", locale, m.Subject)
		}
		// Teams: its markdown is shown, not followed.
		card, _ := json.Marshal(Card(builtin(t, "teams", d)))
		if strings.Contains(string(card), "[x](https") || strings.Contains(string(card), "*bold*") || !strings.Contains(string(card), `\\[x\\]`) {
			t.Errorf("%s: the card = %s", locale, card)
		}
		// Slack: plain text, in which nothing is a mention or a link.
		blocks, _ := json.Marshal(Blocks(builtin(t, "slack", d)))
		var parsed []struct {
			Type string
			Text *struct{ Type, Text string }
		}
		if err := json.Unmarshal(blocks, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed[1].Type != "section" || parsed[1].Text == nil || parsed[1].Text.Type != "plain_text" {
			t.Errorf("%s: the Slack section = %s", locale, blocks)
		}
		for _, raw := range []string{"<@U123>", "<!channel>", "<https://evil", "<img"} {
			if strings.Contains(parsed[1].Text.Text, raw) {
				t.Errorf("%s: %s in Slack: %s", locale, raw, parsed[1].Text.Text)
			}
		}
	}
	// Other kinds keep Slack's markup: what the template writes is markup,
	// what it is given is escaped.
	m, err := render("slack", Parts{Subject: "x", Text: "*{{.Request.Number}}* {{.Body}}"}, Data{Kind: string(api.KindRequestFailed),
		Locale: "en", Body: "<!channel> & co", Request: Request{Number: "REQ-1"}}, Brand{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(Blocks(m)); !strings.Contains(string(b), `"mrkdwn"`) || m.Short != "*REQ-1* &lt;!channel&gt; &amp; co" {
		t.Fatalf("a failure's blocks = %s", b)
	}
	// The message's top-level text, its subject, is mrkdwn too.
	m, err = render("slack", Parts{Subject: "{{.Request.Title}} failed", Text: "x"}, Data{Kind: string(api.KindRequestFailed),
		Locale: "en", Request: Request{Number: "REQ-1", Title: "<!channel> & <https://evil.example|co>"}}, Brand{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, v, err := slack.UnsafeApplyMsgOptions("xoxb", "D1", "https://slack.example/api/", post(m)...); err != nil ||
		v.Get("text") != "&lt;!channel&gt; &amp; &lt;https://evil.example|co&gt; failed" {
		t.Fatalf("the top-level text = %q, %v", v.Get("text"), err)
	}
}

// A card's text can't turn a request's title into a link; the template's
// own markdown is kept.
func TestCardEscapesMarkdown(t *testing.T) {
	d := Data{Kind: string(api.KindRequestFailed), Locale: "en", Body: "see [here](https://evil.example)",
		Request: Request{Number: "REQ-1", Title: "**big** [x](https://evil.example)"}}
	for _, p := range []Parts{{Subject: "{{.Request.Title}}", Text: "**Failed:** {{.Request.Title}} {{.Body}}"}} {
		m, err := render("teams", p, d, Brand{}, "")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(Card(m))
		if strings.Contains(string(b), "[x](https") || strings.Contains(string(b), "[here](") || !strings.Contains(string(b), `\\[x\\]`) ||
			!strings.Contains(string(b), `"**Failed:** \\*\\*big`) {
			t.Fatalf("the card = %s", b)
		}
	}
	b, _ := json.Marshal(Card(builtin(t, "teams", d)))
	if strings.Contains(string(b), "[x](https") || strings.Contains(string(b), "[here](") {
		t.Fatalf("the built-in card = %s", b)
	}
}
