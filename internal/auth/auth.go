// Package auth turns a bearer token into a Principal: the user, their global
// roles and their role in each project.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/damhau/rvav/internal/store"
)

var ErrUnauthenticated = errors.New("unauthenticated")

// ErrProviderUnavailable means the identity provider could not be reached to
// check a token, for example while it is still starting.
var ErrProviderUnavailable = errors.New("identity provider unavailable")

// Principal is who is calling and what they may do.
type Principal struct {
	User        store.User
	GlobalRoles []string
	// ProjectRoles maps project ID to the caller's strongest role there.
	ProjectRoles map[string]string
	// Groups are the caller's OIDC groups (approvers may be a group).
	Groups []string
	// Scope limits an API token to one use (ScopeInventory); "" otherwise.
	Scope string
}

// ScopeInventory: the token only reads the Ansible inventory.
const ScopeInventory = "inventory"

func (p *Principal) HasGlobal(role string) bool { return slices.Contains(p.GlobalRoles, role) }

func (p *Principal) IsAdmin() bool { return p.HasGlobal("admin") }

// SeesEverything is true for administrators and auditors.
func (p *Principal) SeesEverything() bool { return p.IsAdmin() || p.HasGlobal("auditor") }

// ProjectRole returns the caller's role in a project ("" when none).
// Administrators act as project admins everywhere.
// An administrator who is also a member or viewer of a project is still its
// project admin.
func (p *Principal) ProjectRole(projectID string) string {
	if p.IsAdmin() {
		return "project-admin"
	}
	return p.ProjectRoles[projectID]
}

func (p *Principal) CanSeeProject(projectID string) bool {
	return p.SeesEverything() || p.ProjectRoles[projectID] != ""
}

// CanRequest is true for members and project admins (viewers can only look).
func (p *Principal) CanRequest(projectID string) bool {
	r := p.ProjectRole(projectID)
	return r == "member" || r == "project-admin"
}

