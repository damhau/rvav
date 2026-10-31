package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/core"
)

// The local administrator's sign-in (ADR-0030): a session in a cookie,
// beside the provider's bearer tokens.

// signedIn is who the request is: its token when it has an Authorization
// header, otherwise its local session, otherwise what the authenticator
// makes of it (the dev user in dev mode).
func (s *Server) signedIn(r *http.Request) (*auth.Principal, error) {
	if r.Header.Get("Authorization") == "" {
		if id := auth.SessionID(r); id != "" {
			p, err := auth.SessionPrincipal(r.Context(), s.Store, id, time.Now())
			if !errors.Is(err, auth.ErrUnauthenticated) {
				return p, err
			}
		}
	}
	if s.Auth == nil {
		return nil, auth.ErrUnauthenticated
	}
	return s.Auth.Authenticate(r)
}

// sessionMayDo checks what a request signed in with a local session may
// do: a change needs X-Requested-With, which another origin can't send
// without a preflight rvav refuses; until the password is changed, only
// changing it, reading who it is and signing out.
func (s *Server) sessionMayDo(w http.ResponseWriter, r *http.Request, p *auth.Principal) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		if r.Header.Get("X-Requested-With") == "" {
			s.problem(w, r, http.StatusForbidden, "forbidden", "Not allowed",
				"A change made with a session needs the X-Requested-With header, which rvav's web app sends.", nil)
			return false
		}
	}
	if p.MustChangePassword {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me",
			r.Method == http.MethodPost && (r.URL.Path == "/api/v1/auth/password" || r.URL.Path == "/api/v1/auth/sign-out"):
		default:
			s.problem(w, r, http.StatusForbidden, "password-change-required", "Change your password first",
				"The local administrator's password must be changed before anything else.", nil)
			return false
		}
	}
	return true
}

func (s *Server) sessionCookie(id string, maxAge time.Duration) *http.Cookie {
	return &http.Cookie{Name: auth.SessionCookie, Value: id, Path: "/api/", MaxAge: int(maxAge.Seconds()),
		HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode}
}

func (s *Server) SignIn(w http.ResponseWriter, r *http.Request) {
	var body api.SignInRequest
	if !s.decode(w, r, &body) {
		return
	}
	id, mustChange, err := s.Core.SignIn(r.Context(), body.Username, body.Password, time.Now())
	if err != nil {
		s.signInFailed(w, r, err)
		return
	}
	http.SetCookie(w, s.sessionCookie(id, auth.SessionMax))
	writeJSON(w, http.StatusOK, api.SignInResult{PasswordChangeRequired: mustChange})
}

func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var body api.PasswordChange
	if !s.decode(w, r, &body) {
		return
	}
	if err := s.Core.ChangePassword(r.Context(), principal(r), body.CurrentPassword, body.NewPassword, time.Now()); err != nil {
		s.signInFailed(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) SignOut(w http.ResponseWriter, r *http.Request) {
	if err := s.Core.SignOut(r.Context(), principal(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, s.sessionCookie("", -time.Second))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) signInFailed(w http.ResponseWriter, r *http.Request, err error) {
	var tm *core.TooManyAttemptsError
	switch {
	case errors.As(err, &tm):
		secs := int(math.Ceil(tm.RetryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		s.problem(w, r, http.StatusTooManyRequests, "too-many-attempts", "Too many failed attempts",
			"Try again in "+plural(secs, "second")+".", nil)
	case errors.Is(err, core.ErrBadCredentials):
		s.problem(w, r, http.StatusUnauthorized, "unauthenticated", "Sign-in failed", "The username or password is wrong.", nil)
	default:
		s.fail(w, r, err)
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
