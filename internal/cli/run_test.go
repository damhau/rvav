package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/damhau/rvav/pkg/rvavclient"
)

func request(status string) string { return requestAs("req_1", "REQ-1", status) }

func requestAs(id, number, status string) string {
	return fmt.Sprintf(`{"id":%q,"number":%q,"title":"t","status":%q,"kind":"provision",
"catalog_item":{"id":"cat_1","name":"rhel","slug":"rhel","type":"vm"},"environment":"dev","progress":{"done":0,"total":1},
"project":{"id":"prj_web","code":"web","name":"Web"},"requester":{"id":"usr_1","name":"Lea"},"version":{"id":"v1","number":"1"},
"submitted_at":"2026-11-22T10:00:00Z"}`, id, number, status)
}

// fake is an rvav that answers as handle says, and rvavctl calling it with
// an API token.
type fake struct {
	t      *testing.T
	srv    *httptest.Server
	env    map[string]string
	config string
	http   *http.Client
}

func newFake(t *testing.T, handle http.HandlerFunc) *fake {
	srv := httptest.NewServer(handle)
	t.Cleanup(srv.Close)
	return &fake{t: t, srv: srv, env: map[string]string{"RVAV_URL": srv.URL, "RVAV_TOKEN": "rvav_pat_x"},
		config: filepath.Join(t.TempDir(), "rvav", "config.json"), http: &http.Client{Timeout: 5 * time.Second}}
}

func (f *fake) run(args ...string) (int, string, string) {
	f.t.Helper()
	var out, stderr bytes.Buffer
	code := Main(context.Background(), args, Env{In: strings.NewReader(""), Out: &out, Err: &stderr,
		Getenv: func(k string) string { return f.env[k] }, ConfigPath: f.config, HTTP: f.http, Poll: 5 * time.Millisecond})
	return code, out.String(), stderr.String()
}

func answer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprint(w, body)
}

// TestWaitRetries goes on waiting through a proxy's 502 and a dropped
// connection, as during a rolling restart, and stops at once on a 404.
func TestWaitRetries(t *testing.T) {
	var mu sync.Mutex
	n := 0
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		i := n
		mu.Unlock()
		switch i {
		case 1:
			answer(w, 200, request("running"))
		case 2:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(502)
			fmt.Fprint(w, "<html>Bad Gateway</html>")
		case 3, 4, 5:
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		default:
			answer(w, 200, request("succeeded"))
		}
	})
	code, out, stderr := f.run("requests", "wait", "REQ-1")
	if code != 0 || !strings.Contains(out, "succeeded") {
		t.Fatalf("a request that succeeded through a 502 = %d\n%s\n%s", code, out, stderr)
	}
	mustHave(t, stderr, "REQ-1: rvav answered 502 Bad Gateway; trying again", "REQ-1 succeeded")
	if strings.Count(stderr, "trying again") < 2 || !strings.Contains(stderr, "EOF; trying again") {
		t.Fatalf("retries said:\n%s", stderr)
	}

	gone := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		answer(w, 404, `{"title":"Not found","status":404}`)
	})
	if code, _, stderr := gone.run("requests", "wait", "REQ-1"); code != exitFailed || strings.Contains(stderr, "trying again") {
		t.Fatalf("a request rvav doesn't know = %d\n%s", code, stderr)
	}
}

func mustHave(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("no %q in:\n%s", w, got)
		}
	}
}

