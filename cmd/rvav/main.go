// Command rvav runs the rvav processes: the HTTP API, the Temporal worker
// and the vCenter inventory sync, plus admin and development helpers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/provision"
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
  rvav approve <request> [-reject] [-by <user>] [-comment <text>]
                           approve or reject a request waiting for approval

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
	// migrate, seed and vcsim-bootstrap work without auth settings.
	if cmd == "migrate" || cmd == "seed" || cmd == "vcsim-bootstrap" || cmd == "approve" {
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
	case "approve":
		return approve(ctx, a, args)
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("unknown command %q", cmd)
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

func approve(ctx context.Context, a *app.App, args []string) error {
	fs := flag.NewFlagSet("approve", flag.ExitOnError)
	reject := fs.Bool("reject", false, "reject instead of approving")
	by := fs.String("by", "mkeller", "seeded user name or user ID of the approver")
	comment := fs.String("comment", "", "comment sent to the requester")
	var positional []string
	for len(args) > 0 {
		_ = fs.Parse(args)
		args = fs.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	if len(positional) != 1 {
		return errors.New("usage: rvav approve <request> [-reject] [-by <user>] [-comment <text>]")
	}
	tc, err := a.Temporal()
	if err != nil {
		return err
	}
	byID := *by
	if !strings.HasPrefix(byID, "usr_") {
		byID = seed.UserID(byID)
	}
	svc := a.Core(&provision.Temporal{Client: tc, TaskQueue: a.Cfg.Temporal.TaskQueue})
	if err := svc.Decide(ctx, positional[0], !*reject, byID, *comment); err != nil {
		return err
	}
	verb := "approved"
	if *reject {
		verb = "rejected"
	}
	a.Log.Info("decision sent", "request", positional[0], "decision", verb)
	return nil
}
