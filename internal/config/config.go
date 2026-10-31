// Package config reads rvav settings from environment variables.
// See .env.example for the full list.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr string
	// MetricsAddr serves Prometheus metrics (S14.5); ":9090" unless set, "off" for none.
	MetricsAddr string
	DatabaseURL string
	LogLevel    string
	CORSOrigins []string

	Auth AuthConfig

	Temporal TemporalConfig
	VCenter  VCenterConfig
	IPAM     EfficientIPConfig
	AWX      AWXConfig
	// VRA is the vRA the importer reads (P2, ADR-0027).
	VRA VRAConfig
	// NSX is the NSX Manager rvav makes networks and security groups in
	// (ADR-0028).
	NSX NSXConfig
	// Git is the repository the catalog is kept in (ADR-0029).
	Git GitConfig
	// Notification channels (ADR-0023).
	SMTP  SMTPConfig
	Teams TeamsConfig
	Slack SlackConfig

	// PublicURL is where people open rvav: links in notifications start
	// with it (RVAV_PUBLIC_URL).
	PublicURL string

	// SecretsKey encrypts stored secrets (32 bytes, base64); RESTSourceHosts
	// are the hosts `rest` value sources may call (ADR-0019).
	SecretsKey string
	// SecretsOldKeys still open what earlier keys sealed (rotation).
	SecretsOldKeys  []string
	RESTSourceHosts []string
	// WebhookHosts are the hosts subscriptions may post events to
	// (ADR-0021); none, no webhooks.
	WebhookHosts []string

	SyncInterval time.Duration
	// How often the network mappings are read from EfficientIP.
	IPAMSyncInterval time.Duration

	// WMKSSDKURLs are the scripts of VMware's HTML console client, in load
	// order (RVAV_WMKS_SDK_URLS, comma-separated). Without them the console
	// is offered through VMRC only (ADR-0012).
	WMKSSDKURLs []string
	// ConsoleUsername and ConsolePassword are a vCenter account that may
	// only open consoles. VMRC links carry a clone ticket of its session, so
	// users don't sign in to VMRC; without it they sign in with their own
	// account. Never rvav's own account: a clone ticket clones the session.
	ConsoleUsername string
	ConsolePassword string
}

type AuthConfig struct {
	// Mode is "oidc" (default) or "dev". Dev mode trusts a fixed user and
	// must never be used outside a developer machine or tests.
	Mode       string
	OIDCIssuer string
	// OIDCDiscoveryURL is where the API reaches the provider, when that
	// differs from the issuer in tokens. Empty means the issuer.
	OIDCDiscoveryURL string
	OIDCClientID     string
	// Scopes are what the web app asks the provider for (RVAV_OIDC_SCOPES).
	Scopes      string
	GroupsClaim string
	DevUser     string
	// AllowDev must be set (RVAV_ALLOW_DEV_AUTH=1) for dev mode to start, so
	// that a stray RVAV_AUTH_MODE=dev can't open a real installation.
	AllowDev bool
	// AdminPassword is the local administrator's first password
	// (RVAV_ADMIN_PASSWORD, ADR-0030); empty: rvav generates one and logs it
	// once. Ignored once the account exists.
	AdminPassword string
}

type TemporalConfig struct {
	Address   string
	Namespace string
	TaskQueue string
	// TLS to the frontend: a cluster behind TLS, or Temporal Cloud. CAFile
	// trusts a private CA; CertFile and KeyFile are a client certificate
	// (mutual TLS); ServerName checks another name than the address's.
	TLS               bool
	CAFile            string
	CertFile, KeyFile string
	ServerName        string
	// APIKey signs in to Temporal Cloud (with TLS).
	APIKey string
}

type VCenterConfig struct {
	URL      string
	Username string
	Password string
	Insecure bool
	// Name is how the vCenter appears in the UI and API, for example vc01.corp.example.
	Name string
}

// VRAConfig is where vRA is and how rvav signs in there: a refresh token, or
// a user and password in a domain.
type VRAConfig struct {
	URL      string
	Token    string
	Username string
	Password string
	Domain   string
	Insecure bool
}

// NSXConfig: where the NSX Manager is, and rvav's account there, a user
// and password or a client certificate (a principal identity).
type NSXConfig struct {
	URL      string
	Username string
	Password string
	CertFile string
	KeyFile  string
	Insecure bool
}

// Configured says whether rvav has an NSX Manager.
func (c NSXConfig) Configured() bool { return c.URL != "" }

