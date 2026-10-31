package config

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/trust"
)

// Integration settings set from the UI (ADR-0020): stored in the database,
// and used only where no environment variable sets them; saved, they apply
// on next use or as each process drains and starts again (ADR-0030).

// Field is an integration setting and the environment variable that wins
// over the stored value.
type Field struct {
	Name, Env string
	Type      string // string, bool, int, list or duration (a Go duration: 90s, 5m)
	Secret    bool   // the password or token: sealed, never shown
}

// alsoLockedBy are more variables that set a field: vRA's one secret is a
// refresh token, or a user's password.
var alsoLockedBy = map[string]map[string][]string{
	"vra": {"credential": {"RVAV_VRA_PASSWORD"}},
}

// Variables are the environment variables that set something
// Administration sets, each locking it there (ADR-0030): its integrations'
// fields and the inventory token. A new install is given none.
func Variables() []string {
	out := []string{"RVAV_INVENTORY_TOKEN"}
	for kind, fields := range Fields {
		for _, f := range fields {
			out = append(out, f.Env)
			out = append(out, alsoLockedBy[kind][f.Name]...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Intervals: the shortest and longest a sync may be set to from the UI.
const (
	MinInterval = 30 * time.Second
	MaxInterval = 24 * time.Hour
)

// Fields are the settings of each integration.
var Fields = map[string][]Field{
	// The identity provider (ADR-0030): the web app is a public client
	// (PKCE), so there is no secret. It is read on next use.
	"oidc": {
		{"issuer", "RVAV_OIDC_ISSUER", "string", false},
		{"discovery_url", "RVAV_OIDC_DISCOVERY_URL", "string", false},
		{"client_id", "RVAV_OIDC_CLIENT_ID", "string", false},
		{"scopes", "RVAV_OIDC_SCOPES", "string", false},
		{"groups_claim", "RVAV_OIDC_GROUPS_CLAIM", "string", false},
	},
	"vcenter": {
		{"url", "RVAV_VCENTER_URL", "string", false},
		{"username", "RVAV_VCENTER_USERNAME", "string", false},
		{"password", "RVAV_VCENTER_PASSWORD", "string", true},
		{"insecure", "RVAV_VCENTER_INSECURE", "bool", false},
		{"cert_sha256", "RVAV_VCENTER_CERT_SHA256", "string", false},
		{"name", "RVAV_VCENTER_NAME", "string", false},
		{"sync_interval", "RVAV_SYNC_INTERVAL", "duration", false},
	},
	// The console (ADR-0012): a vCenter account that may only open
	// consoles, for VMRC's clone tickets, and the WebMKS SDK's scripts.
	"console": {
		{"username", "RVAV_CONSOLE_VCENTER_USERNAME", "string", false},
		{"password", "RVAV_CONSOLE_VCENTER_PASSWORD", "string", true},
		{"sdk_urls", "RVAV_WMKS_SDK_URLS", "list", false},
	},
	"awx": {
		{"url", "RVAV_AWX_URL", "string", false},
		{"token", "RVAV_AWX_TOKEN", "string", true},
		{"organization", "RVAV_AWX_ORGANIZATION", "string", false},
		{"catalog_labels", "RVAV_AWX_CATALOG_LABELS", "list", false},
		{"action_labels", "RVAV_AWX_ACTION_LABELS", "list", false},
		{"inventory_source_id", "RVAV_AWX_INVENTORY_SOURCE_ID", "int", false},
		{"insecure", "RVAV_AWX_INSECURE", "bool", false},
		{"cert_sha256", "RVAV_AWX_CERT_SHA256", "string", false},
		{"sync_interval", "RVAV_AWX_SYNC_INTERVAL", "duration", false},
	},
	"efficientip": {
		{"url", "RVAV_EFFICIENTIP_URL", "string", false},
		{"auth", "RVAV_EFFICIENTIP_AUTH", "string", false},
		{"username", "RVAV_EFFICIENTIP_USERNAME", "string", false},
		{"password", "RVAV_EFFICIENTIP_PASSWORD", "string", true},
		{"space", "RVAV_EFFICIENTIP_SPACE", "string", false},
		{"dns_server", "RVAV_EFFICIENTIP_DNS_SERVER", "string", false},
		{"dns_view", "RVAV_EFFICIENTIP_DNS_VIEW", "string", false},
		{"instance_id", "RVAV_INSTANCE_ID", "string", false},
		{"insecure", "RVAV_EFFICIENTIP_INSECURE", "bool", false},
		{"cert_sha256", "RVAV_EFFICIENTIP_CERT_SHA256", "string", false},
		{"sync_interval", "RVAV_IPAM_SYNC_INTERVAL", "duration", false},
	},
	// The NSX Manager (ADR-0028): a user and password, or a principal
	// identity's certificate and key files.
	"nsx": {
		{"url", "RVAV_NSX_URL", "string", false},
		{"username", "RVAV_NSX_USERNAME", "string", false},
		{"password", "RVAV_NSX_PASSWORD", "string", true},
		{"cert_file", "RVAV_NSX_CERT_FILE", "string", false},
		{"key_file", "RVAV_NSX_KEY_FILE", "string", false},
		{"insecure", "RVAV_NSX_INSECURE", "bool", false},
		{"cert_sha256", "RVAV_NSX_CERT_SHA256", "string", false},
	},
	// The catalog's repository (ADR-0029): a token over HTTPS, or a deploy
	// key (its private key) over SSH, with the server's host keys.
	"git": {
		{"url", "RVAV_GIT_URL", "string", false},
		{"branch", "RVAV_GIT_BRANCH", "string", false},
		{"dir", "RVAV_GIT_DIR", "string", false},
		{"username", "RVAV_GIT_USERNAME", "string", false},
		{"credential", "RVAV_GIT_CREDENTIAL", "string", true},
		{"known_hosts", "RVAV_GIT_KNOWN_HOSTS", "string", false},
		{"insecure", "RVAV_GIT_INSECURE", "bool", false},
		{"committer", "RVAV_GIT_COMMITTER", "string", false},
		{"sync_interval", "RVAV_GIT_SYNC_INTERVAL", "duration", false},
	},
	// The vRA the importer reads (ADR-0027): a refresh token, or a user's
	// password when a user is given.
	"vra": {
		{"url", "RVAV_VRA_URL", "string", false},
		{"username", "RVAV_VRA_USERNAME", "string", false},
		{"domain", "RVAV_VRA_DOMAIN", "string", false},
		{"credential", "RVAV_VRA_TOKEN", "string", true},
		{"insecure", "RVAV_VRA_INSECURE", "bool", false},
		{"cert_sha256", "RVAV_VRA_CERT_SHA256", "string", false},
	},
	"smtp": {
		{"host", "RVAV_SMTP_HOST", "string", false},
		{"port", "RVAV_SMTP_PORT", "int", false},
		{"tls", "RVAV_SMTP_TLS", "string", false},
		{"from", "RVAV_SMTP_FROM", "string", false},
		{"username", "RVAV_SMTP_USERNAME", "string", false},
		{"password", "RVAV_SMTP_PASSWORD", "string", true},
	},
	"teams": {
		{"app_id", "RVAV_TEAMS_APP_ID", "string", false},
		{"tenant_id", "RVAV_TEAMS_TENANT_ID", "string", false},
		{"service_url", "RVAV_TEAMS_SERVICE_URL", "string", false},
		{"app_password", "RVAV_TEAMS_APP_PASSWORD", "string", true},
		// Microsoft's public cloud by default; another cloud's (GCC High,
		// China) are set here.
		{"login_url", "RVAV_TEAMS_LOGIN_URL", "string", false},
		{"graph_url", "RVAV_TEAMS_GRAPH_URL", "string", false},
		{"openid_url", "RVAV_TEAMS_OPENID_URL", "string", false},
	},
	// Slack's two secrets are sealed together, as SlackSecrets.
	"slack": {
		{"bot_token", "RVAV_SLACK_BOT_TOKEN", "string", true},
		{"signing_secret", "RVAV_SLACK_SIGNING_SECRET", "string", true},
		{"api_url", "RVAV_SLACK_API_URL", "string", false},
	},
	// The CAs every connection trusts besides the system's (package trust).
	// It applies as connections do.
	"trust": {
		{"ca_bundle", "RVAV_CA_BUNDLE", "string", false},
	},
	// The hosts rvav may call (package hosts): REST value sources' and
	// webhooks'.
	"hosts": {
		{"rest_source_hosts", "RVAV_REST_SOURCE_HOSTS", "list", false},
		{"webhook_hosts", "RVAV_WEBHOOK_HOSTS", "list", false},
	},
}

// SlackSecrets are Slack's bot token and signing secret, sealed as one.
type SlackSecrets struct {
	BotToken      string `json:"bot_token,omitempty"`
	SigningSecret string `json:"signing_secret,omitempty"`
}

// ConsoleForKey keeps, with the console's saved settings, the vCenter
// connection its password was given for (ConsoleFor). The account signs in
// to vCenter's address: given another, or its TLS checks turned off, the
// saved password isn't used until it is given again, as a connection's
// secret only goes where it was given for.
const ConsoleForKey = "for_vcenter"

// ConsoleFor names a vCenter connection, for ConsoleForKey.
func ConsoleFor(v VCenterConfig) string {
	if v.Insecure {
		return v.URL + " insecure"
	}
	return v.URL
}

// TLS is how a connection checks its server's certificate (package
// trust): the CAs, or the certificate pinned.
func (c VCenterConfig) TLS() trust.Options {
	return trust.Options{Insecure: c.Insecure, Pin: c.CertSHA256}
}

func (c AWXConfig) TLS() trust.Options { return trust.Options{Insecure: c.Insecure, Pin: c.CertSHA256} }

func (c EfficientIPConfig) TLS() trust.Options {
	return trust.Options{Insecure: c.Insecure, Pin: c.CertSHA256}
}

func (c NSXConfig) TLS() trust.Options { return trust.Options{Insecure: c.Insecure, Pin: c.CertSHA256} }

func (c VRAConfig) TLS() trust.Options { return trust.Options{Insecure: c.Insecure, Pin: c.CertSHA256} }

// Locked lists the settings of an integration an environment variable sets.
func Locked(kind string) []string {
	var out []string
	set := func(env string) bool { v, ok := os.LookupEnv(env); return ok && v != "" }
	for _, f := range Fields[kind] {
		if set(f.Env) || slices.ContainsFunc(alsoLockedBy[kind][f.Name], set) {
			out = append(out, f.Name)
		}
	}
	if Fake(kind) {
		out = append(out, "fake")
	}
	return out
}

// Fake says whether the integration is a fake in this process.
func Fake(kind string) bool {
	switch kind {
	case "awx":
		return boolean("RVAV_FAKE_AWX")
	case "efficientip":
		return boolean("RVAV_FAKE_EFFICIENTIP")
	}
	return false
}

// Stored is an integration's saved settings, its secret opened.
type Stored struct {
	Settings map[string]any
	Secret   string
}

// ApplyStored uses an integration's saved settings where no environment
// variable sets them.
func (c *Config) ApplyStored(kind string, s Stored) {
	locked := map[string]bool{}
	for _, name := range Locked(kind) {
		locked[name] = true
	}
	str := func(name string, dst *string) {
		if v, ok := s.Settings[name].(string); ok && v != "" && !locked[name] {
			*dst = v
		}
	}
	flag := func(name string, dst *bool) {
		if v, ok := s.Settings[name].(bool); ok && !locked[name] {
			*dst = v
		}
	}
	strs := func(name string, dst *[]string) {
		if v, ok := s.Settings[name].([]any); ok && !locked[name] {
			var out []string
			for _, x := range v {
				if t, ok := x.(string); ok && strings.TrimSpace(t) != "" {
					out = append(out, strings.TrimSpace(t))
				}
			}
			if len(out) > 0 {
				*dst = out
			}
		}
	}
	secret := func(name string, dst *string) {
		if s.Secret != "" && !locked[name] {
			*dst = s.Secret
		}
	}
	// A pin cleared in the UI trusts the CAs only.
	pin := func(name string, dst *string) {
		if v, ok := s.Settings[name].(string); ok && !locked[name] {
			*dst = v
		}
	}
	dur := func(name string, dst *time.Duration) {
		if v, ok := s.Settings[name].(string); ok && v != "" && !locked[name] {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				*dst = d
			}
		}
	}
	switch kind {
	case "oidc":
		str("issuer", &c.Auth.OIDCIssuer)
		str("discovery_url", &c.Auth.OIDCDiscoveryURL)
		str("client_id", &c.Auth.OIDCClientID)
		str("scopes", &c.Auth.Scopes)
		str("groups_claim", &c.Auth.GroupsClaim)
	case "vcenter":
		str("url", &c.VCenter.URL)
		str("username", &c.VCenter.Username)
		secret("password", &c.VCenter.Password)
		flag("insecure", &c.VCenter.Insecure)
		pin("cert_sha256", &c.VCenter.CertSHA256)
		str("name", &c.VCenter.Name)
		dur("sync_interval", &c.SyncInterval)
	case "console":
		str("username", &c.ConsoleUsername)
		// Only to the vCenter it was given for: vCenter's is applied first.
		if f, _ := s.Settings[ConsoleForKey].(string); c.VCenter.URL != "" && f == ConsoleFor(c.VCenter) {
			secret("password", &c.ConsolePassword)
		}
		strs("sdk_urls", &c.WMKSSDKURLs)
	case "awx":
		str("url", &c.AWX.URL)
		secret("token", &c.AWX.Token)
		str("organization", &c.AWX.Organization)
		strs("catalog_labels", &c.AWX.CatalogLabels)
		strs("action_labels", &c.AWX.ActionLabels)
		if v, ok := s.Settings["inventory_source_id"].(float64); ok && !locked["inventory_source_id"] {
			c.AWX.InventorySourceID = int(v)
		}
		flag("insecure", &c.AWX.Insecure)
		pin("cert_sha256", &c.AWX.CertSHA256)
		dur("sync_interval", &c.AWX.SyncInterval)
	case "efficientip":
		str("url", &c.IPAM.URL)
		str("auth", &c.IPAM.Auth)
		str("username", &c.IPAM.Username)
		secret("password", &c.IPAM.Password)
		str("space", &c.IPAM.Space)
		str("dns_server", &c.IPAM.DNSServer)
		str("dns_view", &c.IPAM.DNSView)
		str("instance_id", &c.IPAM.Instance)
		flag("insecure", &c.IPAM.Insecure)
		pin("cert_sha256", &c.IPAM.CertSHA256)
		dur("sync_interval", &c.IPAMSyncInterval)
	case "nsx":
		str("url", &c.NSX.URL)
		str("username", &c.NSX.Username)
		secret("password", &c.NSX.Password)
		str("cert_file", &c.NSX.CertFile)
		str("key_file", &c.NSX.KeyFile)
		flag("insecure", &c.NSX.Insecure)
		pin("cert_sha256", &c.NSX.CertSHA256)
	case "git":
		str("url", &c.Git.URL)
		str("branch", &c.Git.Branch)
		str("dir", &c.Git.Dir)
		str("username", &c.Git.Username)
		secret("credential", &c.Git.Credential)
		str("known_hosts", &c.Git.KnownHosts)
		flag("insecure", &c.Git.Insecure)
		str("committer", &c.Git.Committer)
		dur("sync_interval", &c.Git.Interval)
	case "vra":
		str("url", &c.VRA.URL)
		str("username", &c.VRA.Username)
		str("domain", &c.VRA.Domain)
		flag("insecure", &c.VRA.Insecure)
		pin("cert_sha256", &c.VRA.CertSHA256)
		// With a user, the secret is its password; without, a refresh token.
		if s.Secret != "" && !locked["credential"] {
			if c.VRA.Username != "" {
				c.VRA.Password, c.VRA.Token = s.Secret, ""
			} else {
				c.VRA.Token, c.VRA.Password = s.Secret, ""
			}
		}
	case "smtp":
		str("host", &c.SMTP.Host)
		if v, ok := s.Settings["port"].(float64); ok && v > 0 && !locked["port"] {
			c.SMTP.Port = int(v)
		}
		str("tls", &c.SMTP.TLS)
		str("from", &c.SMTP.From)
		str("username", &c.SMTP.Username)
		secret("password", &c.SMTP.Password)
	case "teams":
		str("app_id", &c.Teams.AppID)
		str("tenant_id", &c.Teams.TenantID)
		str("service_url", &c.Teams.ServiceURL)
		secret("app_password", &c.Teams.AppPassword)
		str("login_url", &c.Teams.LoginURL)
		str("graph_url", &c.Teams.GraphURL)
		str("openid_url", &c.Teams.OpenIDURL)
		c.Teams.ServiceURL = strings.TrimRight(c.Teams.ServiceURL, "/")
		c.Teams.LoginURL = strings.TrimRight(c.Teams.LoginURL, "/")
		c.Teams.GraphURL = strings.TrimRight(c.Teams.GraphURL, "/")
	case "trust":
		// Cleared in the UI, the system's CAs only.
		if v, ok := s.Settings["ca_bundle"].(string); ok && !locked["ca_bundle"] {
			c.CABundle = v
		}
	case "hosts":
		strs("rest_source_hosts", &c.RESTSourceHosts)
		strs("webhook_hosts", &c.WebhookHosts)
	case "slack":
		var sec SlackSecrets
		_ = json.Unmarshal([]byte(s.Secret), &sec)
		if sec.BotToken != "" && !locked["bot_token"] {
			c.Slack.BotToken = sec.BotToken
		}
		if sec.SigningSecret != "" && !locked["signing_secret"] {
			c.Slack.SigningSecret = sec.SigningSecret
		}
		str("api_url", &c.Slack.APIURL)
	}
}

// FormatDuration writes a duration as it would be typed: 2m, 1h30m, 45s.
func FormatDuration(d time.Duration) string {
	s := d.String()
	s = strings.TrimSuffix(s, "m0s")
	if s != d.String() {
		s += "m"
	}
	if t := strings.TrimSuffix(s, "h0m"); t != s {
		s = t + "h"
	}
	return s
}

// Settings are an integration's settings as rvav uses them, without the
// secret.
func (c Config) Settings(kind string) map[string]any {
	switch kind {
	case "oidc":
		return map[string]any{"issuer": c.Auth.OIDCIssuer, "discovery_url": c.Auth.OIDCDiscoveryURL, "client_id": c.Auth.OIDCClientID,
			"scopes": c.Auth.Scopes, "groups_claim": c.Auth.GroupsClaim}
	case "vcenter":
		return map[string]any{"url": c.VCenter.URL, "username": c.VCenter.Username, "insecure": c.VCenter.Insecure,
			"cert_sha256": c.VCenter.CertSHA256, "name": c.VCenter.Name,
			"sync_interval": FormatDuration(c.SyncInterval)}
	case "console":
		return map[string]any{"username": c.ConsoleUsername, "sdk_urls": nonNil(c.WMKSSDKURLs)}
	case "awx":
		return map[string]any{"url": c.AWX.URL, "organization": c.AWX.Organization, "catalog_labels": c.AWX.CatalogLabels,
			"action_labels": c.AWX.ActionLabels, "inventory_source_id": c.AWX.InventorySourceID, "insecure": c.AWX.Insecure,
			"cert_sha256": c.AWX.CertSHA256,
			"fake":        c.AWX.Fake, "sync_interval": FormatDuration(c.AWX.SyncInterval)}
	case "efficientip":
		return map[string]any{"url": c.IPAM.URL, "auth": c.IPAM.Auth, "username": c.IPAM.Username, "space": c.IPAM.Space,
			"dns_server": c.IPAM.DNSServer, "dns_view": c.IPAM.DNSView, "instance_id": c.IPAM.Instance, "insecure": c.IPAM.Insecure,
			"cert_sha256": c.IPAM.CertSHA256,
			"fake":        c.IPAM.Fake, "sync_interval": FormatDuration(c.IPAMSyncInterval)}
	case "nsx":
		return map[string]any{"url": c.NSX.URL, "username": c.NSX.Username, "cert_file": c.NSX.CertFile, "key_file": c.NSX.KeyFile,
			"insecure": c.NSX.Insecure, "cert_sha256": c.NSX.CertSHA256}
	case "git":
		return map[string]any{"url": c.Git.URL, "branch": c.Git.Branch, "dir": c.Git.Dir, "username": c.Git.Username,
			"known_hosts": c.Git.KnownHosts, "insecure": c.Git.Insecure, "committer": c.Git.Committer,
			"sync_interval": FormatDuration(c.Git.Interval)}
	case "vra":
		return map[string]any{"url": c.VRA.URL, "username": c.VRA.Username, "domain": c.VRA.Domain, "insecure": c.VRA.Insecure,
			"cert_sha256": c.VRA.CertSHA256}
	case "trust":
		return map[string]any{"ca_bundle": c.CABundle}
	case "hosts":
		return map[string]any{"rest_source_hosts": nonNil(c.RESTSourceHosts), "webhook_hosts": nonNil(c.WebhookHosts)}
	case "smtp":
		return map[string]any{"host": c.SMTP.Host, "port": c.SMTP.Port, "tls": c.SMTP.TLS, "from": c.SMTP.From,
			"username": c.SMTP.Username}
	case "teams":
		return map[string]any{"app_id": c.Teams.AppID, "tenant_id": c.Teams.TenantID, "service_url": c.Teams.ServiceURL,
			"login_url": c.Teams.LoginURL, "graph_url": c.Teams.GraphURL, "openid_url": c.Teams.OpenIDURL}
	case "slack":
		return map[string]any{"api_url": c.Slack.APIURL}
	}
	return nil
}

// SecretSet says whether the integration has its password or token.
func (c Config) SecretSet(kind string) bool {
	switch kind {
	case "vcenter":
		return c.VCenter.Password != ""
	case "console":
		return c.ConsolePassword != ""
	case "vra":
		return c.VRA.Token != "" || c.VRA.Password != ""
	case "awx":
		return c.AWX.Token != ""
	case "efficientip":
		return c.IPAM.Password != ""
	case "nsx":
		return c.NSX.Password != ""
	case "git":
		return c.Git.Credential != ""
	case "smtp":
		return c.SMTP.Password != ""
	case "teams":
		return c.Teams.AppPassword != ""
	case "slack":
		return c.Slack.BotToken != ""
	}
	return false
}

// CheckSettings says what is wrong with settings given for an integration,
// by field.
func CheckSettings(kind string, settings map[string]any) map[string]string {
	errs := map[string]string{}
	known := map[string]Field{}
	for _, f := range Fields[kind] {
		known[f.Name] = f
	}
	for name, v := range settings {
		f, ok := known[name]
		switch {
		case !ok || f.Secret:
			errs[name] = fmt.Sprintf("%s is not a setting of %s.", name, kind)
		case f.Type == "string":
			if _, ok := v.(string); !ok {
				errs[name] = "Text."
			}
		case f.Type == "bool":
			if _, ok := v.(bool); !ok {
				errs[name] = "true or false."
			}
		case f.Type == "int":
			if n, ok := v.(float64); !ok || n != float64(int(n)) || n < 0 {
				errs[name] = "A whole number."
			}
		case f.Type == "list":
			if _, ok := v.([]any); !ok {
				errs[name] = "A list of text."
			}
		case f.Type == "duration":
			if s, ok := v.(string); !ok {
				errs[name] = "A duration, for example 5m."
			} else if d, err := time.ParseDuration(s); s != "" && (err != nil || d < MinInterval || d > MaxInterval) {
				errs[name] = fmt.Sprintf("A duration from %s to %s, for example 5m or 1h30m.", FormatDuration(MinInterval), FormatDuration(MaxInterval))
			}
		}
	}
	for _, key := range []string{"url", "service_url", "issuer", "discovery_url", "login_url", "graph_url", "openid_url", "api_url"} {
		if kind == "git" {
			break // an SSH URL too: gitrepo.CheckURL
		}
		if u, ok := settings[key].(string); ok && u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			errs[key] = "An http or https URL."
		}
	}
	// The WebMKS SDK's scripts, which the browser loads.
	if l, ok := settings["sdk_urls"].([]any); ok && kind == "console" {
		for _, x := range l {
			if u, _ := x.(string); u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "/") {
				errs["sdk_urls"] = "URLs of the scripts, https:// or a path on rvav's own address (/wmks/wmks.min.js)."
			}
		}
	}
	// A host rvav may call: a name or an address, a port, a scheme for
	// plain http (package hosts).
	for _, key := range []string{"rest_source_hosts", "webhook_hosts"} {
		l, _ := settings[key].([]any)
		for _, x := range l {
			h, _ := x.(string)
			bare := strings.TrimPrefix(strings.TrimPrefix(h, "http://"), "https://")
			if u, err := url.Parse("https://" + bare); h != "" && (err != nil || u.Host == "" || u.Host != bare) {
				errs[key] = fmt.Sprintf("%q is not a host: give a name such as cmdb.corp.example, *.corp.example, or http://host:port for plain http.", h)
			}
		}
	}
	if p, ok := settings["cert_sha256"].(string); ok && strings.TrimSpace(p) != "" {
		if _, ok := trust.NormalFingerprint(p); !ok {
			errs["cert_sha256"] = "A certificate's SHA-256: 32 bytes in hex, with or without colons."
		}
	}
	if b, ok := settings["ca_bundle"].(string); ok && kind == "trust" {
		if _, err := trust.Parse(b); err != nil {
			errs["ca_bundle"] = "CA certificates in PEM: " + err.Error() + "."
		}
	}
	if kind == "oidc" {
		if c, ok := settings["groups_claim"].(string); ok && strings.ContainsAny(c, " \t\"'") {
			errs["groups_claim"] = "A claim's name, for example groups or roles."
		}
		if sc, ok := settings["scopes"].(string); ok && sc != "" && !slices.Contains(strings.Fields(sc), "openid") {
			errs["scopes"] = "Include openid, for example openid profile email."
		}
	}
	if kind == "smtp" {
		if t, ok := settings["tls"].(string); ok && t != "starttls" && t != "tls" && t != "none" {
			errs["tls"] = "starttls, tls or none."
		}
		if f, ok := settings["from"].(string); ok && f != "" {
			if _, err := mail.ParseAddress(f); err != nil {
				errs["from"] = "An address, for example rvav <rvav@corp.example>."
			}
		}
	}
	return errs
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
