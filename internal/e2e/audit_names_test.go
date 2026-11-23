package e2e

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auditlog"
)

// TestAuditTargetNames: an audit event names its target as it was called
// when the event was recorded, says whether rvav still keeps it, and keeps
// the name once the target is renamed or deleted. An event from before
// rvav kept names takes the target's name now. The CSV export and the
// forwarded events carry the name too.
func TestAuditTargetNames(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"

		var mu sync.Mutex
		var forwarded []auditlog.Event
		siem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body auditlog.Body
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, &body); err != nil {
				t.Errorf("webhook body: %v", err)
			}
			mu.Lock()
			forwarded = append(forwarded, body.Events...)
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		}))
		t.Cleanup(siem.Close)
		h.allowWebhooks(siem.URL)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "siem-names-hmac", Kind: api.SecretWriteKindToken,
			Value: new("siem-names-signing-secret")}, 201, nil)
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM",
			Webhook: &api.AuditWebhook{Url: siem.URL + "/audit", Secret: "siem-names-hmac"}}, 201, nil)

		event := func(action, targetID string) api.AuditEvent {
			t.Helper()
			var page api.AuditEventPage
			h.json("GET", "/api/v1/audit?limit=500&action="+action, admin, nil, 200, &page)
			i := slices.IndexFunc(page.Items, func(e api.AuditEvent) bool { return e.TargetId == targetID })
			if i < 0 {
				t.Fatalf("no %s on %s in %+v", action, targetID, page.Items)
			}
			return page.Items[i]
		}
		check := func(action, targetID, name string, kept bool) {
			t.Helper()
			e := event(action, targetID)
			if deref(e.TargetName) != name || e.TargetExists == nil || *e.TargetExists != kept {
				t.Fatalf("%s on %s: name %q, kept %v; want %q, %v", action, targetID, deref(e.TargetName), e.TargetExists, name, kept)
			}
		}

		// A deployment, renamed, then deleted: rvav keeps a deleted
		// deployment, so its page still opens.
		d := buildVM(h, model)
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+d.id, "tnguyen", nil, 200, &dep)
		run := func(action string, inputs map[string]any) {
			t.Helper()
			var r api.Request
			h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: action, Inputs: inputs},
				202, &r, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(r.Id, nil)
			h.waitStatus(r.Id, "tnguyen", api.RequestStatusSucceeded)
		}
		run("edit", map[string]any{"name": "web-dev-names", "description": ""})
		run("delete", map[string]any{})
		var page api.AuditEventPage
		h.json("GET", "/api/v1/audit?limit=500&action=action.run", admin, nil, 200, &page)
		var runs []string
		for _, e := range page.Items {
			if e.TargetId == d.id && e.TargetExists != nil && *e.TargetExists {
				runs = append(runs, e.Detail["action"].(string)+" "+deref(e.TargetName))
			}
		}
		if !slices.Equal(runs, []string{"delete web-dev-names", "edit " + dep.Name}) {
			t.Fatalf("the deployment's runs, newest first = %q", runs)
		}

		// A project, renamed, archived, then deleted by hand: the API
		// archives projects and never deletes them.
		var pay api.AdminProject
		h.json("POST", "/api/v1/admin/projects", admin, api.AdminProjectCreate{Code: "names", Name: "Payments",
			Environments: []string{"dev"}}, 201, &pay)
		h.json("PATCH", "/api/v1/admin/projects/"+pay.Id, admin, api.AdminProjectUpdate{Name: new("Billing")}, 200, nil)
		h.json("PATCH", "/api/v1/admin/projects/"+pay.Id, admin, api.AdminProjectUpdate{Archived: new(true)}, 200, nil)
		check("project.create", pay.Id, "Payments", true)
		check("project.update", pay.Id, "Billing", true)
		check("project.archive", pay.Id, "Billing", true)
		if _, err := h.store.Pool.Exec(ctx, `delete from projects where id = $1`, pay.Id); err != nil {
			t.Fatal(err)
		}
		check("project.create", pay.Id, "Payments", false)
		check("project.archive", pay.Id, "Billing", false)

		// A catalog item and a role, deleted: the event of the deletion
		// keeps the name too.
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "names-erp", Name: "ERP application",
			Summary: "Two application servers.", Category: "compute", Type: api.VmBlueprint}, 201, &item)
		check("catalog.item.create", item.Id, "ERP application", true)
		h.json("DELETE", "/api/v1/admin/catalog/items/"+item.Id, admin, nil, 204, nil)
		check("catalog.item.create", item.Id, "ERP application", false)
		check("catalog.item.delete", item.Id, "ERP application", false)

		var role api.Role
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "names-operator", Name: "VM operator",
			Permissions: []string{"project.see", "day2.power:power_on"}}, 201, &role)
		check("role.create", "names-operator", "VM operator", true)
		h.json("DELETE", "/api/v1/admin/roles/names-operator", admin, nil, 204, nil)
		check("role.create", "names-operator", "VM operator", false)
		check("role.delete", "names-operator", "VM operator", false)

		// A target rvav doesn't keep as a record of its own has no name.
		h.json("PUT", "/api/v1/admin/branding", admin, api.BrandingWrite{Name: "Names cloud"}, 200, nil)
		if e := event("settings.branding.update", "branding"); e.TargetName != nil || e.TargetExists != nil {
			t.Fatalf("branding's event = %+v", e)
		}

		// An event recorded before rvav kept names: named as the role is now.
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "names-legacy", Name: "Legacy role",
			Permissions: []string{"project.see"}}, 201, nil)
		if _, err := h.store.Pool.Exec(ctx, `insert into audit_events (action, target_type, target_id) values ('role.legacy', 'role', 'names-legacy')`); err != nil {
			t.Fatal(err)
		}
		check("role.legacy", "names-legacy", "Legacy role", true)

		// The CSV export ends each row with the target's name.
		code, body, _ := h.do("GET", "/api/v1/audit?format=csv&q=names-operator", admin, nil)
		rows, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
		if code != 200 || err != nil || len(rows) < 3 {
			t.Fatalf("CSV %d %v:\n%s", code, err, body)
		}
		if want := []string{"at", "actor", "action", "target_type", "target_id", "project", "detail", "target_name"}; !slices.Equal(rows[0], want) {
			t.Fatalf("CSV header = %q", rows[0])
		}
		for _, r := range rows[1:] {
			if r[3] == "role" && r[4] == "names-operator" && r[7] != "VM operator" {
				t.Fatalf("CSV row = %q", r)
			}
		}

		// Forwarded events carry the name, as recorded.
		if err := h.svc.AuditLog.Once(ctx); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		names := map[string]string{}
		for _, e := range forwarded {
			names[e.Action+" "+e.TargetID] = e.TargetName
		}
		for key, want := range map[string]string{"role.delete names-operator": "VM operator", "catalog.item.delete " + item.Id: "ERP application",
			"project.create " + pay.Id: "Payments", "project.update " + pay.Id: "Billing", "settings.branding.update branding": ""} {
			if got, ok := names[key]; !ok || got != want {
				t.Fatalf("forwarded %s: %q (sent %v), want %q", key, got, ok, want)
			}
		}
	})
}
