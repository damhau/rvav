package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

// The vCenter, AWX and EfficientIP connections, set from the UI (ADR-0020):
// environment variables win and lock their fields; saved settings take
// effect when rvav's processes restart; test connection tries the settings
// given, saved or not.

var integrationKinds = []string{"vcenter", "awx", "efficientip", "smtp", "teams", "slack"}

// storedSlack opens Slack's sealed secrets, both kept as one.
func (s *Service) storedSlack(row *store.IntegrationSettings) config.SlackSecrets {
	var sec config.SlackSecrets
	if row == nil || row.SecretCiphertext == nil {
		return sec
	}
	if plain, err := s.Secrets.Open(IntegrationSecretName("slack"), row.SecretKeyID, row.SecretNonce, row.SecretCiphertext); err == nil {
		_ = json.Unmarshal(plain, &sec)
	}
	return sec
}

// address is where an integration goes: its URL, or the mail server.
func address(kind string, settings map[string]any) string {
	switch kind {
	case "smtp":
		v, _ := settings["host"].(string)
		return v
	case "teams":
		// The secret is the app's, in its tenant.
		app, _ := settings["app_id"].(string)
		tenant, _ := settings["tenant_id"].(string)
		if app == "" && tenant == "" {
			return ""
		}
		return app + "@" + tenant
	}
	v, _ := settings["url"].(string)
	return v
}

// IntegrationSecretName is what an integration's sealed secret is bound to.
func IntegrationSecretName(kind string) string { return "integration:" + kind }

// pendingSettings are the settings saved for an integration over those this
// process uses: what rvav will use after a restart.
func (s *Service) pendingSettings(kind string, row *store.IntegrationSettings) map[string]any {
	out := s.Config.Settings(kind)
	if row == nil {
		return out
	}
	var saved map[string]any
	_ = json.Unmarshal(row.Settings, &saved)
	locked := config.Locked(kind)
	for k, v := range saved {
		if !slices.Contains(locked, k) {
			out[k] = v
		}
	}
	return out
}

