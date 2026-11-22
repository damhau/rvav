package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/snowsync"
	"github.com/damhau/rvav/internal/trust"
)

// snowInstance is a fake ServiceNow instance over HTTPS.
func snowInstance(t *testing.T) (*servicenow.Fake, *httptest.Server) {
	t.Helper()
	f := servicenow.NewFake()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

// snowCMDB is the integration's settings for a fake instance, the CMDB on.
func snowCMDB(snow *servicenow.Fake, srv *httptest.Server) config.ServiceNowConfig {
	return config.ServiceNowConfig{URL: srv.URL, Auth: servicenow.AuthBasic, Username: snow.Username, Secret: snow.Password,
		CertSHA256: trust.Fingerprint(srv.Certificate()), CMDB: true, Class: servicenow.DefaultClass, DataSource: "rvav",
		SyncInterval: 30 * time.Minute}
}

// TestServiceNowCMDB: each VM is a CI, sent through the IRE when its
// events say it changed, updated by a day-2 resize, retired when deleted;
// a pass ServiceNow refuses is tried again without a second CI; the
// reconcile catches a change no event told of and reports a CI rvav
// doesn't keep (ADR-0065).
func TestServiceNowCMDB(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		snow, srv := snowInstance(t)
		snow.Insert(servicenow.TableUser, servicenow.Record{"email": "thomas.nguyen@corp.example", "active": "true"})
		h.svc.Config.ServiceNow = snowCMDB(snow, srv)
		var ahead time.Duration
		sync := &snowsync.Syncer{Store: h.store, Config: func(context.Context) config.ServiceNowConfig { return h.svc.Config.ServiceNow },
			Now: func() time.Time { return time.Now().Add(ahead) }}
		pass := func() {
			t.Helper()
			if _, err := h.svc.Dispatcher.Once(ctx); err != nil {
				t.Fatal(err)
			}
			if err := sync.Pass(ctx); err != nil {
				t.Fatal(err)
			}
		}
		cis := func() []servicenow.Record { return snow.Rows(servicenow.DefaultClass) }
		var cmdb api.ServiceNowCmdb
		report := func() api.ServiceNowCmdb {
			t.Helper()
			h.json("GET", "/api/v1/admin/integrations/servicenow/cmdb", admin, nil, 200, &cmdb)
			return cmdb
		}
		// The first pass reconciles: nothing to keep yet.
		pass()
		if r := report(); !r.Enabled || r.Kept != 0 || r.ReconciledAt == nil || r.Servicenow == nil || *r.Servicenow != 0 || len(r.Orphans) != 0 {
			t.Fatalf("before any VM = %+v", r)
		}
		h.json("GET", "/api/v1/admin/integrations/servicenow/cmdb", "tnguyen", nil, 403, nil)

		d := buildVM(h, model)
		vm := d.vm()
		pass()
		if got := cis(); len(got) != 1 {
			t.Fatalf("after the build: %d CIs", len(got))
		}
		ci := cis()[0]
		var name, ip string
		if err := h.store.Pool.QueryRow(ctx, `select name from resources where id = $1`, d.resource).Scan(&name); err != nil {
			t.Fatal(err)
		}
		for _, n := range vm.Nics {
			for _, a := range valueOf(n.IpAddresses) {
				if ip == "" && !strings.Contains(a, ":") {
					ip = a
				}
			}
		}
		want := map[string]string{"name": name, "vm_instance_uuid": vm.InstanceUuid.String(), "object_id": vm.Moref,
			"cpus": "2", "memory": strconv.Itoa(vm.MemoryMb), "install_status": "1", "operational_status": "1", "state": "on",
			"discovery_source": "rvav", "correlation_id": d.resource, "ip_address": ip}
		for k, v := range want {
			if ci[k] != v {
				t.Errorf("the CI's %s = %q, want %q", k, ci[k], v)
			}
		}
		if !strings.Contains(ci["short_description"], d.id) || !strings.Contains(ci["short_description"], "Web Platform") ||
			!strings.Contains(ci["short_description"], "Thomas Nguyen") || ci["owned_by"] == "" || ci["disks_size"] == "" {
			t.Fatalf("the CI = %v", ci)
		}

		// A day-2 resize updates the same CI.
		d.succeed("tnguyen", "resize", map[string]any{"cpu_count": 4, "memory_gb": 16, "restart": true})
		pass()
		if got := cis(); len(got) != 1 || got[0]["sys_id"] != ci["sys_id"] || got[0]["cpus"] != "4" || got[0]["memory"] != "16384" {
			t.Fatalf("after the resize: %v", got)
		}

		// ServiceNow down: the deployment waits, with why; tried again, it
		// updates the same CI.
		snow.FailOn("/api/now/identifyreconcile", http.StatusServiceUnavailable)
		d.succeed("tnguyen", "power_off", nil)
		pass()
		if r := report(); r.Failing != 1 || len(r.Failures) != 1 || !strings.Contains(r.Failures[0].Error, "503") ||
			r.Failures[0].DeploymentId != d.id {
			t.Fatalf("while ServiceNow fails = %+v", r)
		}
		snow.FailOn("/api/now/identifyreconcile", 0)
		pass()
		if cis()[0]["state"] != "on" {
			t.Fatal("tried again before its backoff")
		}
		ahead += time.Minute
		pass()
		if got := cis(); len(got) != 1 || got[0]["state"] != "off" || report().Failing != 0 {
			t.Fatalf("after the retry: %v, %+v", got, cmdb)
		}
		// An answer lost on the way: sent again, the IRE finds the same CI.
		if _, err := h.store.Pool.Exec(ctx, `delete from servicenow_cis`); err != nil {
			t.Fatal(err)
		}
		if err := h.store.QueueAllForServiceNow(ctx, nil); err != nil {
			t.Fatal(err)
		}
		pass()
		if got := cis(); len(got) != 1 || report().Kept != 1 {
			t.Fatalf("sent again: %d CIs, %+v", len(got), cmdb)
		}

		// A change no event told of, and a CI of rvav's that it doesn't
		// keep: the reconcile finds both.
		if _, err := h.store.Pool.Exec(ctx, `update resources set properties = jsonb_set(properties, '{vm,cpu_count}', '6') where id = $1`,
			d.resource); err != nil {
			t.Fatal(err)
		}
		stray := snow.Insert(servicenow.DefaultClass, servicenow.Record{"name": "old-vra-vm01", "discovery_source": "rvav",
			"install_status": "1", "vm_instance_uuid": "4215aaaa-0000-0000-0000-000000000001"})
		pass()
		if cis()[0]["cpus"] != "4" {
			t.Fatal("updated before the reconcile")
		}
		h.json("POST", "/api/v1/admin/integrations/servicenow/cmdb/reconcile", admin, nil, 202, nil)
		pass()
		r := report()
		if cis()[0]["cpus"] != "6" || r.OrphansTotal != 1 || len(r.Orphans) != 1 || r.Orphans[0].SysId != stray ||
			!strings.HasSuffix(r.Orphans[0].Url, "/cmdb_ci_vmware_instance.do?sys_id="+stray) || r.Servicenow == nil || *r.Servicenow != 2 {
			t.Fatalf("after the reconcile: %v, %+v", cis(), r)
		}

		// Deleted: retired, not deleted.
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		pass()
		gone := snow.Row(servicenow.DefaultClass, ci["sys_id"])
		if gone == nil || gone["install_status"] != "7" || gone["operational_status"] != "6" {
			t.Fatalf("after the delete: %v", gone)
		}
		if r := report(); r.Kept != 0 || r.Retired != 1 || r.Queued != 0 {
			t.Fatalf("after the delete = %+v", r)
		}
		// Turned off: nothing is queued or sent; the retired CI and the stray stay.
		h.svc.Config.ServiceNow.CMDB = false
		pass()
		d2 := buildVM(h, model)
		pass()
		if n := len(cis()); n != 2 || report().Enabled || cmdb.Queued != 0 {
			t.Fatalf("with the CMDB off: %d CIs, %+v (%s)", n, cmdb, d2.id)
		}
	})
}

