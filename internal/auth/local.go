package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/store"
)

// The local administrator's sign-in (ADR-0030): a password kept as an
// argon2id hash (PBKDF2 in FIPS 140 mode, ADR-0060), and a server-side
// session in a cookie.

// SessionCookie holds a local session's ID.
const SessionCookie = "rvav_session"

// Session lifetimes: ended after SessionIdle without a request, and
// SessionMax after sign-in in any case.
const (
	SessionIdle = 30 * time.Minute
	SessionMax  = 12 * time.Hour
)

// passwordParams: 64 MiB, one pass, two lanes, a 16-byte salt and a
// 32-byte key, whatever the machine (the library's defaults take a lane per
// CPU). A hash keeps its parameters, so changing them later still checks
// the old ones.
var passwordParams = &argon2id.Params{Memory: 64 * 1024, Iterations: 1, Parallelism: 2, SaltLength: 16, KeyLength: 32}

// hashSlots bounds the hashes computed at once: each takes 64 MiB, and
// sign-in attempts in parallel would otherwise take as much as they like.
var hashSlots = make(chan struct{}, 4)

// slot waits for a hash slot, as long as the request does: one whose
// client has gone gives up its place.
func slot(ctx context.Context) (release func(), err error) {
	select {
	case hashSlots <- struct{}{}:
		return func() { <-hashSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// HoldHashSlots takes every hash slot until release is called: for tests
// of what waits for one.
func HoldHashSlots() (release func()) {
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}
	return func() {
		for range cap(hashSlots) {
			<-hashSlots
		}
	}
}

// pbkdf2Iterations is OWASP's count for PBKDF2-HMAC-SHA-512, the hash
// FIPS 140 approves for passwords where argon2id isn't (ADR-0060).
const pbkdf2Iterations = 210_000

const pbkdf2Prefix = "$pbkdf2-sha512$"

// ErrArgon2idRefused: FIPS 140-only mode doesn't check argon2id hashes.
var ErrArgon2idRefused = &fips.NotAllowedError{What: "The local administrator's password hash", Algorithm: "argon2id",
	Instead: "set a new password with rvav admin reset-password, or sign in once with RVAV_FIPS=on, which hashes it again with PBKDF2"}

// HashPassword is a password's hash: argon2id in the PHC string format,
// or PBKDF2-HMAC-SHA-512 in FIPS 140 mode.
func HashPassword(ctx context.Context, password string) (string, error) {
	release, err := slot(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	if fips.Current() != fips.Off {
		return hashPBKDF2(password, pbkdf2Iterations)
	}
	return argon2id.CreateHash(password, passwordParams)
}

// CheckPassword compares a password with its hash in constant time; a
// malformed hash never matches. It fails when ctx ends before a slot is
// free, and with ErrArgon2idRefused (CheckHash).
func CheckPassword(ctx context.Context, password, hash string) (bool, error) {
	if err := CheckHash(hash); err != nil {
		return false, err
	}
	release, err := slot(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	if strings.HasPrefix(hash, pbkdf2Prefix) {
		return checkPBKDF2(password, hash), nil
	}
	ok, err := argon2id.ComparePasswordAndHash(password, hash)
	return err == nil && ok, nil
}

// CheckHash says whether this process checks passwords against a hash:
// FIPS 140-only mode refuses argon2id's.
func CheckHash(hash string) error {
	if fips.Current() == fips.Only && strings.HasPrefix(hash, "$argon2id$") {
		return ErrArgon2idRefused
	}
	return nil
}

// NeedsRehash says whether a hash that matched should be made again: an
// argon2id hash in FIPS 140 mode.
func NeedsRehash(hash string) bool {
	return fips.Current() != fips.Off && !strings.HasPrefix(hash, pbkdf2Prefix)
}

func hashPBKDF2(password string, iterations int) (string, error) {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	dk, err := pbkdf2.Key(sha512.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("%si=%d$%s$%s", pbkdf2Prefix, iterations, b64(salt), b64(dk)), nil
}

func checkPBKDF2(password, hash string) bool {
	parts := strings.Split(strings.TrimPrefix(hash, pbkdf2Prefix), "$")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "i=") {
		return false
	}
	iterations, err := strconv.Atoi(parts[0][2:])
	if err != nil || iterations < 1 || iterations > 10*pbkdf2Iterations {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[1])
	dk, err2 := base64.RawStdEncoding.DecodeString(parts[2])
	if err1 != nil || err2 != nil || len(salt) < 16 || len(dk) != 32 {
		return false
	}
	got, err := pbkdf2.Key(sha512.New, password, salt, iterations, len(dk))
	return err == nil && subtle.ConstantTimeCompare(got, dk) == 1
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
	p.Session, p.MustChangePassword, p.Method = hash, acc.MustChange, "local"
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
