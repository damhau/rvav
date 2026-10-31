package core

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
)

// The token AWX's inventory source reads rvav with (ADR-0030): rvav makes
// it for a user of its own, an auditor no one signs in as, so that it
// doesn't depend on the administrator who made it, and puts it in AWX.
const (
	InventoryUserID      = "usr_rvav-inventory"
	inventoryUserSubject = "system:inventory"
	inventoryUserName    = "AWX inventory (rvav)"
	inventoryTokenName   = "AWX inventory"
	inventoryTokenDays   = 365
)

// inventoryTokenLocked: RVAV_INVENTORY_TOKEN sets the token, which rvav
// awx-bootstrap puts in AWX.
func inventoryTokenLocked() bool { return os.Getenv("RVAV_INVENTORY_TOKEN") != "" }

// InventoryToken says which token rvav made for AWX's inventory source.
func (s *Service) InventoryToken(ctx context.Context, p *auth.Principal) (api.InventoryToken, error) {
	if !p.IsAdmin() {
		return api.InventoryToken{}, ErrForbidden
	}
	out := api.InventoryToken{Locked: inventoryTokenLocked(), SourceSet: s.liveConfig(ctx).AWX.InventorySourceID != 0,
		PublicUrl: new(s.Config.PublicURL)}
	list, err := s.Store.APITokens(ctx, InventoryUserID)
	if err != nil {
		return out, err
	}
	now := time.Now()
	for _, t := range list { // newest first
		if t.Name == inventoryTokenName && (t.ExpiresAt == nil || t.ExpiresAt.After(now)) {
			pt := presentToken(t)
			out.Token = &pt
			break
		}
	}
	return out, nil
}

// MakeInventoryToken makes a token that only reads the inventory and puts
// it in AWX, in the credential the inventory source reads rvav with; then
// the earlier ones are revoked and the source is synced.
func (s *Service) MakeInventoryToken(ctx context.Context, p *auth.Principal, w api.InventoryTokenWrite) (api.InventoryTokenMade, error) {
	if !p.IsAdmin() {
		return api.InventoryTokenMade{}, ErrForbidden
	}
	if inventoryTokenLocked() {
		return api.InventoryTokenMade{}, &ConflictError{Msg: "RVAV_INVENTORY_TOKEN sets the token: rvav awx-bootstrap puts that one in AWX."}
	}
	rvavURL := strings.TrimSpace(deref(w.Url))
	if rvavURL != "" {
		if u, err := url.Parse(rvavURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return api.InventoryTokenMade{}, &ValidationError{Errors: []api.FieldError{fieldErr("/url", "invalid",
				"rvav's address as AWX reaches it, such as https://rvav.corp.example.")}}
		}
	}
	source := s.liveConfig(ctx).AWX.InventorySourceID
	switch {
	case s.AWX == nil:
		return api.InventoryTokenMade{}, &ConflictError{Msg: "Set AWX's connection first."}
	case source == 0:
		return api.InventoryTokenMade{}, &ConflictError{Msg: "Set AWX's inventory source ID first: the source that reads rvav's inventory."}
	}
	if err := s.Store.EnsureSystemUser(ctx, InventoryUserID, inventoryUserSubject, inventoryUserName, []string{"auditor"}); err != nil {
		return api.InventoryTokenMade{}, err
	}
	token, id := auth.NewToken(), ids.New(ids.Token)
	expires := time.Now().Add(inventoryTokenDays * 24 * time.Hour)
	if err := s.Store.CreateAPIToken(ctx, id, InventoryUserID, inventoryTokenName, auth.HashToken(token), auth.ScopeInventory,
		&expires); err != nil {
		return api.InventoryTokenMade{}, err
	}
	set, err := s.AWX.SetRvavCredential(ctx, source, rvavURL, s.Config.PublicURL, token)
	if err != nil {
		// AWX keeps the token it had, which stays valid.
		_ = s.Store.RevokeAPIToken(context.WithoutCancel(ctx), id)
		return api.InventoryTokenMade{}, &UpstreamError{System: "AWX", Err: err}
	}
	// The earlier tokens, now that AWX has this one.
	revoked := 0
	list, err := s.Store.APITokens(ctx, InventoryUserID)
	if err != nil {
		return api.InventoryTokenMade{}, err
	}
	for _, t := range list {
		if t.ID != id && t.Name == inventoryTokenName {
			if err := s.Store.RevokeAPIToken(ctx, t.ID); err == nil {
				revoked++
			}
		}
	}
	update := ""
	if job, err := s.AWX.UpdateInventorySource(ctx, source); err != nil {
		update = "The inventory source's sync could not be started: " + err.Error()
	} else {
		update = fmt.Sprintf("AWX is syncing the inventory source with it (update %d).", job)
	}
	s.audit(ctx, p, "awx.inventory_token", "api_token", id, nil, map[string]any{"inventory_source_id": source,
		"credential": set.Name, "url": set.URL, "attached": set.Attached, "revoked": revoked, "expires_at": expires})
	t, err := s.Store.GetAPIToken(ctx, id)
	if err != nil {
		return api.InventoryTokenMade{}, err
	}
	return api.InventoryTokenMade{Token: presentToken(t), Credential: set.Name, Url: set.URL, Attached: set.Attached,
		Revoked: new(revoked), Update: new(update)}, nil
}
