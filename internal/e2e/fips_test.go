package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alexedwards/argon2id"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
)

// fipsOnly are end-to-end tests that also run in FIPS 140-only mode
// (ADR-0060): the processes starting and the local administrator signing
// in (TestInstallSettingsOnly), a request provisioned in vcsim
// (TestSlice1EndToEnd), sign-in with an identity provider (TestOIDCTokens),
// webhooks signed with HMAC (TestEventOutbox), audit events forwarded to
// syslog over mutual TLS and to a webhook with its sealed header, API tokens
// (TestInventoryToken, TestTokensAndAudit) and the System page.
const fipsOnly = "^(TestInstallSettingsOnly|TestSlice1EndToEnd|TestOIDCTokens|TestEventOutbox|TestInventoryToken|" +
	"TestTokensAndAudit|TestLocalAdmin|TestSystemPage|TestAuditForwardingSyslog|TestAuditForwardingWebhook|" +
	"TestAuditForwardingWebhookCredentials)$"

// TestFIPSOnly runs fipsOnly again in a process in FIPS 140-only mode,
// against this run's database.
func TestFIPSOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	if fips.Current() == fips.Only {
		t.Skip("this run is in FIPS 140-only mode already")
	}
	fipstest.Run(t, fips.Only, fipsOnly, "RVAV_TEST_DATABASE_URL="+databaseURL)
}

// fipsAdmin is a local administrator whose password is kept as an argon2id
// hash, as rvav kept it before FIPS 140 mode was on.
func fipsAdmin(t *testing.T, ctx context.Context, h *harness, password string) {
	t.Helper()
	if _, err := core.ResetLocalAdmin(ctx, h.store, password); err != nil {
		t.Fatal(err)
	}
	hash, err := argon2id.CreateHash(password, &argon2id.Params{Memory: 64 * 1024, Iterations: 1, Parallelism: 2, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Pool.Exec(ctx, `update local_accounts set password_hash = $2 where user_id = $1`, core.LocalAdminUserID, hash); err != nil {
		t.Fatal(err)
	}
}

func adminHash(t *testing.T, ctx context.Context, h *harness) string {
	t.Helper()
	var hash string
	if err := h.store.Pool.QueryRow(ctx, `select password_hash from local_accounts where user_id = $1`, core.LocalAdminUserID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

// TestLocalAdminInFIPSMode: in FIPS 140 mode, the local administrator's
// password kept as an argon2id hash still signs in, and is kept as a
// PBKDF2 one from then on (ADR-0060).
func TestLocalAdminInFIPSMode(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	if !fipstest.In(t, fips.On, "RVAV_TEST_DATABASE_URL="+databaseURL) {
		return
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const password = "kept since before FIPS 140 mode"
		fipsAdmin(t, ctx, h, password)
		for range 2 {
			if code, _, _ := newBrowser(t, ctx, h.srv.URL).signIn("admin", password); code != http.StatusOK {
				t.Fatalf("sign-in = %d", code)
			}
			if hash := adminHash(t, ctx, h); !strings.HasPrefix(hash, "$pbkdf2-sha512$i=210000$") {
				t.Fatalf("the password is kept as %.20s…", hash)
			}
		}
		if code, _, _ := newBrowser(t, ctx, h.srv.URL).signIn("admin", "a wrong password"); code != http.StatusUnauthorized {
			t.Fatalf("a wrong password = %d", code)
		}
	})
}

// TestLocalAdminInFIPSOnlyMode: in FIPS 140-only mode, the local
// administrator's password kept as an argon2id hash is refused with what
// to do, counted as a failed attempt so that attempts wait; once reset, it
// signs in (ADR-0060). The System page says the mode.
func TestLocalAdminInFIPSOnlyMode(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	if !fipstest.In(t, fips.Only, "RVAV_TEST_DATABASE_URL="+databaseURL) {
		return
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const password = "kept since before FIPS 140 mode"
		fipsAdmin(t, ctx, h, password)
		b := newBrowser(t, ctx, h.srv.URL)
		for range 3 {
			code, body, _ := b.do("POST", "/api/v1/auth/sign-in", api.SignInRequest{Username: "admin", Password: password}, false)
			var p api.Problem
			_ = json.Unmarshal(body, &p)
			if code != http.StatusConflict || p.Detail == nil || !strings.Contains(*p.Detail, "rvav admin reset-password") ||
				!strings.Contains(*p.Detail, "RVAV_FIPS=on") {
				t.Fatalf("sign-in with an argon2id hash = %d %s", code, body)
			}
		}
		if code, body, _ := b.do("POST", "/api/v1/auth/sign-in", api.SignInRequest{Username: "admin", Password: password}, false); code != http.StatusTooManyRequests {
			t.Fatalf("a fourth sign-in at once = %d %s", code, body)
		}
		var reason string
		if err := h.store.Pool.QueryRow(ctx, `select detail->>'reason' from audit_events where action = 'auth.sign_in_failed'
			order by at desc limit 1`).Scan(&reason); err != nil || !strings.Contains(reason, "FIPS 140-only mode") {
			t.Fatalf("audited reason = %q, %v", reason, err)
		}

		const again = "set again in FIPS 140-only mode"
		if _, err := core.ResetLocalAdmin(ctx, h.store, again); err != nil {
			t.Fatal(err)
		}
		if hash := adminHash(t, ctx, h); !strings.HasPrefix(hash, "$pbkdf2-sha512$") {
			t.Fatalf("the reset keeps it as %.20s…", hash)
		}
		if code, res, _ := b.signIn("admin", again); code != http.StatusOK || !res.PasswordChangeRequired {
			t.Fatalf("sign-in once reset = %d %+v", code, res)
		}

		var sys api.System
		h.json("GET", "/api/v1/admin/system", "mkeller", nil, 200, &sys)
		if sys.Fips.Mode != api.FipsOnly || sys.Fips.Module != fips.Module() {
			t.Fatalf("the System page says %+v", sys.Fips)
		}
	})
}
