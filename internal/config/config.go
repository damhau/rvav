// Package config reads rvav settings from environment variables.
// See .env.example for the full list.
package config

import (
	"fmt"
	"net/netip"
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
	// TrustedProxies are the reverse proxies whose X-Forwarded-For is
	// believed, for the address a sign-in came from (ADR-0058).
	TrustedProxies []netip.Prefix

	Auth AuthConfig

	Temporal TemporalConfig
	VCenter  VCenterConfig
	IPAM     EfficientIPConfig
	AWX      AWXConfig
	// VRA is the vRA the importer reads (P2, ADR-0027).
	VRA VRAConfig
	// AD is the Active Directory computer objects are made in (ADR-0064).
	AD ADConfig
	// ServiceNow is the instance rvav keeps CIs in and takes requests
	// from (ADR-0065).
	ServiceNow ServiceNowConfig
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
	// CABundle are CAs trusted besides the system's, in PEM, for every
	// connection rvav makes (package trust).
	CABundle string

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
	// DrainTimeout is how long a process applying newer connection
	// settings lets its running work finish before it exits (ADR-0030).
	DrainTimeout time.Duration
	// ScheduleOwnerIdleDays is how long a recurring schedule's owner may go
	// without signing in or using a token before its runs are skipped: a
	// run has the groups of their last sign-in (ADR-0057). 0: never.
	ScheduleOwnerIdleDays int

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
	// ManagerClaim names the user's manager, by email or subject
	// (RVAV_OIDC_MANAGER_CLAIM, ADR-0041).
	ManagerClaim string
	DevUser      string
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
	// CertSHA256 is a certificate trusted for this connection besides
	// those the CAs vouch for: pinned by its SHA-256 (package trust).
	CertSHA256 string
	// Name is how the vCenter appears in the UI and API, for example vc01.corp.example.
	Name string
}

// Configured says whether rvav has a vCenter: none on a new install, until
// it is set in Administration (ADR-0030).
func (c VCenterConfig) Configured() bool { return c.URL != "" }

// VRAConfig is where vRA is and how rvav signs in there: a refresh token, or
// a user and password in a domain.
type VRAConfig struct {
	URL      string
	Token    string
	Username string
	Password string
	Domain   string
	Insecure bool
	// CertSHA256 is a certificate trusted for this connection besides
	// those the CAs vouch for: pinned by its SHA-256 (package trust).
	CertSHA256 string
}

// ADConfig is the Active Directory rvav makes VMs' computer objects in
// (ADR-0064): ldaps://, or ldap:// with StartTLS, bound as a service
// account.
type ADConfig struct {
	URL      string
	Username string
	Password string
	BaseDN   string
	Insecure bool
	// PlainLDAP: ldap:// in the clear, without StartTLS, the password too.
	PlainLDAP  bool
	CertSHA256 string
}

// Configured says whether rvav has a directory.
func (c ADConfig) Configured() bool { return c.URL != "" && c.BaseDN != "" }

// ServiceNowConfig is the ServiceNow instance (ADR-0065): https only, as
// a user with its password or an OAuth client with its secret.
type ServiceNowConfig struct {
	URL string
	// Auth is basic or oauth (the client credentials grant).
	Auth       string
	Username   string
	ClientID   string
	Secret     string
	Insecure   bool
	CertSHA256 string
}

// Configured says whether rvav has a ServiceNow instance.
func (c ServiceNowConfig) Configured() bool { return c.URL != "" }

// NSXConfig: where the NSX Manager is, and rvav's account there, a user
// and password or a client certificate (a principal identity).
type NSXConfig struct {
	URL      string
	Username string
	Password string
	CertFile string
	KeyFile  string
	Insecure bool
	// CertSHA256 is a certificate trusted for this connection besides
	// those the CAs vouch for: pinned by its SHA-256 (package trust).
	CertSHA256 string
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
	// CertSHA256 is a certificate trusted for this connection besides
	// those the CAs vouch for: pinned by its SHA-256 (package trust).
	CertSHA256 string
	Space      string
	DNSServer  string
	DNSView    string
	// Instance tells this rvav's objects from another's on a shared
	// SOLIDserver (RVAV_INSTANCE_ID).
	Instance string
}

// Configured says whether rvav has a SOLIDserver, or the fake one.
func (c EfficientIPConfig) Configured() bool { return c.Fake || c.URL != "" }

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
	ActionLabels []string
	SyncInterval time.Duration
	Insecure     bool
	// CertSHA256 is a certificate trusted for this connection besides
	// those the CAs vouch for: pinned by its SHA-256 (package trust).
	CertSHA256        string
	InventorySourceID int
	// Fake only: time each fake task takes, and a task title that fails.
	FakeTaskDelay time.Duration
	FakeFailTask  string
}

