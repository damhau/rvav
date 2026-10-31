package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
)

// The identity provider set in Administration (ADR-0030): the sign-in page
// reads it from the API, the token check follows it, and a test and a
// trial sign-in check it before it is saved.

// liveKinds are the integrations whose saved settings apply on next use,
// without a restart.
var liveKinds = []string{"oidc"}

// trialFor is how long the sign-in page may reach a provider being tried.
const trialFor = 10 * time.Minute

// authConfig is the sign-in configuration as saved now.
func (s *Service) authConfig(ctx context.Context) config.AuthConfig {
	if s.Live != nil {
		return s.Live.Config(ctx).Auth
	}
	return s.Config.Auth
}

// AuthOptions say how the browser signs in now, and the origins the page
// may reach for it: the provider's, and those of providers being tried.
func (s *Service) AuthOptions(ctx context.Context) (api.AuthOptions, []string) {
	var out api.AuthOptions
	var origins []string
	a := s.authConfig(ctx)
	if a.OIDCIssuer != "" {
		out.Provider = &api.AuthProvider{Authority: a.OIDCIssuer, ClientId: a.OIDCClientID, Scope: a.Scopes}
		if o := auth.Origin(a.OIDCIssuer); o != "" {
			origins = append(origins, o)
		}
	}
	trials, err := s.Store.TrialOrigins(ctx)
	if err != nil && ctx.Err() == nil {
		s.Log.Warn("reading the origins of providers being tried", "err", err)
	}
	for _, o := range trials {
		if !slices.Contains(origins, o) {
			origins = append(origins, o)
		}
	}
	return out, origins
}

// providerFrom reads a provider's settings from an integration's settings.
func providerFrom(settings map[string]any) auth.ProviderSettings {
	str := func(k string) string { v, _ := settings[k].(string); return strings.TrimSpace(v) }
	return auth.ProviderSettings{Issuer: str("issuer"), DiscoveryURL: str("discovery_url"), ClientID: str("client_id"),
		GroupsClaim: str("groups_claim")}
}

// testOIDC reads the provider's discovery document and keys. When they
// answer, the sign-in page may reach the provider for a while, so that a
// trial sign-in works before the settings are saved.
func (s *Service) testOIDC(ctx context.Context, settings map[string]any, check checkFunc) {
	ps := providerFrom(settings)
	if ps.Issuer == "" {
		check("Issuer", errors.New("no issuer is set"), "")
		return
	}
	if ps.ClientID == "" {
		check("Client", errors.New("no client ID is set"), "")
		return
	}
	pc := auth.CheckProvider(ctx, ps)
	where := ps.Issuer
	if ps.DiscoveryURL != "" && ps.DiscoveryURL != ps.Issuer {
		where = ps.DiscoveryURL + " (as " + ps.Issuer + ")"
	}
	if !check("Discovery", pc.Discovery, where+" answered: sign-in at "+pc.Info.AuthorizationEndpoint) {
		return
	}
	check("Signing keys", pc.KeysErr, fmt.Sprintf("%d published: %s", len(pc.Keys), strings.Join(pc.Keys, ", ")))
	if o := auth.Origin(ps.Issuer); o != "" {
		if err := s.Store.TryOrigin(ctx, o, time.Now().Add(trialFor)); err != nil {
			s.Log.Warn("letting the sign-in page reach the provider tried", "origin", o, "err", err)
		}
	}
}

