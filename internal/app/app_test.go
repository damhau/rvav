package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/config"
)

func TestDevAuthNeedsExplicitFlag(t *testing.T) {
	a := &App{Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Cfg: config.Config{Auth: config.AuthConfig{Mode: "dev", DevUser: "lmartin"}}}
	if _, err := a.Authenticator(context.Background()); err == nil || !strings.Contains(err.Error(), "RVAV_ALLOW_DEV_AUTH=1") {
		t.Fatalf("dev mode without the flag: err = %v", err)
	}
	a.Cfg.Auth.AllowDev = true
	if _, err := a.Authenticator(context.Background()); err != nil {
		t.Fatalf("dev mode with the flag: %v", err)
	}
}
