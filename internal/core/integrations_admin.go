package core

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/gitrepo"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/secretinput"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/trust"
	"github.com/damhau/rvav/internal/vra"
)

// Integrations set from the UI (ADR-0020, ADR-0030): environment variables
// win and lock their fields; saved settings apply on next use (LiveKinds) or
// as each process drains and starts again (app.Run); test connection tries
// the settings given, saved or not.

var integrationKinds = []string{"oidc", "vcenter", "console", "awx", "efficientip", "nsx", "git", "vra", "smtp", "teams", "slack", "hosts",
	"trust"}

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

// address is where an integration's secret goes: its URL, the mail
// server, the account; "" when it has none.
func address(kind string, settings map[string]any) string {
	str := func(k string) string { v, _ := settings[k].(string); return v }
	switch kind {
	case "console":
		// To the vCenter integration's address, as this account.
		return str("username")
	case "slack":
		return str("api_url")
	case "trust":
		// No secret: set when it holds a CA.
		return strings.TrimSpace(str("ca_bundle"))
	case "hosts":
		var out []string
		for _, k := range []string{"rest_source_hosts", "webhook_hosts"} {
			l, _ := settings[k].([]any)
			for _, x := range l {
				out = append(out, fmt.Sprint(x))
			}
			if l, ok := settings[k].([]string); ok {
				out = append(out, l...)
			}
		}
		return strings.Join(out, ",")
	case "oidc":
		v, _ := settings["issuer"].(string)
		return v
	case "smtp":
		v, _ := settings["host"].(string)
		return v
	case "teams":
		// The secret is the app's, in its tenant, sent to its Bot Connector.
		app, _ := settings["app_id"].(string)
		tenant, _ := settings["tenant_id"].(string)
		service, _ := settings["service_url"].(string)
		if app == "" && tenant == "" && service == "" {
			return ""
		}
		// The client secret goes to the login endpoint.
		return app + "@" + tenant + " " + service + " " + str("login_url")
	}
	v, _ := settings["url"].(string)
	return v
}

// IntegrationSecretName is what an integration's sealed secret is bound to.
func IntegrationSecretName(kind string) string { return "integration:" + kind }

// pendingSettings are the settings saved for an integration over those this
// process uses: what rvav's processes use once they have applied them, or
// use now for those read on next use.
func (s *Service) pendingSettings(ctx context.Context, kind string, row *store.IntegrationSettings) map[string]any {
	out := s.Config.Settings(kind)
	if s.Live != nil && slices.Contains(LiveKinds, kind) {
		out = s.Live.Config(ctx).Settings(kind)
	}
	if row == nil {
		return out
	}
	var saved map[string]any
	_ = json.Unmarshal(row.Settings, &saved)
	locked := config.Locked(kind)
	for k, v := range saved {
		if !slices.Contains(locked, k) && k != config.ConsoleForKey {
			out[k] = v
		}
	}
	return out
}

// consoleSecretSet says whether the console's saved password is used: only
// with the vCenter connection it was given for.
func (s *Service) consoleSecretSet(ctx context.Context, row *store.IntegrationSettings) bool {
	if row == nil || row.SecretCiphertext == nil {
		return false
	}
	var saved map[string]any
	_ = json.Unmarshal(row.Settings, &saved)
	vc := s.liveConfig(ctx).VCenter
	return vc.URL != "" && saved[config.ConsoleForKey] == config.ConsoleFor(vc)
}

