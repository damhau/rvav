package rvavclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://rvav.corp.example":         "https://rvav.corp.example/api/v1",
		"https://rvav.corp.example/":        "https://rvav.corp.example/api/v1",
		"https://rvav.corp.example/api/v1":  "https://rvav.corp.example/api/v1",
		"https://rvav.corp.example/api/v1/": "https://rvav.corp.example/api/v1",
		" http://127.0.0.1:8080?x=1#top ":   "http://127.0.0.1:8080/api/v1",
		"https://corp.example/rvav":         "https://corp.example/rvav/api/v1",
		"https://corp.example/rvav/api/v1":  "https://corp.example/rvav/api/v1",
	} {
		if got, err := BaseURL(in); err != nil || got != want {
			t.Errorf("BaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "rvav.corp.example", "ftp://rvav.corp.example", "https://", ":"} {
		if _, err := BaseURL(in); err == nil {
			t.Errorf("BaseURL(%q) should fail", in)
		}
	}
}

func TestCheck(t *testing.T) {
	ok := &http.Response{StatusCode: 202}
	if err := Check(ok, nil); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"about:blank","title":"Validation failed","status":422,"detail":"The request has invalid inputs.",
		"request_id":"r1","errors":[{"field":"/inputs/role","message":"This value is not in the expected format."},
		{"field":"/count","message":"Request between 1 and 20 at a time."},{"field":"","message":"Not available."}]}`
	err := Check(&http.Response{StatusCode: 422}, []byte(body))
	want := "Validation failed: The request has invalid inputs.\n  role: This value is not in the expected format.\n" +
		"  count: Request between 1 and 20 at a time.\n  Not available."
	if err == nil || err.Error() != want {
		t.Fatalf("Check(422) = %v, want %q", err, want)
	}
	if !IsStatus(err, 422) || IsStatus(err, 404) || IsStatus(errors.New("x"), 422) {
		t.Fatal("IsStatus")
	}
	var e *Error
	if !errors.As(fmt.Errorf("wrapped: %w", err), &e) || len(*e.Problem.Errors) != 3 {
		t.Fatalf("the problem is kept: %+v", e)
	}
	if err := Check(&http.Response{StatusCode: 502}, []byte("<html>bad gateway</html>")); err == nil ||
		err.Error() != "rvav answered 502 Bad Gateway" {
		t.Fatalf("Check(502 HTML) = %v", err)
	}
	if err := Check(nil, nil); err == nil {
		t.Fatal("no response is an error")
	}
}

// TestConnect sends the token, the agent, and nothing when there is no
// token.
func TestConnect(t *testing.T) {
	var auth, agent atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me" {
			http.NotFound(w, r)
			return
		}
		auth.Store(r.Header.Get("Authorization"))
		agent.Store(r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{"id":"usr_1","name":"Léa Martin"},"roles":[],"permissions":[],"projects":[]}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	c, err := Connect(srv.URL, StaticToken("rvav_pat_x"), UserAgent("rvavctl/test"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.GetMeWithResponse(ctx)
	if err != nil || Check(r.HTTPResponse, r.Body) != nil || r.JSON200.User.Name != "Léa Martin" {
		t.Fatalf("GetMe = %v %+v", err, r)
	}
	if auth.Load() != "Bearer rvav_pat_x" || agent.Load() != "rvavctl/test" {
		t.Fatalf("sent Authorization %q, User-Agent %q", auth.Load(), agent.Load())
	}
	c, _ = Connect(srv.URL+"/api/v1", StaticToken(""))
	if _, err := c.GetMeWithResponse(ctx); err != nil || auth.Load() != "" {
		t.Fatalf("no token sent %q: %v", auth.Load(), err)
	}
}

func TestProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rvav", "config.json")
	ps, err := LoadProfiles(path)
	if err != nil || len(ps.Profiles) != 0 {
		t.Fatalf("no file yet = %+v, %v", ps, err)
	}
	ps.Profiles["default"] = &Profile{Server: "https://rvav.corp.example", Token: "rvav_pat_x"}
	if err := ps.Save(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", fi.Mode(), err)
	}
	if d, _ := os.Stat(filepath.Dir(path)); d.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v", d.Mode())
	}
	// Another rvavctl adds a profile meanwhile: Update keeps it.
	other, _ := LoadProfiles(path)
	other.Profiles["test"] = &Profile{Server: "https://rvav-test.corp.example"}
	if err := other.Save(); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, "default", func(p *Profile) { p.Token = "rvav_pat_y" }); err != nil {
		t.Fatal(err)
	}
	ps, _ = LoadProfiles(path)
	if len(ps.Profiles) != 2 || ps.Profiles["default"].Token != "rvav_pat_y" {
		t.Fatalf("profiles = %+v", ps.Profiles)
	}
	if err := Update(path, "nope", func(*Profile) {}); err == nil {
		t.Fatal("updating a missing profile should fail")
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfiles(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("a broken file = %v", err)
	}
}

// TestTokenSource refreshes an expired sign-in once, saves the new token,
// and says to sign in again when the provider refuses.
func TestTokenSource(t *testing.T) {
	if (&Profile{Server: "https://rvav.corp.example"}).TokenSource(context.Background(), nil) != nil {
		t.Fatal("a profile without credentials has no token source")
	}
	if tok, err := (&Profile{Token: "rvav_pat_x"}).TokenSource(context.Background(), nil).Token(); err != nil || tok.AccessToken != "rvav_pat_x" {
		t.Fatalf("API token = %+v, %v", tok, err)
	}

	var refreshes atomic.Int32
	var refuse atomic.Bool
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if refuse.Load() || r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("client_id") != "rvav" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		n := refreshes.Add(1)
		fmt.Fprintf(w, `{"access_token":"at-%d","token_type":"Bearer","expires_in":300,"refresh_token":"rt-%d"}`, n, n)
	}))
	defer idp.Close()
	p := &Profile{OIDC: &OIDCSession{ClientID: "rvav", TokenURL: idp.URL,
		Token: &oauth2.Token{AccessToken: "at-0", RefreshToken: "rt-0", Expiry: time.Now().Add(-time.Minute)}}}
	var saved []string
	src := p.TokenSource(context.Background(), func(t *oauth2.Token) error {
		saved = append(saved, t.AccessToken+"/"+t.RefreshToken)
		return nil
	})
	for range 3 {
		tok, err := src.Token()
		if err != nil || tok.AccessToken != "at-1" {
			t.Fatalf("Token = %+v, %v", tok, err)
		}
	}
	if refreshes.Load() != 1 || len(saved) != 1 || saved[0] != "at-1/rt-1" {
		t.Fatalf("refreshes = %d, saved %v", refreshes.Load(), saved)
	}

	refuse.Store(true)
	p.OIDC.Token.Expiry = time.Now().Add(-time.Minute)
	_, err := p.TokenSource(context.Background(), nil).Token()
	if !errors.Is(err, ErrSignInExpired) {
		t.Fatalf("refused refresh = %v", err)
	}
	failing := p.TokenSource(context.Background(), func(*oauth2.Token) error { return errors.New("disk full") })
	refuse.Store(false)
	if _, err := failing.Token(); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("a refreshed token that can't be saved = %v", err)
	}
}

// TestProfilesShared has the file's writers take turns: none loses what
// another saved, and the directory is its user's only even when it was
// made wider.
func TestProfilesShared(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rvav")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	ps, _ := LoadProfiles(path)
	for i := range 20 {
		ps.Profiles[fmt.Sprint(i)] = &Profile{Server: "https://rvav.corp.example"}
	}
	if err := ps.Save(); err != nil {
		t.Fatal(err)
	}
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v", d.Mode())
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if err := Update(path, fmt.Sprint(i), func(p *Profile) { p.Token = fmt.Sprint("rvav_pat_", i) }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	ps, _ = LoadProfiles(path)
	for i := range 20 {
		if p := ps.Profiles[fmt.Sprint(i)]; p.Token != fmt.Sprint("rvav_pat_", i) {
			t.Errorf("profile %d lost its token: %+v", i, p)
		}
	}
}

// rotatingIdP rotates refresh tokens and refuses one already spent, as
// Keycloak and Okta can.
func rotatingIdP(t *testing.T) (url string, refreshes func() int) {
	var mu sync.Mutex
	current, n := "rt-0", 0
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("refresh_token") != current {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Stale token"}`))
			return
		}
		n++
		current = fmt.Sprintf("rt-%d", n)
		fmt.Fprintf(w, `{"access_token":"at-%d","token_type":"Bearer","expires_in":300,"refresh_token":"%s"}`, n, current)
	}))
	t.Cleanup(idp.Close)
	return idp.URL, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

