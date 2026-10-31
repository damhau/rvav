package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
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
	"github.com/damhau/rvav/internal/httpapi"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/vra"
)

// The settings that only an environment variable set before U1.4 (ADR-0030)
// can be set in Administration: the sync intervals, the hosts REST value
// sources and webhooks may call, the console's account and SDK, the vRA
// the importer reads, Teams' and Slack's URLs. Each is saved, used by the
// API as rvav runs it (app.APIServer) without a restart, and locked by its
// environment variable, which wins.
func TestMovedSettings(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		sdk := *c.URL()
		sdk.User = nil
		env := map[string]string{
			"RVAV_DATABASE_URL": databaseURL, "RVAV_METRICS_ADDR": "off", "RVAV_AUTH_MODE": "dev", "RVAV_ALLOW_DEV_AUTH": "1",
			"RVAV_SECRETS_KEY": base64.StdEncoding.EncodeToString([]byte("e2e-only-secrets-key-32-bytes!!!")),
			"RVAV_FAKE_AWX":    "1", "RVAV_FAKE_EFFICIENTIP": "1",
			"RVAV_VCENTER_URL": sdk.String(), "RVAV_VCENTER_USERNAME": "user", "RVAV_VCENTER_PASSWORD": "pass",
			"RVAV_VCENTER_INSECURE": "1", "RVAV_VCENTER_NAME": "vc01.corp.example",
		}
		moved := []string{"RVAV_SYNC_INTERVAL", "RVAV_IPAM_SYNC_INTERVAL", "RVAV_AWX_SYNC_INTERVAL", "RVAV_GIT_SYNC_INTERVAL",
			"RVAV_REST_SOURCE_HOSTS", "RVAV_WEBHOOK_HOSTS", "RVAV_CONSOLE_VCENTER_USERNAME", "RVAV_CONSOLE_VCENTER_PASSWORD",
			"RVAV_WMKS_SDK_URLS", "RVAV_VRA_URL", "RVAV_VRA_TOKEN", "RVAV_VRA_USERNAME", "RVAV_VRA_PASSWORD", "RVAV_VRA_DOMAIN",
			"RVAV_TEAMS_LOGIN_URL", "RVAV_TEAMS_GRAPH_URL", "RVAV_TEAMS_OPENID_URL", "RVAV_SLACK_API_URL"}
		for _, k := range moved {
			t.Setenv(k, "")
		}
		for k, v := range env {
			t.Setenv(k, v)
		}
		// The API as rvav runs it, on the harness's database.
		open := func() (*app.App, *httpapi.Server, *harness) {
			t.Helper()
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			a, err := app.Open(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			// Its event hub holds a connection until its context is done.
			actx, cancel := context.WithCancel(ctx)
			t.Cleanup(cancel)
			a.Live.Every = 0 // read the settings revision at each use
			srv, err := a.APIServer(actx, h.wf)
			if err != nil {
				t.Fatal(err)
			}
			hs := httptest.NewServer(srv.Handler())
			t.Cleanup(hs.Close)
			on := *h
			on.srv = hs
			return a, srv, &on
		}
		a, srv, api1 := open()
		save := func(on *harness, kind string, settings map[string]any, secret string, want int) api.Integration {
			t.Helper()
			w := api.IntegrationWrite{Settings: settings}
			if secret != "" {
				w.Secret = &secret
			}
			var it api.Integration
			on.json("PUT", "/api/v1/admin/integrations/"+kind, admin, w, want, &it)
			return it
		}
		tested := func(on *harness, kind string) api.IntegrationTest {
			t.Helper()
			var out api.IntegrationTest
			on.json("POST", "/api/v1/admin/integrations/"+kind+"/test", admin, nil, 200, &out)
			return out
		}

		// Sync intervals: read on next use, from 30s to 24h.
		save(api1, "vcenter", map[string]any{"sync_interval": "10s"}, "", 422)
		save(api1, "vcenter", map[string]any{"sync_interval": "45s"}, "", 200)
		save(api1, "efficientip", map[string]any{"sync_interval": "15m"}, "", 200)
		save(api1, "awx", map[string]any{"sync_interval": "1h30m"}, "", 200)
		save(api1, "git", map[string]any{"sync_interval": "20m"}, "", 200)
		now := a.Live.Config(ctx)
		if now.SyncInterval != 45*time.Second || now.IPAMSyncInterval != 15*time.Minute ||
			now.AWX.SyncInterval != 90*time.Minute || now.Git.Interval != 20*time.Minute {
			t.Fatalf("intervals saved: %v %v %v %v", now.SyncInterval, now.IPAMSyncInterval, now.AWX.SyncInterval, now.Git.Interval)
		}

		// The hosts webhooks and REST value sources may call.
		hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
		t.Cleanup(hook.Close)
		cmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"items": [{"id": "a", "name": "A"}]}`)
		}))
		t.Cleanup(cmdb.Close)
		api1.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "moved-hook", Kind: api.SecretWriteKindToken,
			Value: new("hook-secret")}, 201, nil)
		target := api.SubscriptionTarget{}
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+hook.URL+`/rvav", "secret": ""}}`), &target)
		code, body, _ := api1.do("POST", "/api/v1/admin/subscriptions", admin,
			api.SubscriptionWrite{Name: "No secret", Topic: api.ProvisioningPost, Target: target})
		if code != 422 || !strings.Contains(string(body), "give the stored secret") {
			t.Fatalf("a webhook without a secret = %d %s", code, body)
		}
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+hook.URL+`/rvav", "secret": "moved-hook"}}`), &target)
		sub := api.SubscriptionWrite{Name: "Moved settings", Topic: api.ProvisioningPost, Target: target}
		code, body, _ = api1.do("POST", "/api/v1/admin/subscriptions", admin, sub)
		if code != 422 || !strings.Contains(string(body), "Allowed hosts") {
			t.Fatalf("a webhook to a host not allowed = %d %s", code, body)
		}
		item := restItem(t, api1, admin, cmdb.URL+"/items")
		code, body, _ = api1.do("POST", "/api/v1/admin/catalog/items", admin, item)
		if code != 422 || !strings.Contains(string(body), "Allowed hosts") {
			t.Fatalf("a REST value source on a host not allowed = %d %s", code, body)
		}
		save(api1, "hosts", map[string]any{"webhook_hosts": []string{"https://not a host"}}, "", 422)
		save(api1, "hosts", map[string]any{"rest_source_hosts": []string{cmdb.URL}, "webhook_hosts": []string{hook.URL}}, "", 200)
		api1.json("POST", "/api/v1/admin/subscriptions", admin, sub, 201, nil)
		api1.json("POST", "/api/v1/admin/catalog/items", admin, item, 201, nil)
		if r := tested(api1, "hosts"); !r.Ok || !strings.Contains(r.Checks[0].Message, strings.TrimPrefix(cmdb.URL, "http://")) {
			t.Fatalf("hosts' check = %+v", r)
		}

		// The console's account: a session of it for VMRC's clone tickets,
		// made again when it changes, and the WebMKS SDK's scripts.
		if acct, err := srv.Core.ConsoleAccount(ctx); acct != nil || err != nil {
			t.Fatalf("a console account before one is set: %v %v", acct, err)
		}
		if got := srv.WMKSNow(ctx); len(got) != 0 {
			t.Fatalf("WebMKS scripts before any is set: %v", got)
		}
		save(api1, "console", map[string]any{"username": "console@vsphere.local", "sdk_urls": []string{"ftp://x"}}, "", 422)
		save(api1, "console", map[string]any{"username": "console@vsphere.local",
			"sdk_urls": []string{"/wmks/wmks.min.js"}}, "", 200)
		if acct, err := srv.Core.ConsoleAccount(ctx); acct != nil || err != nil {
			t.Fatalf("the console account without its password: %v %v", acct, err)
		}
		if r := tested(api1, "console"); r.Ok || !strings.Contains(r.Checks[0].Message, "give its password") {
			t.Fatalf("console's test without a password = %+v", r)
		}
		save(api1, "console", map[string]any{"username": "console@vsphere.local",
			"sdk_urls": []string{"/wmks/wmks.min.js"}}, "console-pass", 200)
		first, err := srv.Core.ConsoleAccount(ctx)
		if err != nil || first == nil {
			t.Fatalf("the console account saved: %v", err)
		}
		if _, err := first.CloneTicket(ctx); err != nil {
			t.Fatal(err)
		}
		if got := srv.WMKSNow(ctx); !slices.Equal(got, []string{"/wmks/wmks.min.js"}) {
			t.Fatalf("WebMKS scripts saved: %v", got)
		}
		if again, _ := srv.Core.ConsoleAccount(ctx); again != first {
			t.Fatal("the console's session was made again with the same account")
		}
		if r := tested(api1, "console"); !r.Ok {
			t.Fatalf("console's test = %+v", r)
		}
		save(api1, "console", map[string]any{"username": "console2@vsphere.local", "sdk_urls": []string{"/wmks/wmks.min.js"}},
			"console-pass", 200)
		if other, err := srv.Core.ConsoleAccount(ctx); err != nil || other == first {
			t.Fatalf("another console account: the same session (%v)", err)
		}
		// The page's Content-Security-Policy loads the SDK from where it is
		// (nginx copies the header; web/nginx/check.sh).
		scripts := func() string {
			t.Helper()
			code, _, hdr := api1.do("GET", "/api/v1/auth/options", "", nil)
			if code != 200 {
				t.Fatalf("auth options = %d", code)
			}
			return hdr.Get("X-Rvav-Script-Origins")
		}
		if got := scripts(); got != "" {
			t.Fatalf("script origins with the SDK on the web app's own = %q", got)
		}
		save(api1, "console", map[string]any{"username": "console2@vsphere.local", "sdk_urls": []string{
			"https://vc01.corp.example/ui/webconsole/wmks.min.js", "https://vc01.corp.example/ui/webconsole/jquery.js",
			"https://vc02.corp.example:9443/wmks.js"}}, "", 200)
		if got := scripts(); got != "https://vc01.corp.example https://vc02.corp.example:9443" {
			t.Fatalf("script origins = %q", got)
		}

		// The vRA the importer reads.
		api1.json("POST", "/api/v1/admin/vra/snapshots", admin, nil, 422, nil)
		fake := httptest.NewServer(vra.NewFakeServer(vra.SampleEstate(time.Now())))
		t.Cleanup(fake.Close)
		save(api1, "vra", map[string]any{"url": fake.URL}, vra.FakeRefreshToken, 200)
		var st api.VraImportStatus
		api1.json("GET", "/api/v1/admin/vra", admin, nil, 200, &st)
		if !st.Configured || deref(st.Url) != fake.URL {
			t.Fatalf("vRA status = %+v", st)
		}
		var snap api.VraSnapshot
		api1.json("POST", "/api/v1/admin/vra/snapshots", admin, nil, 201, &snap)
		if snap.Source != "vra" || snap.Counts.Templates == 0 {
			t.Fatalf("vRA read = %+v", snap)
		}
		if r := tested(api1, "vra"); !r.Ok {
			t.Fatalf("vRA's test = %+v", r)
		}

		// Teams' and Slack's URLs: the channels send through those saved.
		teams := startTeams(t)
		save(api1, "teams", map[string]any{"app_id": "app-1", "tenant_id": "tenant-1", "service_url": teams.URL + "/smba",
			"login_url": teams.URL, "graph_url": teams.URL, "openid_url": teams.URL + "/openid"}, "app-secret", 200)
		tc, _ := a.Notifier().Current(ctx)["teams"].(*notify.Teams)
		if tc == nil || tc.Config.LoginURL != teams.URL || tc.Config.GraphURL != teams.URL || tc.Config.OpenIDURL != teams.URL+"/openid" {
			t.Fatalf("Teams sends with %+v", tc)
		}
		if r := tested(api1, "teams"); !r.Ok {
			t.Fatalf("Teams' test with the saved URLs = %+v", r)
		}
		slack := startSlack(t)
		save(api1, "slack", map[string]any{"api_url": slack.URL + "/api/"}, "xoxb-test", 200)
		sc, _ := a.Notifier().Current(ctx)["slack"].(*notify.Slack)
		if sc == nil || sc.Config.APIURL != slack.URL+"/api/" {
			t.Fatalf("Slack sends with %+v", sc)
		}
		if r := tested(api1, "slack"); !r.Checks[0].Ok {
			t.Fatalf("Slack's test with the saved URL = %+v", r)
		}
		// A new address for the secret needs it again.
		save(api1, "slack", map[string]any{"api_url": "https://slack.example/api/"}, "", 422)
		save(api1, "teams", map[string]any{"app_id": "app-1", "tenant_id": "tenant-1", "service_url": teams.URL + "/smba",
			"login_url": "https://login.example"}, "", 422)

		// Environment variables win, and lock their fields.
		locks := map[string]string{"RVAV_SYNC_INTERVAL": "3m", "RVAV_GIT_SYNC_INTERVAL": "11m",
			"RVAV_WEBHOOK_HOSTS": "hooks.corp.example", "RVAV_WMKS_SDK_URLS": "/env/wmks.js",
			"RVAV_VRA_PASSWORD": "from-env", "RVAV_TEAMS_GRAPH_URL": "https://graph.example", "RVAV_SLACK_API_URL": slack.URL + "/env/"}
		for k, v := range locks {
			t.Setenv(k, v)
		}
		// vCenter's TLS checks back on: another connection, which the
		// console's saved password wasn't given for.
		t.Setenv("RVAV_VCENTER_INSECURE", "")
		a2, srv2, api2 := open()
		var list api.IntegrationList
		api2.json("GET", "/api/v1/admin/integrations", admin, nil, 200, &list)
		lockedOf := map[string][]string{}
		settingsOf := map[string]map[string]any{}
		for _, it := range list.Items {
			lockedOf[string(it.Kind)], settingsOf[string(it.Kind)] = it.Locked, it.Settings
			if it.Kind == "console" && (it.SecretSet || it.Settings[config.ConsoleForKey] != nil) {
				t.Errorf("the console's password, given for another vCenter connection: %+v", it)
			}
		}
		if acct, err := srv2.Core.ConsoleAccount(ctx); acct != nil || err != nil {
			t.Errorf("the console account with another vCenter connection: %v %v", acct, err)
		}
		if p := a2.Live.Config(ctx).ConsolePassword; p != "" {
			t.Errorf("the console's password is used with another vCenter connection")
		}
		for kind, field := range map[string]string{"vcenter": "sync_interval", "git": "sync_interval", "hosts": "webhook_hosts",
			"console": "sdk_urls", "vra": "credential", "teams": "graph_url", "slack": "api_url"} {
			if !slices.Contains(lockedOf[kind], field) {
				t.Errorf("%s's %s isn't locked by its variable: %v", kind, field, lockedOf[kind])
			}
		}
		if lockedOf["awx"] != nil && slices.Contains(lockedOf["awx"], "sync_interval") {
			t.Error("AWX's interval is locked with no variable set")
		}
		if settingsOf["vcenter"]["sync_interval"] != "3m" || settingsOf["awx"]["sync_interval"] != "1h30m" {
			t.Errorf("intervals shown: %v, %v", settingsOf["vcenter"]["sync_interval"], settingsOf["awx"]["sync_interval"])
		}
		now = a2.Live.Config(ctx)
		if now.SyncInterval != 3*time.Minute || now.Git.Interval != 11*time.Minute || now.AWX.SyncInterval != 90*time.Minute ||
			!slices.Equal(now.WebhookHosts, []string{"hooks.corp.example"}) || now.VRA.Password != "from-env" ||
			now.Teams.GraphURL != "https://graph.example" || now.Slack.APIURL != slack.URL+"/env/" {
			t.Errorf("the environment doesn't win: %v %v %v %v %q %q %q", now.SyncInterval, now.Git.Interval, now.AWX.SyncInterval,
				now.WebhookHosts, now.VRA.Password, now.Teams.GraphURL, now.Slack.APIURL)
		}
		if got := srv2.WMKSNow(ctx); !slices.Equal(got, []string{"/env/wmks.js"}) {
			t.Errorf("WebMKS scripts with the variable set: %v", got)
		}
		// A locked field given is left as the variable sets it.
		it := save(api2, "hosts", map[string]any{"rest_source_hosts": []string{cmdb.URL}, "webhook_hosts": []string{"other.example"}}, "", 200)
		if hosts, _ := it.Settings["webhook_hosts"].([]any); len(hosts) != 1 || hosts[0] != "hooks.corp.example" {
			t.Errorf("webhook hosts after a save with the variable set: %v", it.Settings["webhook_hosts"])
		}
	})
}

// restItem is a catalog item with a field whose values come from a REST
// source at url.
func restItem(t *testing.T, h *harness, admin, url string) api.CatalogItemCreate {
	t.Helper()
	var rhel api.AdminCatalogItem
	h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
	var published api.CatalogItemVersionDetail
	h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, admin, nil, 200, &published)
	def := published.Definition
	def.FormSchema["properties"].(map[string]any)["service"] = map[string]any{"type": "string"}
	def.UiSchema.Fields["service"] = api.UiField{Widget: api.UiWidgetSelect, Label: "Service",
		ValueSource: &api.ValueSource{Kind: api.Rest, Params: &map[string]any{"url": url, "items": "$.items", "value": "$.id",
			"label": "$.name"}}}
	def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "service")
	return api.CatalogItemCreate{Slug: "rest-moved", Name: "With a REST source", Summary: "Its service comes from a CMDB.",
		Category: "compute", Type: api.VmBlueprint, Definition: &def}
}