func (s *Service) Integrations(ctx context.Context, p *auth.Principal) ([]api.Integration, error) {
	if !p.IsAdmin() {
		return nil, ErrForbidden
	}
	stored, err := s.Store.StoredIntegrations(ctx)
	if err != nil {
		return nil, err
	}
	procs, err := s.Store.Processes(ctx, time.Now().Add(-ProcessesSeen))
	if err != nil {
		return nil, err
	}
	out := []api.Integration{}
	for _, kind := range integrationKinds {
		var row *store.IntegrationSettings
		if r, ok := stored[kind]; ok {
			row = &r
		}
		settings := s.pendingSettings(ctx, kind, row)
		it := api.Integration{Kind: api.IntegrationKind(kind), Settings: settings, Locked: nonNilStrings(config.Locked(kind)),
			Configured: address(kind, settings) != "" || config.Fake(kind), SecretSet: s.Config.SecretSet(kind) || row != nil && row.SecretCiphertext != nil}
		if kind == "console" {
			it.SecretSet = s.Config.SecretSet(kind) || s.consoleSecretSet(ctx, row)
		}
		if kind == "slack" {
			sec := s.storedSlack(row)
			it.SecretSet = s.Config.Slack.BotToken != "" || sec.BotToken != ""
			it.SigningSecretSet = new(s.Config.Slack.SigningSecret != "" || sec.SigningSecret != "")
			it.Configured = it.SecretSet
		}
		if row != nil {
			it.Revision = row.Revision
			it.Applying = !slices.Contains(LiveKinds, kind) && slices.ContainsFunc(procs, func(p store.Process) bool { return p.Revision < row.Revision })
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
	if err := noSecretFor(kind, &w); err != nil {
		return api.Integration{}, err
	}
	locked := config.Locked(kind)
	settings := map[string]any{}
	for k, v := range w.Settings {
		if !slices.Contains(locked, k) && k != "fake" && k != config.ConsoleForKey {
			settings[k] = v
		}
	}
	var errs []api.FieldError
	bad := config.CheckSettings(kind, settings)
	if u, ok := settings["url"].(string); ok && u != "" && kind == "git" {
		if err := gitrepo.CheckURL(u); err != nil {
			msg := err.Error()
			bad["url"] = strings.ToUpper(msg[:1]) + msg[1:] + "."
		}
	}
	for field, msg := range bad {
		errs = append(errs, fieldErr("/settings/"+field, "invalid", msg))
	}
	if len(errs) > 0 {
		return api.Integration{}, &ValidationError{Errors: errs}
	}
	if v, ok := settings["cert_sha256"].(string); ok && v != "" {
		settings["cert_sha256"], _ = trust.NormalFingerprint(v)
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
	if kind == "console" {
		// The password goes to the vCenter it is given with; one saved
		// earlier keeps the vCenter it was given for.
		if w.Secret != nil && *w.Secret != "" {
			settings[config.ConsoleForKey] = config.ConsoleFor(s.liveConfig(ctx).VCenter)
		} else if row != nil {
			var saved map[string]any
			_ = json.Unmarshal(row.Settings, &saved)
			if f, ok := saved[config.ConsoleForKey].(string); ok {
				settings[config.ConsoleForKey] = f
			}
		}
	}
	if err := secretGoesWhereGiven(kind, s.pendingSettings(ctx, kind, row), settings, w.Secret != nil && *w.Secret != "", hasSecret); err != nil {
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
	settings := s.pendingSettings(ctx, kind, row)
	if kind == "console" && row != nil {
		var saved map[string]any
		_ = json.Unmarshal(row.Settings, &saved)
		settings[config.ConsoleForKey] = saved[config.ConsoleForKey]
	}
	secret := ""
	if row != nil && row.SecretCiphertext != nil {
		if plain, err := s.Secrets.Open(IntegrationSecretName(kind), row.SecretKeyID, row.SecretNonce, row.SecretCiphertext); err == nil {
			secret = string(plain)
		}
	}
	if w != nil {
		if err := noSecretFor(kind, w); err != nil {
			return out, err
		}
		for field, msg := range config.CheckSettings(kind, withoutFake(w.Settings)) {
			return out, &ValidationError{Errors: []api.FieldError{fieldErr("/settings/"+field, "invalid", msg)}}
		}
		given := w.Secret != nil && *w.Secret != ""
		if err := secretGoesWhereGiven(kind, settings, w.Settings, given, secret != "" || s.Config.SecretSet(kind)); err != nil {
			return out, err
		}
		if kind == "console" && given {
			settings[config.ConsoleForKey] = config.ConsoleFor(s.liveConfig(ctx).VCenter)
		}
		for k, v := range w.Settings {
			if k == config.ConsoleForKey {
				continue
			}
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
	if kind == "console" {
		cfg.VCenter = s.liveConfig(ctx).VCenter // the account signs in where vCenter's settings say
	}
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
	// The CAs rvav's processes trust once they apply the CA bundle saved,
	// or the one given.
	roots, bundle := s.pendingRoots(ctx, stored)
	if kind == "trust" {
		roots, bundle = rootsOf(cfg.CABundle), cfg.CABundle
	}
	if u, o, pinnable := certificateOf(kind, cfg, settings); u != "" {
		o.Roots = roots
		cert, ok := s.checkCertificate(ctx, u, o, pinnable, check)
		out.Certificate = cert
		if !ok {
			out.Ok = false
			s.audit(ctx, p, "settings.test."+kind, "integration", kind, nil, map[string]any{"ok": false})
			return out, nil
		}
	}
	switch kind {
	case "oidc":
		s.testOIDC(ctx, settings, roots, check)
	case "vcenter":
		s.testVCenter(ctx, cfg.VCenter, roots, check)
	case "console":
		testConsole(ctx, cfg, roots, check)
	case "vra":
		testVRA(ctx, cfg.VRA, roots, check)
	case "trust":
		testTrust(cfg.CABundle, check)
	case "hosts":
		check("Hosts", nil, fmt.Sprintf("REST value sources may call %s; webhooks %s.",
			hostList(cfg.RESTSourceHosts), hostList(cfg.WebhookHosts)))
	case "awx":
		s.testAWX(ctx, cfg.AWX, roots, check)
	case "efficientip":
		s.testEfficientIP(ctx, cfg.IPAM, roots, check)
	case "nsx":
		s.testNSX(ctx, cfg.NSX, roots, check)
	case "git":
		testGit(ctx, cfg.Git, bundle, check)
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

// noSecretFor refuses a secret for an integration that has none: the
// identity provider's web app is a public client.
func noSecretFor(kind string, w *api.IntegrationWrite) error {
	if kind != "oidc" || (w.Secret == nil || *w.Secret == "") && (w.SigningSecret == nil || *w.SigningSecret == "") {
		return nil
	}
	return &ValidationError{Errors: []api.FieldError{fieldErr("/secret", "invalid",
		"The identity provider has no secret: rvav's web app signs in as a public client, with PKCE.")}}
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
	// What is given over what is current: a part left out (or locked) stays.
	merged := map[string]any{}
	for k, v := range current {
		merged[k] = v
	}
	for k, v := range given {
		if s, ok := v.(string); !ok || s != "" {
			merged[k] = v
		}
	}
	moved := address(kind, merged) != address(kind, current)
	if v, ok := given["insecure"].(bool); ok && v && current["insecure"] != true {
		moved = true
	}
	// A certificate pinned: a server the CAs don't vouch for is trusted.
	if v, _ := given["cert_sha256"].(string); v != "" {
		pin, _ := trust.NormalFingerprint(v)
		was, _ := current["cert_sha256"].(string)
		if was, _ = trust.NormalFingerprint(was); pin != was {
			moved = true
		}
	}
	// The console's password set by its environment variable goes with
	// vCenter's address set there.
	if moved && kind == "vcenter" && slices.Contains(config.Locked("console"), "password") {
		return &ValidationError{Errors: []api.FieldError{fieldErr("/settings/url", "locked_secret",
			"The console's password is set by RVAV_CONSOLE_VCENTER_PASSWORD: it only goes to the vCenter set with it, so set this one there too.")}}
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
		"A new address, a certificate pinned, or TLS checks turned off, needs the password or token again: the saved one only goes where it was given for.")}}
}

type checkFunc func(name string, err error, ok string) bool

func (s *Service) testVCenter(ctx context.Context, c config.VCenterConfig, roots *x509.CertPool, check checkFunc) {
	if c.URL == "" {
		check("Settings", errors.New("give vCenter's URL"), "")
		return
	}
	o := c.TLS()
	o.Roots = roots
	vc, err := vsphere.New(ctx, c.Name, c.URL, c.Username, c.Password, o)
	if !check("Sign in", err, "Signed in to "+c.URL+" as "+c.Username+".") {
		return
	}
	defer func() { _ = vc.Logout(context.WithoutCancel(ctx)) }()
	inv, err := vc.Inventory(ctx)
	if !check("Inventory", err, fmt.Sprintf("%d clusters, %d datastores, %d networks and %d templates.",
		len(safeInv(inv).Clusters), len(safeInv(inv).Datastores), len(safeInv(inv).Networks), len(safeInv(inv).Templates))) {
		return
	}
	check("Tagging service", inv.TagsError, "Tags can be read, for placement by tag.")
}

// testConsole signs in to vCenter as the console's account and gets a
// clone ticket, as opening a console does (ADR-0012).
func testConsole(ctx context.Context, c config.Config, roots *x509.CertPool, check checkFunc) {
	if c.ConsoleUsername == "" {
		check("Console account", errors.New("none: VMRC asks each user to sign in to vCenter"), "")
	} else if c.VCenter.URL == "" {
		check("Console account", errors.New("set vCenter's connection first"), "")
	} else if c.ConsolePassword == "" {
		check("Console account", errors.New("give its password: none was given for vCenter's address now"), "")
	} else {
		o := c.VCenter.TLS()
		o.Roots = roots
		vc, err := vsphere.New(ctx, c.VCenter.Name, c.VCenter.URL, c.ConsoleUsername, c.ConsolePassword, o)
		if check("Sign in", err, "Signed in to "+c.VCenter.URL+" as "+c.ConsoleUsername+".") {
			defer func() { _ = vc.Logout(context.WithoutCancel(ctx)) }()
			_, err := vc.CloneTicket(ctx)
			check("Clone ticket", err, "VMRC links carry a ticket of this account.")
		}
	}
	if len(c.WMKSSDKURLs) == 0 {
		check("Console in the browser", nil, "No WebMKS SDK: consoles open in VMRC only.")
	} else {
		check("Console in the browser", nil, fmt.Sprintf("%d WebMKS SDK scripts, loaded by the browser.", len(c.WMKSSDKURLs)))
	}
}

// testVRA signs in to the vRA the importer reads and lists its projects.
func testVRA(ctx context.Context, c config.VRAConfig, roots *x509.CertPool, check checkFunc) {
	if c.URL == "" {
		check("Settings", errors.New("give vRA's URL"), "")
		return
	}
	client, err := vra.New(vra.Options{URL: c.URL, Token: c.Token, Username: c.Username, Password: c.Password, Domain: c.Domain,
		Insecure: c.Insecure, Pin: c.CertSHA256, Roots: roots})
	if !check("Settings", err, "") {
		return
	}
	projects, err := client.Projects(ctx)
	check("Sign in", err, fmt.Sprintf("Signed in to %s; %d projects to import.", c.URL, len(projects)))
}

func hostList(l []string) string {
	if len(l) == 0 {
		return "no host"
	}
	return strings.Join(l, ", ")
}

func safeInv(inv *vsphere.Inventory) *vsphere.Inventory {
	if inv == nil {
		return &vsphere.Inventory{}
	}
	return inv
}

func (s *Service) testAWX(ctx context.Context, c config.AWXConfig, roots *x509.CertPool, check checkFunc) {
	if c.Fake {
		check("Fake", nil, "This rvav uses the fake AWX (RVAV_FAKE_AWX=1): jobs are simulated.")
		return
	}
	if c.URL == "" || c.Organization == "" {
		check("Settings", errors.New("give AWX's URL and rvav's organisation"), "")
		return
	}
	o := c.TLS()
	o.Roots = roots
	client, err := awx.NewHTTPClient(c.URL, c.Token, o)
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

func (s *Service) testEfficientIP(ctx context.Context, c config.EfficientIPConfig, roots *x509.CertPool, check checkFunc) {
	if c.Fake {
		check("Fake", nil, "This rvav uses a fake SOLIDserver in its own process (RVAV_FAKE_EFFICIENTIP=1).")
		return
	}
	if c.URL == "" {
		check("Settings", errors.New("give the SOLIDserver's URL"), "")
		return
	}
	client, err := efficientip.New(efficientip.Options{URL: c.URL, Auth: efficientip.Auth(c.Auth), Username: c.Username,
		Password: c.Password, Insecure: c.Insecure, Pin: c.CertSHA256, Roots: roots, Space: c.Space, DNSServer: c.DNSServer,
		DNSView: c.DNSView, Instance: c.Instance})
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

// GitRepo is the catalog's repository as configured (ADR-0029), its clone
// kept under cache, trusting the CAs of caBundle besides the system's.
func GitRepo(c config.GitConfig, cache, caBundle string) (*gitrepo.Repo, error) {
	return gitrepo.New(gitrepo.Options{URL: c.URL, Branch: c.Branch, Dir: c.Dir, Username: c.Username, Credential: c.Credential,
		KnownHosts: c.KnownHosts, Insecure: c.Insecure, Committer: gitrepo.Person{Name: "rvav", Email: c.Committer}, Cache: cache,
		CABundle: caBundle})
}

// testGit signs in to the catalog's repository and reads its branch.
func testGit(ctx context.Context, c config.GitConfig, caBundle string, check checkFunc) {
	if c.URL == "" {
		check("Settings", errors.New("give the repository's URL"), "")
		return
	}
	repo, err := GitRepo(c, "", caBundle)
	if !check("Settings", err, "A repository on "+gitrepo.Scheme(c.URL)+".") {
		return
	}
	head, err := repo.Check(ctx)
	if !check("Sign in", err, "Signed in to "+c.URL+".") {
		return
	}
	if head == "" {
		check("Branch "+repo.Branch(), nil, "Empty: rvav's first commit makes it.")
		return
	}
	check("Branch "+repo.Branch(), nil, "At "+head[:12]+"; items are files in "+repo.Dir()+"/.")
}

// nsx is a client of the NSX Manager rvav is configured with.
func (s *Service) nsx() (*nsx.Client, error) {
	c := s.Config.NSX
	return nsx.New(nsx.Options{URL: c.URL, Username: c.Username, Password: c.Password, CertFile: c.CertFile, KeyFile: c.KeyFile,
		Insecure: c.Insecure, Pin: c.CertSHA256})
}

func (s *Service) testNSX(ctx context.Context, c config.NSXConfig, roots *x509.CertPool, check checkFunc) {
	if c.URL == "" {
		check("Settings", errors.New("give the NSX Manager's URL"), "")
		return
	}
	client, err := nsx.New(nsx.Options{URL: c.URL, Username: c.Username, Password: c.Password, CertFile: c.CertFile, KeyFile: c.KeyFile,
		Insecure: c.Insecure, Pin: c.CertSHA256, Roots: roots})
	if err != nil {
		check("Settings", err, "")
		return
	}
	version, err := client.Version(ctx)
	if !check("Sign in", err, "Signed in to NSX "+version+".") {
		return
	}
	tier1s, err := client.Tier1s(ctx)
	if !check("Tier-1 gateways", err, fmt.Sprintf("%d Tier-1 gateways, for segments made for deployments.", len(tier1s))) {
		return
	}
	segments, err := client.Segments(ctx)
	check("Segments", err, fmt.Sprintf("%d segments.", len(segments)))
	groups, err := client.Groups(ctx)
	check("Groups", err, fmt.Sprintf("%d groups in the default domain.", len(groups)))
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
	// Requests' secret inputs (S14.6).
	inputs, err := st.SealedInputsNotWith(ctx, box.KeyID())
	if err != nil {
		return n, err
	}
	for _, in := range inputs {
		name := secretinput.Name(in.RequestID, in.Field)
		plain, err := box.Open(name, in.KeyID, in.Nonce, in.Ciphertext)
		if err != nil {
			return n, fmt.Errorf("request %s's %s: %w", in.RequestID, in.Field, err)
		}
		keyID, nonce, ct, err := box.Seal(name, plain)
		if err != nil {
			return n, err
		}
		if done, err := st.ResealRequestSecret(ctx, in.RequestID, in.Field, in.Nonce,
			store.SealedInput{Field: in.Field, KeyID: keyID, Nonce: nonce, Ciphertext: ct}); err != nil {
			return n, err
		} else if done {
			n++
		}
	}
	return n, nil
}