// TestProfilesTokenSource has two processes share a profile at a provider
// that rotates refresh tokens: the one that waits takes the token the other
// refreshed, rather than spend its own again.
func TestProfilesTokenSource(t *testing.T) {
	idp, refreshes := rotatingIdP(t)
	path := filepath.Join(t.TempDir(), "config.json")
	ps, _ := LoadProfiles(path)
	expired := func() *oauth2.Token {
		return &oauth2.Token{AccessToken: "at-0", RefreshToken: "rt-0", Expiry: time.Now().Add(-time.Minute)}
	}
	ps.Profiles["default"] = &Profile{Server: "https://rvav.corp.example",
		OIDC: &OIDCSession{Issuer: "https://idp.example", ClientID: "rvav", TokenURL: idp, Token: expired()}}
	ps.Profiles["api"] = &Profile{Server: "https://rvav.corp.example", Token: "rvav_pat_x"}
	ps.Profiles["none"] = &Profile{Server: "https://rvav.corp.example"}
	if err := ps.Save(); err != nil {
		t.Fatal(err)
	}
	if ps.TokenSource(context.Background(), "none") != nil || ps.TokenSource(context.Background(), "nope") != nil {
		t.Fatal("a profile without credentials has no token source")
	}
	if tok, err := ps.TokenSource(context.Background(), "api").Token(); err != nil || tok.AccessToken != "rvav_pat_x" {
		t.Fatalf("API token = %+v, %v", tok, err)
	}
	source := func() oauth2.TokenSource {
		ps, err := LoadProfiles(path)
		if err != nil {
			t.Fatal(err)
		}
		return ps.TokenSource(context.Background(), "default")
	}
	token := func(src oauth2.TokenSource) string {
		t.Helper()
		tok, err := src.Token()
		if err != nil {
			t.Fatal(err)
		}
		return tok.AccessToken
	}
	expire := func(src oauth2.TokenSource) { src.(*fileSource).token.Expiry = time.Now().Add(-time.Minute) }
	expireFile := func() {
		if err := Update(path, "default", func(p *Profile) { p.OIDC.Token.Expiry = time.Now().Add(-time.Minute) }); err != nil {
			t.Fatal(err)
		}
	}
	wait := source()
	if got := token(wait); got != "at-1" {
		t.Fatalf("first refresh = %s", got)
	}
	if got := token(source()); got != "at-1" || refreshes() != 1 {
		t.Fatalf("another process = %s after %d refreshes, want the saved at-1", got, refreshes())
	}
	// Another process refreshes first once both expire.
	expireFile()
	if got := token(source()); got != "at-2" {
		t.Fatalf("second refresh = %s", got)
	}
	expire(wait)
	if got := token(wait); got != "at-2" || refreshes() != 2 {
		t.Fatalf("the waiting process = %s after %d refreshes, want the other's at-2", got, refreshes())
	}

	// Both expired: the waiting one refreshes with the file's refresh token.
	expireFile()
	expire(wait)
	if got := token(wait); got != "at-3" {
		t.Fatalf("refresh with the file's token = %s", got)
	}
	ps, _ = LoadProfiles(path)
	if ps.Profiles["default"].OIDC.Token.RefreshToken != "rt-3" {
		t.Fatalf("saved = %+v", ps.Profiles["default"].OIDC.Token)
	}

	// Signed in again as someone else: the waiting one keeps its own.
	if err := Update(path, "default", func(p *Profile) {
		p.OIDC = &OIDCSession{Issuer: "https://other.example", ClientID: "rvav", TokenURL: idp, Token: expired()}
	}); err != nil {
		t.Fatal(err)
	}
	expire(wait)
	if got := token(wait); got != "at-4" {
		t.Fatalf("after another sign-in = %s", got)
	}
	if ps, _ = LoadProfiles(path); ps.Profiles["default"].OIDC.Issuer != "https://other.example" {
		t.Fatalf("the other sign-in was overwritten: %+v", ps.Profiles["default"].OIDC)
	}
}

