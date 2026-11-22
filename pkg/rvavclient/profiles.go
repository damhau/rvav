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
	// InsecureHTTP is a server at an http:// address on another machine,
	// which its user chose to call unencrypted (rvavctl login --insecure-http).
	InsecureHTTP bool `json:"insecure_http,omitempty"`
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
// at once so that a process reading it never sees half of it. Edit guards
// the read and the write against another process saving in between.
func (ps *Profiles) Save() error {
	dir := filepath.Dir(ps.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := privateDir(dir); err != nil {
		return fmt.Errorf("%s is open to other users, and rvavctl can't close it: %w", dir, err)
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

// Edit changes the profiles in the file as it is now and saves them,
// holding the lock every rvavctl and Terraform provider takes to write the
// file, so that what another one saved meanwhile is kept. An error from
// change leaves the file as it was.
func Edit(path string, change func(ps *Profiles) error) error {
	return locked(path, func() error {
		ps, err := LoadProfiles(path)
		if err != nil {
			return err
		}
		if err := change(ps); err != nil {
			return err
		}
		return ps.Save()
	})
}

// Update changes one profile in the file as it is now, as Edit does.
func Update(path, name string, change func(p *Profile)) error {
	return Edit(path, func(ps *Profiles) error {
		p := ps.Profiles[name]
		if p == nil {
			return fmt.Errorf("no profile %q", name)
		}
		change(p)
		return nil
	})
}

// locked runs f holding the lock on the file beside the profiles.
func locked(path string, f func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lockFile(lock); err != nil {
		return fmt.Errorf("locking %s: %w", path, err)
	}
	defer unlockFile(lock)
	return f()
}

// TokenSource gives the bearer token of the profile name: its API token,
// or its OIDC access token. Once that token expires, it reads the file
// again and takes the token another process refreshed meanwhile, else
// refreshes it and saves it, under the file's lock: a provider that rotates
// refresh tokens refuses one already spent. Nil when the profile holds no
// credentials.
func (ps *Profiles) TokenSource(ctx context.Context, name string) oauth2.TokenSource {
	p := ps.Profiles[name]
	switch {
	case p == nil:
		return nil
	case p.Token != "":
		return StaticToken(p.Token)
	case p.OIDC == nil || p.OIDC.Token == nil:
		return nil
	}
	return &fileSource{ctx: ctx, path: ps.Path, name: name, server: p.Server, session: *p.OIDC, token: p.OIDC.Token}
}

// TokenSource gives the bearer token of a profile not saved yet: its API
// token, or its OIDC access token, refreshed when it expires and then
// handed to save. Nil when the profile holds no credentials.
func (p *Profile) TokenSource(ctx context.Context, save func(*oauth2.Token) error) oauth2.TokenSource {
	switch {
	case p.Token != "":
		return StaticToken(p.Token)
	case p.OIDC != nil && p.OIDC.Token != nil:
		return &savingSource{src: p.OIDC.config().TokenSource(ctx, p.OIDC.Token), last: p.OIDC.Token, save: save}
	}
	return nil
}

func (s *OIDCSession) config() *oauth2.Config {
	// rvav's client is public: no secret, its ID in the body (RFC 6749, 2.3.1).
	return &oauth2.Config{ClientID: s.ClientID, Scopes: s.Scopes,
		Endpoint: oauth2.Endpoint{TokenURL: s.TokenURL, AuthStyle: oauth2.AuthStyleInParams}}
}

// refreshError is a refresh the identity provider refused, which only
// signing in again mends, or one that didn't get an answer, which may work
// later (Transient).
func refreshError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < 500 {
		return fmt.Errorf("%w: %v", ErrSignInExpired, err)
	}
	return fmt.Errorf("refreshing the sign-in at the identity provider: %w", err)
}

type fileSource struct {
	ctx        context.Context
	path, name string
	server     string
	session    OIDCSession

	mu    sync.Mutex
	token *oauth2.Token
}

func (s *fileSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token.Valid() {
		return s.token, nil
	}
	err := locked(s.path, func() error {
		ps, err := LoadProfiles(s.path)
		if err != nil {
			return err
		}
		p := ps.Profiles[s.name]
		// A profile signed in again elsewhere, to another rvav or as
		// someone else, isn't this process's to follow.
		ours := p != nil && p.Server == s.server && p.OIDC != nil && p.OIDC.Token != nil &&
			p.OIDC.Issuer == s.session.Issuer && p.OIDC.ClientID == s.session.ClientID
		if ours {
			s.token = p.OIDC.Token
			if s.token.Valid() {
				return nil
			}
		}
		t, err := s.session.config().TokenSource(s.ctx, s.token).Token()
		if err != nil {
			return refreshError(err)
		}
		s.token = t
		if !ours {
			return nil
		}
		p.OIDC.Token = t
		if err := ps.Save(); err != nil {
			return fmt.Errorf("saving the refreshed sign-in: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.token, nil
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
		return nil, refreshError(err)
	}
	if t != s.last && s.save != nil {
		if err := s.save(t); err != nil {
			return nil, fmt.Errorf("saving the refreshed sign-in: %w", err)
		}
	}
	s.last = t
	return t, nil
}
