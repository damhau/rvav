package e2e

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// TestApplyConnections changes the vCenter in Administration while a VM is
// being provisioned (ADR-0030, U1.3): the worker, the real one from
// app.Open, drains (the AWX job running finishes), exits, and its
// supervisor (this test) starts it again with the new vCenter; the request
// finishes, and the next one is provisioned through the new vCenter only.
// Two saves in quick succession apply at once, with one restart.
func TestApplyConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-apply-e2e"
		h.svc.Workflows = &provision.Temporal{Client: ts.Client(), TaskQueue: taskQueue}

		// Two ways to the same vcsim: what rvav reaches through each is
		// counted, and the first is closed once rvav uses the second.
		p1, p2 := newTCPProxy(t, c.URL().Host), newTCPProxy(t, c.URL().Host)
		sdk := func(p *tcpProxy) string { u := *c.URL(); u.Host = p.addr(); u.User = nil; return u.String() }
		dead := func() string { // nothing listens there
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := l.Addr().String()
			_ = l.Close()
			u := *c.URL()
			u.Host, u.User = addr, nil
			return u.String()
		}()
		const admin = "mkeller"
		saveVCenter := func(url string) api.Integration {
			var it api.Integration
			h.json("PUT", "/api/v1/admin/integrations/vcenter", admin, api.IntegrationWrite{Settings: map[string]any{"url": url,
				"username": "user", "insecure": true, "name": "vc01.corp.example"}, Secret: new("pass")}, 200, &it)
			return it
		}
		saveVCenter(sdk(p1))

		// The worker as rvav runs it, from the environment; its vCenter is
		// the one saved in Administration.
		for k, v := range map[string]string{
			"RVAV_DATABASE_URL": databaseURL, "RVAV_METRICS_ADDR": "off", "RVAV_FAKE_AWX": "1",
			"RVAV_FAKE_AWX_TASK_DELAY": "1s", "RVAV_FAKE_EFFICIENTIP": "1",
			"RVAV_TEMPORAL_ADDRESS": ts.FrontendHostPort(), "RVAV_TEMPORAL_TASK_QUEUE": taskQueue,
			"RVAV_SECRETS_KEY":   base64.StdEncoding.EncodeToString([]byte("e2e-only-secrets-key-32-bytes!!!")),
			"RVAV_DRAIN_TIMEOUT": "1m",
		} {
			t.Setenv(k, v)
		}
		for _, k := range []string{"RVAV_VCENTER_URL", "RVAV_VCENTER_USERNAME", "RVAV_VCENTER_PASSWORD", "RVAV_VCENTER_NAME"} {
			if os.Getenv(k) != "" {
				t.Setenv(k, "")
			}
		}
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		if os.Getenv("RVAV_TEST_VERBOSE") != "" {
			log = slog.New(slog.NewTextHandler(os.Stderr, nil))
		}

		// The supervisor: it starts the worker again each time it drained.
		var (
			current    atomic.Pointer[app.App]
			restarts   atomic.Int32
			sctx, stop = context.WithCancel(ctx)
			exited     = make(chan error, 1)
		)
		go func() {
			for {
				a, err := app.Open(sctx, cfg, log)
				if err != nil {
					exited <- err
					return
				}
				a.Watch = app.Watch{Every: 100 * time.Millisecond, Settle: time.Second}
				current.Store(a)
				err = a.Run(sctx, "worker", a.RunWorker)
				a.Close()
				if !errors.Is(err, app.ErrRestart) {
					exited <- err
					return
				}
				restarts.Add(1)
			}
		}()
		defer func() {
			stop()
			if err := <-exited; err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("the worker: %v", err)
			}
		}()
		processes := func() api.ProcessList {
			var l api.ProcessList
			h.json("GET", "/api/v1/admin/processes", admin, nil, 200, &l)
			return l
		}
		until := func(what string, ok func() bool) {
			t.Helper()
			for deadline := time.Now().Add(time.Minute); !ok(); time.Sleep(25 * time.Millisecond) {
				select {
				case err := <-exited:
					t.Fatalf("the worker exited (%v) while waiting for %s", err, what)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatalf("timed out waiting for %s", what)
				}
			}
		}
		until("the worker to record itself", func() bool { return len(processes().Items) == 1 })

		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		submit := func(cluster string) api.Request {
			var r api.Request
			h.json("POST", "/api/v1/requests", "lmartin", api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId,
				ProjectId: web, Environment: "dev", Inputs: map[string]any{"role": "app", "size": "S", "cluster": cluster,
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
				202, &r, "Idempotency-Key", uuid.NewString())
			return r
		}

		// Provisioning: once the AWX job runs, the VM is built through p1.
		first := submit("GVA-DEV-CL01")
		until("the post-configuration job", func() bool {
			a := current.Load()
			if a != nil && jobLaunched(a.AWX.(*awx.Fake)) {
				return true
			}
			var r api.Request
			h.json("GET", "/api/v1/requests/"+first.Id, "lmartin", nil, 200, &r)
			if r.Status == api.RequestStatusFailed || r.Status == api.RequestStatusSucceeded {
				t.Fatalf("the first request %s before its post-configuration job: %+v", r.Status, r.Error)
			}
			return false
		})
		if p1.sent() == 0 {
			t.Fatal("the VM was built without going through the first vCenter address")
		}
		if r := h.waitStatus(first.Id, "lmartin", api.RequestStatusRunning); r.Status != api.RequestStatusRunning {
			t.Fatalf("request = %s", r.Status)
		}

		// Two saves in quick succession, the first to a vCenter that doesn't
		// answer: only the second applies, with one restart.
		before := restarts.Load()
		saved := saveVCenter(dead)
		time.Sleep(300 * time.Millisecond)
		saved = saveVCenter(sdk(p2))
		if !saved.Applying {
			t.Fatalf("the vCenter's settings are applied before the worker drained: %+v", saved)
		}
		var sawDraining bool
		until("the worker to start again", func() bool {
			l := processes()
			for _, p := range l.Items {
				sawDraining = sawDraining || p.State == api.ProcessStateDraining
			}
			return restarts.Load() > before && len(l.Items) == 1 && l.Items[0].Revision == saved.Revision
		})
		if !sawDraining {
			t.Error("the worker was never seen draining")
		}
		// The job that ran finished on the worker that drained.
		if n := h.auditCount("settings.process_drain"); n != 1 {
			t.Errorf("%d drains audited", n)
		}
		if n := h.auditCount("settings.process_restart"); n != 1 {
			t.Errorf("%d restarts audited", n)
		}
		var list api.IntegrationList
		h.json("GET", "/api/v1/admin/integrations", admin, nil, 200, &list)
		for _, it := range list.Items {
			if it.Kind == api.IntegrationKindVcenter && it.Applying {
				t.Fatal("the vCenter's settings still applying once the worker started again")
			}
		}

		// The first address is gone: the rest of the request and the next
		// one go through the second.
		p1.close()
		before2 := p2.sent()
		if r := h.waitStatus(first.Id, "lmartin", api.RequestStatusSucceeded); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("first request = %s", r.Status)
		}
		second := submit("GVA-DEV-CL01")
		if r := h.waitStatus(second.Id, "lmartin", api.RequestStatusSucceeded); r.Status != api.RequestStatusSucceeded {
			t.Fatalf("second request = %s", r.Status)
		}
		if p2.sent() == before2 {
			t.Error("the second request didn't go through the new vCenter address")
		}
		if n := restarts.Load(); n != 1 {
			t.Errorf("the worker started again %d times, want 1", n)
		}

		// A setting read on next use (an email server) drains nothing: the
		// worker sends with it from then on.
		a := current.Load()
		if _, ok := a.Notifier().Current(ctx)["email"]; ok {
			t.Fatal("email set up before an email server was saved")
		}
		h.json("PUT", "/api/v1/admin/integrations/smtp", admin, api.IntegrationWrite{Settings: map[string]any{
			"host": "127.0.0.1", "port": 2525, "tls": "none", "from": "rvav <rvav@corp.example>"}}, 200, nil)
		until("the worker to send email", func() bool {
			e, ok := a.Notifier().Current(ctx)["email"].(*notify.Email)
			return ok && e.Config.Host == "127.0.0.1" && e.Config.Port == 2525
		})
		if ch := a.Events.ChannelsNow(ctx); !slices.Equal(ch, []string{"email"}) {
			t.Fatalf("notifications are queued on %v", ch)
		}
		latest, err := h.store.SettingsRevision(ctx)
		if err != nil {
			t.Fatal(err)
		}
		until("the worker to read the email server", func() bool {
			l := processes()
			return len(l.Items) == 1 && l.Items[0].Revision == latest
		})
		time.Sleep(1500 * time.Millisecond) // past Settle
		if n := restarts.Load(); n != 1 || current.Load() != a {
			t.Errorf("an email server's settings restarted the worker (%d restarts)", n)
		}

		// APIs drain one at a time, so the others keep answering; one that
		// stopped recording itself (it crashed) holds the others back for
		// 30 s at most.
		since := time.Now().Add(-core.ProcessesSeen)
		for _, id := range []string{"rvav-api-1/api/1-aaaaaa", "rvav-api-2/api/1-bbbbbb"} {
			if err := h.store.SeeProcess(ctx, store.Process{ID: id, Role: "api", Host: id[:10], State: "running"}, since); err != nil {
				t.Fatal(err)
			}
		}
		claim := func(id string) bool {
			ok, err := h.store.ClaimDrain(ctx, id, "api", time.Now().Add(-core.ProcessesSeen))
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}
		if !claim("rvav-api-1/api/1-aaaaaa") || claim("rvav-api-2/api/1-bbbbbb") {
			t.Fatal("two APIs drain at once")
		}
		if _, err := h.store.Pool.Exec(ctx, `update processes set seen_at = now() - interval '31 seconds' where id = 'rvav-api-1/api/1-aaaaaa'`); err != nil {
			t.Fatal(err)
		}
		if !claim("rvav-api-2/api/1-bbbbbb") {
			t.Fatal("an API that stopped recording itself holds the others back")
		}
	})
}