// TestRefreshErrors tells a refresh the provider refused, which needs a
// new sign-in, from one that got no answer, which is worth trying again.
func TestRefreshErrors(t *testing.T) {
	var status atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	refresh := func() error {
		p := &Profile{OIDC: &OIDCSession{ClientID: "rvav", TokenURL: idp.URL,
			Token: &oauth2.Token{AccessToken: "at-0", RefreshToken: "rt-0", Expiry: time.Now().Add(-time.Minute)}}}
		_, err := p.TokenSource(context.Background(), nil).Token()
		return err
	}
	status.Store(400)
	if err := refresh(); !errors.Is(err, ErrSignInExpired) || Transient(err) {
		t.Errorf("refused = %v", err)
	}
	status.Store(503)
	if err := refresh(); errors.Is(err, ErrSignInExpired) || !Transient(err) {
		t.Errorf("provider unavailable = %v", err)
	}
	idp.Close()
	if err := refresh(); errors.Is(err, ErrSignInExpired) || !Transient(err) || !strings.Contains(err.Error(), "refreshing the sign-in") {
		t.Errorf("no answer = %v", err)
	}
}

func TestTransient(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	untrusted := &tls.CertificateVerificationError{Err: errors.New("unknown authority")}
	for err, want := range map[error]bool{
		&Error{Status: 502}: true, &Error{Status: 503}: true, &Error{Status: 429}: true, &Error{Status: 500}: true,
		&Error{Status: 404}: false, &Error{Status: 401}: false, &Error{Status: 409}: false,
		fmt.Errorf("reading: %w", &url.Error{Op: "Get", URL: "https://rvav", Err: refused}): true,
		&url.Error{Op: "Get", URL: "https://rvav", Err: context.Canceled}:                   false,
		&url.Error{Op: "Get", URL: "https://rvav", Err: untrusted}:                          false,
		fmt.Errorf("%w: invalid_grant", ErrSignInExpired):                                   false,
		errors.New("not signed in"):                                                         false,
	} {
		if got := Transient(err); got != want {
			t.Errorf("Transient(%v) = %v", err, got)
		}
	}
}

