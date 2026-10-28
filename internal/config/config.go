// Package config reads rvav settings from environment variables.
// See .env.example for the full list.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	LogLevel    string
	CORSOrigins []string

	Auth AuthConfig

	Temporal TemporalConfig
	VCenter  VCenterConfig
	IPAM     EfficientIPConfig
	AWX      AWXConfig
	// Notification channels (ADR-0023).
	SMTP  SMTPConfig
	Teams TeamsConfig

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
	GroupsClaim      string
	DevUser          string
	// AllowDev must be set (RVAV_ALLOW_DEV_AUTH=1) for dev mode to start, so
	// that a stray RVAV_AUTH_MODE=dev can't open a real installation.
	AllowDev bool
}

type TemporalConfig struct {
	Address   string
	Namespace string
	TaskQueue string
}

type VCenterConfig struct {
	URL      string
	Username string
	Password string
	Insecure bool
	// Name is how the vCenter appears in the UI and API, for example vc01.corp.example.
	Name string
}

type EfficientIPConfig struct {
	Fake      bool
	URL       string
	Auth      string // native, basic or token
	Username  string // the token's key ID with token authentication
	Password  string // the token's secret with token authentication
	Insecure  bool
	Space     string
	DNSServer string
	DNSView   string
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
			GroupsClaim:      env("RVAV_OIDC_GROUPS_CLAIM", "groups"),
			DevUser:          env("RVAV_DEV_USER", "lmartin"),
			AllowDev:         boolean("RVAV_ALLOW_DEV_AUTH"),
		},
		Temporal: TemporalConfig{
			Address:   env("RVAV_TEMPORAL_ADDRESS", "localhost:7233"),
			Namespace: env("RVAV_TEMPORAL_NAMESPACE", "default"),
			TaskQueue: env("RVAV_TEMPORAL_TASK_QUEUE", "rvav-provision"),
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
		SMTP: SMTPConfig{
			Host:     env("RVAV_SMTP_HOST", ""),
			TLS:      env("RVAV_SMTP_TLS", "starttls"),
			From:     env("RVAV_SMTP_FROM", "rvav <rvav@localhost>"),
			Username: env("RVAV_SMTP_USERNAME", ""),
			Password: env("RVAV_SMTP_PASSWORD", ""),
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
	if c.SyncInterval, err = duration("RVAV_SYNC_INTERVAL", 20*time.Second); err != nil {
		return c, err
	}

	switch c.Auth.Mode {
	case "oidc":
		if c.Auth.OIDCIssuer == "" {
			return c, fmt.Errorf("RVAV_OIDC_ISSUER is required when RVAV_AUTH_MODE=oidc")
		}
	case "dev":
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