// VisibleProjects lists the projects the caller can see; nil means all.
func (p *Principal) VisibleProjects() []string {
	if p.SeesEverything() {
		return nil
	}
	out := make([]string, 0, len(p.ProjectRoles))
	for id := range p.ProjectRoles {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

var roleRank = map[string]int{"viewer": 1, "member": 2, "project-admin": 3}

// ForUser builds the principal of a stored user with the groups seen in their
// last token, for acting on their behalf (re-evaluating their request).
func ForUser(ctx context.Context, s *store.Store, userID string) (*Principal, error) {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	groups, err := s.UserGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	return build(ctx, s, u, groups)
}

// build combines a user's own roles with those granted by OIDC groups.
func build(ctx context.Context, s *store.Store, u store.User, groups []string) (*Principal, error) {
	p := &Principal{User: u, GlobalRoles: append([]string{}, u.GlobalRoles...), ProjectRoles: map[string]string{}, Groups: groups}
	members, err := s.Memberships(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	global, fromGroups, err := s.GroupRoles(ctx, groups)
	if err != nil {
		return nil, err
	}
	for _, g := range global {
		if !slices.Contains(p.GlobalRoles, g) {
			p.GlobalRoles = append(p.GlobalRoles, g)
		}
	}
	for _, m := range append(members, fromGroups...) {
		if roleRank[m.Role] > roleRank[p.ProjectRoles[m.ProjectID]] {
			p.ProjectRoles[m.ProjectID] = m.Role
		}
	}
	return p, nil
}

// Authenticator resolves the principal of an HTTP request.
type Authenticator interface {
	Authenticate(r *http.Request) (*Principal, error)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// TokenPrefix marks rvav API tokens.
const TokenPrefix = "rvav_pat_"

// NewToken makes a token value: the prefix and 32 random bytes.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func apiToken(ctx context.Context, s *store.Store, token string) (*Principal, error) {
	u, scope, err := s.UserForToken(ctx, HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	// No roles from groups: those stored from the last sign-in may be stale,
	// and automation should not approve anything.
	p, err := build(ctx, s, u, nil)
	if p != nil {
		p.Scope = scope
	}
	return p, err
}

// OIDC validates access tokens issued by the organisation's identity provider.
type OIDC struct {
	Store       *store.Store
	GroupsClaim string
	// RetryInterval is how long a failed discovery is remembered before the
	// next request tries again (default 5 s); requests meanwhile answer 503
	// at once instead of queueing.
	RetryInterval time.Duration

	issuer, discoveryURL, clientID string

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
	failedAt time.Time
	lastErr  error
}

// NewOIDC prepares token checks against the provider. discoveryURL, when set,
// is where the API reaches the provider when that differs from the issuer in
// tokens (for example http://keycloak:8080 inside docker compose versus
// http://localhost:8081 in the browser).
//
// The provider is discovered on first use and again after a failure, so the
// API starts, and recovers, while the provider is down. The error says
// whether it was reachable now; the API runs either way.
func NewOIDC(ctx context.Context, s *store.Store, issuer, discoveryURL, clientID, groupsClaim string) (*OIDC, error) {
	if discoveryURL == "" {
		discoveryURL = issuer
	}
	o := &OIDC{Store: s, GroupsClaim: groupsClaim, issuer: issuer, discoveryURL: discoveryURL, clientID: clientID}
	_, err := o.verifierFor(ctx)
	return o, err
}

// Ready reports whether the provider has been discovered. The API's
// readiness check uses it, so a wrong issuer or a provider that never comes
// up keeps new instances out of service instead of answering 503.
func (o *OIDC) Ready(ctx context.Context) error {
	_, err := o.verifierFor(ctx)
	return err
}

func (o *OIDC) verifierFor(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.verifier != nil {
		return o.verifier, nil
	}
	retry := o.RetryInterval
	if retry == 0 {
		retry = 5 * time.Second
	}
	if o.lastErr != nil && time.Since(o.failedAt) < retry {
		return nil, o.lastErr
	}
	if o.discoveryURL != o.issuer {
		ctx = oidc.InsecureIssuerURLContext(ctx, o.issuer)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, o.discoveryURL)
	if err != nil {
		o.failedAt, o.lastErr = time.Now(), fmt.Errorf("%w: discovery for %s: %v", ErrProviderUnavailable, o.issuer, err)
		return nil, o.lastErr
	}
	var meta struct {
		JWKSURL string   `json:"jwks_uri"`
		Algs    []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := provider.Claims(&meta); err != nil {
		o.failedAt, o.lastErr = time.Now(), fmt.Errorf("%w: discovery for %s: %v", ErrProviderUnavailable, o.issuer, err)
		return nil, o.lastErr
	}
	// Access tokens from Keycloak carry the client in azp, and in aud only
	// with an audience mapper; the realm export in deploy/ adds one.
	keys := keySet{oidc.NewRemoteKeySet(context.Background(), meta.JWKSURL)}
	o.verifier = oidc.NewVerifier(o.issuer, keys, &oidc.Config{ClientID: o.clientID, SupportedSigningAlgs: meta.Algs})
	o.lastErr = nil
	return o.verifier, nil
}

// keySet notes when the provider's keys could not be fetched: go-oidc reports
// that as a plain signature failure, but it means the provider is down (503),
// not that the token is bad (401).
type keySet struct{ remote *oidc.RemoteKeySet }

type fetchErrKey struct{}

func (k keySet) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	payload, err := k.remote.VerifySignature(ctx, jwt)
	if err != nil && strings.HasPrefix(err.Error(), "fetching keys") {
		if seen, ok := ctx.Value(fetchErrKey{}).(*error); ok {
			*seen = err
		}
	}
	return payload, err
}

func (o *OIDC) Authenticate(r *http.Request) (*Principal, error) {
	token := bearer(r)
	if token == "" {
		return nil, ErrUnauthenticated
	}
	ctx := r.Context()
	if strings.HasPrefix(token, TokenPrefix) {
		return apiToken(ctx, o.Store, token)
	}
	v, err := o.verifierFor(ctx)
	if err != nil {
		return nil, err
	}
	var fetchErr error
	idt, err := v.Verify(context.WithValue(ctx, fetchErrKey{}, &fetchErr), token)
	if fetchErr != nil {
		return nil, fmt.Errorf("%w: fetching keys: %v", ErrProviderUnavailable, fetchErr)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	str := func(k string) string { s, _ := claims[k].(string); return s }
	name := str("name")
	if name == "" {
		name = str("preferred_username")
	}
	var groups []string
	if raw, ok := claims[o.GroupsClaim].([]any); ok {
		for _, g := range raw {
			if s, ok := g.(string); ok {
				groups = append(groups, strings.TrimPrefix(s, "/"))
			}
		}
	}
	u, err := o.Store.UpsertOIDCUser(ctx, idt.Subject, name, str("email"), str("title"))
	if err != nil {
		return nil, err
	}
	// Kept so approvals know a group's members before they sign in again.
	if err := o.Store.SetUserGroups(ctx, u.ID, groups); err != nil {
		return nil, err
	}
	return build(ctx, o.Store, u, groups)
}

// Dev trusts a fixed user, or the one named in the X-Rvav-Dev-User header.
// It exists for local development and tests only.
type Dev struct {
	Store       *store.Store
	DefaultUser string
}

// DevUserHeader selects the acting user in dev mode, for example "mkeller".
const DevUserHeader = "X-Rvav-Dev-User"

func (d *Dev) Authenticate(r *http.Request) (*Principal, error) {
	ctx := r.Context()
	if t := bearer(r); strings.HasPrefix(t, TokenPrefix) {
		return apiToken(ctx, d.Store, t)
	}
	name := r.Header.Get(DevUserHeader)
	if name == "" {
		name = d.DefaultUser
	}
	u, err := d.Store.GetUserBySubject(ctx, "seed:"+name)
	if errors.Is(err, store.ErrNotFound) {
		u, err = d.Store.GetUser(ctx, name)
	}
	if errors.Is(err, store.ErrNotFound) {
		// A seeded user linked to an OIDC subject on first sign-in keeps its ID.
		u, err = d.Store.GetUser(ctx, "usr_"+name)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("%w: dev user %q does not exist", ErrUnauthenticated, name)
	}
	if err != nil {
		return nil, err
	}
	groups, err := d.Store.UserGroups(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return build(ctx, d.Store, u, groups)
}

// PrincipalFor is what a user may do, with the groups stored at their last
// sign-in: for previews made on their behalf (the form designer's "preview
// as"), never to act as them.
func PrincipalFor(ctx context.Context, s *store.Store, userID string) (*Principal, error) {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	groups, err := s.UserGroups(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return build(ctx, s, u, groups)
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal stored by the middleware.
func From(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}
