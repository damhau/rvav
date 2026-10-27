// Command rvav runs the rvav processes: the HTTP API, the Temporal worker
// and the vCenter inventory sync, plus admin and development helpers.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/store"
)

const usage = `rvav: self-service portal for vSphere

Usage:
  rvav api                 serve the HTTP API and live events
  rvav worker              run the Temporal worker (provisioning workflows)
  rvav sync                keep the vCenter inventory cache up to date
  rvav dev                 api, worker and sync in one process (development)
  rvav migrate [-seed]     apply database migrations, optionally load seed data
  rvav seed                load or refresh the example organisation and catalog
  rvav vcsim-bootstrap     create the GVA inventory in vcsim (development)
  rvav fake-efficientip    serve a fake SOLIDserver on RVAV_HTTP_ADDR (development)
  rvav ipam-reconcile      list EfficientIP addresses and records no deployment owns,
                           and owned ones EfficientIP no longer has (JSON)
  rvav healthcheck         exit 0 when the local API is ready (container probe)

Configuration comes from RVAV_* environment variables; see .env.example.
`

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		fmt.Print(usage)
		return
	}
	cmd, args := os.Args[1], os.Args[2:]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cmd, args); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "rvav %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func logger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func run(ctx context.Context, cmd string, args []string) error {
	// The images have no shell or curl, so the container probe is built in.
	if cmd == "healthcheck" {
		return healthcheck(ctx)
	}
	// migrate, seed, vcsim-bootstrap and fake-efficientip work without auth settings.
	if cmd == "migrate" || cmd == "seed" || cmd == "vcsim-bootstrap" || cmd == "fake-efficientip" {
		if os.Getenv("RVAV_AUTH_MODE") == "" {
			os.Setenv("RVAV_AUTH_MODE", "dev")
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger(cfg.LogLevel).With("process", cmd)
	slog.SetDefault(log)

	switch cmd {
	case "migrate":
		fs := flag.NewFlagSet("migrate", flag.ExitOnError)
		withSeed := fs.Bool("seed", false, "load the example organisation and catalog after migrating")
		_ = fs.Parse(args)
		if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
			return err
		}
		log.Info("migrations applied")
		if !*withSeed {
			return nil
		}
		return runSeed(ctx, cfg, log)
	case "seed":
		return runSeed(ctx, cfg, log)
	case "vcsim-bootstrap":
		vc, err := vsphere.New(ctx, cfg.VCenter.Name, cfg.VCenter.URL, cfg.VCenter.Username, cfg.VCenter.Password, cfg.VCenter.Insecure)
		if err != nil {
			return err
		}
		c, err := vc.Vim(ctx)
		if err != nil {
			return err
		}
		if err := vsphere.Bootstrap(ctx, c, vsphere.DefaultLayout()); err != nil {
			return err
		}
		log.Info("GVA inventory created", "vcenter", cfg.VCenter.URL)
		return nil
	}

	if cmd == "fake-efficientip" {
		st, err := store.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer st.Close()
		fake, err := app.NewFakeEfficientIP(ctx, st)
		if err != nil {
			return err
		}
		url, err := fake.Start(ctx, cfg.HTTPAddr)
		if err != nil {
			return err
		}
		log.Warn("serving a fake SOLIDserver; no real addresses or DNS records are created", "url", url)
		<-ctx.Done()
		return nil
	}

	a, err := app.Open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer a.Close()

	switch cmd {
	case "api":
		return a.RunAPI(ctx)
	case "worker":
		return a.RunWorker(ctx)
	case "sync":
		return a.RunSync(ctx)
	case "ipam-reconcile":
		report, err := a.Core(nil).ReconcileIPAM(ctx, &auth.Principal{User: store.User{ID: core.SystemUserID}, GlobalRoles: []string{"admin"}})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case "dev":
		errc := make(chan error, 3)
		go func() { errc <- fmt.Errorf("sync: %w", a.RunSync(ctx)) }()
		go func() { errc <- fmt.Errorf("worker: %w", a.RunWorker(ctx)) }()
		go func() { errc <- fmt.Errorf("api: %w", a.RunAPI(ctx)) }()
		err := <-errc
		stop := ctx.Err() != nil
		if stop {
			return nil
		}
		return err
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("unknown command %q", cmd)
}

// healthcheck asks the API on this host for /readyz, which also checks the
// database.
func healthcheck(ctx context.Context) error {
	addr := os.Getenv("RVAV_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/readyz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("readyz answered %s", resp.Status)
	}
	return nil
}

func runSeed(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := seed.Run(ctx, st); err != nil {
		return err
	}
	log.Info("seed data loaded")
	return nil
}
