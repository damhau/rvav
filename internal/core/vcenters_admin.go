package core

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vcenters"
)

// Several vCenters (ADR-0024): administrators add vCenters besides the
// vCenter integration's, the first; their passwords are sealed.

var vcenterName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,62}$`)

func presentVCenter(v store.VCenter, inv map[string]store.VCenterInventory) api.VCenter {
	out := api.VCenter{Name: v.Name, Url: v.URL, Username: v.Username, Insecure: v.Insecure, PasswordSet: len(v.Ciphertext) > 0}
	if i, ok := inv[v.Name]; ok {
		out.Inventory = &struct {
			Clusters *int       `json:"clusters,omitempty"`
			SyncedAt *time.Time `json:"synced_at,omitempty"`
			Vms      *int       `json:"vms,omitempty"`
		}{Clusters: present.Ptr(i.Clusters), Vms: present.Ptr(i.VMs), SyncedAt: i.SyncedAt}
	}
	return out
}

// ListVCenters lists the vCenter integration's vCenter, then those added.
func (s *Service) ListVCenters(ctx context.Context, p *auth.Principal) (api.VCenterList, error) {
	out := api.VCenterList{Items: []api.VCenter{}}
	if !p.SeesEverything() {
		return out, ErrForbidden
	}
	inv, err := s.Store.VCenterInventories(ctx)
	if err != nil {
		return out, err
	}
	if c := s.Config.VCenter; c.URL != "" {
		primary := presentVCenter(store.VCenter{Name: c.Name, URL: c.URL, Username: c.Username, Insecure: c.Insecure}, inv)
		primary.Primary, primary.PasswordSet = true, c.Password != ""
		out.Items = append(out.Items, primary)
	}
	rows, err := s.Store.VCenters(ctx)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Items = append(out.Items, presentVCenter(r, inv))
	}
	return out, nil
}

// checkVCenter checks what is written, against the vCenter changed (nil
// when added): a password goes only where it was given for.
func (s *Service) checkVCenter(w api.VCenterWrite, current *store.VCenter) []api.FieldError {
	var errs []api.FieldError
	if !vcenterName.MatchString(w.Name) {
		errs = append(errs, fieldErr("/name", "pattern", "Its host name in lower case, for example vc02.corp.example."))
	} else if w.Name == s.Config.VCenter.Name {
		errs = append(errs, fieldErr("/name", "duplicate", w.Name+" is the vCenter integration's: change it in Integrations."))
	}
	if u, err := url.Parse(w.Url); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fieldErr("/url", "invalid", "Its SDK address over HTTPS, for example https://vc02.corp.example/sdk."))
	}
	if strings.TrimSpace(w.Username) == "" {
		errs = append(errs, fieldErr("/username", "required", "Give the account rvav signs in with."))
	}
	given := w.Password != nil && *w.Password != ""
	insecure := w.Insecure != nil && *w.Insecure
	switch {
	case current == nil && !given:
		errs = append(errs, fieldErr("/password", "required", "Give the account's password."))
	case current != nil && !given && len(current.Ciphertext) > 0 && (w.Url != current.URL || insecure && !current.Insecure):
		errs = append(errs, fieldErr("/password", "required",
			"A new address, or the certificate not checked, needs the password again: the saved one only goes where it was given for."))
	}
	return errs
}

// SaveVCenter adds a vCenter (name "") or changes one.
func (s *Service) SaveVCenter(ctx context.Context, p *auth.Principal, name string, w api.VCenterWrite) (api.VCenter, error) {
	if !p.IsAdmin() {
		return api.VCenter{}, ErrForbidden
	}
	create := name == ""
	var current *store.VCenter
	if !create {
		v, err := s.Store.GetVCenter(ctx, name)
		if err != nil {
			return api.VCenter{}, err
		}
		current, w.Name = &v, name
	}
	if errs := s.checkVCenter(w, current); len(errs) > 0 {
		return api.VCenter{}, &ValidationError{Errors: errs}
	}
	v := store.VCenter{Name: w.Name, URL: w.Url, Username: strings.TrimSpace(w.Username), Insecure: w.Insecure != nil && *w.Insecure}
	if w.Password != nil && *w.Password != "" {
		if s.Secrets == nil {
			return api.VCenter{}, &ConflictError{Msg: "rvav has no secrets key (RVAV_SECRETS_KEY) to keep the password."}
		}
		var err error
		if v.KeyID, v.Nonce, v.Ciphertext, err = s.Secrets.Seal(vcenters.SecretName(v.Name), []byte(*w.Password)); err != nil {
			return api.VCenter{}, err
		}
	}
	err := s.Store.SaveVCenter(ctx, v, create)
	if errors.Is(err, store.ErrInUse) {
		return api.VCenter{}, &ConflictError{Msg: "rvav already manages a vCenter named " + v.Name + "."}
	}
	if err != nil {
		return api.VCenter{}, err
	}
	action := "vcenter.update"
	if create {
		action = "vcenter.create"
	}
	s.audit(ctx, p, action, "vcenter", v.Name, nil, map[string]any{"url": v.URL, "username": v.Username, "insecure": v.Insecure})
	saved, err := s.Store.GetVCenter(ctx, v.Name)
	if err != nil {
		return api.VCenter{}, err
	}
	inv, _ := s.Store.VCenterInventories(ctx)
	return presentVCenter(saved, inv), nil
}

// DeleteVCenter stops managing a vCenter and forgets its inventory; not
// while a deployment has a VM there.
func (s *Service) DeleteVCenter(ctx context.Context, p *auth.Principal, name string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	err := s.Store.DeleteVCenter(ctx, name)
	if errors.Is(err, store.ErrInUse) {
		return &ConflictError{Msg: "Deployments have VMs in " + name + ": delete them, or keep the vCenter."}
	}
	if err == nil {
		s.audit(ctx, p, "vcenter.delete", "vcenter", name, nil, nil)
	}
	return err
}

// TestVCenter signs in to a vCenter and reads its inventory, with its
// saved settings or those given.
func (s *Service) TestVCenter(ctx context.Context, p *auth.Principal, name string, w *api.VCenterWrite) (api.IntegrationTest, error) {
	out := api.IntegrationTest{Checks: []api.IntegrationCheck{}}
	if !p.IsAdmin() {
		return out, ErrForbidden
	}
	c := config.VCenterConfig{Name: name}
	current, err := s.Store.GetVCenter(ctx, name)
	switch {
	case err == nil:
		c.URL, c.Username, c.Insecure = current.URL, current.Username, current.Insecure
		if len(current.Ciphertext) > 0 && s.Secrets != nil {
			if plain, err := s.Secrets.Open(vcenters.SecretName(name), current.KeyID, current.Nonce, current.Ciphertext); err == nil {
				c.Password = string(plain)
			}
		}
	case errors.Is(err, store.ErrNotFound) && w != nil:
	default:
		return out, err
	}
	if w != nil {
		w.Name = name
		var cur *store.VCenter
		if err == nil {
			cur = &current
		}
		if errs := s.checkVCenter(*w, cur); len(errs) > 0 {
			return out, &ValidationError{Errors: errs}
		}
		c.URL, c.Username, c.Insecure = w.Url, w.Username, w.Insecure != nil && *w.Insecure
		if w.Password != nil && *w.Password != "" {
			c.Password = *w.Password
		}
	}
	check := func(name string, err error, ok string) bool {
		ch := api.IntegrationCheck{Name: name, Ok: err == nil, Message: ok}
		if err != nil {
			ch.Message = err.Error()
		}
		out.Checks = append(out.Checks, ch)
		return err == nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s.testVCenter(ctx, c, check)
	out.Ok = len(out.Checks) > 0
	for _, ch := range out.Checks {
		out.Ok = out.Ok && ch.Ok
	}
	return out, nil
}
