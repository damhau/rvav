// Package auth turns a bearer token into a Principal: the user, their global
// roles and their role in each project.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/damhau/rvav/internal/store"
)

var ErrUnauthenticated = errors.New("unauthenticated")

// Principal is who is calling and what they may do.
type Principal struct {
	User        store.User
	GlobalRoles []string
	// ProjectRoles maps project ID to the caller's strongest role there.
	ProjectRoles map[string]string
}

func (p *Principal) HasGlobal(role string) bool { return slices.Contains(p.GlobalRoles, role) }

func (p *Principal) IsAdmin() bool { return p.HasGlobal("admin") }

// SeesEverything is true for administrators and auditors.
func (p *Principal) SeesEverything() bool { return p.IsAdmin() || p.HasGlobal("auditor") }

// ProjectRole returns the caller's role in a project ("" when none).
// Administrators act as project admins everywhere.
func (p *Principal) ProjectRole(projectID string) string {
	if r, ok := p.ProjectRoles[projectID]; ok {
		return r
	}
	if p.IsAdmin() {
		return "project-admin"
	}
	return ""
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

// build combines a user's own roles with those granted by OIDC groups.
func build(ctx context.Context, s *store.Store, u store.User, groups []string) (*Principal, error) {
	p := &Principal{User: u, GlobalRoles: append([]string{}, u.GlobalRoles...), ProjectRoles: map[string]string{}}
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

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func apiToken(ctx context.Context, s *store.Store, token string) (*Principal, error) {
	u, err := s.UserForToken(ctx, HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	return build(ctx, s, u, nil)
}

// OIDC validates access tokens issued by the organisation's identity provider.
type OIDC struct {
	Store       *store.Store
	Verifier    *oidc.IDTokenVerifier
	GroupsClaim string
}

func NewOIDC(ctx context.Context, s *store.Store, issuer, clientID, groupsClaim string) (*OIDC, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", issuer, err)
	}
	// Access tokens from Keycloak carry the client in azp, and in aud only
	// with an audience mapper; the realm export in deploy/ adds one.
	v := provider.Verifier(&oidc.Config{ClientID: clientID})
	return &OIDC{Store: s, Verifier: v, GroupsClaim: groupsClaim}, nil
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
	idt, err := o.Verifier.Verify(ctx, token)
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
		return nil, fmt.Errorf("%w: dev user %q does not exist", ErrUnauthenticated, name)
	}
	if err != nil {
		return nil, err
	}
	return build(ctx, d.Store, u, nil)
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
