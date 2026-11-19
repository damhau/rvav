package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestRequestableVersions: an administrator keeps an earlier version
// requestable beside a new one; requesters pick either, the newest by
// default, and the request and deployment keep the one picked. Updates go
// to any requestable version. A retired version is refused as stale, also
// when it is retired while the request is checked; the newest left becomes
// the default, and withdrawing retires them all (ADR-0046).
func TestRequestableVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "lmartin", "tnguyen", "prj_web"
		const path = "/api/v1/admin/catalog/items/rhel9-server/versions/"
		// Fresh values each time: a field the answer leaves out isn't kept from the last.
		change := func(to string, body any) api.AdminCatalogItem {
			t.Helper()
			var it api.AdminCatalogItem
			h.json("POST", path+to, admin, body, 200, &it)
			return it
		}
		catalogItem := func() api.CatalogItem {
			t.Helper()
			var it api.CatalogItem
			h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, owner, nil, 200, &it)
			return it
		}

		var item api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &item)
		v1 := item.PublishedVersion.Id
		var draft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions", admin, api.CatalogItemVersionCreate{Changelog: new("Newer.")}, 201, &draft)
		v2 := draft.Id
		item = change(v2+"/publish", api.CatalogItemVersionPublish{KeepPublished: new(true)})
		if item.PublishedVersion == nil || item.PublishedVersion.Id != v2 || stateOf(item, v1) != "published" {
			t.Fatalf("published beside 1: default %+v, state of 1 %s", item.PublishedVersion, stateOf(item, v1))
		}

		// The catalog lists both, newest first; the form of either is served.
		rhel := catalogItem()
		if rhel.Version.Id != v2 || rhel.Versions == nil || len(*rhel.Versions) != 2 || (*rhel.Versions)[0].Id != v2 || (*rhel.Versions)[1].Id != v1 {
			t.Fatalf("catalog item: version %s, versions %+v", rhel.Version.Id, rhel.Versions)
		}
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web+"&version_id="+v1, owner, nil, 200, &form)
		if form.VersionId != v1 {
			t.Fatalf("form of 1 = %s", form.VersionId)
		}

		inputs := map[string]any{"role": "app", "size": "M", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}
		request := func(version string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: rhel.Id, VersionId: version, ProjectId: web, Environment: "dev", Inputs: inputs}
		}
		stale := func(errs []api.FieldError) string {
			for _, e := range errs {
				if e.Field == "/version_id" && deref(e.Code) == "stale_version" {
					return e.Message
				}
			}
			return ""
		}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", owner, request(v1), 200, &pv)
		if !pv.Valid || stale(pv.Errors) != "" {
			t.Fatalf("preview on 1: %+v", pv.Errors)
		}

		// A request on the earlier version keeps it, as its deployment does.
		var req api.Request
		h.json("POST", "/api/v1/requests", owner, request(v1), 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, owner, nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded || req.Version.Id != v1 || req.DeploymentIds == nil {
			t.Fatalf("request on 1: %s %s", req.Status, req.Version.Id)
		}
		dep := (*req.DeploymentIds)[0]
		var d api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+dep, owner, nil, 200, &d)
		if d.Version.Id != v1 {
			t.Fatalf("deployment's version = %s", d.Version.Id)
		}

		// Updates go to any requestable version: its own, or the newest.
		update := func(version string) api.UpdatePreview {
			t.Helper()
			var up api.UpdatePreview
			h.json("POST", "/api/v1/deployments/"+dep+"/update/preview", owner, api.UpdateDraft{VersionId: version, Inputs: inputs}, 200, &up)
			return up
		}
		if up := update(v1); stale(up.Errors) != "" || up.ToVersion.Id != v1 {
			t.Fatalf("update on its own version: to %s, %+v", up.ToVersion.Id, up.Errors)
		}
		if up := update(v2); stale(up.Errors) != "" || up.ToVersion.Id != v2 || up.FromVersion.Id != v1 {
			t.Fatalf("update to 2: from %s to %s, %+v", up.FromVersion.Id, up.ToVersion.Id, up.Errors)
		}

		// Retired while a request or an update is checked, after its
		// preview: it waits for the retirement, then is refused as stale.
		whileRetiring := func(version, what string, send func() (int, []byte)) {
			t.Helper()
			tx, err := h.store.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, `update catalog_item_versions set state = 'deprecated' where id = $1`, version); err != nil {
				t.Fatal(err)
			}
			type result struct {
				status int
				body   []byte
			}
			sent := make(chan result, 1)
			go func() {
				status, body := send()
				sent <- result{status, body}
			}()
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				var waiting int
				_ = h.store.Pool.QueryRow(ctx, `select count(*) from pg_locks where locktype = 'transactionid' and not granted`).Scan(&waiting)
				if waiting > 0 {
					break
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			r := <-sent
			var problem struct{ Errors []api.FieldError }
			_ = json.Unmarshal(r.body, &problem)
			if r.status != 422 || stale(problem.Errors) != "This version was retired meanwhile. Reload the form to get the current version." {
				t.Fatalf("%s while its version is retired = %d %s", what, r.status, r.body)
			}
			if _, err := h.store.Pool.Exec(ctx, `update catalog_item_versions set state = 'published' where id = $1`, version); err != nil {
				t.Fatal(err)
			}
		}
		whileRetiring(v1, "a request", func() (int, []byte) {
			status, body, _ := h.do("POST", "/api/v1/requests", owner, request(v1), "Idempotency-Key", uuid.NewString())
			return status, body
		})
		whileRetiring(v2, "an update", func() (int, []byte) {
			status, body, _ := h.do("POST", "/api/v1/deployments/"+dep+"/update", owner, api.UpdateDraft{VersionId: v2, Inputs: inputs},
				"Idempotency-Key", uuid.NewString())
			return status, body
		})

		// Retired, the earlier version is refused, the form of it too; its deployment keeps it.
		item = change(v1+"/deprecate", nil)
		if item.PublishedVersion == nil || item.PublishedVersion.Id != v2 || stateOf(item, v1) != "deprecated" {
			t.Fatalf("1 retired: default %+v, state of 1 %s", item.PublishedVersion, stateOf(item, v1))
		}
		if rhel := catalogItem(); rhel.Versions != nil {
			t.Fatalf("one version left, yet versions = %+v", *rhel.Versions)
		}
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web+"&version_id="+v1, owner, nil, 404, nil)
		h.json("POST", "/api/v1/requests/preview", owner, request(v1), 200, &pv)
		if msg := stale(pv.Errors); !strings.Contains(msg, "is no longer offered: request "+item.PublishedVersion.Number) {
			t.Fatalf("stale preview: %q in %+v", msg, pv.Errors)
		}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/requests", owner, request(v1), 422, &problem, "Idempotency-Key", uuid.NewString())
		if stale(problem.Errors) == "" {
			t.Fatalf("a request on a retired version: %+v", problem.Errors)
		}
		if up := update(v1); stale(up.Errors) == "" {
			t.Fatalf("an update to a retired version: %+v", up.Errors)
		}
		h.json("GET", "/api/v1/deployments/"+dep, owner, nil, 200, &d)
		if d.Version.Id != v1 {
			t.Fatalf("the deployment's version after retiring it = %s", d.Version.Id)
		}

		// Offered again beside 2, 1 isn't the default: 2 is newer. Retiring 2 makes 1 the default.
		item = change(v1+"/publish", api.CatalogItemVersionPublish{KeepPublished: new(true)})
		if item.PublishedVersion.Id != v2 || stateOf(item, v1) != "published" {
			t.Fatalf("1 offered again: default %s, state of 1 %s", item.PublishedVersion.Id, stateOf(item, v1))
		}
		item = change(v2+"/deprecate", api.CatalogItemVersionDeprecate{})
		if item.PublishedVersion == nil || item.PublishedVersion.Id != v1 {
			t.Fatalf("2 retired: default %+v", item.PublishedVersion)
		}
		h.json("POST", path+v2+"/deprecate", admin, nil, 409, nil)

		// Withdrawing retires every requestable version.
		item = change(v2+"/publish", api.CatalogItemVersionPublish{KeepPublished: new(true)})
		item = change(v2+"/deprecate", api.CatalogItemVersionDeprecate{Withdraw: new(true)})
		if item.PublishedVersion != nil || slices.ContainsFunc(item.Versions, func(v api.CatalogItemVersion) bool { return v.State == "published" }) {
			t.Fatalf("withdrawn: %+v", item.Versions)
		}
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, owner, nil, 404, nil)

		// Publishing without keeping deprecates the others, as before.
		item = change(v1+"/publish", api.CatalogItemVersionPublish{KeepPublished: new(true)})
		item = change(v2+"/publish", nil)
		if item.PublishedVersion.Id != v2 || stateOf(item, v1) != "deprecated" {
			t.Fatalf("published alone: default %s, state of 1 %s", item.PublishedVersion.Id, stateOf(item, v1))
		}
		var kept int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action = 'catalog.version.publish'
			and target_id = $1 and (detail->>'keep_published')::boolean`, item.Id).Scan(&kept); err != nil || kept != 4 {
			t.Fatalf("publishes keeping the others in the audit log = %d, %v", kept, err)
		}
	})
}
