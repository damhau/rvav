package e2e

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
)

// TestLockedIntegrationField (#211): a save that changes a field an
// environment variable sets is refused at that field, and no process
// restarts; one that sends it as it is saves. A save that changes nothing,
// of any integration, sent back as the page or a client read it, writes
// nothing: no revision, no audit, no restart.
func TestLockedIntegrationField(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, nsxURL = "mkeller", "https://nsx01.corp.example"
		t.Setenv("RVAV_NSX_URL", nsxURL)
		h.svc.Config.NSX.URL = nsxURL
		env := h.svc.Config
		path := func(kind string) string { return "/api/v1/admin/integrations/" + kind }
		tlsServer := httptest.NewTLSServer(nil)
		defer tlsServer.Close()
		ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw}))
		// Saved as an API client or the certificate panel sends them: text
		// with a line break after it, which the page trims.
		for kind, settings := range map[string]map[string]any{
			"vcenter": {"url": "https://vc01.corp.example/sdk", "username": "svc-rvav", "insecure": true, "name": "vc01",
				"sync_interval": "2m"},
			"console": {"username": "svc-console", "sdk_urls": []any{"/wmks/wmks.min.js"}},
			"awx": {"url": "https://awx.corp.example", "organization": "Corp", "catalog_labels": []any{"catalog"},
				"action_labels": []any{"day2"}, "inventory_source_id": 12, "sync_interval": "5m"},
			"efficientip": {"url": "https://ipam.corp.example", "auth": "native", "username": "rvav", "space": "Local",
				"dns_server": "ns1", "instance_id": "rvav", "sync_interval": "5m"},
			"nsx": {"username": "rvav-nsx", "insecure": true},
			"git": {"url": "https://git.corp.example/infra/catalog.git", "branch": "main", "dir": "catalog", "username": "rvav",
				"known_hosts": "git.corp.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA\n", "committer": "rvav@corp.example",
				"sync_interval": "5m"},
			"vra": {"url": "https://vra.corp.example", "domain": "corp.example"},
			"ad":  {"url": "ldaps://dc01.corp.example", "username": "svc-rvav", "base_dn": "DC=corp,DC=example"},
			"servicenow": {"url": "https://corp.service-now.com", "auth": "basic", "username": "rvav", "cmdb": true,
				"sync_interval": "30m", "requests_per_minute": 60},
			"smtp":  {"host": "smtp.corp.example", "port": 587, "tls": "starttls", "from": "rvav <rvav@corp.example>"},
			"teams": {"app_id": "app", "tenant_id": "tenant", "service_url": "https://smba.trafficmanager.net/emea/"},
			"slack": {"api_url": "https://slack.com/api"},
			"hosts": {"rest_source_hosts": []any{"cmdb.corp.example"}},
			"trust": {"ca_bundle": ca + "\n"},
		} {
			h.json("PUT", path(kind), admin, api.IntegrationWrite{Settings: settings}, 200, nil)
		}
		revision := func() int64 {
			t.Helper()
			rev, err := h.store.SettingsRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return rev
		}
		read := func(kind string) api.Integration {
			t.Helper()
			var l api.IntegrationList
			h.json("GET", "/api/v1/admin/integrations", admin, nil, 200, &l)
			for _, it := range l.Items {
				if string(it.Kind) == kind {
					return it
				}
			}
			t.Fatalf("no %s integration", kind)
			return api.Integration{}
		}

		worker := startWorker(t, ctx, h, env)
		rev, audits := revision(), h.auditCount("settings.integration.nsx")
		var problem api.Problem
		h.json("PUT", path("nsx"), admin, api.IntegrationWrite{Settings: map[string]any{"url": "https://nsx02.corp.example",
			"username": "rvav-nsx", "insecure": true}}, 422, &problem)
		const locked = "Set by RVAV_NSX_URL, an environment variable of rvav's processes: change it there."
		if problem.Errors == nil || len(*problem.Errors) != 1 || (*problem.Errors)[0].Field != "/settings/url" ||
			deref((*problem.Errors)[0].Code) != "locked" || (*problem.Errors)[0].Message != locked {
			t.Fatalf("a locked field changed: %+v", problem)
		}
		if revision() != rev || h.auditCount("settings.integration.nsx") != audits {
			t.Fatal("a refused save wrote the settings")
		}

		for _, kind := range []string{"oidc", "vcenter", "console", "awx", "efficientip", "nsx", "git", "vra", "ad", "servicenow",
			"smtp", "teams", "slack", "hosts", "trust"} {
			it := read(kind)
			page := map[string]any{}
			for _, f := range config.Fields[kind] {
				if f.Secret || slices.Contains(it.Locked, f.Name) {
					continue
				}
				page[f.Name] = asThePageSends(f.Type, it.Settings[f.Name])
			}
			for how, settings := range map[string]map[string]any{"as read": it.Settings, "as the page sends it": page} {
				rev, audits := revision(), h.auditCount("settings.integration."+kind)
				var saved api.Integration
				h.json("PUT", path(kind), admin, api.IntegrationWrite{Settings: settings}, 200, &saved)
				if revision() != rev || h.auditCount("settings.integration."+kind) != audits || saved.Applying {
					t.Errorf("%s saved %s, unchanged: revision %d to %d, applying %v", kind, how, rev, revision(), saved.Applying)
				}
			}
		}
		if worker() {
			t.Fatal("a worker drained for a save that changed nothing")
		}

		// Sent as read, with another field changed: saved, the URL as the
		// environment sets it; a worker applies it.
		worker = startWorker(t, ctx, h, env)
		it := read("nsx")
		it.Settings["insecure"] = false
		var saved api.Integration
		h.json("PUT", path("nsx"), admin, api.IntegrationWrite{Settings: it.Settings}, 200, &saved)
		if saved.Settings["url"] != nsxURL || saved.Settings["insecure"] != false || saved.Revision <= rev {
			t.Fatalf("saved with the locked URL as it is: %+v", saved)
		}
		if !worker() {
			t.Fatal("a worker didn't drain to apply a connection changed")
		}
	})
}

