package config

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"os"
	"strings"
)

// Integration settings set from the UI (ADR-0020): stored in the database,
// used when rvav's processes start, and only where no environment variable
// sets them.

// Field is an integration setting and the environment variable that wins
// over the stored value.
type Field struct {
	Name, Env string
	Type      string // string, bool, int or list
	Secret    bool   // the password or token: sealed, never shown
}

// Fields are the settings of each integration.
var Fields = map[string][]Field{
	"vcenter": {
		{"url", "RVAV_VCENTER_URL", "string", false},
		{"username", "RVAV_VCENTER_USERNAME", "string", false},
		{"password", "RVAV_VCENTER_PASSWORD", "string", true},
		{"insecure", "RVAV_VCENTER_INSECURE", "bool", false},
		{"name", "RVAV_VCENTER_NAME", "string", false},
	},
	"awx": {
		{"url", "RVAV_AWX_URL", "string", false},
		{"token", "RVAV_AWX_TOKEN", "string", true},
		{"organization", "RVAV_AWX_ORGANIZATION", "string", false},
		{"catalog_labels", "RVAV_AWX_CATALOG_LABELS", "list", false},
		{"action_labels", "RVAV_AWX_ACTION_LABELS", "list", false},
		{"inventory_source_id", "RVAV_AWX_INVENTORY_SOURCE_ID", "int", false},
		{"insecure", "RVAV_AWX_INSECURE", "bool", false},
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
	},
	// Slack's two secrets are sealed together, as SlackSecrets.
	"slack": {
		{"bot_token", "RVAV_SLACK_BOT_TOKEN", "string", true},
		{"signing_secret", "RVAV_SLACK_SIGNING_SECRET", "string", true},
	},
}

// SlackSecrets are Slack's bot token and signing secret, sealed as one.
type SlackSecrets struct {
	BotToken      string `json:"bot_token,omitempty"`
	SigningSecret string `json:"signing_secret,omitempty"`
}

// Locked lists the settings of an integration an environment variable sets.
func Locked(kind string) []string {
	var out []string
	for _, f := range Fields[kind] {
		if v, ok := os.LookupEnv(f.Env); ok && v != "" {
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
	switch kind {
	case "vcenter":
		str("url", &c.VCenter.URL)
		str("username", &c.VCenter.Username)
		secret("password", &c.VCenter.Password)
		flag("insecure", &c.VCenter.Insecure)
		str("name", &c.VCenter.Name)
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
	case "nsx":
		str("url", &c.NSX.URL)
		str("username", &c.NSX.Username)
		secret("password", &c.NSX.Password)
		str("cert_file", &c.NSX.CertFile)
		str("key_file", &c.NSX.KeyFile)
		flag("insecure", &c.NSX.Insecure)
	case "git":
		str("url", &c.Git.URL)
		str("branch", &c.Git.Branch)
		str("dir", &c.Git.Dir)
		str("username", &c.Git.Username)
		secret("credential", &c.Git.Credential)
		str("known_hosts", &c.Git.KnownHosts)
		flag("insecure", &c.Git.Insecure)
		str("committer", &c.Git.Committer)
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
	case "slack":
		var sec SlackSecrets
		_ = json.Unmarshal([]byte(s.Secret), &sec)
		if sec.BotToken != "" && !locked["bot_token"] {
			c.Slack.BotToken = sec.BotToken
		}
		if sec.SigningSecret != "" && !locked["signing_secret"] {
			c.Slack.SigningSecret = sec.SigningSecret
		}
	}
}

// Settings are an integration's settings as rvav uses them, without the
// secret.
func (c Config) Settings(kind string) map[string]any {
	switch kind {
	case "vcenter":
		return map[string]any{"url": c.VCenter.URL, "username": c.VCenter.Username, "insecure": c.VCenter.Insecure, "name": c.VCenter.Name}
	case "awx":
		return map[string]any{"url": c.AWX.URL, "organization": c.AWX.Organization, "catalog_labels": c.AWX.CatalogLabels,
			"action_labels": c.AWX.ActionLabels, "inventory_source_id": c.AWX.InventorySourceID, "insecure": c.AWX.Insecure,
			"fake": c.AWX.Fake}
	case "efficientip":
		return map[string]any{"url": c.IPAM.URL, "auth": c.IPAM.Auth, "username": c.IPAM.Username, "space": c.IPAM.Space,
			"dns_server": c.IPAM.DNSServer, "dns_view": c.IPAM.DNSView, "instance_id": c.IPAM.Instance, "insecure": c.IPAM.Insecure,
			"fake": c.IPAM.Fake}
	case "nsx":
		return map[string]any{"url": c.NSX.URL, "username": c.NSX.Username, "cert_file": c.NSX.CertFile, "key_file": c.NSX.KeyFile,
			"insecure": c.NSX.Insecure}
	case "git":
		return map[string]any{"url": c.Git.URL, "branch": c.Git.Branch, "dir": c.Git.Dir, "username": c.Git.Username,
			"known_hosts": c.Git.KnownHosts, "insecure": c.Git.Insecure, "committer": c.Git.Committer}
	case "smtp":
		return map[string]any{"host": c.SMTP.Host, "port": c.SMTP.Port, "tls": c.SMTP.TLS, "from": c.SMTP.From,
			"username": c.SMTP.Username}
	case "teams":
		return map[string]any{"app_id": c.Teams.AppID, "tenant_id": c.Teams.TenantID, "service_url": c.Teams.ServiceURL}
	case "slack":
		return map[string]any{}
	}
	return nil
}

// SecretSet says whether the integration has its password or token.
func (c Config) SecretSet(kind string) bool {
	switch kind {
	case "vcenter":
		return c.VCenter.Password != ""
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
		}
	}
	for _, key := range []string{"url", "service_url"} {
		if kind == "git" {
			break // an SSH URL too: gitrepo.CheckURL
		}
		if u, ok := settings[key].(string); ok && u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			errs[key] = "An http or https URL."
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
