package restcall

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func server(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Caller) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, &Caller{Hosts: []string{srv.URL}, Client: NewClient(2 * time.Second)}
}

func TestDo(t *testing.T) {
	var got *http.Request
	var body string
	srv, c := server(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got, body = r, string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	a, err := c.Do(context.Background(), Request{Method: "POST", URL: srv.URL + "/ci", Body: []byte(`{"a":1}`),
		Credential: &Credential{Kind: "token", Value: "tok"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != 201 || string(a.Body) != `{"id":1}` || body != `{"a":1}` {
		t.Fatalf("answer %+v, body sent %q", a, body)
	}
	if got.Header.Get("Authorization") != "Bearer tok" || got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers sent = %v", got.Header)
	}
	for _, tc := range []struct {
		c    Credential
		name string
		want string
	}{
		{Credential{Kind: "basic", Username: "u", Value: "p"}, "Authorization", "Basic " + base64.StdEncoding.EncodeToString([]byte("u:p"))},
		{Credential{Kind: "header", Header: "X-Api-Key", Value: "k"}, "X-Api-Key", "k"},
	} {
		if _, err := c.Do(context.Background(), Request{Method: "GET", URL: srv.URL, Credential: &tc.c}); err != nil {
			t.Fatal(err)
		}
		if got.Header.Get(tc.name) != tc.want {
			t.Errorf("%s: %s = %q, want %q", tc.c.Kind, tc.name, got.Header.Get(tc.name), tc.want)
		}
	}
}

func TestDoRefuses(t *testing.T) {
	calls := 0
	srv, c := server(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		case "/large":
			_, _ = w.Write([]byte(strings.Repeat("x", MaxAnswer+1)))
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		}
	})
	ctx := context.Background()

	// A host not listed isn't called.
	other := &Caller{Hosts: []string{"https://elsewhere.example"}, Client: c.Client}
	if _, err := other.Do(ctx, Request{Method: "GET", URL: srv.URL}); !errors.Is(err, ErrNotAllowed) || calls != 0 {
		t.Fatalf("a host not allowed: %v, %d calls", err, calls)
	}
	// A redirect is an answer, not followed.
	a, err := c.Do(ctx, Request{Method: "GET", URL: srv.URL + "/redirect"})
	if err != nil || a.Status != 302 || calls != 1 {
		t.Fatalf("a redirect: %+v %v, %d calls", a, err, calls)
	}
	if _, err := c.Do(ctx, Request{Method: "GET", URL: srv.URL + "/large"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over 1 MB: %v", err)
	}
	// A failure says nothing of the URL, whose query may hold anything.
	slow := &Caller{Hosts: c.Hosts, Client: NewClient(50 * time.Millisecond)}
	if _, err := slow.Do(ctx, Request{Method: "GET", URL: srv.URL + "/slow?token=hidden"}); err == nil || strings.Contains(err.Error(), "hidden") {
		t.Fatalf("a timeout = %v", err)
	}
}

// From the review of S8: what inputs may change in a REST source's URL.
func TestFixedOrigin(t *testing.T) {
	for tpl, want := range map[string]bool{
		"https://cmdb.corp.example/apps":                   true,
		"https://cmdb.corp.example/apps?app=${inputs.app}": true,
		"https://cmdb.corp.example/${inputs.path}/x":       true,
		"https://cmdb.corp.example/ci/${steps.vm.id}":      true,
		"https://cmdb.corp.example#${inputs.x}":            true,
		"https://${inputs.h}.corp.example/api":             false,
		"https://cmdb.corp.example:${inputs.port}/api":     false,
		"https://cmdb.corp.example${inputs.rest}":          false,
		"${inputs.scheme}://cmdb.corp.example/api":         false,
		"https://user:${inputs.pw}@cmdb.corp.example/api":  false,
	} {
		if got := FixedOrigin(tpl); got != want {
			t.Errorf("FixedOrigin(%q) = %v, want %v", tpl, got, want)
		}
	}
}

func TestParseURL(t *testing.T) {
	parts, err := ParseURL(`https://h/a/${steps.vm.name}?x=${{'a}': 'b'}[inputs.k]}&y=${ inputs.y }`)
	if err != nil {
		t.Fatal(err)
	}
	want := []Part{{Text: "https://h/a/"}, {Expr: "steps.vm.name"}, {Text: "?x="}, {Expr: "{'a}': 'b'}[inputs.k]"}, {Text: "&y="}, {Expr: "inputs.y"}}
	if len(parts) != len(want) {
		t.Fatalf("parts = %+v", parts)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("part %d = %+v, want %+v", i, parts[i], want[i])
		}
	}
	for _, bad := range []string{"https://h/${inputs.a", "https://h/${'}", "https://h/${}"} {
		if _, err := ParseURL(bad); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
	if got := Skeleton("https://h:8/a/${x.y}?q=${z}"); got != "https://h:8/a/x?q=x" {
		t.Errorf("Skeleton = %s", got)
	}
	if got := Expressions("https://h/${a}/${b}"); len(got) != 2 || got[1] != "b" {
		t.Errorf("Expressions = %v", got)
	}
}

func TestFill(t *testing.T) {
	values := map[string]any{"s": "a b/c?d", "n": float64(42), "big": float64(12345678), "i": int64(7), "b": true,
		"none": nil, "empty": "", "dot": ".", "dots": "..", "map": map[string]any{"a": 1}, "list": []any{"a"}}
	fill := func(tpl string) (string, error) {
		parts, err := ParseURL(tpl)
		if err != nil {
			t.Fatal(err)
		}
		return Fill(parts, func(e string) (any, error) { return values[e], nil })
	}
	got, err := fill("https://h/${s}/${n}/${big}/${i}?b=${b}")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://h/a%20b%2Fc%3Fd/42/12345678/7?b=true"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	for _, e := range []string{"none", "empty", "dot", "dots", "map", "list"} {
		if _, err := fill("https://h/ci/${" + e + "}"); err == nil {
			t.Errorf("${%s} filled", e)
		}
	}
}

func TestScrubber(t *testing.T) {
	const secret = `s3cr"t/+ pass`
	s := NewScrubber(secret, "u:"+secret, "12345", "")
	b64 := base64.StdEncoding.EncodeToString([]byte(secret))
	basic := base64.StdEncoding.EncodeToString([]byte("u:" + secret))
	for _, text := range []string{
		"Bearer " + secret,
		"Basic " + basic,
		b64,
		`{"token":"s3cr\"t/+ pass"}`, // escaped by a JSON answer held as text
		"token=s3cr%22t%2F%2B+pass",  // in a query
		"token=s3cr%22t%2F%2B%20pass",
		"at /s3cr%22t%2F+%20pass",
	} {
		if got := s.Text(text); strings.Contains(got, "s3cr") || strings.Contains(got, b64) || strings.Contains(got, basic) || !strings.Contains(got, Removed) {
			t.Errorf("%s -> %s", text, got)
		}
	}
	got := s.Value(map[string]any{
		"echo":  map[string]any{"Authorization": "Bearer " + secret, "list": []any{secret, "kept"}},
		secret:  "as a key",
		"pin":   float64(12345),
		"other": float64(7),
		"ok":    true,
		"none":  nil,
	}).(map[string]any)
	if got["pin"] != Removed || got["other"] != float64(7) || got["ok"] != true || got["none"] != nil {
		t.Errorf("got %v", got)
	}
	if got[Removed] != "as a key" {
		t.Errorf("a key: %v", got)
	}
	echo := got["echo"].(map[string]any)
	if echo["Authorization"] != "Bearer "+Removed || echo["list"].([]any)[0] != Removed || echo["list"].([]any)[1] != "kept" {
		t.Errorf("echo: %v", echo)
	}
	if empty := NewScrubber(""); empty.Text("nothing to do") != "nothing to do" {
		t.Error("an empty value scrubbed something")
	}
}

func TestStatusText(t *testing.T) {
	if StatusText(503) != "503 Service Unavailable" || StatusText(599) != "599" {
		t.Errorf("%q %q", StatusText(503), StatusText(599))
	}
}
