package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// The local administrator (ADR-0030): the one account whose password rvav
// keeps, for first setup and for when the identity provider is down.
const (
	LocalAdminUserID   = "usr_admin"
	LocalAdminUsername = "admin"
	localAdminSubject  = "local:admin"
	localAdminName     = "Local administrator"
	minPasswordLength  = 12
	maxPasswordLength  = 256
)

// ErrBadCredentials: the username or the password is wrong (HTTP 401).
var ErrBadCredentials = errors.New("the username or password is wrong")

// TooManyAttemptsError: sign-in waits after failures in a row (HTTP 429).
type TooManyAttemptsError struct{ RetryAfter time.Duration }

func (e *TooManyAttemptsError) Error() string {
	return fmt.Sprintf("too many failed attempts: try again in %s", e.RetryAfter.Round(time.Second))
}

// EnsureLocalAdmin makes the local administrator at first start, with
// RVAV_ADMIN_PASSWORD or a generated password. It returns whether this
// call made the account, and the password when rvav generated it, to be
// logged once.
func (s *Service) EnsureLocalAdmin(ctx context.Context) (created bool, generated string, err error) {
	if _, err := s.Store.LocalAccountOf(ctx, LocalAdminUserID); err == nil {
		return false, "", nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, "", err
	}
	password := s.Config.Auth.AdminPassword
	if password == "" {
		generated = auth.GeneratePassword()
		password = generated
	}
	hash, err := auth.HashPassword(ctx, password)
	if err != nil {
		return false, "", err
	}
	created, err = s.Store.CreateLocalAccount(ctx, LocalAdminUserID, localAdminSubject, localAdminName, LocalAdminUsername,
		hash, []string{"admin"})
	if err != nil || !created {
		return false, "", err
	}
	s.audit(ctx, system(), "auth.local_admin_created", "user", LocalAdminUserID, nil,
		map[string]any{"password": map[bool]string{true: "generated", false: "RVAV_ADMIN_PASSWORD"}[generated != ""]})
	return true, generated, nil
}

// SignIn checks the local administrator's username and password and starts
// a session, returning its ID (for the cookie) and whether the password
// must be changed first. From the third failure in a row, attempts wait.
// An unknown username is refused at once: the one account's name, admin,
// is no secret, and checking a password against nothing would only take a
// hash slot the administrator's sign-in waits for.
func (s *Service) SignIn(ctx context.Context, username, password string, now time.Time) (sessionID string, mustChange bool, err error) {
	name := strings.TrimSpace(username)
	if utf8.RuneCountInString(name) > 64 || utf8.RuneCountInString(password) > maxPasswordLength {
		return "", false, ErrBadCredentials
	}
	acc, err := s.Store.LocalAccountByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		s.auditSignIn(ctx, nil, "auth.sign_in_failed", "", map[string]any{"username": clipName(name), "reason": "unknown username"})
		return "", false, ErrBadCredentials
	}
	if err != nil {
		return "", false, err
	}
	acc, right, err := s.attempt(ctx, acc.UserID, password, now)
	if err != nil {
		return "", false, err
	}
	if !right {
		s.auditSignIn(ctx, &acc.UserID, "auth.sign_in_failed", acc.UserID,
			map[string]any{"reason": "wrong password", "failures_in_a_row": acc.FailedAttempts})
		return "", false, ErrBadCredentials
	}
	if err := s.Store.LocalSignInSucceeded(ctx, acc.UserID); err != nil {
		return "", false, err
	}
	id := auth.NewSessionID()
	if err := s.Store.CreateSession(ctx, auth.HashToken(id), acc.UserID, now, now.Add(auth.SessionMax), auth.SessionIdle); err != nil {
		return "", false, err
	}
	s.auditSignIn(ctx, &acc.UserID, "auth.sign_in", acc.UserID, map[string]any{"password_change_required": acc.MustChange})
	return id, acc.MustChange, nil
}

// attempt checks a password of a local account, counted as a failure
// before it is checked (store.LocalAttempt) and the count started again if
// it is right: attempts at once can't pass the wait together. While the
// account waits, the password isn't checked: TooManyAttemptsError.
func (s *Service) attempt(ctx context.Context, userID, password string, now time.Time) (store.LocalAccount, bool, error) {
	acc, ok, err := s.Store.LocalAttempt(ctx, userID, now)
	if err != nil {
		return acc, false, err
	}
	if !ok {
		wait := time.Second
		if acc.LockedUntil != nil && acc.LockedUntil.After(now) {
			wait = acc.LockedUntil.Sub(now)
		}
		return acc, false, &TooManyAttemptsError{RetryAfter: wait}
	}
	right, err := auth.CheckPassword(ctx, password, acc.PasswordHash)
	if err != nil || !right {
		return acc, false, err
	}
	return acc, true, s.Store.LocalAttemptRight(ctx, userID)
}

