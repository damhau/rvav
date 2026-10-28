package core

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// API tokens and the audit log in the UI (ADR-0020).

// maxTokenDays is the longest expiry a token made from the UI gets; tokens
// that never expire are made with `rvav api-token -days 0`.
const maxTokenDays = 366

func presentToken(t store.APIToken) api.ApiToken {
	scope := api.ApiTokenScopeAll
	if t.Scope == auth.ScopeInventory {
		scope = api.ApiTokenScopeInventory
	}
	return api.ApiToken{Id: t.ID, Name: t.Name, Scope: scope, User: api.UserRef{Id: t.UserID, Name: t.UserName},
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt}
}

// APITokens lists the caller's tokens, or everyone's for administrators.
func (s *Service) APITokens(ctx context.Context, p *auth.Principal, all bool) ([]api.ApiToken, error) {
	user := p.User.ID
	if all {
		if !p.IsAdmin() {
			return nil, ErrForbidden
		}
		user = ""
	}
	list, err := s.Store.APITokens(ctx, user)
	if err != nil {
		return nil, err
	}
	out := make([]api.ApiToken, len(list))
	for i, t := range list {
		out[i] = presentToken(t)
	}
	return out, nil
}

// CreateAPIToken makes a token for the caller; its value is in the answer
// only.
func (s *Service) CreateAPIToken(ctx context.Context, p *auth.Principal, c api.ApiTokenCreate) (api.ApiTokenCreated, error) {
	var errs []api.FieldError
	name := strings.TrimSpace(c.Name)
	if name == "" {
		errs = append(errs, fieldErr("/name", "required", "Say what the token is for."))
	}
	now := time.Now()
	expires := now.Add(90 * 24 * time.Hour)
	if c.ExpiresAt != nil {
		expires = *c.ExpiresAt
	}
	switch {
	case !expires.After(now):
		errs = append(errs, fieldErr("/expires_at", "past", "An expiry in the future."))
	case expires.After(now.Add(maxTokenDays * 24 * time.Hour)):
		errs = append(errs, fieldErr("/expires_at", "too_far", "At most a year from now."))
	}
	scope := ""
	if c.Scope != nil && *c.Scope == api.ApiTokenCreateScopeInventory {
		scope = auth.ScopeInventory
	}
	if len(errs) > 0 {
		return api.ApiTokenCreated{}, &ValidationError{Errors: errs}
	}
	token, id := auth.NewToken(), ids.New(ids.Token)
	if err := s.Store.CreateAPIToken(ctx, id, p.User.ID, name, auth.HashToken(token), scope, &expires); err != nil {
		return api.ApiTokenCreated{}, err
	}
	s.audit(ctx, p, "token.create", "api_token", id, nil, map[string]any{"name": name, "scope": scope, "expires_at": expires})
	t, err := s.Store.GetAPIToken(ctx, id)
	if err != nil {
		return api.ApiTokenCreated{}, err
	}
	pt := presentToken(t)
	return api.ApiTokenCreated{Id: pt.Id, Name: pt.Name, Scope: api.ApiTokenCreatedScope(pt.Scope), User: pt.User,
		CreatedAt: pt.CreatedAt, ExpiresAt: pt.ExpiresAt, Token: token}, nil
}

// RevokeAPIToken revokes one's own token, or anyone's for administrators.
func (s *Service) RevokeAPIToken(ctx context.Context, p *auth.Principal, id string) error {
	t, err := s.Store.GetAPIToken(ctx, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && t.UserID != p.User.ID && !p.IsAdmin() {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := s.Store.RevokeAPIToken(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, p, "token.revoke", "api_token", id, nil, map[string]any{"name": t.Name, "user_id": t.UserID})
	return nil
}

// AuditQuery is a search of the audit log.
type AuditQuery struct {
	Search, ActorID, Action, ProjectID string
	From, To                           *time.Time
	Cursor                             string
	Limit                              int
}

// AuditEvents searches the audit log: administrators and auditors see all
// of it, project admins their projects' events.
func (s *Service) AuditEvents(ctx context.Context, p *auth.Principal, q AuditQuery) ([]api.AuditEvent, *string, error) {
	f := store.AuditFilter{Search: strings.TrimSpace(q.Search), ActorID: q.ActorID, Action: q.Action, ProjectID: q.ProjectID,
		From: q.From, To: q.To, Limit: q.Limit}
	if !p.SeesEverything() {
		var mine []string
		for id, role := range p.ProjectRoles {
			if role == "project-admin" {
				mine = append(mine, id)
			}
		}
		if len(mine) == 0 {
			return nil, nil, ErrForbidden
		}
		f.Projects = mine
	}
	if q.Cursor != "" {
		before, err := strconv.ParseInt(q.Cursor, 10, 64)
		if err != nil {
			return nil, nil, &BadRequestError{Msg: "That cursor is not one this API gave."}
		}
		f.Before = before
	}
	rows, err := s.Store.AuditEvents(ctx, f)
	if err != nil {
		return nil, nil, err
	}
	out := make([]api.AuditEvent, len(rows))
	for i, r := range rows {
		e := api.AuditEvent{Id: strconv.FormatInt(r.ID, 10), At: r.At, Action: r.Action, TargetType: r.TargetType,
			TargetId: r.TargetID, Detail: map[string]any{}}
		_ = json.Unmarshal(r.Detail, &e.Detail)
		if e.Detail == nil {
			e.Detail = map[string]any{}
		}
		if r.ActorID != nil {
			e.Actor = &api.UserRef{Id: *r.ActorID, Name: deref(r.ActorName)}
		}
		if r.ProjectID != nil {
			e.Project = &api.ProjectRef{Id: *r.ProjectID, Code: deref(r.ProjectCode), Name: deref(r.ProjectName)}
		}
		out[i] = e
	}
	var next *string
	if q.Limit > 0 && len(rows) == q.Limit {
		c := strconv.FormatInt(rows[len(rows)-1].ID, 10)
		next = &c
	}
	return out, next, nil
}
