package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

// LiveSettings are the saved settings as they are now (ADR-0030): what is
// read on next use follows a change without a restart. The settings
// revision is read at most once per Every, and the settings again when it
// moved.
type LiveSettings struct {
	Store   *store.Store
	Secrets *secrets.Box
	// Base is the configuration from the environment, before any saved
	// setting: a setting cleared in the UI goes back to it.
	Base  config.Config
	Every time.Duration
	Log   *slog.Logger

	mu      sync.Mutex
	checked time.Time
	rev     int64
	loaded  bool
	cfg     config.Config
}

// Config is the configuration with the settings saved now. When the
// database can't be read, the last one read (or the base) is used.
func (l *LiveSettings) Config(ctx context.Context) config.Config {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loaded && time.Since(l.checked) < l.Every {
		return l.cfg
	}
	if !l.loaded {
		l.cfg = l.Base
	}
	rev, err := l.Store.SettingsRevision(ctx)
	if err != nil {
		l.warn(ctx, "reading the settings revision", err)
		return l.cfg
	}
	if l.loaded && rev == l.rev {
		l.checked = time.Now()
		return l.cfg
	}
	stored, err := l.Store.StoredIntegrations(ctx)
	if err != nil {
		l.warn(ctx, "reading the saved settings", err)
		return l.cfg
	}
	cfg := l.Base
	if _, err := ApplyStored(&cfg, stored, l.Secrets, l.Log); err != nil {
		l.warn(ctx, "reading the saved settings", err)
		return l.cfg
	}
	l.cfg, l.rev, l.loaded, l.checked = cfg, rev, true, time.Now()
	return cfg
}

// warn logs a failed read, unless the request that asked went away.
func (l *LiveSettings) warn(ctx context.Context, msg string, err error) {
	if l.Log != nil && ctx.Err() == nil {
		l.Log.Warn(msg, "err", err)
	}
}

// Provider is the identity provider's settings now, for the token check.
func (l *LiveSettings) Provider(ctx context.Context) auth.ProviderSettings {
	return providerOf(l.Config(ctx).Auth)
}

func providerOf(a config.AuthConfig) auth.ProviderSettings {
	return auth.ProviderSettings{Issuer: a.OIDCIssuer, DiscoveryURL: a.OIDCDiscoveryURL, ClientID: a.OIDCClientID, GroupsClaim: a.GroupsClaim}
}

// ApplyStored merges the integrations' saved settings into cfg, where no
// environment variable sets them, and gives their latest revision. A secret
// that can't be opened is left out, and logged.
func ApplyStored(cfg *config.Config, stored map[string]store.IntegrationSettings, box *secrets.Box, log *slog.Logger) (int64, error) {
	var rev int64
	for kind, row := range stored {
		var s config.Stored
		if err := json.Unmarshal(row.Settings, &s.Settings); err != nil {
			return 0, fmt.Errorf("stored %s settings: %w", kind, err)
		}
		if row.SecretCiphertext != nil {
			plain, err := box.Open(IntegrationSecretName(kind), row.SecretKeyID, row.SecretNonce, row.SecretCiphertext)
			if err != nil {
				if log != nil {
					log.Warn("the stored "+kind+" password or token can't be opened; it is not used", "err", err)
				}
			} else {
				s.Secret = string(plain)
			}
		}
		cfg.ApplyStored(kind, s)
		rev = max(rev, row.Revision)
	}
	return rev, nil
}