// GitConfig: the repository the catalog is kept in (ADR-0029), and how rvav
// signs in: a token over HTTPS, or a deploy key over SSH (Credential).
type GitConfig struct {
	URL        string
	Branch     string
	Dir        string
	Username   string
	Credential string
	KnownHosts string
	Insecure   bool
	// Committer is the address of rvav's commits; WebhookSecret checks the
	// Git server's calls; Interval is how often the repository is read;
	// Cache is where the clone is kept.
	Committer     string
	WebhookSecret string
	Interval      time.Duration
	Cache         string
}

// Configured says whether rvav keeps the catalog in Git.
func (c GitConfig) Configured() bool { return c.URL != "" }

type EfficientIPConfig struct {
	Fake bool
	// FakeLatency is how long the fake SOLIDserver takes to answer each
	// call, as a real one would (load tests).
	FakeLatency time.Duration
	URL         string
	Auth        string // native, basic or token
	Username    string // the token's key ID with token authentication
	Password    string // the token's secret with token authentication
	Insecure    bool
	Space       string
	DNSServer   string
	DNSView     string
	// Instance tells this rvav's objects from another's on a shared
	// SOLIDserver (RVAV_INSTANCE_ID).
	Instance string
}

// SMTPConfig sends notifications by email; no host, no email.
type SMTPConfig struct {
	Host     string
	Port     int
	TLS      string // starttls (default), tls or none
	From     string
	Username string
	Password string
}

// TeamsConfig sends notifications through an Azure bot; no app ID, no Teams.
type TeamsConfig struct {
	AppID       string
	TenantID    string
	AppPassword string
	// ServiceURL is the Bot Connector rvav starts conversations on.
	ServiceURL string
	// Microsoft's endpoints, for other clouds and tests: sign-in, Graph,
	// and the Bot Framework's OpenID configuration (its signing keys).
	LoginURL, GraphURL, OpenIDURL string
}

// SlackConfig sends notifications through a Slack app; no bot token, no
// Slack.
type SlackConfig struct {
	BotToken      string
	SigningSecret string
	// APIURL is Slack's Web API (tests point it elsewhere).
	APIURL string
}

type AWXConfig struct {
	Fake         bool
	URL          string
	Token        string
	Organization string // template lookups stay in this organisation when set
	// The catalog imports the organisation's templates with any of these labels.
	CatalogLabels []string
	// Templates with any of these labels become day-2 actions.
	ActionLabels      []string
	SyncInterval      time.Duration
	Insecure          bool
	InventorySourceID int
	// Fake only: time each fake task takes, and a task title that fails.
	FakeTaskDelay time.Duration
	FakeFailTask  string
}

