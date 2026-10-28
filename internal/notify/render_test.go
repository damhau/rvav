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
	api.KindLeaseExpiring, api.KindLeaseExpired, api.KindOwnerChanged}

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
