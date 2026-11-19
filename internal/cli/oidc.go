package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/damhau/rvav/pkg/rvavclient"
)

// signIn signs in at rvav's identity provider with the client the web app
// uses, in the browser (authorization code with PKCE, RFC 8252) or on
// another device (RFC 8628).
func (a *app) signIn(ctx context.Context, p rvavclient.AuthProvider, f loginFlags) (*rvavclient.OIDCSession, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, a.HTTP)
	provider, err := oidc.NewProvider(ctx, p.Authority)
	if err != nil {
		return nil, fmt.Errorf("reaching rvav's identity provider at %s: %w", p.Authority, err)
	}
	var meta struct {
		Scopes []string `json:"scopes_supported"`
	}
	_ = provider.Claims(&meta)
	scopes := strings.Fields(p.Scope)
	// A refresh token that outlives the provider's session, where it has
	// them: Entra ID gives none without it.
	if slices.Contains(meta.Scopes, "offline_access") && !slices.Contains(scopes, "offline_access") {
		scopes = append(scopes, "offline_access")
	}
	cfg := oauth2.Config{ClientID: p.ClientId, Endpoint: provider.Endpoint(), Scopes: scopes}
	cfg.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	if f.clientID != "" {
		cfg.ClientID = f.clientID
	}
	var t *oauth2.Token
	if f.device {
		t, err = a.deviceSignIn(ctx, cfg)
	} else {
		t, err = a.browserSignIn(ctx, cfg, f.callbackPort)
	}
	if err != nil {
		return nil, err
	}
	return &rvavclient.OIDCSession{Issuer: p.Authority, ClientID: cfg.ClientID, TokenURL: cfg.Endpoint.TokenURL,
		Scopes: scopes, Token: t}, nil
}

func (a *app) deviceSignIn(ctx context.Context, cfg oauth2.Config) (*oauth2.Token, error) {
	if cfg.Endpoint.DeviceAuthURL == "" {
		return nil, errors.New("rvav's identity provider offers no sign-in on another device: sign in in the browser, without --device")
	}
	da, err := cfg.DeviceAuth(ctx)
	if err != nil {
		return nil, refused(rvavclient.SignInStageProvider, fmt.Errorf("asking the identity provider for a code: %w", err))
	}
	if da.VerificationURIComplete != "" {
		fmt.Fprintf(a.Err, "To sign in, open %s\nand check that it shows the code %s.\n", da.VerificationURIComplete, da.UserCode)
	} else {
		fmt.Fprintf(a.Err, "To sign in, open %s\nand enter the code %s.\n", da.VerificationURI, da.UserCode)
	}
	t, err := cfg.DeviceAccessToken(ctx, da)
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.ErrorCode == "access_denied" {
			return nil, refused(rvavclient.SignInStageProvider, fmt.Errorf("signing in: %w", err))
		}
		return nil, refused(rvavclient.SignInStageToken, fmt.Errorf("signing in: %w", err))
	}
	return t, nil
}

func (a *app) browserSignIn(ctx context.Context, cfg oauth2.Config, port int) (*oauth2.Token, error) {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, fmt.Errorf("listening for the browser on 127.0.0.1: %w", err)
	}
	defer l.Close()
	cfg.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", l.Addr().(*net.TCPAddr).Port)
	state, verifier := random(), oauth2.GenerateVerifier()

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("state") != state:
			res.err = refused(rvavclient.SignInStageState, errors.New("the browser came back from another sign-in: try again"))
		case q.Get("error") != "":
			res.err = &signInError{stage: rvavclient.SignInStageProvider, code: q.Get("error"), description: q.Get("error_description"),
				err: fmt.Errorf("the identity provider refused: %s %s", q.Get("error"), q.Get("error_description"))}
		case q.Get("code") == "":
			res.err = refused(rvavclient.SignInStageProvider, errors.New("the identity provider sent no code"))
		default:
			res.code = q.Get("code")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := "Signed in to rvav. You can close this tab and go back to the terminal."
		if res.err != nil {
			page = "rvavctl could not sign in: " + res.err.Error()
		}
		fmt.Fprintf(w, "<!doctype html><title>rvavctl</title><p>%s</p>\n", html.EscapeString(page))
		select {
		case done <- res:
		default:
		}
	})}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()

	url := cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	fmt.Fprintf(a.Err, "Signing in in your browser. If it doesn't open, open this address:\n%s\n", url)
	if a.OpenBrowser != nil {
		_ = a.OpenBrowser(url)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	select {
	case <-wait.Done():
		return nil, errors.New("no sign-in from the browser within 5 minutes: try again, or use --device")
	case res := <-done:
		if res.err != nil {
			return nil, res.err
		}
		t, err := cfg.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
		if err != nil {
			return nil, refused(rvavclient.SignInStageToken, fmt.Errorf("signing in: %w", err))
		}
		return t, nil
	}
}

// signInError is a sign-in that failed at the identity provider or on the
// way back from it, which rvav's audit log records (ADR-0058).
type signInError struct {
	stage             rvavclient.SignInFailureStage
	code, description string
	err               error
}

func (e *signInError) Error() string { return e.err.Error() }
func (e *signInError) Unwrap() error { return e.err }

// refused is err at stage, with the provider's error code and description
// when it gave them.
func refused(stage rvavclient.SignInFailureStage, err error) error {
	e := &signInError{stage: stage, description: err.Error(), err: err}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.ErrorCode != "" {
		e.code, e.description = re.ErrorCode, re.ErrorDescription
	}
	return e
}

// failure is what rvav is told of e: the provider's code and description,
// or rvavctl's message, never a code or a token.
func (e *signInError) failure() *rvavclient.SignInFailure {
	f := &rvavclient.SignInFailure{Stage: e.stage}
	if c := printable(e.code, 64); c != "" {
		f.Error = &c
	}
	if d := oneLine(e.description, 500); d != "" {
		f.Description = &d
	}
	return f
}

func printable(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return -1
		}
		return r
	}, s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.FieldsFunc(s, unicode.IsControl), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

func random() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}
