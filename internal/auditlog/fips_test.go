package auditlog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
	"github.com/damhau/rvav/internal/store"
)

// In FIPS 140-only mode an audit webhook is signed as in any other, and
// one whose secret is shorter than 112 bits isn't sent: Go would panic
// signing it (ADR-0060). TestFIPSOnly forwards to syslog over TLS.
func TestForwardingInFIPSOnlyMode(t *testing.T) {
	if !fipstest.In(t, fips.Only) {
		return
	}
	var signed string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		signed = r.Header.Get("X-Rvav-Signature")
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	secrets := map[string]string{"long": "a secret of 32 bytes, or about.", "short": "audit-hook"}
	f := &Forwarder{Timeout: 2 * time.Second, HTTP: srv.Client(),
		WebhookHosts: func(context.Context) []string { return []string{"http://" + u.Host} },
		Secret:       func(_ context.Context, name string) (string, error) { return secrets[name], nil }}
	events := []Event{{ID: "1", At: time.Now(), Action: "token.create", Detail: json.RawMessage(`{}`)}}

	s, err := f.webhook(context.Background(), store.AuditDestination{}, api.AuditWebhook{Url: srv.URL, Secret: "long"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.send(context.Background(), events); err != nil || !strings.HasPrefix(signed, "t=") {
		t.Fatalf("with a long secret: %v, signature %q", err, signed)
	}
	if _, err := f.webhook(context.Background(), store.AuditDestination{}, api.AuditWebhook{Url: srv.URL, Secret: "short"}); err == nil ||
		!strings.Contains(err.Error(), "shorter than 112 bits") || !strings.Contains(err.Error(), "short") {
		t.Fatalf("with a 10-byte secret: %v", err)
	}
}