// ChangePassword replaces the local administrator's password, checking the
// current one, and ends the account's other sessions and its API tokens.
// A wrong current password counts as a failed sign-in.
func (s *Service) ChangePassword(ctx context.Context, p *auth.Principal, current, next string, now time.Time) error {
	if p.Session == "" {
		return fmt.Errorf("%w: only the local administrator has a password in rvav; change yours at the identity provider",
			ErrForbidden)
	}
	acc, err := s.Store.LocalAccountOf(ctx, p.User.ID)
	if err != nil {
		return err
	}
	// Checked first, so that a new password refused isn't a failure.
	if errs := checkNewPassword(acc.Username, current, next); len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	acc, right, err := s.attempt(ctx, acc.UserID, current, now)
	if err != nil {
		return err
	}
	if !right {
		s.auditSignIn(ctx, &acc.UserID, "auth.sign_in_failed", acc.UserID,
			map[string]any{"reason": "wrong current password", "failures_in_a_row": acc.FailedAttempts})
		return &ValidationError{Errors: []api.FieldError{fieldErr("/current_password", "wrong", "The current password is wrong.")}}
	}
	hash, err := auth.HashPassword(ctx, next)
	if err != nil {
		return err
	}
	if err := s.Store.SetLocalPassword(ctx, acc.UserID, hash, false); err != nil {
		return err
	}
	ended, err := s.Store.DeleteSessions(ctx, acc.UserID, p.Session)
	if err != nil {
		return err
	}
	revoked, err := s.Store.RevokeUserAPITokens(ctx, acc.UserID)
	if err != nil {
		return err
	}
	s.auditSignIn(ctx, &acc.UserID, "auth.password_changed", acc.UserID,
		map[string]any{"sessions_ended": ended, "tokens_revoked": revoked})
	return nil
}

// checkNewPassword: at least 12 characters, other than the current
// password and the username.
func checkNewPassword(username, current, next string) []api.FieldError {
	switch {
	case utf8.RuneCountInString(next) < minPasswordLength:
		return []api.FieldError{fieldErr("/new_password", "too_short",
			fmt.Sprintf("Use at least %d characters.", minPasswordLength))}
	case utf8.RuneCountInString(next) > maxPasswordLength:
		return []api.FieldError{fieldErr("/new_password", "too_long",
			fmt.Sprintf("Use at most %d characters.", maxPasswordLength))}
	case next == current:
		return []api.FieldError{fieldErr("/new_password", "unchanged", "Choose a password other than the current one.")}
	case strings.EqualFold(strings.TrimSpace(next), username):
		return []api.FieldError{fieldErr("/new_password", "username", "Choose a password other than the username.")}
	}
	return nil
}

// SignOut ends a local session (its ID's hash).
func (s *Service) SignOut(ctx context.Context, p *auth.Principal) error {
	if p == nil || p.Session == "" {
		return nil
	}
	if err := s.Store.DeleteSession(ctx, p.Session); err != nil {
		return err
	}
	s.auditSignIn(ctx, &p.User.ID, "auth.sign_out", p.User.ID, nil)
	return nil
}

// clipName keeps an unknown username short in the audit log.
func clipName(name string) string {
	if r := []rune(name); len(r) > 64 {
		return string(r[:64])
	}
	return name
}

func (s *Service) auditSignIn(ctx context.Context, actor *string, action, userID string, detail map[string]any) {
	if err := s.Store.Audit(ctx, s.Store.Pool, store.AuditEvent{ActorID: actor, Action: action, TargetType: "user",
		TargetID: userID, Detail: detail}); err != nil {
		s.Log.Error("audit", "action", action, "err", err)
	}
}

// ResetLocalAdmin sets the local administrator's password (generated when
// password is empty, and returned), to be changed at the next sign-in, and
// ends its sessions and revokes its API tokens: `rvav admin
// reset-password`, for when someone else may know the password. It makes
// the account if there is none.
func ResetLocalAdmin(ctx context.Context, st *store.Store, password string) (string, error) {
	generated := ""
	if password == "" {
		generated = auth.GeneratePassword()
		password = generated
	} else if n := utf8.RuneCountInString(password); n < minPasswordLength || n > maxPasswordLength {
		return "", fmt.Errorf("the password needs %d to %d characters", minPasswordLength, maxPasswordLength)
	}
	hash, err := auth.HashPassword(ctx, password)
	if err != nil {
		return "", err
	}
	if _, err := st.CreateLocalAccount(ctx, LocalAdminUserID, localAdminSubject, localAdminName, LocalAdminUsername,
		hash, []string{"admin"}); err != nil {
		return "", err
	}
	if err := st.SetLocalPassword(ctx, LocalAdminUserID, hash, true); err != nil {
		return "", err
	}
	ended, err := st.DeleteSessions(ctx, LocalAdminUserID, "")
	if err != nil {
		return "", err
	}
	revoked, err := st.RevokeUserAPITokens(ctx, LocalAdminUserID)
	if err != nil {
		return "", err
	}
	id := SystemUserID
	if err := st.Audit(ctx, st.Pool, store.AuditEvent{ActorID: &id, Action: "auth.password_reset", TargetType: "user",
		TargetID: LocalAdminUserID, Detail: map[string]any{"by": "rvav admin reset-password", "sessions_ended": ended,
			"tokens_revoked": revoked}}); err != nil {
		return "", err
	}
	return generated, nil
}
