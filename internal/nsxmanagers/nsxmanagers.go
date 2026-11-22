// Package nsxmanagers connects to the NSX Managers rvav manages (ADR-0062):
// the NSX integration's, the first, named "", and those administrators
// add, each once, and again when its settings change.
package nsxmanagers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

// ErrUnknown: the name is not an NSX Manager rvav manages.
var ErrUnknown = errors.New("not an NSX Manager rvav manages")

// SecretName is the name an NSX Manager's password is sealed under.
func SecretName(name string) string { return "nsx:" + name }

// Options are how rvav connects to an added NSX Manager.
func Options(row store.NSXManager, password string) nsx.Options {
	return nsx.Options{URL: row.URL, Username: row.Username, Password: password, CertFile: row.CertFile, KeyFile: row.KeyFile,
		Insecure: row.Insecure, Pin: row.CertSHA256}
}

// Set is the NSX Managers rvav manages.
type Set struct {
	Store   *store.Store
	Secrets *secrets.Box
	// Primary is the NSX integration's Manager; nil without one.
	Primary *nsx.Client

	mu    sync.Mutex
	conns map[string]conn
}

type conn struct {
	c       *nsx.Client
	updated time.Time
}

// Get connects to an NSX Manager by name, "" for the NSX integration's.
func (s *Set) Get(ctx context.Context, name string) (*nsx.Client, error) {
	if name == "" {
		if s.Primary == nil {
			return nil, fmt.Errorf("NSX is not set (Administration › Integrations › NSX): %w", ErrUnknown)
		}
		return s.Primary, nil
	}
	row, err := s.Store.GetNSXManager(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("%s: %w", name, ErrUnknown)
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.conns[name]; ok && c.updated.Equal(row.UpdatedAt) {
		return c.c, nil
	}
	password, err := Password(s.Secrets, row)
	if err != nil {
		return nil, err
	}
	c, err := nsx.New(Options(row, password))
	if err != nil {
		return nil, fmt.Errorf("NSX Manager %s: %w", name, err)
	}
	if s.conns == nil {
		s.conns = map[string]conn{}
	}
	s.conns[name] = conn{c: c, updated: row.UpdatedAt}
	return c, nil
}

// Names are every NSX Manager's, "" first when the integration has one.
func (s *Set) Names(ctx context.Context) ([]string, error) {
	var out []string
	if s.Primary != nil {
		out = append(out, "")
	}
	rows, err := s.Store.NSXManagers(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out, nil
}

// Password opens an added NSX Manager's password; "" when it has none.
func Password(box *secrets.Box, row store.NSXManager) (string, error) {
	if len(row.Ciphertext) == 0 {
		return "", nil
	}
	if box == nil {
		return "", errors.New("rvav has no secrets key (RVAV_SECRETS_KEY) to open the password of " + row.Name)
	}
	plain, err := box.Open(SecretName(row.Name), row.KeyID, row.Nonce, row.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("open the password of %s: %w", row.Name, err)
	}
	return string(plain), nil
}
