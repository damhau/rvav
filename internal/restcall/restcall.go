// Package restcall is how rvav calls a REST API an administrator named: a
// `rest` value source (ADR-0019) and a composite's REST call step
// (ADR-0033). Only allowed hosts, no redirects, a timeout and a size limit,
// a stored secret as bearer, basic or a header, and nothing of the secret
// left in what the call leaves behind.
package restcall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/hosts"
	"github.com/damhau/rvav/internal/trust"
)

// MaxAnswer is the most of an answer rvav reads.
const MaxAnswer = 1 << 20

// ErrTooLarge: the answer is over MaxAnswer.
var ErrTooLarge = errors.New("its answer is over 1 MB")

// ErrNotAllowed: the URL's host isn't one of the allowed hosts.
var ErrNotAllowed = errors.New("not one of the allowed hosts")

// NewClient gives a client that never follows a redirect (one could take a
// secret to a host that isn't allowed).
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: trust.Transport(trust.Options{}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Caller makes the calls.
type Caller struct {
	// Hosts are the allowed hosts: "cmdb.corp.example", "*.corp.example"
	// (subdomains), "host:8443" (package hosts).
	Hosts []string
	// HostsNow, when set, gives them as saved now (ADR-0030).
	HostsNow func(context.Context) []string
	Client   *http.Client
}

// Allowed says whether a URL's host may be called.
func (c *Caller) Allowed(ctx context.Context, u *url.URL) bool {
	if c.HostsNow != nil {
		return hosts.Allowed(c.HostsNow(ctx), u)
	}
	return hosts.Allowed(c.Hosts, u)
}

// Credential is a stored secret, opened, for one call.
type Credential struct {
	// Kind is the secret's: token (a bearer token), basic, header.
	Kind     string
	Username string
	Header   string
	Value    string
}

// Sensitive is what must not be left behind of it, as Scrubber's values.
func (c Credential) Sensitive() []string {
	if c.Kind == "basic" {
		return []string{c.Value, c.Username + ":" + c.Value}
	}
	return []string{c.Value}
}

// Request is one call. Body, when set, is JSON.
type Request struct {
	Method string
	URL    string
	Body   []byte
	// Credential authenticates it, when set.
	Credential *Credential
}

// Answer is what came back: only the status and the body, never a header.
type Answer struct {
	Status int
	Body   []byte
}

// Do makes the call, if the host is allowed. An error says nothing of the
// URL (its query may hold anything) and nothing of the answer.
func (c *Caller) Do(ctx context.Context, r Request) (Answer, error) {
	u, err := url.Parse(r.URL)
	if err != nil {
		return Answer{}, errors.New("its URL isn't valid")
	}
	if !c.Allowed(ctx, u) {
		return Answer{}, ErrNotAllowed
	}
	var rd io.Reader
	if r.Body != nil {
		rd = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, rd)
	if err != nil {
		return Answer{}, errors.New("its URL or method isn't valid")
	}
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.Credential != nil {
		Authorize(req, *r.Credential)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return Answer{}, withoutURL(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxAnswer+1))
	switch {
	case err != nil:
		return Answer{}, withoutURL(err)
	case len(raw) > MaxAnswer:
		return Answer{Status: resp.StatusCode}, ErrTooLarge
	}
	return Answer{Status: resp.StatusCode, Body: raw}, nil
}

// withoutURL drops the URL net/http puts in its errors.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Authorize adds the secret to a request, as its kind says.
func Authorize(req *http.Request, c Credential) {
	switch c.Kind {
	case "token":
		req.Header.Set("Authorization", "Bearer "+c.Value)
	case "basic":
		req.SetBasicAuth(c.Username, c.Value)
	case "header":
		req.Header.Set(c.Header, c.Value)
	}
}

// StatusText says an answer's status: its code and the standard's words for
// it, never the server's own (resp.Status is whatever it sent).
func StatusText(code int) string {
	return strings.TrimSpace(fmt.Sprintf("%d %s", code, http.StatusText(code)))
}

// Escape escapes a value for a URL's path or query.
func Escape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

// FixedOrigin says whether a template leaves a URL's scheme, host and port
// alone: expressions may fill its path and query only, so a form or an
// earlier step can't send a request, or a secret, anywhere else.
func FixedOrigin(tpl string) bool {
	ref := strings.Index(tpl, "${")
	if ref < 0 {
		return true
	}
	scheme := strings.Index(tpl, "://")
	if scheme < 0 || ref < scheme+3 {
		return false
	}
	end := strings.IndexAny(tpl[scheme+3:], "/?#")
	return end >= 0 && ref >= scheme+3+end
}
