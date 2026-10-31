package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/core"
)

// TestLocalAdminAttempts: what the local administrator's sign-in holds
// against someone guessing, beside TestLocalAdmin's schedule. Attempts
// made at once can't all pass the wait; one whose client has gone gives up
// its hash slot, and an unknown username takes none; the account's API
// tokens end with a password change or a reset, and aren't taken while
// the password must be changed.
func TestLocalAdminAttempts(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const password = "the administrator's own password"
		if _, err := core.ResetLocalAdmin(ctx, h.store, password); err != nil {
			t.Fatal(err)
		}

		// Every hash slot taken: an unknown username is refused at once, and
		// an attempt whose client goes gives up waiting.
		release := auth.HoldHashSlots()
		start := time.Now()
		if _, _, err := h.svc.SignIn(ctx, "nobody", "a guess", time.Now()); !errors.Is(err, core.ErrBadCredentials) ||
			time.Since(start) > time.Second {
			t.Fatalf("an unknown username with every hash slot taken: %v after %s", err, time.Since(start))
		}
		gone, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		if _, _, err := h.svc.SignIn(gone, "admin", password, time.Now()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the administrator's sign-in, its client gone: %v", err)
		}
		cancel()
		release()
		if _, _, err := h.svc.SignIn(ctx, "admin", password, time.Now()); err != nil {
			t.Fatalf("sign-in once the slots are free: %v", err)
		}

		// Forty wrong passwords at once: three are checked, the others wait.
		const n = 40
		var wg sync.WaitGroup
		errs := make([]error, n)
		now := time.Now()
		for i := range n {
			wg.Go(func() { _, _, errs[i] = h.svc.SignIn(ctx, "admin", "a guess", now) })
		}
		wg.Wait()
		checked, waited := 0, 0
		for _, err := range errs {
			var tm *core.TooManyAttemptsError
			switch {
			case errors.Is(err, core.ErrBadCredentials):
				checked++
			case errors.As(err, &tm):
				waited++
			default:
				t.Errorf("an attempt at once: %v", err)
			}
		}
		if checked != 3 || waited != n-3 {
			t.Fatalf("%d attempts at once: %d checked, %d waiting; want 3 checked", n, checked, waited)
		}
		var failed int
		_ = h.store.Pool.QueryRow(ctx, `select failed_attempts from local_accounts where user_id = $1`, core.LocalAdminUserID).Scan(&failed)
		if failed != 3 {
			t.Fatalf("failures in a row = %d, want 3", failed)
		}
		if err := h.store.LocalAttemptRight(ctx, core.LocalAdminUserID); err != nil {
			t.Fatal(err)
		}

		// The account's API tokens end with a password change, and with a
		// reset.
		b := newBrowser(t, ctx, h.srv.URL)
		if code, res, _ := b.signIn("admin", password); code != http.StatusOK || !res.PasswordChangeRequired {
			t.Fatalf("sign-in = %d %+v", code, res)
		}
		changed := "the administrator's next password"
		if code, out, _ := b.do("POST", "/api/v1/auth/password", api.PasswordChange{CurrentPassword: password, NewPassword: changed}, false); code != http.StatusNoContent {
			t.Fatalf("change = %d %s", code, out)
		}
		token := func() string {
			t.Helper()
			code, out, _ := b.do("POST", "/api/v1/tokens", api.ApiTokenCreate{Name: "a script"}, false)
			var tok api.ApiTokenCreated
			if code != http.StatusCreated || json.Unmarshal(out, &tok) != nil {
				t.Fatalf("token = %d %s", code, out)
			}
			return tok.Token
		}
		bearer := func(tok string) int {
			t.Helper()
			code, _, _ := newBrowser(t, ctx, h.srv.URL).do("GET", "/api/v1/admin/integrations", nil, true, "Authorization", "Bearer "+tok)
			return code
		}
		tok := token()
		if code := bearer(tok); code != http.StatusOK {
			t.Fatalf("the token = %d", code)
		}
		other := newBrowser(t, ctx, h.srv.URL)
		if code, _, _ := other.signIn("admin", changed); code != http.StatusOK {
			t.Fatalf("a second sign-in = %d", code)
		}
		if code, out, _ := other.do("POST", "/api/v1/auth/password", api.PasswordChange{CurrentPassword: changed, NewPassword: password}, false); code != http.StatusNoContent {
			t.Fatalf("a second change = %d %s", code, out)
		}
		if code := bearer(tok); code != http.StatusUnauthorized {
			t.Fatalf("the token after a password change = %d, want 401", code)
		}
		b = other
		tok = token()
		if _, err := core.ResetLocalAdmin(ctx, h.store, ""); err != nil {
			t.Fatal(err)
		}
		if code := bearer(tok); code != http.StatusUnauthorized {
			t.Fatalf("the token after rvav admin reset-password = %d, want 401", code)
		}
		var revoked int
		_ = h.store.Pool.QueryRow(ctx, `select (detail->>'tokens_revoked')::int from audit_events
			where action = 'auth.password_reset' order by id desc limit 1`).Scan(&revoked)
		if revoked != 1 {
			t.Errorf("the reset audited %d tokens revoked, want 1", revoked)
		}

		// A token made before the password had to be changed isn't taken
		// until it is.
		if err := h.store.SetLocalPassword(ctx, core.LocalAdminUserID, mustHash(t, password), false); err != nil {
			t.Fatal(err)
		}
		b = newBrowser(t, ctx, h.srv.URL)
		if code, _, _ := b.signIn("admin", password); code != http.StatusOK {
			t.Fatalf("sign-in = %d", code)
		}
		tok = token()
		if err := h.store.SetLocalPassword(ctx, core.LocalAdminUserID, mustHash(t, password), true); err != nil {
			t.Fatal(err)
		}
		if code := bearer(tok); code != http.StatusUnauthorized {
			t.Fatalf("a token while the password must be changed = %d, want 401", code)
		}
	})
}

func mustHash(t *testing.T, password string) string {
	t.Helper()
	h, err := auth.HashPassword(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