// TrialIdentityProvider checks a token from a trial sign-in with the
// provider settings given over the saved ones, and says what rvav reads
// from it. Nothing is saved.
func (s *Service) TrialIdentityProvider(ctx context.Context, p *auth.Principal, req api.OidcTrialRequest) (api.OidcTrial, error) {
	out := api.OidcTrial{Claims: map[string]any{}, Groups: []string{}, GlobalRoles: []string{}, ProjectRoles: []api.OidcTrialProjectRole{}}
	if !p.IsAdmin() {
		return out, ErrForbidden
	}
	token := strings.TrimSpace(deref(req.AccessToken))
	if token == "" {
		return out, &ValidationError{Errors: []api.FieldError{fieldErr("/access_token", "required", "The trial sign-in's access token.")}}
	}
	stored, err := s.Store.StoredIntegrations(ctx)
	if err != nil {
		return out, err
	}
	var settings map[string]any
	if r, ok := stored["oidc"]; ok {
		settings = s.pendingSettings(ctx, "oidc", &r)
	} else {
		settings = s.pendingSettings(ctx, "oidc", nil)
	}
	locked := config.Locked("oidc")
	given := withoutFake(req.Settings)
	for field, msg := range config.CheckSettings("oidc", given) {
		return out, &ValidationError{Errors: []api.FieldError{fieldErr("/settings/"+field, "invalid", msg)}}
	}
	for k, v := range given {
		if !slices.Contains(locked, k) {
			settings[k] = v
		}
	}
	ps := providerFrom(settings)
	if ps.Issuer == "" {
		return out, &ValidationError{Errors: []api.FieldError{fieldErr("/settings/issuer", "required", "The provider's issuer.")}}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	t := auth.TrialToken(ctx, ps, token)
	claim := ps.GroupsClaim
	if claim == "" {
		claim = "groups"
	}
	out.Accepted, out.Claims, out.GroupsClaim = t.Refused == nil, t.Claims, &claim
	if t.Subject != "" {
		out.Subject = &t.Subject
	}
	if t.Name != "" {
		out.Name = &t.Name
	}
	if t.Email != "" {
		out.Email = &t.Email
	}
	if t.Groups != nil {
		out.Groups = t.Groups
	}
	if msg := trialAdvice(t, ps, claim); msg != "" {
		out.Message = &msg
	}
	if out.Accepted {
		roles, err := auth.FromGroups(ctx, s.Store, t.Groups)
		if err != nil {
			return out, err
		}
		out.GlobalRoles = nonNilStrings(roles.GlobalRoles)
		ids := make([]string, 0, len(roles.ProjectRoles))
		for id := range roles.ProjectRoles {
			ids = append(ids, id)
		}
		projects, err := s.Store.ProjectsByID(ctx, ids)
		if err != nil {
			return out, err
		}
		for id, role := range roles.ProjectRoles {
			pr, ok := projects[id]
			if !ok {
				continue
			}
			out.ProjectRoles = append(out.ProjectRoles, api.OidcTrialProjectRole{Project: api.ProjectRef{Id: pr.ID, Code: pr.Code, Name: pr.Name}, Role: role})
		}
		slices.SortFunc(out.ProjectRoles, func(a, b api.OidcTrialProjectRole) int { return strings.Compare(a.Project.Name, b.Project.Name) })
	}
	s.audit(ctx, p, "settings.trial.oidc", "integration", "oidc", nil, map[string]any{"issuer": ps.Issuer, "accepted": out.Accepted,
		"subject": t.Subject, "groups": len(t.Groups)})
	return out, nil
}

// Microsoft Graph's application ID: Entra ID's access token for Graph, not
// for rvav, when no scope of rvav's API is asked for.
const graphAudience = "00000003-0000-0000-c000-000000000000"

// trialAdvice says why a trial's token is refused, with what to change for
// the usual causes, or what to look at in one accepted.
func trialAdvice(t auth.Trial, ps auth.ProviderSettings, claim string) string {
	auds := audiences(t.Claims["aud"])
	if t.Refused != nil {
		msg := t.Refused.Error()
		iss, _ := t.Claims["iss"].(string)
		switch {
		case slices.Contains(auds, graphAudience) || slices.Contains(auds, "https://graph.microsoft.com"):
			msg += ". The token is for Microsoft Graph: add a scope of rvav's API (api://" + ps.ClientID + "/…) to the scopes."
		case strings.HasPrefix(iss, "https://sts.windows.net/"):
			msg += ". Entra ID gave a v1 token: set the app's requestedAccessTokenVersion to 2 (Manifest)."
		case strings.Contains(msg, "expected audience"):
			msg += ". rvav expects the client ID as the audience: set the provider to put it in aud (Keycloak: an audience mapper)."
		}
		return msg
	}
	if _, ok := t.Claims[claim]; !ok {
		return "The token has no " + claim + " claim, so no group mapping gives a role. Entra ID: add the groups claim, or app roles and set the claim to roles."
	}
	return ""
}

func audiences(v any) []string {
	switch a := v.(type) {
	case string:
		return []string{a}
	case []any:
		var out []string
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
