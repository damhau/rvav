package core

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
)

// The web app and rvavctl sign in at the identity provider themselves, so
// they report how it went (ADR-0058). Anyone may report a failure: at most
// reportsPerMinute are kept from one address, or of one user's sign-ins,
// and allReportsPerMinute failures in all, in each API process.
const (
	reportsPerMinute    = 10
	allReportsPerMinute = 300
)

// failureReasons says what each stage of a reported failure was, as the
// local administrator's failures say theirs.
var failureReasons = map[api.SignInFailureStage]string{
	api.SignInStageState:    "state mismatch",
	api.SignInStageProvider: "provider refused",
	api.SignInStageToken:    "token exchange failed",
}

// CheckSignInReport refuses a report that isn't valid, before its token is
// checked.
func CheckSignInReport(r api.SignInReport) error {
	var errs []api.FieldError
	if !r.Client.Valid() {
		errs = append(errs, fieldErr("/client", "enum", "web or rvavctl."))
	}
	if !r.Method.Valid() {
		errs = append(errs, fieldErr("/method", "enum", "oidc, device, dev or token."))
	}
	if f := r.Failure; f != nil {
		if !f.Stage.Valid() {
			errs = append(errs, fieldErr("/failure/stage", "enum", "state, provider or token."))
		}
		if f.Error != nil && (utf8.RuneCountInString(*f.Error) > 64 || strings.IndexFunc(*f.Error, notPrintable) >= 0) {
			errs = append(errs, fieldErr("/failure/error", "invalid", "The provider's error code: at most 64 printable characters."))
		}
		if f.Description != nil && (utf8.RuneCountInString(*f.Description) > 500 || strings.IndexFunc(*f.Description, unicode.IsControl) >= 0) {
			errs = append(errs, fieldErr("/failure/description", "invalid", "At most 500 characters, on one line."))
		}
	}
	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

func notPrintable(r rune) bool { return r < 0x20 || r > 0x7e }

// SignInFailed records a sign-in that failed before there was a token, as
// the client reports it.
func (s *Service) SignInFailed(ctx context.Context, r api.SignInReport) error {
	if err := CheckSignInReport(r); err != nil {
		return err
	}
	if r.Failure == nil {
		return &ValidationError{Errors: []api.FieldError{fieldErr("/failure", "required", "Say why the sign-in failed.")}}
	}
	if !s.mayRecordFailure(ctx) {
		return nil
	}
	f := r.Failure
	detail := map[string]any{"client": r.Client, "method": r.Method, "stage": f.Stage, "reason": failureReasons[f.Stage],
		"reported_by": "client"}
	if f.Error != nil && *f.Error != "" {
		detail["error"] = *f.Error
	}
	if f.Description != nil && *f.Description != "" {
		detail["description"] = *f.Description
	}
	s.auditSignIn(ctx, nil, "auth.sign_in_failed", "", detail)
	return nil
}

// TokenRefused records the token a client signed in with that rvav refused.
func (s *Service) TokenRefused(ctx context.Context, r api.SignInReport, why error) error {
	if err := CheckSignInReport(r); err != nil {
		return err
	}
	if !s.mayRecordFailure(ctx) {
		return nil
	}
	// The verifier's reason, never the token.
	msg := strings.TrimPrefix(why.Error(), auth.ErrUnauthenticated.Error())
	msg = strings.TrimSpace(strings.TrimPrefix(msg, ":"))
	detail := map[string]any{"client": r.Client, "method": r.Method, "reason": "token refused"}
	if msg != "" {
		detail["error"] = clipName(msg)
	}
	s.auditSignIn(ctx, nil, "auth.sign_in_failed", "", detail)
	return nil
}

// SignedIn records a sign-in at the identity provider, by the principal of
// the token the client got.
func (s *Service) SignedIn(ctx context.Context, p *auth.Principal, r api.SignInReport) error {
	if err := CheckSignInReport(r); err != nil {
		return err
	}
	if r.Failure != nil {
		return &ValidationError{Errors: []api.FieldError{fieldErr("/failure", "with_token",
			"A failed sign-in has no token: send the report without one.")}}
	}
	if p.Session != "" {
		return &ValidationError{Errors: []api.FieldError{fieldErr("/method", "local",
			"The local administrator's sign-ins are recorded as they happen.")}}
	}
	if !s.signInReports.allow("user:"+p.User.ID, reportsPerMinute, time.Now()) {
		return nil
	}
	method := p.Method
	if method == "oidc" && r.Method == api.SignInMethodDevice {
		method = string(api.SignInMethodDevice)
	}
	groups := p.Groups
	if groups == nil {
		groups = []string{}
	}
	s.auditSignIn(ctx, &p.User.ID, "auth.sign_in", p.User.ID, map[string]any{"client": r.Client, "method": method,
		"roles": p.Roles, "groups": groups})
	return nil
}

func (s *Service) mayRecordFailure(ctx context.Context) bool {
	now := time.Now()
	return s.signInReports.allow("ip:"+auth.ClientFrom(ctx).IP, reportsPerMinute, now) &&
		s.signInReports.allow("", allReportsPerMinute, now)
}

// windowLimit allows so many events a minute for each key, in this process.
type windowLimit struct {
	mu   sync.Mutex
	seen map[string]window
}

type window struct {
	start time.Time
	n     int
}

func (l *windowLimit) allow(key string, n int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]window{}
	}
	if len(l.seen) > 10000 {
		for k, w := range l.seen {
			if now.Sub(w.start) >= time.Minute {
				delete(l.seen, k)
			}
		}
	}
	w := l.seen[key]
	if now.Sub(w.start) >= time.Minute {
		w = window{start: now}
	}
	if w.n >= n {
		return false
	}
	w.n++
	l.seen[key] = w
	return true
}
