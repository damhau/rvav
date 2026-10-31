package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/alexedwards/argon2id"

	"github.com/damhau/rvav/internal/store"
)

// The local administrator's sign-in (ADR-0030): a password kept as an
// argon2id hash, and a server-side session in a cookie.

// SessionCookie holds a local session's ID.
const SessionCookie = "rvav_session"

// Session lifetimes: ended after SessionIdle without a request, and
// SessionMax after sign-in in any case.
const (
	SessionIdle = 30 * time.Minute
	SessionMax  = 12 * time.Hour
)

// passwordParams: 64 MiB, one pass, two lanes, a 16-byte salt.
var passwordParams = argon2id.DefaultParams

// hashSlots bounds the hashes computed at once: each takes 64 MiB, and
// sign-in attempts in parallel would otherwise take as much as they like.
var hashSlots = make(chan struct{}, 4)

// HashPassword is a password's argon2id hash, in the PHC string format.
func HashPassword(password string) (string, error) {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2id.CreateHash(password, passwordParams)
}

// CheckPassword compares a password with its hash in constant time; a
// malformed hash never matches.
func CheckPassword(password, hash string) bool {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	ok, err := argon2id.ComparePasswordAndHash(password, hash)
	return err == nil && ok
}

// passwordAlphabet leaves out characters read alike (0 and O, 1, l and I).
const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword is a random password of 24 characters (about 140 bits).
func GeneratePassword() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	out := make([]byte, len(b))
	for i, c := range b {
		// 256 is not a multiple of the alphabet's length: reject the bytes
		// that would favour its first characters.
		for int(c) >= 256-256%len(passwordAlphabet) {
			var one [1]byte
			_, _ = rand.Read(one[:])
			c = one[0]
		}
		out[i] = passwordAlphabet[int(c)%len(passwordAlphabet)]
	}
	return string(out)
}

// NewSessionID is a session's ID: 32 random bytes.
func NewSessionID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// SessionPrincipal is who a local session is, and whether they must change
// their password first. It returns ErrUnauthenticated for a session that
// ended or never was.
func SessionPrincipal(ctx context.Context, s *store.Store, id string, now time.Time) (*Principal, error) {
	hash := HashToken(id)
	userID, err := s.Session(ctx, hash, now, SessionIdle)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	acc, err := s.LocalAccountOf(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	p, err := build(ctx, s, u, nil)
	if err != nil {
		return nil, err
	}
	p.Session, p.MustChangePassword = hash, acc.MustChange
	return p, nil
}

// SessionID is the local session's ID a request carries, if any.
func SessionID(r *http.Request) string {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}
