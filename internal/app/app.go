// Package app wires configuration, the database and the integrations into
// the processes started by the rvav command: api, worker and sync.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/httpapi"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

type App struct {
	Cfg    config.Config
	Log    *slog.Logger
	Store  *store.Store
	IPAM   efficientip.Client
	AWX    awx.Client
	Events *events.Publisher

	vcOnce sync.Once
	vc     vsphere.Driver
	vcErr  error

	tcOnce sync.Once
	tc     client.Client
	tcErr  error
}

// Open connects to the database and builds the EfficientIP and AWX clients
// (or their fakes). vCenter and Temporal connect on first use.
func Open(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Log: log, Store: st, Events: &events.Publisher{Store: st, Logger: log}}

	if cfg.IPAM.Fake {
		f := efficientip.NewFake()
		f.InUse = st.IPAddressesInUse
		a.IPAM = f
		log.Warn("using the fake EfficientIP (RVAV_FAKE_EFFICIENTIP=1); no real addresses or DNS records are created")
	} else if cfg.IPAM.URL != "" {
		if a.IPAM, err = efficientip.NewHTTPClient(cfg.IPAM.URL, cfg.IPAM.Username, cfg.IPAM.Password, cfg.IPAM.Insecure); err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("set RVAV_EFFICIENTIP_URL, or RVAV_FAKE_EFFICIENTIP=1 for development")
	}

	if cfg.AWX.Fake {
		a.AWX = awx.NewFake(cfg.AWX.FakeTaskDelay, cfg.AWX.FakeFailTask)
		log.Warn("using the fake AWX (RVAV_FAKE_AWX=1); jobs are simulated")
	} else if cfg.AWX.URL != "" {
		if a.AWX, err = awx.NewHTTPClient(cfg.AWX.URL, cfg.AWX.Token, cfg.AWX.Insecure); err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("set RVAV_AWX_URL, or RVAV_FAKE_AWX=1 for development")
	}
	return a, nil
}

func (a *App) Close() {
	if a.tc != nil {
		a.tc.Close()
	}
	a.Store.Close()
}

// VCenter connects to vCenter once.
func (a *App) VCenter(ctx context.Context) (vsphere.Driver, error) {
	a.vcOnce.Do(func() {
		if a.Cfg.VCenter.URL == "" {
			a.vcErr = errors.New("RVAV_VCENTER_URL is not set")
			return
		}
		a.vc, a.vcErr = vsphere.New(ctx, a.Cfg.VCenter.Name, a.Cfg.VCenter.URL, a.Cfg.VCenter.Username,
			a.Cfg.VCenter.Password, a.Cfg.VCenter.Insecure)
	})
	return a.vc, a.vcErr
}

// Temporal returns a Temporal client. It connects on first use, so the API
// serves reads while Temporal is down and submitting answers 502.
func (a *App) Temporal() (client.Client, error) {
	a.tcOnce.Do(func() {
		a.tc, a.tcErr = client.NewLazyClient(client.Options{
			HostPort:  a.Cfg.Temporal.Address,
			Namespace: a.Cfg.Temporal.Namespace,
			Logger:    tlog.NewStructuredLogger(a.Log.With("component", "temporal")),
		})
		if a.tcErr != nil {
			a.tcErr = fmt.Errorf("connect to Temporal at %s: %w", a.Cfg.Temporal.Address, a.tcErr)
		}
	})
	return a.tc, a.tcErr
}

// Authenticator builds the configured authentication mode.
func (a *App) Authenticator(ctx context.Context) (auth.Authenticator, error) {
	if a.Cfg.Auth.Mode == "dev" {
		if !a.Cfg.Auth.AllowDev {
			return nil, errors.New("RVAV_AUTH_MODE=dev trusts every request without signing in; " +
				"it only starts with RVAV_ALLOW_DEV_AUTH=1, which is for developer machines and tests")
		}
		a.Log.Warn("AUTH MODE IS DEV: every request is trusted as a seeded user. Never use this outside a developer machine.",
			"default_user", a.Cfg.Auth.DevUser, "switch_with_header", auth.DevUserHeader)
		return &auth.Dev{Store: a.Store, DefaultUser: a.Cfg.Auth.DevUser}, nil
	}
	return auth.NewOIDC(ctx, a.Store, a.Cfg.Auth.OIDCIssuer, a.Cfg.Auth.OIDCDiscoveryURL, a.Cfg.Auth.OIDCClientID, a.Cfg.Auth.GroupsClaim)
}

// Core builds the business service with the given workflow engine.
func (a *App) Core(w core.Workflows) *core.Service {
	return &core.Service{Store: a.Store, IPAM: a.IPAM, Workflows: w, Events: a.Events, Log: a.Log}
}

// RunAPI serves HTTP until ctx is done.
func (a *App) RunAPI(ctx context.Context) error {
	authn, err := a.Authenticator(ctx)
	if err != nil {
		return err
	}
	tc, err := a.Temporal()
	if err != nil {
		return err
	}
	hub := events.NewHub(a.Store, a.Log)
	go hub.Run(ctx)
	go hub.Purge(ctx, 24*time.Hour)
	srv := &httpapi.Server{
		Store: a.Store, Core: a.Core(&provision.Temporal{Client: tc, TaskQueue: a.Cfg.Temporal.TaskQueue}),
		Hub: hub, Auth: authn, Log: a.Log, AuthMode: a.Cfg.Auth.Mode, CORSOrigins: a.Cfg.CORSOrigins,
	}
	httpSrv := &http.Server{Addr: a.Cfg.HTTPAddr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() {
		a.Log.Info("api listening", "addr", a.Cfg.HTTPAddr)
		errc <- httpSrv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(sctx)
	}
}

// Activities builds the workflow activities.
func (a *App) Activities(ctx context.Context) (*provision.Activities, error) {
	vc, err := a.VCenter(ctx)
	if err != nil {
		return nil, err
	}
	return &provision.Activities{Store: a.Store, VSphere: vc, IPAM: a.IPAM, AWX: a.AWX,
		AWXInventorySource: a.Cfg.AWX.InventorySourceID, Events: a.Events, Log: a.Log}, nil
}

// RunWorker runs the Temporal worker until ctx is done.
func (a *App) RunWorker(ctx context.Context) error {
	acts, err := a.Activities(ctx)
	if err != nil {
		return err
	}
	tc, err := a.Temporal()
	if err != nil {
		return err
	}
	w := provision.NewWorker(tc, a.Cfg.Temporal.TaskQueue, acts)
	if err := w.Start(); err != nil {
		return err
	}
	a.Log.Info("worker started", "task_queue", a.Cfg.Temporal.TaskQueue)
	<-ctx.Done()
	w.Stop()
	return nil
}

// RunSync runs the inventory sync until ctx is done.
func (a *App) RunSync(ctx context.Context) error {
	vc, err := a.VCenter(ctx)
	if err != nil {
		return err
	}
	s := &inventory.Syncer{Store: a.Store, VSphere: vc, Events: a.Events, Log: a.Log, Interval: a.Cfg.SyncInterval}
	return s.Run(ctx)
}