// asThePageSends is a setting as the page's form sends it back: text
// trimmed, a list split on commas, an empty number 0.
func asThePageSends(typ string, v any) any {
	switch typ {
	case "bool":
		return v == true
	case "int":
		if v == nil {
			return 0
		}
		return v
	case "list":
		out := []any{}
		l, _ := v.([]any)
		for _, x := range l {
			if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// startWorker runs a worker started with the settings saved now over the
// environment's (ADR-0030); stopping it says whether it drained meanwhile
// to apply newer connection settings.
func startWorker(t *testing.T, ctx context.Context, h *harness, env config.Config) func() bool {
	t.Helper()
	stored, err := h.store.StoredIntegrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	started := env
	rev, err := core.ApplyStored(&started, stored, h.svc.Secrets, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app.App{Cfg: started, Log: discardLog(), Store: h.store, SettingsRevision: rev,
		Live:  &core.LiveSettings{Store: h.store, Secrets: h.svc.Secrets, Base: env, Every: time.Millisecond, Log: discardLog()},
		Watch: app.Watch{Every: 10 * time.Millisecond}}
	rctx, cancel := context.WithCancel(ctx)
	running, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- a.Run(rctx, "worker", func(c context.Context) error {
			close(running)
			<-c.Done()
			return c.Err()
		})
	}()
	select {
	case <-running:
	case err := <-done:
		cancel()
		t.Fatalf("the worker didn't start: %v", err)
	}
	return func() bool {
		t.Helper()
		// Long enough for the watcher to read what was saved last, and drain.
		time.Sleep(time.Second)
		cancel()
		err := <-done
		return errors.Is(err, app.ErrRestart)
	}
}