// Configured says whether rvav has an AWX, or the fake one.
func (c AWXConfig) Configured() bool { return c.Fake || c.URL != "" }

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
			ManagerClaim:     env("RVAV_OIDC_MANAGER_CLAIM", "manager"),
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
			URL:        env("RVAV_VCENTER_URL", ""),
			Username:   env("RVAV_VCENTER_USERNAME", ""),
			Password:   env("RVAV_VCENTER_PASSWORD", ""),
			Insecure:   boolean("RVAV_VCENTER_INSECURE"),
			CertSHA256: env("RVAV_VCENTER_CERT_SHA256", ""),
			Name:       env("RVAV_VCENTER_NAME", "vc01.corp.example"),
		},
		IPAM: EfficientIPConfig{
			Fake:       boolean("RVAV_FAKE_EFFICIENTIP"),
			URL:        env("RVAV_EFFICIENTIP_URL", ""),
			Username:   env("RVAV_EFFICIENTIP_USERNAME", ""),
			Password:   env("RVAV_EFFICIENTIP_PASSWORD", ""),
			Insecure:   boolean("RVAV_EFFICIENTIP_INSECURE"),
			CertSHA256: env("RVAV_EFFICIENTIP_CERT_SHA256", ""),
			Auth:       env("RVAV_EFFICIENTIP_AUTH", "native"),
			Space:      env("RVAV_EFFICIENTIP_SPACE", ""),
			DNSServer:  env("RVAV_EFFICIENTIP_DNS_SERVER", ""),
			DNSView:    env("RVAV_EFFICIENTIP_DNS_VIEW", ""),
			Instance:   env("RVAV_INSTANCE_ID", "rvav"),
		},
		SecretsKey:      env("RVAV_SECRETS_KEY", ""),
		SecretsOldKeys:  list("RVAV_SECRETS_OLD_KEYS"),
		RESTSourceHosts: list("RVAV_REST_SOURCE_HOSTS"),
		WebhookHosts:    list("RVAV_WEBHOOK_HOSTS"),
		PublicURL:       strings.TrimRight(env("RVAV_PUBLIC_URL", "http://localhost:5173"), "/"),
		CABundle:        env("RVAV_CA_BUNDLE", ""),
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
			URL:        env("RVAV_NSX_URL", ""),
			Username:   env("RVAV_NSX_USERNAME", ""),
			Password:   env("RVAV_NSX_PASSWORD", ""),
			CertFile:   env("RVAV_NSX_CERT_FILE", ""),
			KeyFile:    env("RVAV_NSX_KEY_FILE", ""),
			Insecure:   boolean("RVAV_NSX_INSECURE"),
			CertSHA256: env("RVAV_NSX_CERT_SHA256", ""),
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
			URL:        env("RVAV_VRA_URL", ""),
			Token:      env("RVAV_VRA_TOKEN", ""),
			Username:   env("RVAV_VRA_USERNAME", ""),
			Password:   env("RVAV_VRA_PASSWORD", ""),
			Domain:     env("RVAV_VRA_DOMAIN", ""),
			Insecure:   boolean("RVAV_VRA_INSECURE"),
			CertSHA256: env("RVAV_VRA_CERT_SHA256", ""),
		},
		AD: ADConfig{
			URL:        env("RVAV_AD_URL", ""),
			Username:   env("RVAV_AD_USERNAME", ""),
			Password:   env("RVAV_AD_PASSWORD", ""),
			BaseDN:     env("RVAV_AD_BASE_DN", ""),
			Insecure:   boolean("RVAV_AD_INSECURE"),
			PlainLDAP:  boolean("RVAV_AD_PLAIN_LDAP"),
			CertSHA256: env("RVAV_AD_CERT_SHA256", ""),
		},
		ServiceNow: ServiceNowConfig{
			URL:        env("RVAV_SERVICENOW_URL", ""),
			Auth:       env("RVAV_SERVICENOW_AUTH", "basic"),
			Username:   env("RVAV_SERVICENOW_USERNAME", ""),
			ClientID:   env("RVAV_SERVICENOW_CLIENT_ID", ""),
			Secret:     env("RVAV_SERVICENOW_SECRET", ""),
			Insecure:   boolean("RVAV_SERVICENOW_INSECURE"),
			CertSHA256: env("RVAV_SERVICENOW_CERT_SHA256", ""),
		},
		AWX: AWXConfig{
			Fake:          boolean("RVAV_FAKE_AWX"),
			URL:           env("RVAV_AWX_URL", ""),
			Token:         env("RVAV_AWX_TOKEN", ""),
			Organization:  env("RVAV_AWX_ORGANIZATION", ""),
			CatalogLabels: list("RVAV_AWX_CATALOG_LABELS"),
			ActionLabels:  list("RVAV_AWX_ACTION_LABELS"),
			Insecure:      boolean("RVAV_AWX_INSECURE"),
			CertSHA256:    env("RVAV_AWX_CERT_SHA256", ""),
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
	if c.DrainTimeout, err = duration("RVAV_DRAIN_TIMEOUT", 5*time.Minute); err != nil {
		return c, err
	}
	if c.ScheduleOwnerIdleDays, err = integer("RVAV_SCHEDULE_OWNER_IDLE_DAYS", 90); err != nil {
		return c, err
	}
	if c.ScheduleOwnerIdleDays < 0 {
		return c, fmt.Errorf("RVAV_SCHEDULE_OWNER_IDLE_DAYS must be 0 or more, got %d", c.ScheduleOwnerIdleDays)
	}

	if c.TrustedProxies, err = prefixes("RVAV_TRUSTED_PROXIES"); err != nil {
		return c, err
	}

	if a := c.ServiceNow.Auth; a != "basic" && a != "oauth" {
		return c, fmt.Errorf("RVAV_SERVICENOW_AUTH must be basic or oauth, got %q", a)
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

// prefixes reads a list of networks (CIDR) or single addresses.
func prefixes(key string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list(key) {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, aerr := netip.ParseAddr(s)
			if aerr != nil {
				return nil, fmt.Errorf("%s: %q is neither an address nor a network such as 10.0.0.0/8", key, s)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, p.Masked())
	}
	return out, nil
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