func (s *Service) Integrations(ctx context.Context, p *auth.Principal) ([]api.Integration, error) {
	if !p.IsAdmin() {
		return nil, ErrForbidden
	}
	stored, err := s.Store.StoredIntegrations(ctx)
	if err != nil {
		return nil, err
	}
	out := []api.Integration{}
	for _, kind := range integrationKinds {
		var row *store.IntegrationSettings
		if r, ok := stored[kind]; ok {
			row = &r
		}
		settings := s.pendingSettings(kind, row)
		it := api.Integration{Kind: api.IntegrationKind(kind), Settings: settings, Locked: nonNilStrings(config.Locked(kind)),
			Configured: address(kind, settings) != "" || config.Fake(kind), SecretSet: s.Config.SecretSet(kind) || row != nil && row.SecretCiphertext != nil}
		if kind == "slack" {
			sec := s.storedSlack(row)
			it.SecretSet = s.Config.Slack.BotToken != "" || sec.BotToken != ""
			it.SigningSecretSet = new(s.Config.Slack.SigningSecret != "" || sec.SigningSecret != "")
			it.Configured = it.SecretSet
		}
		if row != nil {
			it.RestartNeeded = row.Revision > s.SettingsRevision
			it.UpdatedAt = &row.UpdatedAt
			if row.UpdatedBy != nil {
				it.UpdatedBy = &api.UserRef{Id: *row.UpdatedBy, Name: deref(row.UpdatedByName)}
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func checkIntegrationKind(kind string) error {
	if !slices.Contains(integrationKinds, kind) {
		return ErrNotFound
	}
	return nil
}

// SetIntegration saves an integration's settings; fields an environment
// variable sets are left out. The secret, when given, is sealed.
func (s *Service) SetIntegration(ctx context.Context, p *auth.Principal, kind string, w api.IntegrationWrite) (api.Integration, error) {
	if !p.IsAdmin() {
		return api.Integration{}, ErrForbidden
	}
	if err := checkIntegrationKind(kind); err != nil {
		return api.Integration{}, err
	}
	locked := config.Locked(kind)
	settings := map[string]any{}
	for k, v := range w.Settings {
		if !slices.Contains(locked, k) && k != "fake" {
			settings[k] = v
		}
	}
	var errs []api.FieldError
	for field, msg := range config.CheckSettings(kind, settings) {
		errs = append(errs, fieldErr("/settings/"+field, "invalid", msg))
	}
	if len(errs) > 0 {
		return api.Integration{}, &ValidationError{Errors: errs}
	}
	stored, err := s.Store.StoredIntegrations(ctx)
	if err != nil {
		return api.Integration{}, err
	}
	var row *store.IntegrationSettings
	if r, ok := stored[kind]; ok {
		row = &r
	}
	hasSecret := s.Config.SecretSet(kind) || row != nil && row.SecretCiphertext != nil
	if err := secretGoesWhereGiven(kind, s.pendingSettings(kind, row), settings, w.Secret != nil && *w.Secret != "", hasSecret); err != nil {
		return api.Integration{}, err
	}
	var sealed *store.Sealed
	given := func(v *string) bool { return v != nil && *v != "" }
	if given(w.Secret) || kind == "slack" && given(w.SigningSecret) {
		var plain []byte
		if kind == "slack" {
			// The one given replaces its half; the other is kept.
			sec := s.storedSlack(row)
			if given(w.Secret) {
				sec.BotToken = *w.Secret
			}
			if given(w.SigningSecret) {
				sec.SigningSecret = *w.SigningSecret
			}
			plain, _ = json.Marshal(sec)
		} else {
			plain = []byte(*w.Secret)
		}
		keyID, nonce, ct, err := s.Secrets.Seal(IntegrationSecretName(kind), plain)
		if errors.Is(err, secrets.ErrNoKey) {
			return api.Integration{}, &ConflictError{Msg: "RVAV_SECRETS_KEY is not set, so rvav can't store the password or token."}
		} else if err != nil {
			return api.Integration{}, err
		}
		sealed = &store.Sealed{KeyID: keyID, Nonce: nonce, Ciphertext: ct}
	}
	raw, _ := json.Marshal(settings)
	if _, err := s.Store.SaveIntegration(ctx, kind, raw, sealed, p.User.ID); err != nil {
		return api.Integration{}, err
	}
	s.audit(ctx, p, "settings.integration."+kind, "integration", kind, nil, map[string]any{"settings": settings, "secret_changed": sealed != nil})
	list, err := s.Integrations(ctx, p)
	if err != nil {
		return api.Integration{}, err
	}
	for _, it := range list {
		if string(it.Kind) == kind {
			return it, nil
		}
	}
	return api.Integration{}, ErrNotFound
}

// TestIntegration connects with the settings given over the saved ones
// (environment variables still win), and says which checks pass.
func (s *Service) TestIntegration(ctx context.Context, p *auth.Principal, kind string, w *api.IntegrationWrite) (api.IntegrationTest, error) {
	out := api.IntegrationTest{Checks: []api.IntegrationCheck{}}
	if !p.IsAdmin() {
		return out, ErrForbidden
	}
	if err := checkIntegrationKind(kind); err != nil {
		return out, err
	}
	stored, err := s.Store.StoredIntegrations(ctx)
	if err != nil {
		return out, err
	}
	var row *store.IntegrationSettings
	if r, ok := stored[kind]; ok {
		row = &r
	}
	settings := s.pendingSettings(kind, row)
	secret := ""
	if row != nil && row.SecretCiphertext != nil {
		if plain, err := s.Secrets.Open(IntegrationSecretName(kind), row.SecretKeyID, row.SecretNonce, row.SecretCiphertext); err == nil {
			secret = string(plain)
		}
	}
	if w != nil {
		for field, msg := range config.CheckSettings(kind, withoutFake(w.Settings)) {
			return out, &ValidationError{Errors: []api.FieldError{fieldErr("/settings/"+field, "invalid", msg)}}
		}
		given := w.Secret != nil && *w.Secret != ""
		if err := secretGoesWhereGiven(kind, settings, w.Settings, given, secret != "" || s.Config.SecretSet(kind)); err != nil {
			return out, err
		}
		for k, v := range w.Settings {
			settings[k] = v
		}
		switch {
		case kind == "slack":
			sec := s.storedSlack(row)
			if given {
				sec.BotToken = *w.Secret
			}
			if w.SigningSecret != nil && *w.SigningSecret != "" {
				sec.SigningSecret = *w.SigningSecret
			}
			b, _ := json.Marshal(sec)
			secret = string(b)
		case given:
			secret = *w.Secret
		}
	}
	cfg := s.Config
	cfg.ApplyStored(kind, config.Stored{Settings: settings, Secret: secret})
	check := func(name string, err error, ok string) bool {
		c := api.IntegrationCheck{Name: name, Ok: err == nil, Message: ok}
		if err != nil {
			c.Message = err.Error()
		}
		out.Checks = append(out.Checks, c)
		return err == nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch kind {
	case "vcenter":
		s.testVCenter(ctx, cfg.VCenter, check)
	case "awx":
		s.testAWX(ctx, cfg.AWX, check)
	case "efficientip":
		s.testEfficientIP(ctx, cfg.IPAM, check)
	case "smtp":
		if cfg.SMTP.Host == "" {
			check("Mail server", errors.New("no SMTP host is set"), "")
			break
		}
		check("Connect and sign in", (&notify.Email{Config: cfg.SMTP}).Test(ctx),
			fmt.Sprintf("%s:%d accepted the connection", cfg.SMTP.Host, cfg.SMTP.Port))
	case "slack":
		if cfg.Slack.BotToken == "" {
			check("Bot token", errors.New("no bot token is set"), "")
			break
		}
		team, err := (&notify.Slack{Config: cfg.Slack}).Test(ctx)
		check("Sign in as the bot", err, "Slack accepted the bot token for "+team)
		if cfg.Slack.SigningSecret == "" {
			check("Signing secret", errors.New("no signing secret is set: Approve and Reject can't be checked"), "")
		}
	case "teams":
		if cfg.Teams.AppID == "" || cfg.Teams.TenantID == "" {
			check("Bot app", errors.New("the app ID and tenant ID are needed"), "")
			break
		}
		check("Sign in as the bot", (&notify.Teams{Config: cfg.Teams}).Test(ctx), "Microsoft accepted the app's credentials")
	}
	out.Ok = !slices.ContainsFunc(out.Checks, func(c api.IntegrationCheck) bool { return !c.Ok })
	s.audit(ctx, p, "settings.test."+kind, "integration", kind, nil, map[string]any{"ok": out.Ok})
	return out, nil
}

func withoutFake(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if k != "fake" {
			out[k] = v
		}
	}
	return out
}

// secretGoesWhereGiven refuses settings that would send the saved password
// or token to another address (a new URL, or TLS checks turned off) without
// it being given again, so a secret never reaches a host it wasn't entered
// for. One set by an environment variable goes only to the address set with
// it.
func secretGoesWhereGiven(kind string, current, given map[string]any, newSecret bool, hasSecret bool) error {
	moved := false
	if u := address(kind, given); u != "" {
		moved = u != address(kind, current)
	}
	if v, ok := given["insecure"].(bool); ok && v && current["insecure"] != true {
		moved = true
	}
	if !moved || !hasSecret {
		return nil
	}
	for _, f := range config.Fields[kind] {
		if f.Secret && slices.Contains(config.Locked(kind), f.Name) {
			return &ValidationError{Errors: []api.FieldError{fieldErr("/settings/url", "locked_secret",
				"The "+f.Name+" is set by "+f.Env+": it only goes to the address set with it, so set this one there too.")}}
		}
	}
	if newSecret {
		return nil
	}
	return &ValidationError{Errors: []api.FieldError{fieldErr("/secret", "required",
		"A new address, or TLS checks turned off, needs the password or token again: the saved one only goes where it was given for.")}}
}

type checkFunc func(name string, err error, ok string) bool

func (s *Service) testVCenter(ctx context.Context, c config.VCenterConfig, check checkFunc) {
	if c.URL == "" {
		check("Settings", errors.New("give vCenter's URL"), "")
		return
	}
	vc, err := vsphere.New(ctx, c.Name, c.URL, c.Username, c.Password, c.Insecure)
	if !check("Sign in", err, "Signed in to "+c.URL+" as "+c.Username+".") {
		return
	}
	inv, err := vc.Inventory(ctx)
	if !check("Inventory", err, fmt.Sprintf("%d clusters, %d datastores, %d networks and %d templates.",
		len(safeInv(inv).Clusters), len(safeInv(inv).Datastores), len(safeInv(inv).Networks), len(safeInv(inv).Templates))) {
		return
	}
	check("Tagging service", inv.TagsError, "Tags can be read, for placement by tag.")
}

func safeInv(inv *vsphere.Inventory) *vsphere.Inventory {
	if inv == nil {
		return &vsphere.Inventory{}
	}
	return inv
}

func (s *Service) testAWX(ctx context.Context, c config.AWXConfig, check checkFunc) {
	if c.Fake {
		check("Fake", nil, "This rvav uses the fake AWX (RVAV_FAKE_AWX=1): jobs are simulated.")
		return
	}
	if c.URL == "" || c.Organization == "" {
		check("Settings", errors.New("give AWX's URL and rvav's organisation"), "")
		return
	}
	client, err := awx.NewHTTPClient(c.URL, c.Token, c.Insecure)
	if !check("Connection", err, "") {
		return
	}
	client.Organization = c.Organization
	inventories, err := client.Inventories(ctx, c.Organization)
	if !check("Sign in and organisation", err, fmt.Sprintf("Signed in; %s has %d inventories.", c.Organization, len(inventories))) {
		return
	}
	templates, err := client.Templates(ctx, awx.TemplateFilter{Organization: c.Organization, Labels: c.CatalogLabels})
	check("Catalog templates", err, fmt.Sprintf("%d templates with the label %v.", len(templates), c.CatalogLabels))
	if c.InventorySourceID == 0 {
		check("Inventory source", errors.New("not set: jobs on rvav's VMs will be refused"), "")
	}
}

func (s *Service) testEfficientIP(ctx context.Context, c config.EfficientIPConfig, check checkFunc) {
	if c.Fake {
		check("Fake", nil, "This rvav uses a fake SOLIDserver in its own process (RVAV_FAKE_EFFICIENTIP=1).")
		return
	}
	if c.URL == "" {
		check("Settings", errors.New("give the SOLIDserver's URL"), "")
		return
	}
	client, err := efficientip.New(efficientip.Options{URL: c.URL, Auth: efficientip.Auth(c.Auth), Username: c.Username,
		Password: c.Password, Insecure: c.Insecure, Space: c.Space, DNSServer: c.DNSServer, DNSView: c.DNSView, Instance: c.Instance})
	if !check("Settings", err, "") {
		return
	}
	subnets, err := client.Subnets(ctx)
	if !check("Sign in and IPAM", err, fmt.Sprintf("Signed in; %d terminal subnets.", len(subnets))) {
		return
	}
	zones, err := client.Zones(ctx)
	check("DNS", err, fmt.Sprintf("%d forward zones on %s.", len(zones), c.DNSServer))
}

// RotateSecrets seals every stored secret again with the current key, so
// RVAV_SECRETS_OLD_KEYS can be emptied afterwards (rvav secrets rotate).
func RotateSecrets(ctx context.Context, st *store.Store, box *secrets.Box) (int, error) {
	if box == nil {
		return 0, secrets.ErrNoKey
	}
	n := 0
	list, err := st.Secrets(ctx)
	if err != nil {
		return 0, err
	}
	for _, v := range list {
		if v.KeyID == box.KeyID() {
			continue
		}
		plain, err := box.Open(v.Name, v.KeyID, v.Nonce, v.Ciphertext)
		if err != nil {
			return n, fmt.Errorf("secret %s: %w", v.Name, err)
		}
		keyID, nonce, ct, err := box.Seal(v.Name, plain)
		if err != nil {
			return n, err
		}
		// A secret replaced meanwhile is sealed with the current key already.
		if done, err := st.ResealSecret(ctx, v.Name, v.Nonce, keyID, nonce, ct); err != nil {
			return n, err
		} else if done {
			n++
		}
	}
	stored, err := st.StoredIntegrations(ctx)
	if err != nil {
		return n, err
	}
	for kind, row := range stored {
		if row.SecretCiphertext == nil || row.SecretKeyID == box.KeyID() {
			continue
		}
		name := IntegrationSecretName(kind)
		plain, err := box.Open(name, row.SecretKeyID, row.SecretNonce, row.SecretCiphertext)
		if err != nil {
			return n, fmt.Errorf("%s settings: %w", kind, err)
		}
		keyID, nonce, ct, err := box.Seal(name, plain)
		if err != nil {
			return n, err
		}
		if done, err := st.ResealIntegration(ctx, kind, row.SecretNonce, store.Sealed{KeyID: keyID, Nonce: nonce, Ciphertext: ct}); err != nil {
			return n, err
		} else if done {
			n++
		}
	}
	return n, nil
}
