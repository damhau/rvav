// Package app wires configuration, the database and the integrations into
// the processes started by the rvav command: api, worker and sync.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/httpapi"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

type App struct {
	Cfg    config.Config
	Log    *slog.Logger
	Store  *store.Store
	IPAM   efficientip.Client
	AWX    awx.Client
	Events *events.Publisher
	// Secrets seals stored secrets; nil without RVAV_SECRETS_KEY.
	Secrets *secrets.Box
	// SettingsRevision is the revision of the saved settings this process
	// started with.
	SettingsRevision int64

	vcMu    sync.Mutex
	vc      vsphere.Driver
	console vsphere.Driver // the console-only account, for VMRC clone tickets

	tcOnce sync.Once
	tc     client.Client
	tcErr  error

	stopFake context.CancelFunc
}

// awxCatalog imports the AWX templates of the organisation that have one of
// the catalog labels, and those with an action label as day-2 actions.
func (a *App) awxCatalog() *awxcatalog.Sync {
	return &awxcatalog.Sync{Store: a.Store, AWX: a.AWX, Log: a.Log, Interval: a.Cfg.AWX.SyncInterval,
		Filter:       awx.TemplateFilter{Organization: a.Cfg.AWX.Organization, Labels: a.Cfg.AWX.CatalogLabels},
		ActionLabels: a.Cfg.AWX.ActionLabels}
}

// NewFakeEfficientIP is a fake SOLIDserver holding the subnets of rvav's
// network mappings, which never offers an address rvav has recorded.
func NewFakeEfficientIP(ctx context.Context, st *store.Store) (*efficientip.FakeServer, error) {
	fake := efficientip.NewFakeServer()
	fake.UniqueIDs()
	fake.InUse = st.IPAddressesInUse
	networks, err := st.NetworkSubnets(ctx)
	if err != nil {
		return nil, err
	}
	// In a fixed order, so subnet IDs are the same each time the fake starts.
	for _, network := range slices.Sorted(maps.Keys(networks)) {
		n := networks[network]
		if err := fake.AddNetwork(efficientip.FakeNetwork{Network: n.Network, CIDR: n.Subnet, Gateway: n.Gateway,
			Zone: n.DNSZone, DNSServers: n.DNSServers}); err != nil {
			return nil, err
		}
	}
	return fake, nil
}

// Open connects to the database and builds the EfficientIP and AWX clients
// (or their fakes). vCenter and Temporal connect on first use.
func Open(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Log: log, Store: st, Events: &events.Publisher{Store: st, Logger: log}}
	if a.Secrets, err = secrets.New(cfg.SecretsKey, cfg.SecretsOldKeys...); err != nil {
		return nil, err
	}
	// Connection settings saved from the UI, where no environment variable
	// sets them (ADR-0020); they take effect here, at start.
	if err := a.applyStoredSettings(ctx); err != nil {
		return nil, err
	}
	cfg = a.Cfg
	// Notifications are queued on the channels set up (ADR-0023).
	for ch := range a.senders() {
		a.Events.Channels = append(a.Events.Channels, ch)
	}

	if cfg.IPAM.Fake {
		fake, err := NewFakeEfficientIP(ctx, st)
		if err != nil {
			return nil, err
		}
		fctx, stop := context.WithCancel(context.WithoutCancel(ctx))
		a.stopFake = stop
		url, err := fake.Start(fctx, "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		if a.IPAM, err = fake.Client(url); err != nil {
			return nil, err
		}
		log.Warn("using a fake SOLIDserver in this process (RVAV_FAKE_EFFICIENTIP=1); no real addresses or DNS records are created")
	} else if cfg.IPAM.URL != "" {
		if a.IPAM, err = efficientip.New(efficientip.Options{URL: cfg.IPAM.URL, Auth: efficientip.Auth(cfg.IPAM.Auth),
			Username: cfg.IPAM.Username, Password: cfg.IPAM.Password, Insecure: cfg.IPAM.Insecure, Space: cfg.IPAM.Space,
			DNSServer: cfg.IPAM.DNSServer, DNSView: cfg.IPAM.DNSView, Instance: cfg.IPAM.Instance}); err != nil {
			return nil, err
		}
		if strings.HasPrefix(cfg.IPAM.URL, "http://") {
			log.Warn("RVAV_EFFICIENTIP_URL is not HTTPS: rvav's SOLIDserver credentials are sent in clear")
		}
	} else {
		return nil, errors.New("set RVAV_EFFICIENTIP_URL, or RVAV_FAKE_EFFICIENTIP=1 for development")
	}

	if cfg.AWX.Fake {
		a.AWX = awx.NewFake(cfg.AWX.FakeTaskDelay, cfg.AWX.FakeFailTask)
		log.Warn("using the fake AWX (RVAV_FAKE_AWX=1); jobs are simulated")
	} else if cfg.AWX.URL != "" {
		// Templates become catalog items and day-2 actions, and pickers list
		// inventories and credentials: only from rvav's own organisation.
		if cfg.AWX.Organization == "" {
			return nil, errors.New("set RVAV_AWX_ORGANIZATION: rvav uses the templates, inventories and credentials of its own AWX organisation only")
		}
		c, err := awx.NewHTTPClient(cfg.AWX.URL, cfg.AWX.Token, cfg.AWX.Insecure)
		if err != nil {
			return nil, err
		}
		c.Organization = cfg.AWX.Organization
		a.AWX = c
		if cfg.AWX.InventorySourceID == 0 {
			log.Warn("RVAV_AWX_INVENTORY_SOURCE_ID is not set: jobs on rvav's VMs (post-configuration, AWX day-2 actions) will be refused")
		}
	} else {
		return nil, errors.New("set RVAV_AWX_URL, or RVAV_FAKE_AWX=1 for development")
	}
	return a, nil
}

