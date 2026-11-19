// Package auth turns a bearer token into a Principal: the user, the roles
// they hold and what those allow (ADR-0034).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
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
	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/trust"
)

var ErrUnauthenticated = errors.New("unauthenticated")

// ErrProviderUnavailable means the identity provider could not be reached to
// check a token, for example while it is still starting.
var ErrProviderUnavailable = errors.New("identity provider unavailable")

// Principal is who is calling and what they may do.
type Principal struct {
	User store.User
	// Grants are what the principal's roles allow, computed once per
	// request (ADR-0034).
	Grants
	// Roles are the keys of the roles held for all of rvav, user and
	// custom ones included, as policies' CEL names them (user.roles).
	Roles []string
	// rolesIn maps a project to the keys of the roles given in it.
	rolesIn map[string][]string
	// known are the roles held, to name them.
	known map[string]Role
	// Groups are the caller's OIDC groups (approvers may be a group).
	Groups []string
	// Scope limits an API token to one use (ScopeInventory); "" otherwise.
	Scope string
	// Session is the hash of the local session's ID when the caller signed
	// in with the local administrator's account (ADR-0030); "" otherwise.
	Session string
	// MustChangePassword: the local administrator must change the password
	// before doing anything else.
	MustChangePassword bool
	// Method is how the principal signed in, as the audit log says it:
	// oidc, token, dev or local.
	Method string
}

// ScopeInventory: the token only reads the Ansible inventory.
const ScopeInventory = "inventory"

// PermissionsIn lists the permissions held in a project, those held for all
// of rvav included; "" lists those held for all of rvav.
func (p *Principal) PermissionsIn(projectID string) []string {
	held := p.Held(projectID)
	out := make([]string, len(held))
	for i, h := range held {
		out[i] = string(h)
	}
	return out
}

// RoleRefs names the roles of these keys.
func (p *Principal) RoleRefs(keys []string) []Role {
	out := make([]Role, 0, len(keys))
	for _, k := range keys {
		if r, ok := p.known[k]; ok {
			out = append(out, r)
		}
	}
	return out
}