// TestServiceNowConnection: the ServiceNow integration takes an https URL
// only, signs in as a user or an OAuth client, and its test reads a
// requested item; its certificate is shown and pinned as any connection's
// (ADR-0065).
func TestServiceNowConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		snow, srv := snowInstance(t)
		basic := map[string]any{"url": srv.URL, "auth": "basic", "username": snow.Username}
		with := func(m map[string]any, extra map[string]any) map[string]any {
			out := map[string]any{}
			for k, v := range m {
				out[k] = v
			}
			for k, v := range extra {
				out[k] = v
			}
			return out
		}
		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, api.IntegrationWrite{Settings: basic,
			Secret: new(snow.Password)}, 200, &test)
		if test.Ok || test.Certificate == nil || test.Certificate.Trusted || !test.Certificate.Pinnable {
			t.Fatalf("untrusted = %+v", test)
		}
		pin := test.Certificate.Sha256
		pinned := with(basic, map[string]any{"cert_sha256": pin})
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, api.IntegrationWrite{Settings: pinned,
			Secret: new(snow.Password)}, 200, &test)
		if !test.Ok || !test.Certificate.Pinned || !slices.ContainsFunc(test.Checks, func(c api.IntegrationCheck) bool {
			return c.Name == "Requested items" && strings.Contains(c.Message, "as "+snow.Username)
		}) {
			t.Fatalf("pinned = %+v", test)
		}
		oauth := with(pinned, map[string]any{"auth": "oauth", "client_id": snow.ClientID})
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, api.IntegrationWrite{Settings: oauth,
			Secret: new(snow.ClientSecret)}, 200, &test)
		if !test.Ok || !slices.ContainsFunc(test.Checks, func(c api.IntegrationCheck) bool {
			return strings.Contains(c.Message, "the OAuth client "+snow.ClientID)
		}) {
			t.Fatalf("oauth = %+v", test)
		}
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, api.IntegrationWrite{Settings: oauth,
			Secret: new("wrong")}, 200, &test)
		if test.Ok || !strings.Contains(test.Checks[len(test.Checks)-1].Message, "OAuth client was refused") {
			t.Fatalf("a wrong client secret = %+v", test)
		}

		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/integrations/servicenow", admin, api.IntegrationWrite{Settings: with(basic,
			map[string]any{"url": "http://corp.service-now.com", "auth": "kerberos"})}, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Contains(f, "/settings/url") || !slices.Contains(f, "/settings/auth") {
			t.Fatalf("plain http and an unknown sign-in = %v", problem.Errors)
		}
		h.json("PUT", "/api/v1/admin/integrations/servicenow", admin, api.IntegrationWrite{Settings: with(basic,
			map[string]any{"cmdb_class": "vm", "data_source": "rvav^x", "projects": []string{"web", "nope"}})}, 422, &problem)
		if f := fieldsOf(problem.Errors); len(f) != 3 || !slices.Contains(f, "/settings/projects") {
			t.Fatalf("a class, a discovery source and a project none has = %v", problem.Errors)
		}
		var saved api.Integration
		h.json("PUT", "/api/v1/admin/integrations/servicenow", admin, api.IntegrationWrite{Settings: with(basic,
			map[string]any{"cert_sha256": trust.Fingerprint(srv.Certificate())}), Secret: new(snow.Password)}, 200, &saved)
		if !saved.Configured || !saved.SecretSet || saved.Applying {
			t.Fatalf("saved = %+v", saved)
		}
		// The password only goes to the instance it was given for.
		h.json("PUT", "/api/v1/admin/integrations/servicenow", admin, api.IntegrationWrite{Settings: map[string]any{
			"url": "https://other.service-now.com"}}, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/secret") {
			t.Fatalf("a new URL without the password = %v", problem.Errors)
		}
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, nil, 200, &test)
		if !test.Ok || test.Certificate == nil || !test.Certificate.Pinned {
			t.Fatalf("saved test = %+v", test)
		}
	})
}