// TestExitCodes tells scripts outcomes apart: the request, the command
// line, the time given and rvav itself.
func TestExitCodes(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/requests/REQ-1/cancel":
			answer(w, 202, request("cancelling"))
		case "/api/v1/requests/req_1":
			answer(w, 200, request("cancelled"))
		case "/api/v1/requests/REQ-2":
			answer(w, 200, requestAs("req_2", "REQ-2", "failed"))
		case "/api/v1/requests/REQ-3":
			answer(w, 200, requestAs("req_3", "REQ-3", "running"))
		case "/api/v1/requests/REQ-4/cancel":
			answer(w, 202, requestAs("req_4", "REQ-4", "running"))
		case "/api/v1/requests/req_4":
			answer(w, 200, requestAs("req_4", "REQ-4", "succeeded"))
		case "/api/v1/requests/REQ-5":
			answer(w, 404, `{"title":"Not found","status":404,"detail":"It does not exist, or you cannot see it."}`)
		case "/api/v1/requests/REQ-6":
			answer(w, 502, "<html>502 Bad Gateway</html>")
		default:
			answer(w, 401, `{"title":"Unauthorized","status":401}`)
		}
	})
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	for _, c := range []struct {
		name string
		env  map[string]string
		args []string
		want int
		says string
	}{
		{name: "cancel --wait, cancelled", args: []string{"requests", "cancel", "REQ-1", "--wait"}, want: 0},
		{name: "cancel --wait, succeeded anyway", args: []string{"requests", "cancel", "REQ-4", "--wait"}, want: exitFailed,
			says: "REQ-4 succeeded"},
		{name: "a failed request", args: []string{"requests", "wait", "REQ-2"}, want: exitFailed, says: "REQ-2 failed"},
		{name: "unknown flag", args: []string{"requests", "wait", "REQ-2", "--nope"}, want: exitUsage, says: "unknown flag"},
		{name: "unknown command", args: []string{"requests", "nope"}, want: exitUsage},
		{name: "arguments", args: []string{"requests", "wait"}, want: exitUsage},
		{name: "output", args: []string{"requests", "wait", "REQ-2", "-o", "yaml"}, want: exitUsage},
		{name: "required flag", args: []string{"catalog", "request", "rhel"}, want: exitUsage, says: "env"},
		{name: "no comment", args: []string{"approvals", "reject", "apv_1"}, want: exitUsage, says: "--comment"},
		{name: "--limit and --all", args: []string{"requests", "list", "--limit", "5", "--all"}, want: exitUsage},
		{name: "bad --data", args: []string{"requests", "signal", "REQ-1", "go", "--data", "[1]"}, want: exitUsage},
		{name: "timeout", args: []string{"requests", "wait", "REQ-3", "--timeout", "30ms"}, want: exitTimeout,
			says: "REQ-3 hasn't finished after 30ms"},
		{name: "sign-in refused", args: []string{"whoami"}, want: exitRvav, says: "run rvavctl login"},
		{name: "call refused", args: []string{"requests", "show", "REQ-5"}, want: exitFailed, says: "Not found"},
		{name: "5xx", args: []string{"requests", "show", "REQ-6"}, want: exitRvav, says: "502"},
		{name: "no answer", env: map[string]string{"RVAV_URL": closed.URL, "RVAV_TOKEN": "rvav_pat_x"},
			args: []string{"requests", "show", "REQ-1"}, want: exitRvav},
		{name: "not signed in", env: map[string]string{}, args: []string{"whoami"}, want: exitRvav, says: "not signed in"},
		{name: "bad address", env: map[string]string{"RVAV_URL": "rvav.corp.example", "RVAV_TOKEN": "rvav_pat_x"},
			args: []string{"whoami"}, want: exitUsage, says: "not an rvav address"},
	} {
		f.env = map[string]string{"RVAV_URL": f.srv.URL, "RVAV_TOKEN": "rvav_pat_x"}
		if c.env != nil {
			f.env = c.env
		}
		code, _, stderr := f.run(c.args...)
		if code != c.want || !strings.Contains(stderr, c.says) {
			t.Errorf("%s: exit %d, want %d (%q)\n%s", c.name, code, c.want, c.says, stderr)
		}
	}
}

