package e2e

import (
	"context"
	"fmt"
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
	"github.com/damhau/rvav/internal/auth"
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
		// Its pages keep one order, or they may repeat or skip CIs.
		read := slices.IndexFunc(snow.Calls(), func(c string) bool {
			return strings.HasPrefix(c, "GET /api/now/table/"+servicenow.DefaultClass+"?")
		})
		if read < 0 || !strings.Contains(snow.Calls()[read], "ORDERBYsys_id") {
			t.Fatalf("the reconcile's read: %v", snow.Calls())
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
			map[string]any{"cmdb_class": "vm", "data_source": "rvav^x", "projects": []string{"web", "nope"},
				"allowed_sources": []string{"10.0.0.0/33"}, "requests_per_minute": 0})}, 422, &problem)
		if f := fieldsOf(problem.Errors); len(f) != 5 || !slices.Contains(f, "/settings/projects") || !slices.Contains(f, "/settings/allowed_sources") {
			t.Fatalf("a class, a discovery source, a project none has, an address and a limit = %v", problem.Errors)
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

// TestServiceNowRequests: ServiceNow's token requests on a user's behalf,
// as that user and never more; the request keeps its requested item, whose
// work notes and state follow it, in order; no one else is taken under
// /servicenow/, and the token nowhere else (ADR-0065).
func TestServiceNowRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		snow, srv := snowInstance(t)
		h.svc.Config.ServiceNow = config.ServiceNowConfig{URL: srv.URL, Auth: servicenow.AuthBasic, Username: snow.Username,
			Secret: snow.Password, CertSHA256: trust.Fingerprint(srv.Certificate()), RequestsPerMinute: 30}
		var ahead time.Duration
		sync := &snowsync.Syncer{Store: h.store, PublicURL: "https://rvav.corp.example",
			Config: func(context.Context) config.ServiceNowConfig { return h.svc.Config.ServiceNow },
			Now:    func() time.Time { return time.Now().Add(ahead) }}
		pass := func() {
			t.Helper()
			if _, err := h.svc.Dispatcher.Once(ctx); err != nil {
				t.Fatal(err)
			}
			if err := sync.Pass(ctx); err != nil {
				t.Fatal(err)
			}
		}

		h.json("POST", "/api/v1/admin/integrations/servicenow/token", "tnguyen", nil, 403, nil)
		var made api.ServiceNowTokenMade
		h.json("POST", "/api/v1/admin/integrations/servicenow/token", admin, nil, 201, &made)
		if made.Token.Scope != api.ApiTokenScopeServicenow || made.Token.User.Id != auth.ServiceNowUserID || made.Secret == "" {
			t.Fatalf("the token = %+v", made.Token)
		}
		token := []string{"Authorization", "Bearer " + made.Secret}
		call := func(method, path string, body any, want int, out any) {
			t.Helper()
			h.json(method, "/api/v1/servicenow"+path, "", body, want, out, token...)
		}

		// No one else is taken under /servicenow/, an administrator's own
		// token included; the token is taken nowhere else.
		h.json("GET", "/api/v1/servicenow/catalog", admin, nil, 403, nil)
		h.json("GET", "/api/v1/servicenow/catalog", "tnguyen", nil, 403, nil)
		var own api.ApiTokenCreated
		h.json("POST", "/api/v1/tokens", admin, api.ApiTokenCreate{Name: "scripts"}, 201, &own)
		h.json("GET", "/api/v1/servicenow/catalog", "", nil, 403, nil, "Authorization", "Bearer "+own.Token)
		for _, path := range []string{"/api/v1/me", "/api/v1/requests?project_id=prj_web", "/api/v1/admin/integrations/servicenow/token"} {
			h.json("GET", path, "", nil, 403, nil, token...)
		}
		// No role grants it: a custom role can't name it.
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "snow", Name: "ServiceNow", Permissions: []string{"servicenow.request"}},
			422, nil)

		// The catalog: what a ServiceNow variable can hold, what rvav finds
		// at request time said; an item shared nowhere isn't offered.
		var cat api.ServiceNowCatalog
		call("GET", "/catalog", nil, 200, &cat)
		var rhel *api.ServiceNowCatalogItem
		for i, it := range cat.Items {
			if it.Slug == "rhel9-server" {
				rhel = &cat.Items[i]
			}
			if it.Slug == "pg-node" {
				t.Errorf("an item shared nowhere is offered: %+v", it)
			}
		}
		if rhel == nil {
			t.Fatalf("the catalog = %+v", cat)
		}
		inputs := map[string]api.ServiceNowInput{}
		for _, in := range rhel.Inputs {
			inputs[in.Name] = in
		}
		if size := inputs["size"]; size.Options == nil || len(*size.Options) != 4 || size.Dynamic || !inputs["cluster"].Dynamic {
			t.Fatalf("the inputs = %+v", rhel.Inputs)
		}
		if _, ok := inputs["post_config"]; ok {
			t.Fatalf("a list is offered: %+v", inputs["post_config"])
		}

		// Where the user requests, by email in any case or by user name.
		var ent api.ServiceNowEntitlements
		for _, who := range []string{"Thomas.Nguyen@corp.example", "tnguyen"} {
			call("GET", "/entitlements?user="+who, nil, 200, &ent)
			if ent.User.Id != "usr_tnguyen" || len(ent.Projects) != 1 || ent.Projects[0].Project.Id != "prj_web" ||
				!slices.Contains(ent.Projects[0].Items, rhel.Id) || slices.Contains(ent.Projects[0].Items, "itm_postgres-cluster") ||
				len(ent.Projects[0].Environments) != 3 {
				t.Fatalf("%s's entitlements = %+v", who, ent)
			}
		}
		call("GET", "/entitlements?user=nobody@corp.example", nil, 404, nil)

		ritm := snow.Insert(servicenow.TableRequestedItem, servicenow.Record{"number": "RITM0010045", "state": "1"})
		given := map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310"}
		draft := api.ServiceNowRequestDraft{RitmSysId: ritm, RitmNumber: "RITM0010045", User: "thomas.nguyen@corp.example",
			OrderedBy: "thomas.nguyen@corp.example", CatalogItemId: "rhel9-server", ProjectId: "web", Environment: "dev", Inputs: &given}
		with := func(change func(d *api.ServiceNowRequestDraft)) api.ServiceNowRequestDraft {
			d := draft
			change(&d)
			return d
		}

		// Never more than the user may: someone rvav doesn't know, an item
		// not shared in the project, a project the user isn't in.
		var problem struct{ Errors []api.FieldError }
		call("POST", "/requests", with(func(d *api.ServiceNowRequestDraft) {
			d.User, d.OrderedBy = "nobody@corp.example", "nobody@corp.example"
		}),
			422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Equal(f, []string{"/user"}) {
			t.Fatalf("an unknown user = %v", problem.Errors)
		}
		call("POST", "/requests", with(func(d *api.ServiceNowRequestDraft) { d.CatalogItemId = "postgres-cluster" }), 404, nil)
		call("POST", "/requests", with(func(d *api.ServiceNowRequestDraft) {
			d.User, d.OrderedBy = "sophie.brunner@corp.example", "sophie.brunner@corp.example"
		}), 404, nil)
		var refused int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action = 'servicenow.request_refused'
			and actor_id = $1 and detail->>'ritm_number' = 'RITM0010045'`, auth.ServiceNowUserID).Scan(&refused); err != nil || refused != 3 {
			t.Fatalf("refusals audited: %d, %v", refused, err)
		}

		var got api.ServiceNowRequest
		call("POST", "/requests", draft, 202, &got)
		if got.User.Id != "usr_tnguyen" || got.RitmSysId != ritm || got.RequestNumber == "" || len(got.DeploymentIds) != 1 {
			t.Fatalf("the request = %+v", got)
		}
		// The same requested item again: the same request.
		var again api.ServiceNowRequest
		call("POST", "/requests", draft, 200, &again)
		call("GET", "/requests/"+ritm, nil, 200, &again)
		if again.RequestId != got.RequestId {
			t.Fatalf("again = %+v, first %+v", again, got)
		}
		call("GET", "/requests/"+strings.Repeat("0", 32), nil, 404, nil)

		// The user's request, as they'd have made it, that says where it
		// came from; the audit log says who for.
		var req api.Request
		h.json("GET", "/api/v1/requests/"+got.RequestId, "tnguyen", nil, 200, &req)
		if req.Requester.Id != "usr_tnguyen" || req.Servicenow == nil || req.Servicenow.Number != "RITM0010045" ||
			req.Servicenow.Url != srv.URL+"/sc_req_item.do?sys_id="+ritm || req.Justification == nil ||
			!strings.Contains(*req.Justification, "RITM0010045") {
			t.Fatalf("the request = %+v", req)
		}
		var actor, email string
		if err := h.store.Pool.QueryRow(ctx, `select actor_id, detail->'on_behalf_of'->>'email' from audit_events
			where action = 'servicenow.request' and target_id = $1`, got.RequestId).Scan(&actor, &email); err != nil ||
			actor != auth.ServiceNowUserID || email != "thomas.nguyen@corp.example" {
			t.Fatalf("the audit = %s for %s, %v", actor, email, err)
		}

		// Its progress reaches the requested item, in order, ServiceNow's
		// refusal waiting with backoff.
		snow.FailOn("/api/now/table/sc_req_item", http.StatusServiceUnavailable)
		h.runWorkflow(got.RequestId, nil)
		h.waitStatus(got.RequestId, "tnguyen", api.RequestStatusSucceeded)
		pass()
		if notes := snow.WorkNotes(ritm); len(notes) != 0 {
			t.Fatalf("while ServiceNow fails: %v", notes)
		}
		snow.FailOn("/api/now/table/sc_req_item", 0)
		ahead += time.Minute
		pass()
		notes, item := snow.WorkNotes(ritm), snow.Row(servicenow.TableRequestedItem, ritm)
		if len(notes) != 2 || !strings.Contains(notes[0], got.RequestNumber+" (") || !strings.Contains(notes[0], "submitted") ||
			!strings.Contains(notes[1], "succeeded") || !strings.Contains(notes[1], "https://rvav.corp.example/deployments/"+got.DeploymentIds[0]) ||
			item["state"] != servicenow.ItemClosedComplete {
			t.Fatalf("the requested item: %v, %v", notes, item)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+got.DeploymentIds[0], "tnguyen", nil, 200, &dep)
		if dep.Servicenow == nil || dep.Servicenow.SysId != ritm {
			t.Fatalf("the deployment's requested item = %+v", dep.Servicenow)
		}

		// Its addresses only, once set.
		h.svc.Config.ServiceNow.AllowedSources = []string{"10.99.0.0/16"}
		call("GET", "/catalog", nil, 403, nil)
		h.svc.Config.ServiceNow.AllowedSources = []string{"10.99.0.0/16", "127.0.0.1"}
		call("GET", "/catalog", nil, 200, nil)

		// So many submissions a minute, across processes. A requested item
		// submitted again is a read, so these are new ones, for no one rvav
		// knows.
		h.svc.Config.ServiceNow.RequestsPerMinute = 1
		limited := false
		for i := range 3 {
			fresh := with(func(d *api.ServiceNowRequestDraft) {
				d.RitmSysId, d.RitmNumber, d.User = fmt.Sprintf("%032x", 0xf00+i), fmt.Sprintf("RITM%07d", 20000+i), "nobody@corp.example"
			})
			code, _, hdr := h.do("POST", "/api/v1/servicenow/requests", "", fresh, token...)
			if code == http.StatusTooManyRequests {
				if secs, err := strconv.Atoi(hdr.Get("Retry-After")); err != nil || secs < 1 || secs > 60 {
					t.Fatalf("Retry-After = %q", hdr.Get("Retry-After"))
				}
				limited = true
				break
			}
		}
		if !limited {
			t.Fatal("never limited")
		}

		// Revoked: ServiceNow is cut off.
		var shown api.ServiceNowToken
		h.json("GET", "/api/v1/admin/integrations/servicenow/token", admin, nil, 200, &shown)
		if shown.Token == nil || shown.Token.Id != made.Token.Id || shown.RequestsPerMinute != 1 {
			t.Fatalf("the token shown = %+v", shown)
		}
		h.json("DELETE", "/api/v1/admin/integrations/servicenow/token", admin, nil, 204, nil)
		call("GET", "/catalog", nil, 401, nil)
		var after api.ServiceNowToken
		h.json("GET", "/api/v1/admin/integrations/servicenow/token", admin, nil, 200, &after)
		if after.Token != nil {
			t.Fatalf("after the revoke = %+v", after)
		}
	})
}
