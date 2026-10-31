package config

import (
	"net/url"
	"os"
	"regexp"
	"strings"
)

// InstallSetting is a setting given in the environment only (ADR-0030),
// as the System page shows it. A test checks this list against the rows
// of configuration.md set at install.
type InstallSetting struct {
	Env string
	// Secret: only whether it is set is shown, never the value, but for
	// the database URL, shown without its password.
	Secret bool
	value  func(Config) string
}

// Install lists the install settings a Go process reads, in
// configuration.md's order; the web container's own are its.
var Install = []InstallSetting{
	{"RVAV_DATABASE_URL", true, func(c Config) string { return RedactDatabaseURL(c.DatabaseURL) }},
	{"RVAV_HTTP_ADDR", false, func(c Config) string { return c.HTTPAddr }},
	{"RVAV_METRICS_ADDR", false, func(c Config) string { return c.MetricsAddr }},
	{"RVAV_LOG_LEVEL", false, func(c Config) string { return c.LogLevel }},
	{"RVAV_PUBLIC_URL", false, func(c Config) string { return c.PublicURL }},
	{"RVAV_CORS_ORIGINS", false, func(c Config) string { return strings.Join(c.CORSOrigins, ", ") }},
	{"RVAV_DRAIN_TIMEOUT", false, func(c Config) string { return c.DrainTimeout.String() }},
	{"RVAV_ADMIN_PASSWORD", true, nil},
	{"RVAV_AUTH_MODE", false, func(c Config) string { return c.Auth.Mode }},
	{"RVAV_ALLOW_DEV_AUTH", false, func(c Config) string { return onOff(c.Auth.AllowDev) }},
	{"RVAV_DEV_USER", false, func(c Config) string { return c.Auth.DevUser }},
	{"RVAV_TEMPORAL_ADDRESS", false, func(c Config) string { return c.Temporal.Address }},
	{"RVAV_TEMPORAL_NAMESPACE", false, func(c Config) string { return c.Temporal.Namespace }},
	{"RVAV_TEMPORAL_TASK_QUEUE", false, func(c Config) string { return c.Temporal.TaskQueue }},
	{"RVAV_TEMPORAL_TLS", false, func(c Config) string { return onOff(c.Temporal.TLS) }},
	{"RVAV_TEMPORAL_TLS_CA_FILE", false, func(c Config) string { return c.Temporal.CAFile }},
	{"RVAV_TEMPORAL_TLS_CERT_FILE", false, func(c Config) string { return c.Temporal.CertFile }},
	// The key is secret, not where it is.
	{"RVAV_TEMPORAL_TLS_KEY_FILE", false, func(c Config) string { return c.Temporal.KeyFile }},
	{"RVAV_TEMPORAL_TLS_SERVER_NAME", false, func(c Config) string { return c.Temporal.ServerName }},
	{"RVAV_TEMPORAL_API_KEY", true, nil},
	{"RVAV_FAKE_EFFICIENTIP", false, func(c Config) string { return onOff(c.IPAM.Fake) }},
	{"RVAV_FAKE_EFFICIENTIP_LATENCY", false, func(c Config) string { return c.IPAM.FakeLatency.String() }},
	{"RVAV_FAKE_AWX", false, func(c Config) string { return onOff(c.AWX.Fake) }},
	{"RVAV_FAKE_AWX_TASK_DELAY", false, func(c Config) string { return c.AWX.FakeTaskDelay.String() }},
	{"RVAV_FAKE_AWX_FAIL_TASK", false, func(c Config) string { return c.AWX.FakeFailTask }},
	{"RVAV_GIT_WEBHOOK_SECRET", true, nil},
	{"RVAV_GIT_CACHE", false, func(c Config) string { return c.Git.Cache }},
	{"RVAV_SECRETS_KEY", true, nil},
	{"RVAV_SECRETS_OLD_KEYS", true, nil},
}

// Shown is what the System page shows of a setting: the value in effect
// (none for a secret), and whether the environment sets it.
func (s InstallSetting) Shown(c Config) (value *string, set bool) {
	set = os.Getenv(s.Env) != ""
	if s.value == nil {
		return nil, set
	}
	v := s.value(c)
	return &v, set
}

// dsnPassword is a key=value connection string's password, or
// sslpassword (the client key's).
var dsnPassword = regexp.MustCompile(`(?i)(\b\w*password\s*=\s*)('(?:[^'\\]|\\.)*'|\S+)`)

// RedactDatabaseURL takes the passwords out of a database URL, or of a
// key=value connection string: the user's, and password or sslpassword
// in the query. A URL with no host (a Unix socket's, its directory in the
// query) is a URL too. One url.Parse can't read, a password with a / or #
// not escaped say, is shown as its scheme only.
func RedactDatabaseURL(raw string) string {
	scheme, _, isURL := strings.Cut(raw, "://")
	if !isURL {
		return dsnPassword.ReplaceAllString(raw, "${1}xxxxx")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return scheme + "://xxxxx"
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "xxxxx")
	}
	q, hidden := u.Query(), false
	for k := range q {
		if strings.Contains(strings.ToLower(k), "password") {
			q.Set(k, "xxxxx")
			hidden = true
		}
	}
	if hidden {
		u.RawQuery = q.Encode()
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