// applyStoredSettings merges the integrations' saved settings into the
// configuration and records their revision.
func (a *App) applyStoredSettings(ctx context.Context) error {
	stored, err := a.Store.StoredIntegrations(ctx)
	if err != nil {
		return err
	}
	for kind, row := range stored {
		var s config.Stored
		if err := json.Unmarshal(row.Settings, &s.Settings); err != nil {
			return fmt.Errorf("stored %s settings: %w", kind, err)
		}
		if row.SecretCiphertext != nil {
			plain, err := a.Secrets.Open(core.IntegrationSecretName(kind), row.SecretKeyID, row.SecretNonce, row.SecretCiphertext)
			if err != nil {
				a.Log.Warn("the stored "+kind+" password or token can't be opened; it is not used", "err", err)
			} else {
				s.Secret = string(plain)
			}
		}
		a.Cfg.ApplyStored(kind, s)
		if row.Revision > a.SettingsRevision {
			a.SettingsRevision = row.Revision
		}
	}
	return nil
}

func (a *App) Close() {
	if a.stopFake != nil {
		a.stopFake()
	}
	if a.tc != nil {
		a.tc.Close()
	}
	a.Store.Close()
}

// VCenter connects to vCenter, once it succeeds; a failure is tried again
// on the next call.
func (a *App) VCenter(ctx context.Context) (vsphere.Driver, error) {
	a.vcMu.Lock()
	defer a.vcMu.Unlock()
	if a.vc != nil {
		return a.vc, nil
	}
	if a.Cfg.VCenter.URL == "" {
		return nil, errors.New("RVAV_VCENTER_URL is not set")
	}
	vc, err := vsphere.New(ctx, a.Cfg.VCenter.Name, a.Cfg.VCenter.URL, a.Cfg.VCenter.Username,
		a.Cfg.VCenter.Password, a.Cfg.VCenter.Insecure)
	if err != nil {
		return nil, err
	}
	a.vc = vc
	return vc, nil
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
	o, err := auth.NewOIDC(ctx, a.Store, a.Cfg.Auth.OIDCIssuer, a.Cfg.Auth.OIDCDiscoveryURL, a.Cfg.Auth.OIDCClientID, a.Cfg.Auth.GroupsClaim)
	if err != nil {
		// Requests answer 503 and /readyz fails until the provider answers.
		a.Log.Warn("identity provider not reachable yet; not ready until it is", "err", err)
	}
	return o, nil
}

// Core builds the business service with the given workflow engine.
func (a *App) Core(w core.Workflows) *core.Service {
	svc := &core.Service{Store: a.Store, IPAM: a.IPAM, Workflows: w, Events: a.Events, Log: a.Log,
		VCenter:    func(ctx context.Context) (core.ConsoleSource, error) { return a.VCenter(ctx) },
		AWXCatalog: a.awxCatalog(), AWX: a.AWX, AWXOrganization: a.Cfg.AWX.Organization,
		Secrets: a.Secrets, REST: core.NewRESTSources(a.Cfg.RESTSourceHosts), Config: a.Cfg, SettingsRevision: a.SettingsRevision,
		Dispatcher: a.Dispatcher(), Notifier: a.Notifier()}
	if a.Cfg.ConsoleUsername != "" {
		svc.ConsoleAccount = func(ctx context.Context) (core.CloneTicketer, error) { return a.consoleAccount(ctx) }
	}
	return svc
}