// ProjectsGiven lists the projects where a role is given to the principal,
// sorted.
func (p *Principal) ProjectsGiven() []string {
	out := make([]string, 0, len(p.rolesIn))
	for id := range p.rolesIn {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// RolesIn are the keys of the roles given in a project, sorted.
func (p *Principal) RolesIn(projectID string) []string { return p.rolesIn[projectID] }

// Requester is who the principal is to a project's catalog: an item's
// sharing there may name them, one of their groups or a role they hold in
// the project (ADR-0019).
func (p *Principal) Requester(projectID string) store.Requester {
	return store.Requester{UserID: p.User.ID, Groups: p.Groups, Roles: p.RolesIn(projectID)}
}

// Role finds a role the principal holds by its key, to name it.
func (p *Principal) Role(key string) (Role, bool) {
	r, ok := p.known[key]
	return r, ok
}

// System is the principal of a user rvav acts as itself (ADR-0030), with
// these built-in roles for all of rvav.
func System(u store.User, roles ...string) *Principal {
	as := make([]Assignment, len(roles))
	for i, r := range roles {
		as[i] = Assignment{Role: r}
	}
	return newPrincipal(u, nil, as, nil)
}

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

// Again is what a principal holds now: the same user, groups and token,
// with the roles read again after a change of them.
func Again(ctx context.Context, s *store.Store, p *Principal) (*Principal, error) {
	u, err := s.GetUser(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}
	q, err := build(ctx, s, u, p.Groups)
	if err != nil {
		return nil, err
	}
	q.Scope, q.Session, q.MustChangePassword = p.Scope, p.Session, p.MustChangePassword
	return q, nil
}

// FromGroups is the principal of a user with these groups and no roles of
// their own: what the group mappings give, shown by the provider's trial
// sign-in (ADR-0030).
func FromGroups(ctx context.Context, s *store.Store, groups []string) (*Principal, error) {
	return build(ctx, s, store.User{}, groups)
}

// build gathers in one query the roles a user holds, themselves and through
// their groups (nil for none), and what they grant.
func build(ctx context.Context, s *store.Store, u store.User, groups []string) (*Principal, error) {
	held, err := s.RoleGrants(ctx, u.ID, u.GlobalRoles, groups)
	if err != nil {
		return nil, err
	}
	as := make([]Assignment, len(held))
	custom := map[string]Role{}
	for i, h := range held {
		as[i] = Assignment{Role: h.Role, ProjectID: h.ProjectID}
		if r, ok := RoleOf(h); ok && !r.BuiltIn {
			custom[h.Role] = r
		}
	}
	return newPrincipal(u, groups, as, custom), nil
}

// RoleOf is the role a grant names: built in, or custom with the
// permissions rvav still knows (one it no longer knows grants nothing);
// false for a key that is no role.
func RoleOf(h store.RoleGrant) (Role, bool) {
	if r, ok := BuiltinRole(h.Role); ok {
		return r, true
	}
	if !h.Known || !h.Custom {
		return Role{}, false
	}
	r := Role{Key: h.Role, Name: h.Name}
	for _, perm := range h.Permissions {
		if _, ok := Info(Permission(perm)); ok {
			r.Permissions = append(r.Permissions, Permission(perm))
		}
	}
	return r, true
}

func newPrincipal(u store.User, groups []string, as []Assignment, custom map[string]Role) *Principal {
	role := func(key string) (Role, bool) {
		if r, ok := BuiltinRole(key); ok {
			return r, true
		}
		r, ok := custom[key]
		return r, ok
	}
	p := &Principal{User: u, Groups: groups, Grants: Compute(as, role), Roles: []string{},
		rolesIn: map[string][]string{}, known: map[string]Role{}}
	for _, a := range as {
		r, ok := role(a.Role)
		if !ok || !r.Assignable(a.ProjectID) {
			continue
		}
		p.known[r.Key] = r
		if a.ProjectID == "" {
			if !slices.Contains(p.Roles, r.Key) {
				p.Roles = append(p.Roles, r.Key)
			}
			continue
		}
		if in := p.rolesIn[a.ProjectID]; !slices.Contains(in, r.Key) {
			in = append(in, r.Key)
			slices.Sort(in)
			p.rolesIn[a.ProjectID] = in
		}
	}
	return p
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
	var use store.TokenUse
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		if use, err = s.UseToken(ctx, tx, HashToken(token)); err != nil || !use.FirstToday {
			return err
		}
		return s.Audit(ctx, tx, tokenUsed(ctx, use))
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if use.Expired {
		return nil, fmt.Errorf("%w: the API token %q has expired", ErrUnauthenticated, use.Name)
	}
	u, err := s.GetUser(ctx, use.UserID)
	if err != nil {
		return nil, err
	}
	// The local administrator's, while its password must be changed (reset
	// with `rvav admin reset-password`, which revokes them too): refused.
	if strings.HasPrefix(u.Subject, "local:") {
		acc, err := s.LocalAccountOf(ctx, u.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if err == nil && acc.MustChange {
			return nil, ErrUnauthenticated
		}
	}
	// No roles from groups: those stored from the last sign-in may be stale,
	// and automation should not approve anything.
	p, err := build(ctx, s, u, nil)
	if p != nil {
		p.Scope, p.Method = use.Scope, "token"
	}
	return p, err
}

// tokenUsed is a token's first use of the day, or its first refusal of the
// day once expired: enough to see where it is used without a row per call.
func tokenUsed(ctx context.Context, t store.TokenUse) store.AuditEvent {
	action, detail := "auth.token_used", map[string]any{"name": t.Name, "method": "token"}
	if t.Expired {
		action, detail["reason"] = "auth.sign_in_failed", "token_expired"
	}
	return store.AuditEvent{ActorID: &t.UserID, Action: action, TargetType: "api_token", TargetID: t.ID,
		Detail: ClientFrom(ctx).Detail(detail)}
}

// ProviderSettings are what the token check needs from the identity
// provider's settings.
type ProviderSettings struct {
	Issuer string
	// DiscoveryURL is where the API reaches the provider, when that differs
	// from the issuer in tokens. Empty means the issuer.
	DiscoveryURL string
	// ClientID is the audience a token must have.
	ClientID string
	// GroupsClaim holds the user's groups (or Entra ID's app roles); empty
	// means groups.
	GroupsClaim string
	// ManagerClaim holds the user's manager, by email or subject (ADR-0041);
	// empty means manager.
	ManagerClaim string
	// Roots are the CAs the provider's certificate is checked with: the
	// process's (package trust) unless given, as for a test.
	Roots *x509.CertPool
}

// httpClient reaches the provider, trusting the CAs of the settings.
func (ps ProviderSettings) httpClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: trust.Transport(trust.Options{Roots: ps.Roots})}
}

func (ps ProviderSettings) normal() ProviderSettings {
	if ps.DiscoveryURL == "" {
		ps.DiscoveryURL = ps.Issuer
	}
	if ps.GroupsClaim == "" {
		ps.GroupsClaim = "groups"
	}
	if ps.ManagerClaim == "" {
		ps.ManagerClaim = "manager"
	}
	return ps
}

// OIDC validates access tokens issued by the organisation's identity provider.
type OIDC struct {
	Store *store.Store
	// RetryInterval is how long a failed discovery is remembered before the
	// next request tries again (default 5 s); requests meanwhile answer 503
	// at once instead of queueing.
	RetryInterval time.Duration

	// settings gives the provider's settings as they are now (ADR-0030); the
	// check is built again when they change.
	settings func(context.Context) ProviderSettings
	// holdReady: the provider was set at install, and /readyz waits for it.
	holdReady bool

	mu       sync.Mutex
	built    ProviderSettings // what verifier was built with
	verifier *oidc.IDTokenVerifier
	failedAt time.Time
	lastErr  error

	// seen remembers the user and groups of a token already checked
	// (S14.7): the user is written and its groups kept once per token, not
	// on every call it makes.
	seenMu sync.Mutex
	seen   map[[32]byte]seenToken
}

type seenToken struct {
	userID string
	groups []string
	until  time.Time
}

// seenFor is how long a token's user and groups are remembered, at most:
// a change of name or groups at the provider shows within it.
const seenFor = 5 * time.Minute

func (o *OIDC) remembered(key [32]byte) (seenToken, bool) {
	o.seenMu.Lock()
	defer o.seenMu.Unlock()
	t, ok := o.seen[key]
	if ok && time.Now().After(t.until) {
		delete(o.seen, key)
		ok = false
	}
	return t, ok
}

func (o *OIDC) remember(key [32]byte, t seenToken) {
	o.seenMu.Lock()
	defer o.seenMu.Unlock()
	if o.seen == nil || len(o.seen) >= 10000 {
		now := time.Now()
		if o.seen == nil {
			o.seen = map[[32]byte]seenToken{}
		}
		for k, v := range o.seen {
			if now.After(v.until) {
				delete(o.seen, k)
			}
		}
		if len(o.seen) >= 10000 {
			o.seen = map[[32]byte]seenToken{}
		}
	}
	o.seen[key] = t
}

// forget drops the tokens checked with other settings: a provider changed
// or removed no longer signs anyone in.
func (o *OIDC) forget() {
	o.seenMu.Lock()
	defer o.seenMu.Unlock()
	o.seen = nil
}

// NewOIDC prepares token checks against a provider set at install.
// discoveryURL, when set, is where the API reaches the provider when that
// differs from the issuer in tokens (for example http://keycloak:8080
// inside docker compose versus http://localhost:8081 in the browser).
//
// The provider is discovered on first use and again after a failure, so the
// API starts, and recovers, while the provider is down. The error says
// whether it was reachable now; the API runs either way.
//
// Without an issuer there is no provider: API tokens and the local
// administrator sign in, and the provider's tokens are refused (ADR-0030).
func NewOIDC(ctx context.Context, s *store.Store, issuer, discoveryURL, clientID, groupsClaim string) (*OIDC, error) {
	ps := ProviderSettings{Issuer: issuer, DiscoveryURL: discoveryURL, ClientID: clientID, GroupsClaim: groupsClaim}
	return NewLiveOIDC(ctx, s, issuer != "", func(context.Context) ProviderSettings { return ps })
}

// NewLiveOIDC prepares token checks that follow the provider's settings as
// they are saved (ADR-0030): settings is called on each check, and the
// check is built again when they changed. holdReady says the provider was
// set at install: /readyz then waits for it. One set in Administration
// doesn't hold it, so that the local administrator can still sign in and
// correct a wrong one.
func NewLiveOIDC(ctx context.Context, s *store.Store, holdReady bool, settings func(context.Context) ProviderSettings) (*OIDC, error) {
	o := &OIDC{Store: s, settings: settings, holdReady: holdReady}
	_, _, err := o.verifierFor(ctx)
	if errors.Is(err, ErrNoProvider) {
		err = nil
	}
	return o, err
}

// ErrNoProvider: no identity provider is set.
var ErrNoProvider = errors.New("no identity provider is set")

// Ready reports whether the provider set at install has been discovered.
// The API's readiness check uses it, so a wrong issuer or a provider that
// never comes up keeps new instances out of service instead of answering
// 503.
func (o *OIDC) Ready(ctx context.Context) error {
	if !o.holdReady {
		return nil
	}
	_, _, err := o.verifierFor(ctx)
	if errors.Is(err, ErrNoProvider) {
		return nil
	}
	return err
}

func (o *OIDC) verifierFor(ctx context.Context) (*oidc.IDTokenVerifier, ProviderSettings, error) {
	ps := o.settings(ctx).normal()
	if ps.Issuer == "" {
		return nil, ps, ErrNoProvider
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if ps != o.built {
		o.built, o.verifier, o.lastErr = ps, nil, nil
		o.forget()
	}
	if o.verifier != nil {
		return o.verifier, ps, nil
	}
	retry := o.RetryInterval
	if retry == 0 {
		retry = 5 * time.Second
	}
	if o.lastErr != nil && time.Since(o.failedAt) < retry {
		return nil, ps, o.lastErr
	}
	v, _, err := newVerifier(ctx, ps)
	if err != nil {
		o.failedAt, o.lastErr = time.Now(), fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
		return nil, ps, o.lastErr
	}
	o.verifier, o.lastErr = v, nil
	return v, ps, nil
}

// ProviderInfo is what a provider's discovery document says.
type ProviderInfo struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURL               string   `json:"jwks_uri"`
	Algs                  []string `json:"id_token_signing_alg_values_supported"`
}

// newVerifier reads the provider's discovery document and builds the token
// check for its keys.
func newVerifier(ctx context.Context, ps ProviderSettings) (*oidc.IDTokenVerifier, ProviderInfo, error) {
	var meta ProviderInfo
	if ps.DiscoveryURL != ps.Issuer {
		ctx = oidc.InsecureIssuerURLContext(ctx, ps.Issuer)
	}
	client := ps.httpClient()
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, client), 5*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, ps.DiscoveryURL)
	if err != nil {
		return nil, meta, fmt.Errorf("discovery for %s: %v", ps.Issuer, err)
	}
	if err := provider.Claims(&meta); err != nil {
		return nil, meta, fmt.Errorf("discovery for %s: %v", ps.Issuer, err)
	}
	// Access tokens from Keycloak carry the client in azp, and in aud only
	// with an audience mapper; the realm export in deploy/ adds one.
	keys := keySet{oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), meta.JWKSURL)}
	return oidc.NewVerifier(ps.Issuer, keys, &oidc.Config{ClientID: ps.ClientID, SupportedSigningAlgs: signingAlgs(meta.Algs)}), meta, nil
}