// jobLaunched says whether the fake AWX launched a job template (not an
// inventory update): its IDs follow 18240.
func jobLaunched(f *awx.Fake) bool {
	for id := 18241; id < 18260; id++ {
		if l := f.Launched(id); l.Limit != "" || len(l.ExtraVars) > 0 {
			return true
		}
	}
	return false
}

// tcpProxy forwards connections to a host, counting the bytes sent through
// it; closed, it refuses new ones and ends those open.
type tcpProxy struct {
	l     net.Listener
	n     atomic.Int64
	mu    sync.Mutex
	conns []net.Conn
}

// counted counts what is written through it.
type counted struct {
	io.Writer
	n *atomic.Int64
}

func (c counted) Write(b []byte) (int, error) {
	n, err := c.Writer.Write(b)
	c.n.Add(int64(n))
	return n, err
}

func newTCPProxy(t *testing.T, target string) *tcpProxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tcpProxy{l: l}
	t.Cleanup(p.close)
	go func() {
		for {
			in, err := l.Accept()
			if err != nil {
				return
			}
			out, err := net.Dial("tcp", target)
			if err != nil {
				_ = in.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, in, out)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(counted{out, &p.n}, in); _ = out.Close() }()
			go func() { _, _ = io.Copy(in, out); _ = in.Close() }()
		}
	}()
	return p
}

func (p *tcpProxy) addr() string { return p.l.Addr().String() }
func (p *tcpProxy) sent() int64  { return p.n.Load() }

func (p *tcpProxy) close() {
	_ = p.l.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}