// TestInsecureHTTP sends no token to an http:// rvav on another machine,
// unless --insecure-http says to; one on this machine is fine.
func TestInsecureHTTP(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	sent := func() int { mu.Lock(); defer mu.Unlock(); return len(auth) }
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		answer(w, 200, request("succeeded"))
	})
	// rvav.corp.example is the fake rvav, as a proxy would reach it.
	addr := f.srv.Listener.Addr().String()
	f.http = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}}
	f.env["RVAV_URL"] = "http://rvav.corp.example"
	code, _, stderr := f.run("requests", "show", "REQ-1")
	if code != exitUsage || sent() != 0 {
		t.Fatalf("http:// elsewhere = %d, sent %d\n%s", code, sent(), stderr)
	}
	mustHave(t, stderr, "unencrypted", "--insecure-http")
	if code, _, stderr := f.run("requests", "wait", "REQ-1", "--insecure-http"); code != 0 || sent() != 1 {
		t.Fatalf("--insecure-http = %d\n%s", code, stderr)
	}

	// Signed in with --insecure-http, the profile remembers it.
	f.env = map[string]string{}
	if code, _, stderr := f.run("login", "--server", "http://rvav.corp.example", "--with-token"); code != exitUsage {
		t.Fatalf("login over http:// = %d\n%s", code, stderr)
	}
	var out, errs bytes.Buffer
	code = Main(context.Background(), []string{"login", "--server", "http://rvav.corp.example", "--with-token", "--insecure-http"},
		Env{In: strings.NewReader("rvav_pat_x\n"), Out: &out, Err: &errs, Getenv: func(string) string { return "" },
			ConfigPath: f.config, HTTP: f.http})
	ps, _ := rvavclient.LoadProfiles(f.config)
	if code != 0 || ps.Profiles["default"] == nil || !ps.Profiles["default"].InsecureHTTP {
		t.Fatalf("login --insecure-http = %d %+v\n%s", code, ps.Profiles["default"], errs.String())
	}
	if code, _, stderr := f.run("requests", "show", "REQ-1"); code != 0 {
		t.Fatalf("the profile's http:// rvav = %d\n%s", code, stderr)
	}
}

// TestListsSayMore says on standard error when a list shows only the first
// of its items, and --all reads them all.
func TestListsSayMore(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		start := 0
		fmt.Sscan(r.URL.Query().Get("cursor"), &start)
		var n int
		fmt.Sscan(r.URL.Query().Get("limit"), &n)
		var items []string
		for i := start; i < start+n && i < 260; i++ {
			items = append(items, strings.Replace(request("running"), `"req_1"`, fmt.Sprintf(`"req_%d"`, i), 1))
		}
		next := "null"
		if start+n < 260 {
			next = fmt.Sprintf(`"%d"`, start+n)
		}
		answer(w, 200, `{"items":[`+strings.Join(items, ",")+`],"next_cursor":`+next+`}`)
	})
	count := func(out string) int {
		var got []json.RawMessage
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return len(got)
	}
	code, out, stderr := f.run("requests", "list", "-o", "json")
	if code != 0 || count(out) != 50 {
		t.Fatalf("requests list = %d\n%s", code, stderr)
	}
	mustHave(t, stderr, "Showing the first 50 requests; there are more")
	code, out, stderr = f.run("requests", "list", "--all", "-o", "json")
	if code != 0 || count(out) != 260 || stderr != "" {
		t.Fatalf("requests list --all = %d, %d requests\n%s", code, count(out), stderr)
	}
	code, out, stderr = f.run("approvals", "list", "--limit", "300", "-o", "json")
	if code != 0 || count(out) != 260 || stderr != "" {
		t.Fatalf("approvals list --limit 300 = %d\n%s", code, stderr)
	}
}

