// Command rvav runs the rvav processes: the HTTP API, the Temporal worker
// and the vCenter inventory sync, plus admin and development helpers.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vra"
	"github.com/damhau/rvav/internal/vraimport"

	"github.com/damhau/rvav/internal/observe"
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
  rvav awx-bootstrap [-content deploy/awx/content.yml] [-rvav-url URL -rvav-token TOKEN]
                           set up rvav's test content in the AWX at RVAV_AWX_URL
  rvav admin reset-password [-password-stdin]
                           set a new password for the local administrator (generated and
                           printed, or read from stdin), to be changed at the next sign-in;
                           ends its sessions
  rvav api-token -user ID -name NAME [-scope inventory] [-days N] [-revoke]
                           make an API token for a user and print it once
                           (-revoke deletes the user's tokens of that name)
  rvav ipam-reconcile      list EfficientIP addresses and records no deployment owns,
                           and owned ones EfficientIP no longer has (JSON)
  rvav secrets rotate      seal every stored secret again with RVAV_SECRETS_KEY, so the
                           keys in RVAV_SECRETS_OLD_KEYS can then be removed
  rvav vra-export [-o FILE] read the vRA at RVAV_VRA_URL (projects, cloud templates, forms,
                           catalog items and sharing, policies, deployments) into JSON
  rvav vra-plan [-f FILE]  what the importer would make of the vRA (or of vra-export's FILE):
                           each cloud template's catalog item and what it couldn't map (JSON)
  rvav vra-adopt [-f FILE] [-project VRA_ID=RVAV_ID,...] [-apply]
                           which of vRA's deployments rvav would adopt, and why not (JSON);
                           -apply adopts them through onboarding
  rvav vro-triage [-f FILE | -standard] [-md]
                           vRO's workflows and actions sorted into ADR-0007's groups A to D,
                           with what uses each and why (JSON, or Markdown with -md)
  rvav fake-vra            serve a fake vRA with a sample estate on RVAV_HTTP_ADDR (development)
  rvav fake-nsx            serve a fake NSX Manager with sample gateways, segments and groups on
                           RVAV_HTTP_ADDR (development)
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
	err := run(ctx, cmd, args)
	if errors.Is(err, app.ErrRestart) {
		stop()
		restart(cmd)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
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
	// migrate, seed and the development helpers work without auth settings.
	if cmd == "migrate" || cmd == "seed" || cmd == "vcsim-bootstrap" || cmd == "fake-efficientip" || cmd == "awx-bootstrap" ||
		cmd == "admin" || cmd == "vra-export" || cmd == "vra-plan" || cmd == "vro-triage" || cmd == "fake-vra" || cmd == "fake-nsx" {
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
	case "vra-export":
		fs := flag.NewFlagSet("vra-export", flag.ExitOnError)
		out := fs.String("o", "", "write to this file (standard output by default)")
		_ = fs.Parse(args)
		c, err := vra.New(vra.Options{URL: cfg.VRA.URL, Token: cfg.VRA.Token, Username: cfg.VRA.Username,
			Password: cfg.VRA.Password, Domain: cfg.VRA.Domain, Insecure: cfg.VRA.Insecure})
		if err != nil {
			return err
		}
		e, err := c.Read(ctx)
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			return err
		}
		log.Info("vRA read", "projects", len(e.Projects), "templates", len(e.Blueprints), "forms", len(e.Forms),
			"items", len(e.CatalogItems), "policies", len(e.Policies), "deployments", len(e.Deployments),
			"subscriptions", len(e.Subscriptions), "day2_actions", len(e.ResourceActions), "vro_workflows", len(e.Workflows),
			"vro_actions", len(e.Actions))
		if *out == "" {
			_, err = os.Stdout.Write(append(b, '\n'))
			return err
		}
		return os.WriteFile(*out, b, 0o600)
	case "vra-plan":
		fs := flag.NewFlagSet("vra-plan", flag.ExitOnError)
		file := fs.String("f", "", "an estate vra-export wrote (the vRA at RVAV_VRA_URL by default)")
		_ = fs.Parse(args)
		e, err := readEstate(ctx, cfg, *file)
		if err != nil {
			return err
		}
		plan := vraimport.MakePlan(e)
		b, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(b, '\n'))
		return err
	case "vro-triage":
		fs := flag.NewFlagSet("vro-triage", flag.ExitOnError)
		file := fs.String("f", "", "an estate vra-export wrote (the vRA at RVAV_VRA_URL by default)")
		md := fs.Bool("md", false, "a Markdown report instead of JSON")
		standard := fs.Bool("standard", false, "the vRO workflows an enterprise vRA typically has, not a vRA's (D2's sizing)")
		_ = fs.Parse(args)
		var e *vra.Estate
		var err error
		if *standard {
			s := vra.StandardVRO(time.Now())
			e = &s
		} else if e, err = readEstate(ctx, cfg, *file); err != nil {
			return err
		}
		t := vraimport.TriageVRO(e)
		if *md {
			_, err = os.Stdout.WriteString(t.Markdown())
			return err
		}
		b, err := json.MarshalIndent(t, "", "  ")
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(b, '\n'))
		return err
	case "fake-vra":
		url, err := vra.NewFakeServer(vra.SampleEstate(time.Now())).Start(ctx, cfg.HTTPAddr)
		if err != nil {
			return err
		}
		log.Warn("serving a fake vRA with a sample estate", "url", url, "user", "rvav-importer", "password", "vra",
			"domain", "corp.example", "refresh_token", vra.FakeRefreshToken)
		<-ctx.Done()
		return nil
	case "fake-nsx":
		f := nsx.NewFakeServer()
		// Its vcsim isn't NSX's compute manager: VMs are taken as they are tagged.
		f.AnyVM = true
		// A segment's port group in the stack's vcsim, as NSX makes one in vCenter.
		if cfg.VCenter.URL != "" {
			vc, err := vsphere.New(ctx, cfg.VCenter.Name, cfg.VCenter.URL, cfg.VCenter.Username, cfg.VCenter.Password, cfg.VCenter.Insecure)
			if err != nil {
				return err
			}
			f.OnSegment = func(s nsx.Segment, deleted bool) {
				c, err := vc.Vim(ctx)
				if err == nil {
					err = vsphere.SegmentPortGroup(ctx, c, s.DisplayName, "dvpg-app-dev-vl310", deleted)
				}
				if err != nil {
					log.Error("segment port group", "segment", s.DisplayName, "err", err)
				}
			}
		}
		url, err := f.Start(ctx, cfg.HTTPAddr)
		if err != nil {
			return err
		}
		log.Warn("serving a fake NSX Manager; nothing is made in a real NSX", "url", url, "user", nsx.FakeUser, "password", nsx.FakePassword)
		<-ctx.Done()
		return nil
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
	case "awx-bootstrap":
		fs := flag.NewFlagSet("awx-bootstrap", flag.ExitOnError)
		path := fs.String("content", "deploy/awx/content.yml", "the content to set up")
		rvavURL := fs.String("rvav-url", "", "rvav's URL as AWX reaches it, for the rvav inventory's source")
		rvavToken := fs.String("rvav-token", os.Getenv("RVAV_INVENTORY_TOKEN"), "an rvav API token of a user with the auditor role (rvav api-token)")
		_ = fs.Parse(args)
		content, err := awx.LoadContent(*path)
		if err != nil {
			return err
		}
		c, err := awx.NewHTTPClient(cfg.AWX.URL, cfg.AWX.Token, cfg.AWX.Insecure)
		if err != nil {
			return err
		}
		_, err = awx.Bootstrap(ctx, c, content, awx.Rvav{URL: *rvavURL, Token: *rvavToken}, log)
		return err
	}

	// The local administrator's reset needs the database only: it is for
	// when nothing else works (ADR-0030).
	if cmd == "admin" {
		if len(args) == 0 || args[0] != "reset-password" {
			return errors.New("usage: rvav admin reset-password [-password-stdin]")
		}
		fs := flag.NewFlagSet("admin reset-password", flag.ExitOnError)
		fromStdin := fs.Bool("password-stdin", false, "read the new password from stdin instead of generating one")
		_ = fs.Parse(args[1:])
		password := ""
		if *fromStdin {
			b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
			if err != nil {
				return err
			}
			if password = strings.TrimRight(string(b), "\r\n"); password == "" {
				return errors.New("no password on stdin")
			}
		}
		st, err := store.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer st.Close()
		generated, err := core.ResetLocalAdmin(ctx, st, password)
		if err != nil {
			return err
		}
		if generated != "" {
			fmt.Println(generated)
		}
		fmt.Fprintf(os.Stderr, "the password of %s is reset: it must be changed at the next sign-in, and its sessions have ended\n",
			core.LocalAdminUsername)
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
		fake.Latency = cfg.IPAM.FakeLatency
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

	// Metrics and traces of the long-running processes (S14.5).
	switch cmd {
	case "api", "worker", "sync":
		shutdown, err := observe.Tracing(ctx, cmd)
		if err != nil {
			return fmt.Errorf("tracing: %w", err)
		}
		defer func() { _ = shutdown(context.Background()) }()
		observe.Serve(ctx, cfg.MetricsAddr, log)
	}

	switch cmd {
	case "api":
		return a.Run(ctx, "api", a.RunAPI)
	case "worker":
		return a.Run(ctx, "worker", a.RunWorker)
	case "sync":
		return a.Run(ctx, "sync", a.RunSync)
	case "api-token":
		fs := flag.NewFlagSet("api-token", flag.ExitOnError)
		user := fs.String("user", "", "the user the token acts as, for example usr_svc-awx")
		name := fs.String("name", "", "what the token is for")
		scope := fs.String("scope", "", `"inventory": it only reads the Ansible inventory; empty: all its user may do`)
		days := fs.Int("days", 365, "days until it expires (0: never)")
		revoke := fs.Bool("revoke", false, "delete the user's tokens of this name instead")
		_ = fs.Parse(args)
		if *user == "" || *name == "" {
			return errors.New("-user and -name are required")
		}
		if *scope != "" && *scope != auth.ScopeInventory {
			return fmt.Errorf("-scope %q: use %q or nothing", *scope, auth.ScopeInventory)
		}
		if _, err := a.Store.GetUser(ctx, *user); err != nil {
			return fmt.Errorf("user %s: %w", *user, err)
		}
		if *revoke {
			n, err := a.Store.DeleteAPITokens(ctx, *user, *name)
			if err == nil {
				fmt.Printf("%d token(s) revoked\n", n)
			}
			return err
		}
		var expires *time.Time
		if *days > 0 {
			t := time.Now().Add(time.Duration(*days) * 24 * time.Hour)
			expires = &t
		}
		token := auth.NewToken()
		if err := a.Store.CreateAPIToken(ctx, ids.New(ids.Token), *user, *name, auth.HashToken(token), *scope, expires); err != nil {
			return err
		}
		fmt.Println(token)
		return nil
	case "secrets":
		if len(args) != 1 || args[0] != "rotate" {
			return errors.New("usage: rvav secrets rotate")
		}
		n, err := core.RotateSecrets(ctx, a.Store, a.Secrets)
		if err != nil {
			return err
		}
		fmt.Printf("%d secret(s) sealed again with key %s; RVAV_SECRETS_OLD_KEYS can be emptied once every process has the new key\n",
			n, a.Secrets.KeyID())
		return nil
	case "vra-adopt":
		fs := flag.NewFlagSet("vra-adopt", flag.ExitOnError)
		file := fs.String("f", "", "an estate vra-export wrote (the vRA at RVAV_VRA_URL by default)")
		projects := fs.String("project", "", "vRA projects mapped to rvav's: VRA_ID=RVAV_ID,... (by name otherwise)")
		apply := fs.Bool("apply", false, "adopt them; without it, only the plan")
		_ = fs.Parse(args)
		e, err := readEstate(ctx, cfg, *file)
		if err != nil {
			return err
		}
		pm := map[string]string{}
		for _, kv := range strings.Split(*projects, ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				pm[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		plans, err := a.Core(nil).AdoptVRADeployments(ctx, &auth.Principal{User: store.User{ID: core.SystemUserID},
			GlobalRoles: []string{"admin"}}, e, pm, *apply)
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(plans, "", "  ")
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(b, '\n'))
		return err
	case "ipam-reconcile":
		report, err := a.Core(nil).ReconcileIPAM(ctx, &auth.Principal{User: store.User{ID: core.SystemUserID}, GlobalRoles: []string{"admin"}})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case "dev":
		return a.Run(ctx, "dev", func(ctx context.Context) error {
			// All three until one stops; to drain, all three do.
			ctx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			errc := make(chan error, 3)
			go func() { errc <- fmt.Errorf("sync: %w", a.RunSync(ctx)) }()
			go func() { errc <- fmt.Errorf("worker: %w", a.RunWorker(ctx)) }()
			go func() { errc <- fmt.Errorf("api: %w", a.RunAPI(ctx)) }()
			err := <-errc
			if ctx.Err() != nil {
				for range 2 {
					<-errc
				}
				return nil
			}
			cancel(err)
			return err
		})
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

// readEstate reads vRA, or an estate vra-export wrote.
func readEstate(ctx context.Context, cfg config.Config, file string) (*vra.Estate, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var e vra.Estate
		return &e, json.Unmarshal(b, &e)
	}
	c, err := vra.New(vra.Options{URL: cfg.VRA.URL, Token: cfg.VRA.Token, Username: cfg.VRA.Username,
		Password: cfg.VRA.Password, Domain: cfg.VRA.Domain, Insecure: cfg.VRA.Insecure})
	if err != nil {
		return nil, err
	}
	return c.Read(ctx)
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

// restart ends a process that drained to apply newer connection settings
// (ADR-0030): with ExitRestart, for its supervisor to start it again; `rvav
// dev`, which has none, starts itself again in place.
func restart(cmd string) {
	if cmd == "dev" {
		if exe, err := os.Executable(); err == nil {
			fmt.Fprintln(os.Stderr, "rvav dev: starting again with the newer connection settings")
			err = syscall.Exec(exe, os.Args, os.Environ())
			fmt.Fprintf(os.Stderr, "rvav dev: can't start again: %v\n", err)
		}
	}
	fmt.Fprintf(os.Stderr, "rvav %s: %v; exiting with status %d to be started again\n", cmd, app.ErrRestart, app.ExitRestart)
	os.Exit(app.ExitRestart)
}
