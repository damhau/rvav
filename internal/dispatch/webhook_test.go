package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
	"github.com/damhau/rvav/internal/store"
)

// In FIPS 140-only mode a webhook is signed with HMAC-SHA-256 as in any
// other, and one whose secret is shorter than 112 bits isn't sent: Go
// would panic signing it (ADR-0060).
func TestWebhookInFIPSOnlyMode(t *testing.T) {
	if !fipstest.In(t, fips.Only) {
		return
	}
	var got http.Header
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	secrets := map[string]string{"long": "a secret of 32 bytes, or about.", "short": "hook-secret"}
	w := &Webhook{Hosts: []string{"http://" + u.Host}, Client: srv.Client(),
		Secret: func(_ context.Context, name string) (string, error) { return secrets[name], nil }}
	deliver := func(secret string) Result {
		target, _ := json.Marshal(map[string]any{"webhook": WebhookTarget{URL: srv.URL, Secret: secret}})
		return w.Deliver(context.Background(), Delivery{Sub: store.Subscription{Target: target, TimeoutSeconds: 5},
			Event: json.RawMessage(`{"id":"evt_1","topic":"deployment.requested"}`)})
	}

	if res := deliver("long"); !res.OK {
		t.Fatalf("with a long secret: %+v", res)
	}
	sig := got.Get("X-Rvav-Signature")
	ts, _, _ := strings.Cut(strings.TrimPrefix(sig, "t="), ",")
	at, _ := strconv.ParseInt(ts, 10, 64)
	if sig != Sign(secrets["long"], time.Unix(at, 0), body) {
		t.Fatalf("signature %q", sig)
	}

	got = nil
	res := deliver("short")
	if res.OK || got != nil || !strings.Contains(res.Err, "shorter than 112 bits") || !strings.Contains(res.Err, "short") {
		t.Fatalf("with an 11-byte secret: %+v, sent: %v", res, got != nil)
	}
}
