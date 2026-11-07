package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

var kinds = []api.NotificationKind{api.KindRequestSubmitted, api.KindApprovalNeeded, api.KindRequestApproved,
	api.KindRequestRejected, api.KindRequestSucceeded, api.KindRequestFailed, api.KindRequestRolledBack,
	api.KindLeaseExpiring, api.KindLeaseExpired, api.KindOwnerChanged, api.KindStepMessage}

// Every kind renders in every language, with what it is about and nothing
// missing.
func TestRenderEveryKind(t *testing.T) {
	ends := time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)
	for _, locale := range Locales {
		for _, kind := range kinds {
			d := Data{Kind: string(kind), Locale: locale, UserName: "Tom Nguyen", Title: "t", Body: "Out of quota",
				Link: "https://rvav.corp.example/requests/REQ-10077", Preferences: "https://rvav.corp.example/notifications",
				Subject: store.NotificationSubject{RequestNumber: "REQ-10077", RequestTitle: "RHEL 9 server web-prd-front01",
					ItemName: "RHEL 9 server", ProjectName: "Web Platform", Environment: "prd", DeploymentName: "web-prd-front01",
					LeaseExpiresAt: &ends, ApprovalName: "Infrastructure leads", ApprovalReasons: []string{"Production VM"}},
				Extra: map[string]any{"days": 7, "from": "Lea Martin", "ready": true}}
			m, err := render(string(kind), locale, d)
			if err != nil {
				t.Fatalf("%s %s: %v", locale, kind, err)
			}
			for name, s := range map[string]string{"subject": m.Subject, "text": m.Text, "html": m.HTML} {
				if s == "" || strings.Contains(s, "<no value>") || strings.Contains(s, "%!") {
					t.Fatalf("%s %s %s = %q", locale, kind, name, s)
				}
			}
			if !strings.Contains(m.Text, d.Link) || !strings.Contains(m.HTML, `href="`+d.Link+`"`) {
				t.Fatalf("%s %s has no link: %s", locale, kind, m.Text)
			}
			if !strings.Contains(m.Subject, "REQ-10077") && !strings.Contains(m.Subject, "web-prd-front01") {
				t.Fatalf("%s %s: what is it about? %q", locale, kind, m.Subject)
			}
		}
	}
	fr, _ := render(string(api.KindLeaseExpiring), "fr", Data{Locale: "fr", Subject: store.NotificationSubject{
		DeploymentName: "web01", LeaseExpiresAt: &ends}, Extra: map[string]any{"days": 1}, Link: "x"})
	if fr.Subject != "Le bail de web01 se termine d'ici un jour" || !strings.Contains(fr.Text, "4 novembre 2026 à 09:30 UTC") {
		t.Fatalf("in French: %q / %q", fr.Subject, fr.Text)
	}
	// Names written into HTML are escaped.
	esc, _ := render(string(api.KindOwnerChanged), "en", Data{Locale: "en", UserName: "<b>x</b>",
		Subject: store.NotificationSubject{DeploymentName: "a<script>"}, Extra: map[string]any{}})
	if strings.Contains(esc.HTML, "<script>") || strings.Contains(esc.HTML, "<b>x</b>") {
		t.Fatalf("not escaped: %s", esc.HTML)
	}
	// A language without templates falls back to English.
	if m, err := render(string(api.KindOwnerChanged), "de", Data{Locale: "de", Extra: map[string]any{}}); err != nil ||
		!strings.HasPrefix(m.Subject, "You now own") {
		t.Fatalf("fallback = %q (%v)", m.Subject, err)
	}
}

// A card's text can't turn a request's title into a link.
// A step's message holds what a requester typed, and it reaches other people
// (ADR-0033): to each channel it is data, not markup.
func TestStepMessageEscapes(t *testing.T) {
	const typed = `<img src=x onerror=alert(1)> <script>x</script> *bold* <@U123> <!channel> <https://evil.example|click> [x](https://evil.example) & done`
	for _, locale := range Locales {
		m, err := render(string(api.KindStepMessage), locale, Data{Kind: string(api.KindStepMessage), Locale: locale, Body: typed, Link: "https://rvav.corp.example/requests/REQ-1",
			Subject: store.NotificationSubject{RequestNumber: "REQ-1", RequestTitle: "Web\nBcc: x@evil.example"}, Extra: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		// Mail: its text as it is, its HTML escaped; a subject of one line.
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
		card, _ := json.Marshal(Card(m))
		if strings.Contains(string(card), "[x](https") || strings.Contains(string(card), "*bold*") || !strings.Contains(string(card), `\\[x\\]`) {
			t.Errorf("%s: the card = %s", locale, card)
		}
		// Slack: plain text, in which nothing is a mention or a link.
		blocks, _ := json.Marshal(Blocks(m))
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
	// Other kinds keep Slack's markup, their text being rvav's own.
	m, err := render(string(api.KindRequestFailed), "en", Data{Kind: string(api.KindRequestFailed), Locale: "en", Body: "x",
		Subject: store.NotificationSubject{RequestNumber: "REQ-1", RequestTitle: "t"}, Extra: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(Blocks(m)); !strings.Contains(string(b), `"mrkdwn"`) {
		t.Fatalf("a failure's blocks = %s", b)
	}
}

func TestCardEscapesMarkdown(t *testing.T) {
	m, err := render(string(api.KindRequestFailed), "en", Data{Locale: "en", Body: "see [here](https://evil.example)",
		Subject: store.NotificationSubject{RequestNumber: "REQ-1", RequestTitle: "**big** [x](https://evil.example)"}, Extra: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(Card(m))
	if strings.Contains(string(b), "[x](https") || strings.Contains(string(b), "[here](") || !strings.Contains(string(b), `\\[x\\]`) {
		t.Fatalf("the card = %s", b)
	}
}
