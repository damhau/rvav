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

	SyncInterval time.Duration
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
	Fake     bool
	URL      string
	Username string
	Password string
	Insecure bool
}

type AWXConfig struct {
	Fake              bool
	URL               string
	Token             string
	Insecure          bool
	InventorySourceID int
	// Fake only: time each fake task takes, and a task title that fails.
	FakeTaskDelay time.Duration
	FakeFailTask  string
}

func Load() (Config, error) {
	c := Config{
		HTTPAddr:    env("RVAV_HTTP_ADDR", ":8080"),
		DatabaseURL: env("RVAV_DATABASE_URL", "postgres://rvav:rvav@localhost:5432/rvav?sslmode=disable"),
		LogLevel:    env("RVAV_LOG_LEVEL", "info"),
		CORSOrigins: list("RVAV_CORS_ORIGINS"),
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
			Fake:     boolean("RVAV_FAKE_EFFICIENTIP"),
			URL:      env("RVAV_EFFICIENTIP_URL", ""),
			Username: env("RVAV_EFFICIENTIP_USERNAME", ""),
			Password: env("RVAV_EFFICIENTIP_PASSWORD", ""),
			Insecure: boolean("RVAV_EFFICIENTIP_INSECURE"),
		},
		AWX: AWXConfig{
			Fake:     boolean("RVAV_FAKE_AWX"),
			URL:      env("RVAV_AWX_URL", ""),
			Token:    env("RVAV_AWX_TOKEN", ""),
			Insecure: boolean("RVAV_AWX_INSECURE"),
		},
	}

	var err error
	if c.AWX.InventorySourceID, err = integer("RVAV_AWX_INVENTORY_SOURCE_ID", 0); err != nil {
		return c, err
	}
	if c.AWX.FakeTaskDelay, err = duration("RVAV_FAKE_AWX_TASK_DELAY", 1500*time.Millisecond); err != nil {
		return c, err
	}
	c.AWX.FakeFailTask = env("RVAV_FAKE_AWX_FAIL_TASK", "")
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