// TestRetry waits Interval between reads, longer after each error worth
// trying again, and gives up on any other error or once the context ends.
func TestRetry(t *testing.T) {
	var pauses []time.Duration
	r := &Retry{Interval: time.Millisecond, MaxPause: 5 * time.Millisecond,
		Retrying: func(_ error, pause time.Duration) { pauses = append(pauses, pause) }}
	ctx := context.Background()
	for _, err := range []error{&Error{Status: 502}, &Error{Status: 502}, &Error{Status: 503}, &Error{Status: 503}, nil, &Error{Status: 502}} {
		if got := r.Wait(ctx, err); got != nil {
			t.Fatalf("Wait(%v) = %v", err, got)
		}
	}
	if fmt.Sprint(pauses) != "[2ms 4ms 5ms 5ms 2ms]" {
		t.Fatalf("pauses = %v", pauses)
	}
	notFound := &Error{Status: 404}
	if got := r.Wait(ctx, notFound); got != notFound {
		t.Fatalf("Wait(404) = %v", got)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if got := r.Wait(ctx, nil); !errors.Is(got, context.Canceled) {
		t.Fatalf("Wait after the end = %v", got)
	}
}

func TestUnencrypted(t *testing.T) {
	for server, want := range map[string]bool{
		"http://rvav.corp.example": true, "http://10.0.0.5:8080": true, "HTTP://rvav.corp.example/api/v1": true,
		"https://rvav.corp.example": false, "http://localhost:8080": false, "http://127.0.0.1:8080": false,
		"http://[::1]:8080": false, "http://127.1.2.3": false,
	} {
		if got := Unencrypted(server); got != want {
			t.Errorf("Unencrypted(%q) = %v", server, got)
		}
	}
}
