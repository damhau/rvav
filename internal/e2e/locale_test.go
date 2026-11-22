package e2e

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestMyLocale: one language for the web app and what is sent, chosen on
// either side; until someone chooses, /me says nothing and the app follows
// the browser (O1.3, ADR-0061).
func TestMyLocale(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		me := func() *api.MeLocale {
			var m api.Me
			h.json("GET", "/api/v1/me", "mkeller", nil, 200, &m)
			return m.Locale
		}
		if l := me(); l != nil {
			t.Fatalf("before choosing, /me says %s", *l)
		}

		h.json("PUT", "/api/v1/me/locale", "mkeller", map[string]string{"locale": "de"}, 422, nil)
		h.json("PUT", "/api/v1/me/locale", "mkeller", map[string]any{"locale": "fr", "theme": "dark"}, 400, nil)
		var chosen api.MyLocale
		h.json("PUT", "/api/v1/me/locale", "mkeller", api.MyLocale{Locale: "fr"}, 200, &chosen)
		if l := me(); chosen.Locale != "fr" || l == nil || *l != "fr" {
			t.Fatalf("after choosing French: %+v, /me %v", chosen, l)
		}
		var prefs api.NotificationPreferences
		h.json("GET", "/api/v1/me/notification-preferences", "mkeller", nil, 200, &prefs)
		if prefs.Locale != "fr" {
			t.Fatalf("messages after choosing French: %s", prefs.Locale)
		}
		for _, r := range prefs.Rules {
			if !r.Default {
				t.Fatalf("choosing a language changed a rule: %+v", r)
			}
		}

		// The preferences choose it too, and choosing it keeps their rules.
		h.json("PUT", "/api/v1/me/notification-preferences", "mkeller", api.NotificationPreferencesWrite{Locale: "en",
			Rules: []api.NotificationRule{{Kind: api.KindRequestFailed, Channels: []api.NotificationChannel{}}}}, 200, nil)
		if l := me(); l == nil || *l != "en" {
			t.Fatalf("after the preferences chose English, /me %v", l)
		}
		h.json("PUT", "/api/v1/me/locale", "mkeller", api.MyLocale{Locale: "fr"}, 200, nil)
		h.json("GET", "/api/v1/me/notification-preferences", "mkeller", nil, 200, &prefs)
		for _, r := range prefs.Rules {
			if r.Kind == api.KindRequestFailed && (r.Default || len(r.Channels) != 0) {
				t.Fatalf("choosing a language lost a rule: %+v", r)
			}
		}
		if prefs.Locale != "fr" {
			t.Fatalf("messages = %s", prefs.Locale)
		}

		var other api.Me
		h.json("GET", "/api/v1/me", "tnguyen", nil, 200, &other)
		if other.Locale != nil {
			t.Fatalf("Marc's choice reached Thomas: %s", *other.Locale)
		}
	})
}
