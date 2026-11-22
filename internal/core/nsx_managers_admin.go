package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/nsxmanagers"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/trust"
)

// Several NSX Managers (ADR-0062): administrators add NSX Managers besides
// the NSX integration's, the first, and link each vCenter to one; their
// passwords are sealed.

// primaryNSXName is how the NSX integration's Manager is known in the API:
// its host name; "" when it has none.
func (s *Service) primaryNSXName() string {
	if !s.Config.NSX.Configured() {
		return ""
	}
	u, err := url.Parse(s.Config.NSX.URL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// vcenterOfKey is a vCenter's name in the API from its key in vcenter_nsx,
// where the vCenter integration's is ""; false when that one isn't set.
func (s *Service) vcenterOfKey(key string) (string, bool) {
	if key != "" {
		return key, true
	}
	return s.Config.VCenter.Name, s.Config.VCenter.URL != ""
}

func (s *Service) presentNSXManager(m store.NSXManager, links map[string]string, key string) api.NsxManager {
	out := api.NsxManager{Name: m.Name, Url: m.URL, Username: m.Username, CertFile: nonEmpty(m.CertFile), KeyFile: nonEmpty(m.KeyFile),
		Insecure: m.Insecure, CertSha256: nonEmpty(m.CertSHA256), PasswordSet: len(m.Ciphertext) > 0 && m.CertFile == "", Vcenters: []string{}}
	for vc, mgr := range links {
		if name, ok := s.vcenterOfKey(vc); ok && mgr == key {
			out.Vcenters = append(out.Vcenters, name)
		}
	}
	primary := s.Config.VCenter.Name
	slices.SortFunc(out.Vcenters, func(a, b string) int {
		switch {
		case a == primary:
			return -1
		case b == primary:
			return 1
		}
		return strings.Compare(a, b)
	})
	return out
}

// presentPrimaryNSX is the NSX integration's Manager as the API lists it.
func (s *Service) presentPrimaryNSX(links map[string]string) api.NsxManager {
	c := s.Config.NSX
	out := s.presentNSXManager(store.NSXManager{Name: s.primaryNSXName(), URL: c.URL, Username: c.Username, CertFile: c.CertFile,
		KeyFile: c.KeyFile, Insecure: c.Insecure, CertSHA256: c.CertSHA256}, links, "")
	out.Primary, out.PasswordSet = true, c.Password != ""
	return out
}

// ListNSXManagers lists the NSX integration's Manager, then those added,
// each with the vCenters it serves.
func (s *Service) ListNSXManagers(ctx context.Context, p *auth.Principal) (api.NsxManagerList, error) {
	out := api.NsxManagerList{Items: []api.NsxManager{}}
	if !p.Everywhere(auth.InfrastructureRead) {
		return out, ErrForbidden
	}
	links, err := s.Store.VCenterNSX(ctx)
	if err != nil {
		return out, err
	}
	if s.Config.NSX.Configured() {
		out.Items = append(out.Items, s.presentPrimaryNSX(links))
	}
	rows, err := s.Store.NSXManagers(ctx)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Items = append(out.Items, s.presentNSXManager(r, links, r.Name))
	}
	return out, nil
}

// linkedVCenters are the vCenters named, by their key in vcenter_nsx, or
// what is wrong with them.
func (s *Service) linkedVCenters(ctx context.Context, names []string, at string) ([]string, []api.FieldError) {
	var keys []string
	var errs []api.FieldError
	for i, n := range names {
		n = strings.TrimSpace(n)
		key := n
		if primary, ok := s.vcenterOfKey(""); ok && n == primary {
			key = ""
		} else if _, err := s.Store.GetVCenter(ctx, n); err != nil {
			errs = append(errs, fieldErr(fmt.Sprintf("%s/%d", at, i), "unknown", "rvav manages no vCenter "+n+"."))
			continue
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys, errs
}

// checkNSXManager checks what is written, against the Manager changed (nil
// when added): a password goes only where it was given for.
func (s *Service) checkNSXManager(w api.NsxManagerWrite, current *store.NSXManager) []api.FieldError {
	var errs []api.FieldError
	if !vcenterName.MatchString(w.Name) {
		errs = append(errs, fieldErr("/name", "pattern", "Its host name in lower case, for example nsx02.corp.example."))
	} else if w.Name == s.primaryNSXName() {
		errs = append(errs, fieldErr("/name", "duplicate", w.Name+" is the NSX integration's: change it in Integrations."))
	}
	if u, err := url.Parse(w.Url); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fieldErr("/url", "invalid", "Its address over HTTPS, for example https://nsx02.corp.example."))
	}
	pin, pinOK := trust.NormalFingerprint(deref(w.CertSha256))
	if deref(w.CertSha256) != "" && !pinOK {
		errs = append(errs, fieldErr("/cert_sha256", "invalid", "A certificate's SHA-256: 32 bytes in hex, with or without colons."))
	}
	insecure := w.Insecure != nil && *w.Insecure
	moved := current != nil && (w.Url != current.URL || insecure && !current.Insecure || pin != "" && pin != current.CertSHA256)
	certFile, keyFile := strings.TrimSpace(deref(w.CertFile)), strings.TrimSpace(deref(w.KeyFile))
	switch {
	case certFile != "" && keyFile == "":
		errs = append(errs, fieldErr("/key_file", "required", "Give the certificate's private key too."))
	case keyFile != "" && certFile == "":
		errs = append(errs, fieldErr("/cert_file", "required", "Give the certificate this private key goes with."))
	case certFile != "":
		return errs // a principal identity: no account, and the password saved is dropped
	}
	if strings.TrimSpace(deref(w.Username)) == "" {
		errs = append(errs, fieldErr("/username", "required", "Give the account rvav signs in with, or a principal-identity certificate."))
	}
	given := w.Password != nil && *w.Password != ""
	// One saved before a principal identity is gone: the address it was
	// given for may have changed since.
	saved := current != nil && len(current.Ciphertext) > 0 && current.CertFile == ""
	switch {
	case !saved && !given:
		errs = append(errs, fieldErr("/password", "required", "Give the account's password."))
	case !given && moved:
		errs = append(errs, fieldErr("/password", "required",
			"A new address, a certificate pinned, or the certificate not checked, needs the password again: the saved one only goes where it was given for."))
	}
	return errs
}

// SaveNSXManager adds an NSX Manager (name "") or changes one.
func (s *Service) SaveNSXManager(ctx context.Context, p *auth.Principal, name string, w api.NsxManagerWrite) (api.NsxManager, error) {
	if !p.Everywhere(auth.IntegrationsManage) {
		return api.NsxManager{}, ErrForbidden
	}
	create := name == ""
	var current *store.NSXManager
	if !create {
		if name == s.primaryNSXName() {
			return api.NsxManager{}, &ConflictError{Msg: name + " is the NSX integration's: change it in Integrations."}
		}
		v, err := s.Store.GetNSXManager(ctx, name)
		if err != nil {
			return api.NsxManager{}, err
		}
		current, w.Name = &v, name
	}
	errs := s.checkNSXManager(w, current)
	var links []string
	if w.Vcenters != nil {
		var bad []api.FieldError
		links, bad = s.linkedVCenters(ctx, *w.Vcenters, "/vcenters")
		errs = append(errs, bad...)
	}
	if len(errs) > 0 {
		return api.NsxManager{}, &ValidationError{Errors: errs}
	}
	v := store.NSXManager{Name: w.Name, URL: w.Url, Username: strings.TrimSpace(deref(w.Username)),
		CertFile: strings.TrimSpace(deref(w.CertFile)), KeyFile: strings.TrimSpace(deref(w.KeyFile)), Insecure: w.Insecure != nil && *w.Insecure}
	v.CertSHA256, _ = trust.NormalFingerprint(deref(w.CertSha256))
	if w.Password != nil && *w.Password != "" && v.CertFile == "" {
		if s.Secrets == nil {
			return api.NsxManager{}, &ConflictError{Msg: "rvav has no secrets key (RVAV_SECRETS_KEY) to keep the password."}
		}
		var err error
		if v.KeyID, v.Nonce, v.Ciphertext, err = s.Secrets.Seal(nsxmanagers.SecretName(v.Name), []byte(*w.Password)); err != nil {
			return api.NsxManager{}, err
		}
	}
	err := s.Store.SaveNSXManager(ctx, v, create)
	if errors.Is(err, store.ErrInUse) {
		return api.NsxManager{}, &ConflictError{Msg: "rvav already manages an NSX Manager named " + v.Name + "."}
	}
	if err != nil {
		return api.NsxManager{}, err
	}
	if w.Vcenters != nil {
		if err := s.Store.LinkVCenters(ctx, v.Name, links); err != nil {
			return api.NsxManager{}, err
		}
	}
	action := "nsx_manager.update"
	if create {
		action = "nsx_manager.create"
	}
	saved, err := s.Store.GetNSXManager(ctx, v.Name)
	if err != nil {
		return api.NsxManager{}, err
	}
	all, err := s.Store.VCenterNSX(ctx)
	if err != nil {
		return api.NsxManager{}, err
	}
	out := s.presentNSXManager(saved, all, saved.Name)
	s.audit(ctx, p, action, "nsx_manager", v.Name, nil, map[string]any{"url": v.URL, "username": v.Username, "cert_file": v.CertFile,
		"key_file": v.KeyFile, "insecure": v.Insecure, "cert_sha256": v.CertSHA256, "vcenters": out.Vcenters})
	return out, nil
}

// SetNSXManagerVCenters makes an NSX Manager, the integration's too, serve
// these vCenters; one another served moves.
func (s *Service) SetNSXManagerVCenters(ctx context.Context, p *auth.Principal, name string, w api.NsxManagerVCenters) (api.NsxManager, error) {
	if !p.Everywhere(auth.IntegrationsManage) {
		return api.NsxManager{}, ErrForbidden
	}
	key := name
	var row store.NSXManager
	if name != "" && name == s.primaryNSXName() {
		key = ""
	} else {
		var err error
		if row, err = s.Store.GetNSXManager(ctx, name); err != nil {
			return api.NsxManager{}, err
		}
	}
	links, errs := s.linkedVCenters(ctx, w.Vcenters, "/vcenters")
	if len(errs) > 0 {
		return api.NsxManager{}, &ValidationError{Errors: errs}
	}
	if err := s.Store.LinkVCenters(ctx, key, links); err != nil {
		return api.NsxManager{}, err
	}
	all, err := s.Store.VCenterNSX(ctx)
	if err != nil {
		return api.NsxManager{}, err
	}
	out := s.presentNSXManager(row, all, key)
	if key == "" {
		out = s.presentPrimaryNSX(all)
	}
	s.audit(ctx, p, "nsx_manager.link", "nsx_manager", name, nil, map[string]any{"vcenters": out.Vcenters})
	return out, nil
}

// DeleteNSXManager stops managing an added NSX Manager; not while a
// deployment has a segment or a group there.
func (s *Service) DeleteNSXManager(ctx context.Context, p *auth.Principal, name string) error {
	if !p.Everywhere(auth.IntegrationsManage) {
		return ErrForbidden
	}
	if name != "" && name == s.primaryNSXName() {
		return &ConflictError{Msg: name + " is the NSX integration's: clear it in Integrations."}
	}
	err := s.Store.DeleteNSXManager(ctx, name)
	if errors.Is(err, store.ErrInUse) {
		return &ConflictError{Msg: "Deployments have NSX segments or security groups on " + name + ": delete them, or keep the NSX Manager."}
	}
	if err == nil {
		s.audit(ctx, p, "nsx_manager.delete", "nsx_manager", name, nil, nil)
	}
	return err
}

// TestNSXManager signs in to an NSX Manager and reads what rvav uses, with
// its saved settings or, for an added one, those given.
func (s *Service) TestNSXManager(ctx context.Context, p *auth.Principal, name string, w *api.NsxManagerWrite) (api.IntegrationTest, error) {
	out := api.IntegrationTest{Checks: []api.IntegrationCheck{}}
	if !p.Everywhere(auth.IntegrationsManage) {
		return out, ErrForbidden
	}
	var c config.NSXConfig
	if name != "" && name == s.primaryNSXName() {
		if w != nil {
			return out, &ConflictError{Msg: name + " is the NSX integration's: test what you change there."}
		}
		c = s.Config.NSX
	} else {
		current, err := s.Store.GetNSXManager(ctx, name)
		switch {
		case err == nil:
			c = config.NSXConfig{URL: current.URL, Username: current.Username, CertFile: current.CertFile, KeyFile: current.KeyFile,
				Insecure: current.Insecure, CertSHA256: current.CertSHA256}
			if pw, err := nsxmanagers.Password(s.Secrets, current); err == nil {
				c.Password = pw
			}
		case errors.Is(err, store.ErrNotFound) && w != nil:
		default:
			return out, err
		}
		if w != nil {
			w.Name = name
			var cur *store.NSXManager
			if err == nil {
				cur = &current
			}
			if errs := s.checkNSXManager(*w, cur); len(errs) > 0 {
				return out, &ValidationError{Errors: errs}
			}
			c.URL, c.Username, c.Insecure = w.Url, strings.TrimSpace(deref(w.Username)), w.Insecure != nil && *w.Insecure
			c.CertFile, c.KeyFile = strings.TrimSpace(deref(w.CertFile)), strings.TrimSpace(deref(w.KeyFile))
			c.CertSHA256, _ = trust.NormalFingerprint(deref(w.CertSha256))
			if w.Password != nil && *w.Password != "" {
				c.Password = *w.Password
			}
			if c.CertFile != "" {
				c.Password = ""
			}
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
	stored, err := s.Store.StoredIntegrations(ctx)
	if err != nil {
		return out, err
	}
	roots, _ := s.pendingRoots(ctx, stored)
	o := c.TLS()
	o.Roots = roots
	ok := true
	if u, _, _ := certificateOf("nsx", config.Config{NSX: c}, nil); u != "" {
		out.Certificate, ok = s.checkCertificate(ctx, u, o, true, check)
	}
	if ok {
		s.testNSX(ctx, c, roots, check)
	}
	out.Ok = len(out.Checks) > 0
	for _, ch := range out.Checks {
		out.Ok = out.Ok && ch.Ok
	}
	return out, nil
}
