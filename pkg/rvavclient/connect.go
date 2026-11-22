package rvavclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

// APIPath is where an rvav serves its API.
const APIPath = "/api/v1"

// BaseURL is the API's address for an rvav's: https://rvav.corp.example
// and https://rvav.corp.example/api/v1 both give the latter.
func BaseURL(server string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(server))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not an rvav address, such as https://rvav.corp.example", server)
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), APIPath) + APIPath
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// Unencrypted says whether calls to server would cross a network in clear
// text: an http:// address of another machine than this one.
func Unencrypted(server string) bool {
	u, err := url.Parse(strings.TrimSpace(server))
	if err != nil || !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

// StaticToken sends an API token (rvav_pat_…) or an access token as it is;
// none when token is empty.
func StaticToken(token string) oauth2.TokenSource {
	if token == "" {
		return nil
	}
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"})
}

// Connect returns a client of the rvav at server that sends the bearer
// token tokens gives on every call, or none when tokens is nil.
func Connect(server string, tokens oauth2.TokenSource, opts ...ClientOption) (*ClientWithResponses, error) {
	base, err := BaseURL(server)
	if err != nil {
		return nil, err
	}
	if tokens != nil {
		opts = append([]ClientOption{WithRequestEditorFn(Bearer(tokens))}, opts...)
	}
	return NewClientWithResponses(base, opts...)
}

// Bearer sets the Authorization header from tokens, refreshing it first
// when the source does.
func Bearer(tokens oauth2.TokenSource) RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		t, err := tokens.Token()
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+t.AccessToken)
		return nil
	}
}

// UserAgent names the tool calling, as rvav's logs show it.
func UserAgent(agent string) ClientOption {
	return WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		req.Header.Set("User-Agent", agent)
		return nil
	})
}

// Error is a call rvav refused or failed: its status and, when the body
// has them, the API's problem details (RFC 9457).
type Error struct {
	Status  int
	Problem *Problem
}

func (e *Error) Error() string {
	if e.Problem == nil {
		return fmt.Sprintf("rvav answered %d %s", e.Status, http.StatusText(e.Status))
	}
	var b strings.Builder
	b.WriteString(e.Problem.Title)
	if e.Problem.Detail != nil && *e.Problem.Detail != "" && *e.Problem.Detail != e.Problem.Title {
		b.WriteString(": " + *e.Problem.Detail)
	}
	if e.Problem.Errors != nil {
		for _, fe := range *e.Problem.Errors {
			b.WriteString("\n  ")
			if fe.Field != "" {
				b.WriteString(FieldName(fe.Field) + ": ")
			}
			b.WriteString(fe.Message)
		}
	}
	return b.String()
}

// FieldName is a field error's JSON pointer as a person names the field:
// /inputs/role is role, /count is count.
func FieldName(pointer string) string {
	return strings.TrimPrefix(strings.TrimPrefix(pointer, "/inputs/"), "/")
}

// Check returns nil for a 2xx response, else an *Error read from its body.
func Check(resp *http.Response, body []byte) error {
	if resp == nil {
		return errors.New("no response from rvav")
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	e := &Error{Status: resp.StatusCode}
	var p Problem
	if json.Unmarshal(body, &p) == nil && p.Title != "" {
		e.Problem = &p
	}
	return e
}

// IsStatus says whether err is a call rvav answered with status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}
