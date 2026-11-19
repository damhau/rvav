package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
)

// In FIPS 140-only mode the Git server's webhook is checked as in any
// other, but HMAC with a secret shorter than 112 bits is refused with a
// reason, not a panic; GitLab's token needs no HMAC (ADR-0060).
func TestGitWebhookInFIPSOnlyMode(t *testing.T) {
	if !fipstest.In(t, fips.Only) {
		return
	}
	body := []byte(`{"ref":"refs/heads/main"}`)
	const long = "a webhook secret of 32 bytes...."
	mac := hmac.New(sha256.New, []byte(long))
	mac.Write(body)
	signed := http.Header{"X-Hub-Signature-256": {"sha256=" + hex.EncodeToString(mac.Sum(nil))}}
	if ok, err := gitSigned(signed, body, long); !ok || err != nil {
		t.Fatalf("GitHub's signature: %v %v", ok, err)
	}
	if ok, err := gitSigned(http.Header{"X-Gitlab-Token": {"hook-s3cret"}}, body, "hook-s3cret"); !ok || err != nil {
		t.Fatalf("GitLab's token, short: %v %v", ok, err)
	}
	ok, err := gitSigned(http.Header{"X-Hub-Signature-256": {"sha256=00"}}, body, "hook-s3cret")
	if ok || !errors.Is(err, fips.ErrShortHMACKey) || !strings.Contains(err.Error(), "RVAV_GIT_WEBHOOK_SECRET") {
		t.Fatalf("an HMAC with a short secret: %v %v", ok, err)
	}
}
