package rvavclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