func Load() (Config, error) {
	c := Config{
		HTTPAddr:        env("RVAV_HTTP_ADDR", ":8080"),
		MetricsAddr:     metricsAddr(),
		DatabaseURL:     env("RVAV_DATABASE_URL", "postgres://rvav:rvav@localhost:5432/rvav?sslmode=disable"),
		LogLevel:        env("RVAV_LOG_LEVEL", "info"),
		CORSOrigins:     list("RVAV_CORS_ORIGINS"),
		WMKSSDKURLs:     list("RVAV_WMKS_SDK_URLS"),
		ConsoleUsername: env("RVAV_CONSOLE_VCENTER_USERNAME", ""),
		ConsolePassword: env("RVAV_CONSOLE_VCENTER_PASSWORD", ""),
		Auth: AuthConfig{
			Mode:             env("RVAV_AUTH_MODE", "oidc"),
			OIDCIssuer:       env("RVAV_OIDC_ISSUER", ""),
			OIDCDiscoveryURL: env("RVAV_OIDC_DISCOVERY_URL", ""),
			OIDCClientID:     env("RVAV_OIDC_CLIENT_ID", "rvav"),
			Scopes:           env("RVAV_OIDC_SCOPES", "openid profile email"),
			GroupsClaim:      env("RVAV_OIDC_GROUPS_CLAIM", "groups"),
			DevUser:          env("RVAV_DEV_USER", "lmartin"),
			AllowDev:         boolean("RVAV_ALLOW_DEV_AUTH"),
			AdminPassword:    env("RVAV_ADMIN_PASSWORD", ""),
		},
		Temporal: TemporalConfig{
			Address:   env("RVAV_TEMPORAL_ADDRESS", "localhost:7233"),
			Namespace: env("RVAV_TEMPORAL_NAMESPACE", "default"),
			TaskQueue: env("RVAV_TEMPORAL_TASK_QUEUE", "rvav-provision"),
			// An API key or a client certificate means TLS.
			TLS: boolean("RVAV_TEMPORAL_TLS") || os.Getenv("RVAV_TEMPORAL_API_KEY") != "" ||
				os.Getenv("RVAV_TEMPORAL_TLS_CERT_FILE") != "",
			CAFile:     env("RVAV_TEMPORAL_TLS_CA_FILE", ""),
			CertFile:   env("RVAV_TEMPORAL_TLS_CERT_FILE", ""),
			KeyFile:    env("RVAV_TEMPORAL_TLS_KEY_FILE", ""),
			ServerName: env("RVAV_TEMPORAL_TLS_SERVER_NAME", ""),
			APIKey:     env("RVAV_TEMPORAL_API_KEY", ""),
		},
		VCenter: VCenterConfig{
			URL:      env("RVAV_VCENTER_URL", ""),
			Username: env("RVAV_VCENTER_USERNAME", ""),
			Password: env("RVAV_VCENTER_PASSWORD", ""),
			Insecure: boolean("RVAV_VCENTER_INSECURE"),
			Name:     env("RVAV_VCENTER_NAME", "vc01.corp.example"),
		},
		IPAM: EfficientIPConfig{
			Fake:      boolean("RVAV_FAKE_EFFICIENTIP"),
			URL:       env("RVAV_EFFICIENTIP_URL", ""),
			Username:  env("RVAV_EFFICIENTIP_USERNAME", ""),
			Password:  env("RVAV_EFFICIENTIP_PASSWORD", ""),
			Insecure:  boolean("RVAV_EFFICIENTIP_INSECURE"),
			Auth:      env("RVAV_EFFICIENTIP_AUTH", "native"),
			Space:     env("RVAV_EFFICIENTIP_SPACE", ""),
			DNSServer: env("RVAV_EFFICIENTIP_DNS_SERVER", ""),
			DNSView:   env("RVAV_EFFICIENTIP_DNS_VIEW", ""),
			Instance:  env("RVAV_INSTANCE_ID", "rvav"),
		},
		SecretsKey:      env("RVAV_SECRETS_KEY", ""),
		SecretsOldKeys:  list("RVAV_SECRETS_OLD_KEYS"),
		RESTSourceHosts: list("RVAV_REST_SOURCE_HOSTS"),
		WebhookHosts:    list("RVAV_WEBHOOK_HOSTS"),
		PublicURL:       strings.TrimRight(env("RVAV_PUBLIC_URL", "http://localhost:5173"), "/"),
		Teams: TeamsConfig{
			AppID:       env("RVAV_TEAMS_APP_ID", ""),
			TenantID:    env("RVAV_TEAMS_TENANT_ID", ""),
			AppPassword: env("RVAV_TEAMS_APP_PASSWORD", ""),
			ServiceURL:  strings.TrimRight(env("RVAV_TEAMS_SERVICE_URL", "https://smba.trafficmanager.net/teams"), "/"),
			LoginURL:    strings.TrimRight(env("RVAV_TEAMS_LOGIN_URL", "https://login.microsoftonline.com"), "/"),
			GraphURL:    strings.TrimRight(env("RVAV_TEAMS_GRAPH_URL", "https://graph.microsoft.com"), "/"),
			OpenIDURL:   env("RVAV_TEAMS_OPENID_URL", "https://login.botframework.com/v1/.well-known/openidconfiguration"),
		},
		Slack: SlackConfig{
			BotToken:      env("RVAV_SLACK_BOT_TOKEN", ""),
			SigningSecret: env("RVAV_SLACK_SIGNING_SECRET", ""),
			APIURL:        env("RVAV_SLACK_API_URL", "https://slack.com/api/"),
		},
		SMTP: SMTPConfig{
			Host:     env("RVAV_SMTP_HOST", ""),
			TLS:      env("RVAV_SMTP_TLS", "starttls"),
			From:     env("RVAV_SMTP_FROM", "rvav <rvav@localhost>"),
			Username: env("RVAV_SMTP_USERNAME", ""),
			Password: env("RVAV_SMTP_PASSWORD", ""),
		},
		NSX: NSXConfig{
			URL:      env("RVAV_NSX_URL", ""),
			Username: env("RVAV_NSX_USERNAME", ""),
			Password: env("RVAV_NSX_PASSWORD", ""),
			CertFile: env("RVAV_NSX_CERT_FILE", ""),
			KeyFile:  env("RVAV_NSX_KEY_FILE", ""),
			Insecure: boolean("RVAV_NSX_INSECURE"),
		},
		Git: GitConfig{
			URL:           env("RVAV_GIT_URL", ""),
			Branch:        env("RVAV_GIT_BRANCH", "main"),
			Dir:           env("RVAV_GIT_DIR", "catalog"),
			Username:      env("RVAV_GIT_USERNAME", ""),
			Credential:    env("RVAV_GIT_CREDENTIAL", ""),
			KnownHosts:    env("RVAV_GIT_KNOWN_HOSTS", ""),
			Insecure:      boolean("RVAV_GIT_INSECURE"),
			Committer:     env("RVAV_GIT_COMMITTER", "rvav@localhost"),
			WebhookSecret: env("RVAV_GIT_WEBHOOK_SECRET", ""),
			Cache:         env("RVAV_GIT_CACHE", filepath.Join(os.TempDir(), "rvav-git")),
		},
		VRA: VRAConfig{
			URL:      env("RVAV_VRA_URL", ""),
			Token:    env("RVAV_VRA_TOKEN", ""),
			Username: env("RVAV_VRA_USERNAME", ""),
			Password: env("RVAV_VRA_PASSWORD", ""),
			Domain:   env("RVAV_VRA_DOMAIN", ""),
			Insecure: boolean("RVAV_VRA_INSECURE"),
		},
		AWX: AWXConfig{
			Fake:          boolean("RVAV_FAKE_AWX"),
			URL:           env("RVAV_AWX_URL", ""),
			Token:         env("RVAV_AWX_TOKEN", ""),
			Organization:  env("RVAV_AWX_ORGANIZATION", ""),
			CatalogLabels: list("RVAV_AWX_CATALOG_LABELS"),
			ActionLabels:  list("RVAV_AWX_ACTION_LABELS"),
			Insecure:      boolean("RVAV_AWX_INSECURE"),
		},
	}

	var err error
	if c.AWX.InventorySourceID, err = integer("RVAV_AWX_INVENTORY_SOURCE_ID", 0); err != nil {
		return c, err
	}
	if c.SMTP.Port, err = integer("RVAV_SMTP_PORT", 587); err != nil {
		return c, err
	}
	if c.AWX.FakeTaskDelay, err = duration("RVAV_FAKE_AWX_TASK_DELAY", 1500*time.Millisecond); err != nil {
		return c, err
	}
	c.AWX.FakeFailTask = env("RVAV_FAKE_AWX_FAIL_TASK", "")
	if len(c.AWX.CatalogLabels) == 0 {
		c.AWX.CatalogLabels = []string{"catalog"}
	}
	if len(c.AWX.ActionLabels) == 0 {
		c.AWX.ActionLabels = []string{"day2"}
	}
	if c.AWX.SyncInterval, err = duration("RVAV_AWX_SYNC_INTERVAL", 5*time.Minute); err != nil {
		return c, err
	}
	if c.IPAMSyncInterval, err = duration("RVAV_IPAM_SYNC_INTERVAL", 5*time.Minute); err != nil {
		return c, err
	}
	if c.Git.Interval, err = duration("RVAV_GIT_SYNC_INTERVAL", 5*time.Minute); err != nil {
		return c, err
	}
	if c.IPAM.FakeLatency, err = duration("RVAV_FAKE_EFFICIENTIP_LATENCY", 0); err != nil {
		return c, err
	}
	if c.SyncInterval, err = duration("RVAV_SYNC_INTERVAL", 2*time.Minute); err != nil {
		return c, err
	}

	// Without an identity provider, only the local administrator and API
	// tokens sign in (ADR-0030).
	switch c.Auth.Mode {
	case "oidc", "dev":
	default:
		return c, fmt.Errorf("RVAV_AUTH_MODE must be oidc or dev, got %q", c.Auth.Mode)
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func boolean(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func integer(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func duration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func list(key string) []string {
	var out []string
	for _, s := range strings.Split(os.Getenv(key), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func metricsAddr() string {
	switch v := os.Getenv("RVAV_METRICS_ADDR"); v {
	case "":
		return ":9090"
	case "off":
		return ""
	default:
		return v
	}
}