// TestTargetRotatingRefresh has two rvavctl share a profile at a provider
// that rotates refresh tokens and refuses one already spent: the one that
// waits takes the token the other refreshed.
func TestTargetRotatingRefresh(t *testing.T) {
	var mu sync.Mutex
	current, n := "rt-0", 0
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = r.ParseForm()
		if r.PostForm.Get("refresh_token") != current {
			answer(w, 400, `{"error":"invalid_grant","error_description":"Stale token"}`)
			return
		}
		n++
		current = fmt.Sprintf("rt-%d", n)
		// Shorter than oauth2's ten seconds of margin: expired at once.
		answer(w, 200, fmt.Sprintf(`{"access_token":"at-%d","token_type":"Bearer","expires_in":5,"refresh_token":"%s"}`, n, current))
	}))
	t.Cleanup(idp.Close)
	path := filepath.Join(t.TempDir(), "config.json")
	ps, _ := rvavclient.LoadProfiles(path)
	ps.Profiles["default"] = &rvavclient.Profile{Server: "https://rvav.corp.example", OIDC: &rvavclient.OIDCSession{
		Issuer: "https://idp.corp.example", ClientID: "rvav", TokenURL: idp.URL,
		Token: &oauth2.Token{AccessToken: "at-0", RefreshToken: "rt-0", Expiry: time.Now().Add(-time.Minute)}}}
	if err := ps.Save(); err != nil {
		t.Fatal(err)
	}
	source := func() oauth2.TokenSource {
		a := &app{Env: Env{ConfigPath: path, Getenv: func(string) string { return "" }, HTTP: http.DefaultClient}, profile: "default"}
		_, tokens, err := a.target(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return tokens
	}
	wait := source()
	if _, err := wait.Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := source().Token(); err != nil {
		t.Fatal(err)
	}
	if tok, err := wait.Token(); err != nil {
		t.Fatalf("the waiting rvavctl, once the other refreshed: %v", err)
	} else if tok.AccessToken != "at-3" {
		t.Fatalf("the waiting rvavctl's token = %s", tok.AccessToken)
	}
}

// TestLogoutRevokes revokes the refresh token at a provider that offers
// revocation, and says so at one that doesn't.
func TestLogoutRevokes(t *testing.T) {
	var mu sync.Mutex
	var revoked []string
	revocation := true
	var idp *httptest.Server
	idp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			meta := map[string]any{"issuer": idp.URL, "token_endpoint": idp.URL + "/token", "authorization_endpoint": idp.URL + "/auth"}
			if revocation {
				meta["revocation_endpoint"] = idp.URL + "/revoke"
			}
			b, _ := json.Marshal(meta)
			answer(w, 200, string(b))
		case "/revoke":
			_ = r.ParseForm()
			revoked = append(revoked, r.PostForm.Get("token")+" "+r.PostForm.Get("token_type_hint")+" "+r.PostForm.Get("client_id"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(idp.Close)
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	f.env = map[string]string{}
	signIn := func() {
		ps, _ := rvavclient.LoadProfiles(f.config)
		ps.Profiles["default"] = &rvavclient.Profile{Server: f.srv.URL, OIDC: &rvavclient.OIDCSession{Issuer: idp.URL,
			ClientID: "rvavctl", TokenURL: idp.URL + "/token",
			Token: &oauth2.Token{AccessToken: "at-1", RefreshToken: "rt-1", Expiry: time.Now().Add(time.Hour)}}}
		if err := ps.Save(); err != nil {
			t.Fatal(err)
		}
	}
	signIn()
	code, out, stderr := f.run("logout")
	if code != 0 || len(revoked) != 1 || revoked[0] != "rt-1 refresh_token rvavctl" {
		t.Fatalf("logout = %d, revoked %q\n%s", code, revoked, stderr)
	}
	mustHave(t, out, "Signed out of "+f.srv.URL+", and the identity provider revoked the sign-in.")
	if ps, _ := rvavclient.LoadProfiles(f.config); ps.Profiles["default"] != nil {
		t.Fatal("the profile is kept")
	}

	mu.Lock()
	revocation = false
	mu.Unlock()
	signIn()
	code, out, _ = f.run("logout")
	if code != 0 || len(revoked) != 1 {
		t.Fatalf("logout without revocation = %d, revoked %q", code, revoked)
	}
	mustHave(t, out, "offers no revocation, so the sign-in stays valid there until it expires")
}
