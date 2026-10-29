// Package vcenters connects to the vCenters rvav manages (ADR-0024): the
// vCenter integration's, the first, and those administrators add, each
// once, and again when its settings change.
package vcenters

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

// ErrUnknown: the name is not a vCenter rvav manages.
var ErrUnknown = errors.New("not a vCenter rvav manages")

// SecretName is the name a vCenter's password is sealed under.
func SecretName(name string) string { return "vcenter:" + name }

// Set is the vCenters rvav manages.
type Set struct {
	Store   *store.Store
	Secrets *secrets.Box
	// PrimaryName and Primary are the vCenter integration's.
	PrimaryName string
	Primary     func(ctx context.Context) (vsphere.Driver, error)
	// Dial connects to an added vCenter (vsphere.New when nil).
	Dial func(ctx context.Context, name, url, username, password string, insecure bool) (vsphere.Driver, error)

	mu    sync.Mutex
	conns map[string]conn
}

type conn struct {
	d       vsphere.Driver
	updated time.Time
}

// Get connects to a vCenter by name; "" is the primary, and so is a name
// rvav doesn't know: the integration's may have been renamed since a VM
// was built (only vCenters without VMs can be removed).
func (s *Set) Get(ctx context.Context, name string) (vsphere.Driver, error) {
	row, err := s.Store.GetVCenter(ctx, name)
	if name == "" || name == s.PrimaryName || errors.Is(err, store.ErrNotFound) {
		if s.Primary == nil {
			return nil, fmt.Errorf("%s: %w", name, ErrUnknown)
		}
		return s.Primary(ctx)
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	c, ok := s.conns[name]
	s.mu.Unlock()
	if ok && c.updated.Equal(row.UpdatedAt) {
		return c.d, nil
	}
	password := ""
	if len(row.Ciphertext) > 0 {
		if s.Secrets == nil {
			return nil, errors.New("rvav has no secrets key (RVAV_SECRETS_KEY) to open the password of " + name)
		}
		plain, err := s.Secrets.Open(SecretName(name), row.KeyID, row.Nonce, row.Ciphertext)
		if err != nil {
			return nil, fmt.Errorf("open the password of %s: %w", name, err)
		}
		password = string(plain)
	}
	dial := s.Dial
	if dial == nil {
		dial = func(ctx context.Context, name, url, username, password string, insecure bool) (vsphere.Driver, error) {
			return vsphere.New(ctx, name, url, username, password, insecure)
		}
	}
	// Outside the lock: a vCenter slow to answer doesn't hold up the others.
	d, err := dial(ctx, name, row.URL, row.Username, password, row.Insecure)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns == nil {
		s.conns = map[string]conn{}
	}
	s.conns[name] = conn{d: d, updated: row.UpdatedAt}
	return d, nil
}

// Names are every vCenter's, the primary's first when there is one.
func (s *Set) Names(ctx context.Context) ([]string, error) {
	var out []string
	if s.Primary != nil {
		out = append(out, s.PrimaryName)
	}
	rows, err := s.Store.VCenters(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out, nil
}
