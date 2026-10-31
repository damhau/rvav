package e2e

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/store"
)

// newInstall is an install given its install settings only (ADR-0030,
// U1.5): a database with the schema, and rvav's api, worker and sync as
// their supervisor runs them, each from app.Open with the environment and
// started again each time it exits to apply newer settings.
type newInstall struct {
	t     *testing.T
	ctx   context.Context
	url   string // the API's
	store *store.Store
	procs map[string]*supervised
	stop  func()
}

// supervised is one of the processes: the start running now, how many
// there were, and, once done is closed, why it exited other than to start
// again.
type supervised struct {
	current atomic.Pointer[app.App]
	starts  atomic.Int32
	done    chan struct{}
	err     error
}

const installPassword = "the install's own password"

// startInstall starts a new install; env adds to the install settings.
func startInstall(t *testing.T, ctx context.Context, ts *testsuite.DevServer, env map[string]string) *newInstall {
	t.Helper()
	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool.Exec(ctx, `drop schema public cascade; create schema public`); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	// Nothing Administration sets, nor a fake.
	for _, v := range append(config.Variables(), "RVAV_FAKE_AWX", "RVAV_FAKE_EFFICIENTIP", "RVAV_ALLOW_DEV_AUTH", "RVAV_SECRETS_OLD_KEYS") {
		t.Setenv(v, "")
	}
	settings := map[string]string{
		"RVAV_DATABASE_URL": databaseURL, "RVAV_SECRETS_KEY": base64.StdEncoding.EncodeToString([]byte("e2e-only-secrets-key-32-bytes!!!")),
		"RVAV_TEMPORAL_ADDRESS": ts.FrontendHostPort(), "RVAV_TEMPORAL_TASK_QUEUE": "rvav-install-" + uuid.NewString()[:8],
		"RVAV_HTTP_ADDR": addr, "RVAV_PUBLIC_URL": "http://" + addr, "RVAV_METRICS_ADDR": "off",
		"RVAV_AUTH_MODE": "oidc", "RVAV_ADMIN_PASSWORD": installPassword, "RVAV_DRAIN_TIMEOUT": "1m",
	}
	maps.Copy(settings, env)
	for k, v := range settings {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("RVAV_TEST_VERBOSE") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	sctx, cancel := context.WithCancel(ctx)
	in := &newInstall{t: t, ctx: ctx, url: "http://" + addr, store: st, procs: map[string]*supervised{}}
	for _, role := range []string{"api", "worker", "sync"} {
		p := &supervised{done: make(chan struct{})}
		in.procs[role] = p
		go func() {
			defer close(p.done)
			for {
				a, err := app.Open(sctx, cfg, log)
				if err != nil {
					p.err = err
					return
				}
				a.Watch = app.Watch{Every: 100 * time.Millisecond, Settle: time.Second, Grace: 100 * time.Millisecond}
				p.current.Store(a)
				p.starts.Add(1)
				run := map[string]func(context.Context) error{"api": a.RunAPI, "worker": a.RunWorker, "sync": a.RunSync}[role]
				err = a.Run(sctx, role, run)
				a.Close()
				if !errors.Is(err, app.ErrRestart) {
					p.err = err
					return
				}
			}
		}()
	}
	var once sync.Once
	in.stop = func() {
		once.Do(func() {
			cancel()
			for role, p := range in.procs {
				<-p.done
				if p.err != nil && !errors.Is(p.err, context.Canceled) {
					t.Errorf("the %s: %v", role, p.err)
				}
			}
		})
	}
	t.Cleanup(in.stop)
	return in
}

// until waits for ok, failing when a process exits other than to start
// again.
func (in *newInstall) until(what string, ok func() bool) {
	in.t.Helper()
	for deadline := time.Now().Add(time.Minute); !ok(); time.Sleep(50 * time.Millisecond) {
		for role, p := range in.procs {
			select {
			case <-p.done:
				in.t.Fatalf("the %s exited (%v) while waiting for %s", role, p.err, what)
			default:
			}
		}
		if time.Now().After(deadline) {
			in.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// processes are those recorded now, by role.
func (in *newInstall) processes() map[string]store.Process {
	in.t.Helper()
	l, err := in.store.Processes(in.ctx, time.Now().Add(-core.ProcessesSeen))
	if err != nil {
		in.t.Fatal(err)
	}
	out := map[string]store.Process{}
	for _, p := range l {
		out[p.Role] = p
	}
	return out
}

// applied waits until each process runs with the settings of revision rev,
// then until the API answers.
func (in *newInstall) applied(rev int64) {
	in.t.Helper()
	in.until("each process to start again with the settings saved", func() bool {
		ps := in.processes()
		for role := range in.procs {
			if p, ok := ps[role]; !ok || p.Revision < rev || p.State != "running" {
				return false
			}
		}
		return true
	})
	in.ready()
}

// ready waits until the API answers /readyz.
func (in *newInstall) ready() {
	in.t.Helper()
	in.until("the API to be ready", func() bool {
		resp, err := http.Get(in.url + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusNoContent
	})
}

// TestInstallSettingsOnly: an install given its install settings only
// (U1.5) starts each of its processes, with none of vCenter, EfficientIP
// or AWX: what isn't set says so where it is used. The local administrator
// signs in and sets vCenter and EfficientIP; each process drains and its
// supervisor starts it again with them, and the sync reads vcsim's
// inventory and EfficientIP's subnets. Settings that make no client (AWX
// without its organisation) don't stop a process from starting, so
// Administration can correct them.
func TestInstallSettingsOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := vsphere.Bootstrap(ctx, c, vsphere.DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		in := startInstall(t, ctx, ts, nil)
		defer in.stop()
		in.until("the processes to record themselves", func() bool { return len(in.processes()) == 3 })
		in.ready()
		worker := in.procs["worker"].current.Load()
		if _, err := worker.IPAM.NextFree(ctx, efficientip.Subnet{}); !errors.Is(err, efficientip.ErrNotSet) {
			t.Fatalf("EfficientIP not set: %v", err)
		}
		if _, err := worker.AWX.Templates(ctx, awx.TemplateFilter{}); !errors.Is(err, awx.ErrNotSet) {
			t.Fatalf("AWX not set: %v", err)
		}
		if _, err := worker.VCenters().Get(ctx, ""); err == nil || err.Error() != "vCenter is not set (Administration › Integrations › vCenter): not a vCenter rvav manages" {
			t.Fatalf("vCenter not set: %v", err)
		}

		a := newBrowser(t, ctx, in.url)
		if code, res, _ := a.signIn("admin", installPassword); code != http.StatusOK || !res.PasswordChangeRequired {
			t.Fatalf("sign-in = %d %+v", code, res)
		}
		a.json("POST", "/api/v1/auth/password", api.PasswordChange{CurrentPassword: installPassword, NewPassword: "a password of my own"}, 204, nil)
		var st api.Setup
		a.json("GET", "/api/v1/admin/setup", nil, 200, &st)
		if !st.Open || !st.Steps[0].Done || st.Steps[3].Done {
			t.Fatalf("checklist = %+v", st)
		}

		fake, err := app.NewFakeEfficientIP(ctx, in.store)
		if err != nil {
			t.Fatal(err)
		}
		ipam := httptest.NewServer(fake)
		defer ipam.Close()
		vcsim := *c.URL()
		vcsim.User = nil
		a.json("PUT", "/api/v1/admin/integrations/vcenter", api.IntegrationWrite{Settings: map[string]any{
			"url": vcsim.String(), "username": "user", "name": "vc01.corp.example", "insecure": true}, Secret: new("pass")}, 200, nil)
		var saved api.Integration
		a.json("PUT", "/api/v1/admin/integrations/efficientip", api.IntegrationWrite{Settings: map[string]any{
			"url": ipam.URL, "username": "rvav", "dns_server": "ns1.corp.example"}, Secret: new("rvav")}, 200, &saved)
		in.applied(saved.Revision)
		for role, p := range in.procs {
			if n := p.starts.Load(); n < 2 {
				t.Errorf("the %s started %d times", role, n)
			}
		}
		if _, ok := in.procs["worker"].current.Load().IPAM.(efficientip.NotSet); ok {
			t.Fatal("the worker started again without EfficientIP")
		}

		in.until("the sync to read vcsim's inventory", func() bool {
			var n int
			if err := in.store.Pool.QueryRow(ctx, `select count(*) from inv_clusters`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n > 0
		})
		in.until("the sync to read EfficientIP's subnets", func() bool {
			var mappings api.NetworkMappingList
			a.json("GET", "/api/v1/admin/network-mappings", nil, 200, &mappings)
			n := 0
			for _, m := range mappings.Items {
				if m.Source == api.NetworkMappingSourceEfficientip {
					n++
				}
			}
			return n == len(seed.Subnets())
		})
		a.json("GET", "/api/v1/admin/setup", nil, 200, &st)
		if !st.Steps[3].Done || !st.Steps[5].Done {
			t.Fatalf("vCenter and IPAM set: %+v", st.Steps)
		}

		// An AWX saved without its organisation makes no client: the
		// processes start again anyway, and say what to set.
		a.json("PUT", "/api/v1/admin/integrations/awx", api.IntegrationWrite{Settings: map[string]any{
			"url": "https://awx.corp.example"}, Secret: new("awx-token")}, 200, &saved)
		in.applied(saved.Revision)
		if _, err := in.procs["worker"].current.Load().AWX.Templates(ctx, awx.TemplateFilter{}); err == nil ||
			!strings.Contains(err.Error(), "AWX has no organisation") {
			t.Fatalf("AWX without its organisation: %v", err)
		}
	})
}