// consoleAccount logs in to vCenter as the console-only account, once it succeeds.
func (a *App) consoleAccount(ctx context.Context) (vsphere.Driver, error) {
	a.vcMu.Lock()
	defer a.vcMu.Unlock()
	if a.console != nil {
		return a.console, nil
	}
	vc, err := vsphere.New(ctx, a.Cfg.VCenter.Name, a.Cfg.VCenter.URL, a.Cfg.ConsoleUsername,
		a.Cfg.ConsolePassword, a.Cfg.VCenter.Insecure)
	if err != nil {
		return nil, err
	}
	a.console = vc
	return vc, nil
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
		WMKSSDKURLs: a.Cfg.WMKSSDKURLs, ConsoleInsecure: a.Cfg.VCenter.Insecure,
	}
	if a.Cfg.Teams.AppID != "" {
		srv.Teams = &notify.TeamsVerifier{AppID: a.Cfg.Teams.AppID, OpenIDURL: a.Cfg.Teams.OpenIDURL, TenantID: a.Cfg.Teams.TenantID}
	}
	if a.Cfg.Slack.BotToken != "" {
		srv.Slack = &notify.Slack{Config: a.Cfg.Slack, Store: a.Store}
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
		AWXInventorySource: a.Cfg.AWX.InventorySourceID, AWXInventoryRequired: !a.Cfg.AWX.Fake, Events: a.Events, Log: a.Log,
		Subscriptions: a.Dispatcher(), TaskQueue: a.Cfg.Temporal.TaskQueue}, nil
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
	// Retention and lease policies start requests of their own.
	acts.Housekeeper = a.Core(&provision.Temporal{Client: tc, TaskQueue: a.Cfg.Temporal.TaskQueue})
	w := provision.NewWorker(tc, a.Cfg.Temporal.TaskQueue, acts)
	if err := w.Start(); err != nil {
		return err
	}
	if err := provision.EnsureHousekeeping(ctx, tc, a.Cfg.Temporal.TaskQueue); err != nil {
		a.Log.Warn("housekeeping schedule", "err", err)
	}
	a.Log.Info("worker started", "task_queue", a.Cfg.Temporal.TaskQueue)
	// Events to subscriptions (ADR-0021) and notifications to their
	// channels (ADR-0023): every worker replica takes part.
	go a.Dispatcher().Run(ctx)
	go a.Notifier().Run(ctx)
	<-ctx.Done()
	w.Stop()
	return nil
}

// senders are the notification channels set up, by name.
func (a *App) senders() map[string]notify.Sender {
	out := map[string]notify.Sender{}
	if a.Cfg.SMTP.Host != "" {
		out["email"] = &notify.Email{Config: a.Cfg.SMTP}
	}
	if a.Cfg.Teams.AppID != "" && a.Cfg.Teams.AppPassword != "" {
		out["teams"] = &notify.Teams{Config: a.Cfg.Teams, Store: a.Store}
	}
	if a.Cfg.Slack.BotToken != "" {
		out["slack"] = &notify.Slack{Config: a.Cfg.Slack, Store: a.Store}
	}
	return out
}

// Notifier sends notifications on their channels.
func (a *App) Notifier() *notify.Notifier {
	return &notify.Notifier{Store: a.Store, Senders: a.senders(), Log: a.Log, PublicURL: a.Cfg.PublicURL}
}

// Dispatcher delivers integration events to subscriptions.
func (a *App) Dispatcher() *dispatch.Dispatcher {
	t := dispatch.Targets{Webhook: &dispatch.Webhook{Hosts: a.Cfg.WebhookHosts, Secret: dispatch.StoredSecret(a.Store, a.Secrets)}}
	if a.AWX != nil {
		t.AWX = &dispatch.AWX{Client: a.AWX}
	}
	if tc, err := a.Temporal(); err == nil {
		t.Workflow = &dispatch.Workflow{Client: tc, Store: a.Store, TaskQueue: a.Cfg.Temporal.TaskQueue}
	}
	return &dispatch.Dispatcher{Store: a.Store, Log: a.Log, Targets: t}
}

// RunSync runs the inventory sync until ctx is done.
func (a *App) RunSync(ctx context.Context) error {
	vc, err := a.VCenter(ctx)
	if err != nil {
		return err
	}
	subnets := &inventory.SubnetSync{Store: a.Store, IPAM: a.IPAM, Log: a.Log, Interval: a.Cfg.IPAMSyncInterval}
	go func() { _ = subnets.Run(ctx) }()
	go func() { _ = a.awxCatalog().Run(ctx) }()
	s := &inventory.Syncer{Store: a.Store, VSphere: vc, Events: a.Events, Log: a.Log, Interval: a.Cfg.SyncInterval}
	return s.Run(ctx)
}
