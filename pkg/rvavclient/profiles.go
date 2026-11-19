package rvavclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/oauth2"
)

// ErrSignInExpired is a profile whose sign-in can't be refreshed any more:
// sign in again.
var ErrSignInExpired = errors.New("the sign-in has expired")

// Profiles are the rvavs rvavctl signed in to, by name, with their
// credentials: a file only its user may read.
type Profiles struct {
	Path     string              `json:"-"`
	Profiles map[string]*Profile `json:"profiles"`
}

// Profile is one rvav and how to call it.
type Profile struct {
	Server string `json:"server"`
	// Token is an API token (rvav_pat_…).
	Token string `json:"token,omitempty"`
	// OIDC is a sign-in at rvav's identity provider, refreshed as needed.
	OIDC *OIDCSession `json:"oidc,omitempty"`
}

// OIDCSession is what refreshing a sign-in at the identity provider needs.
type OIDCSession struct {
	Issuer   string        `json:"issuer"`
	ClientID string        `json:"client_id"`
	TokenURL string        `json:"token_url"`
	Scopes   []string      `json:"scopes,omitempty"`
	Token    *oauth2.Token `json:"token"`
}

// DefaultProfilesPath is rvavctl's file in the user's configuration
// directory, for example ~/.config/rvav/config.json on Linux.
func DefaultProfilesPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rvav", "config.json"), nil
}

// LoadProfiles reads the file at path; none yet is no profile.
func LoadProfiles(path string) (*Profiles, error) {
	ps := &Profiles{Path: path, Profiles: map[string]*Profile{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ps, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, ps); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if ps.Profiles == nil {
		ps.Profiles = map[string]*Profile{}
	}
	return ps, nil
}

// Save writes the profiles readable by their user only, replacing the file
// at once so that a process reading it never sees half of it.
func (ps *Profiles) Save() error {
	dir := filepath.Dir(ps.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), ps.Path)
}

// Update changes one profile in the file as it is now, so that what
// another rvavctl saved meanwhile is kept, and saves it.
func Update(path, name string, change func(p *Profile)) error {
	ps, err := LoadProfiles(path)
	if err != nil {
		return err
	}
	p := ps.Profiles[name]
	if p == nil {
		return fmt.Errorf("no profile %q", name)
	}
	change(p)
	return ps.Save()
}

// TokenSource gives the profile's bearer token: its API token, or its
// OIDC access token, refreshed when it expires and then handed to save.
// Nil when the profile holds no credentials.
func (p *Profile) TokenSource(ctx context.Context, save func(*oauth2.Token) error) oauth2.TokenSource {
	switch {
	case p.Token != "":
		return StaticToken(p.Token)
	case p.OIDC != nil && p.OIDC.Token != nil:
		// rvav's client is public: no secret, its ID in the body (RFC 6749, 2.3.1).
		cfg := oauth2.Config{ClientID: p.OIDC.ClientID, Scopes: p.OIDC.Scopes,
			Endpoint: oauth2.Endpoint{TokenURL: p.OIDC.TokenURL, AuthStyle: oauth2.AuthStyleInParams}}
		return &savingSource{src: cfg.TokenSource(ctx, p.OIDC.Token), last: p.OIDC.Token, save: save}
	}
	return nil
}

// savingSource saves each token its source refreshes: that source hands
// back the token it holds until it refreshes it.
type savingSource struct {
	mu   sync.Mutex
	src  oauth2.TokenSource
	last *oauth2.Token
	save func(*oauth2.Token) error
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.src.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSignInExpired, err)
	}
	if t != s.last && s.save != nil {
		if err := s.save(t); err != nil {
			return nil, fmt.Errorf("saving the refreshed sign-in: %w", err)
		}
	}
	s.last = t
	return t, nil
}