// signingAlgs are the algorithms the provider advertises that rvav accepts:
// signatures with a public key only, never a shared secret or none (S14.6).
// Without any, go-oidc takes RS256.
func signingAlgs(advertised []string) []string {
	var out []string
	for _, a := range advertised {
		switch a {
		case oidc.RS256, oidc.RS384, oidc.RS512, oidc.ES256, oidc.ES384, oidc.ES512, oidc.PS256, oidc.PS384, oidc.PS512, oidc.EdDSA:
			out = append(out, a)
		}
	}
	return out
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

// groupsIn reads the groups (or roles) a token's claims carry; Keycloak's
// full group paths lose their leading slash.
func groupsIn(claims map[string]any, claim string) []string {
	var groups []string
	if raw, ok := claims[claim].([]any); ok {
		for _, g := range raw {
			if s, ok := g.(string); ok {
				groups = append(groups, strings.TrimPrefix(s, "/"))
			}
		}
	}
	return groups
}

// managerIn reads the manager claim: a string, or the first of a list, as a
// multivalued attribute gives it.
func managerIn(claims map[string]any, claim string) string {
	switch v := claims[claim].(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		for _, m := range v {
			if s, ok := m.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
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
	v, ps, err := o.verifierFor(ctx)
	if errors.Is(err, ErrNoProvider) {
		return nil, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
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
	// A token checked before: its user and groups are known; roles are
	// read again, so a change in rvav applies at once.
	key := sha256.Sum256([]byte(token))
	if t, ok := o.remembered(key); ok {
		u, err := o.Store.GetUser(ctx, t.userID)
		if err != nil {
			return nil, err
		}
		return signedInWith("oidc")(build(ctx, o.Store, u, t.groups))
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
	groups := groupsIn(claims, ps.GroupsClaim)
	if ReservedSubject(idt.Subject) {
		return nil, fmt.Errorf("%w: the subject %q is one of rvav's own users'", ErrUnauthenticated, idt.Subject)
	}
	u, err := o.Store.UpsertOIDCUser(ctx, idt.Subject, name, str("email"), str("title"), managerIn(claims, ps.ManagerClaim))
	if err != nil {
		return nil, err
	}
	// Kept so approvals know a group's members before they sign in again.
	if err := o.Store.SetUserGroups(ctx, u.ID, groups); err != nil {
		return nil, err
	}
	until := time.Now().Add(seenFor)
	if idt.Expiry.Before(until) {
		until = idt.Expiry
	}
	o.remember(key, seenToken{userID: u.ID, groups: groups, until: until})
	return signedInWith("oidc")(build(ctx, o.Store, u, groups))
}

// signedInWith sets the method of the principal build returns.
func signedInWith(method string) func(*Principal, error) (*Principal, error) {
	return func(p *Principal, err error) (*Principal, error) {
		if p != nil {
			p.Method = method
		}
		return p, err
	}
}

// ReservedSubject says whether a subject is one of rvav's own users': the
// local administrator (local:), seeded users waiting for their first
// sign-in (seed:), and users rvav acts as itself (system:). A provider's
// token is never taken as one of them, whatever its subject.
func ReservedSubject(subject string) bool {
	for _, prefix := range []string{"local:", "seed:", "system:"} {
		if strings.HasPrefix(subject, prefix) {
			return true
		}
	}
	return false
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
	return signedInWith("dev")(build(ctx, d.Store, u, groups))
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
