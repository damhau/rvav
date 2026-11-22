package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/trust"
)

// TestServiceNowOrderer: who placed an order in ServiceNow is checked as
// in rvav. Ordering for someone else takes request.on_behalf in the
// project; the orderer is kept and audited next to the user; a policy
// applies when it holds for either and approves automatically only when
// it holds for both. A bare user name is a user's whole user name, and a
// sign-in too old to go by is refused.
func TestServiceNowOrderer(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		snow, srv := snowInstance(t)
		h.svc.Config.ServiceNow = config.ServiceNowConfig{URL: srv.URL, Auth: servicenow.AuthBasic, Username: snow.Username,
			Secret: snow.Password, CertSHA256: trust.Fingerprint(srv.Certificate()), RequestsPerMinute: 200}
		h.svc.Config.ScheduleOwnerIdleDays = 90
		var made api.ServiceNowTokenMade
		h.json("POST", "/api/v1/admin/integrations/servicenow/token", "mkeller", nil, 201, &made)
		token := []string{"Authorization", "Bearer " + made.Secret}
		n := 0
		submit := func(user, by, env string, want int) (api.ServiceNowRequest, []api.FieldError) {
			t.Helper()
			n++
			inputs := map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310"}
			if env == "prd" {
				inputs = map[string]any{"role": "front", "size": "S", "cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02",
					"network": "dvpg-web-prd-vl210"}
			}
			d := api.ServiceNowRequestDraft{RitmSysId: fmt.Sprintf("%032x", n), RitmNumber: fmt.Sprintf("RITM%07d", n), User: user,
				OrderedBy: by, CatalogItemId: "rhel9-server", ProjectId: "web", Environment: env, Inputs: &inputs}
			code, b, _ := h.do("POST", "/api/v1/servicenow/requests", "", d, token...)
			if code != want {
				t.Fatalf("%s for %s in %s = %d %s", by, user, env, code, b)
			}
			var out api.ServiceNowRequest
			var problem struct{ Errors []api.FieldError }
			_ = json.Unmarshal(b, &out)
			_ = json.Unmarshal(b, &problem)
			return out, problem.Errors
		}
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := h.store.Pool.Exec(ctx, q, args...); err != nil {
				t.Fatal(err)
			}
		}
		// Thomas is an infrastructure lead: pol_prd_vm approves his
		// production VMs automatically.
		exec(`insert into user_groups (user_id, group_name) values ('usr_tnguyen', 'rvav-infra-leads') on conflict do nothing`)
		if got, _ := submit("thomas.nguyen@corp.example", "thomas.nguyen@corp.example", "prd", 202); got.Status == api.RequestStatusPendingApproval ||
			got.OrderedBy != nil {
			t.Fatalf("his own = %+v", got)
		}

		// Sophie may not request for others in Web Platform; nobody is no one.
		if _, errs := submit("thomas.nguyen@corp.example", "sophie.brunner@corp.example", "prd", 422); !slices.Equal(fieldsOf(errs),
			[]string{"/ordered_by"}) || deref(errs[0].Code) != "not_allowed" {
			t.Fatalf("Sophie for Thomas = %+v", errs)
		}
		if _, errs := submit("thomas.nguyen@corp.example", "nobody@corp.example", "prd", 422); len(errs) != 1 ||
			deref(errs[0].Code) != "unknown_user" {
			t.Fatalf("nobody for Thomas = %+v", errs)
		}
		var refusedBy string
		if err := h.store.Pool.QueryRow(ctx, `select detail->'ordered_by'->>'id' from audit_events
			where action = 'servicenow.request_refused' and detail->>'ordered_by_given' = 'sophie.brunner@corp.example'`).
			Scan(&refusedBy); err != nil || refusedBy != "usr_sbrunner" {
			t.Fatalf("the refusal's audit names %q, %v", refusedBy, err)
		}

		// Léa, Web Platform's admin, may: Thomas's rights alone don't
		// approve it, and the orderer is kept.
		got, _ := submit("thomas.nguyen@corp.example", "lea.martin@corp.example", "prd", 202)
		if got.Status != api.RequestStatusPendingApproval || got.User.Id != "usr_tnguyen" || got.OrderedBy == nil ||
			got.OrderedBy.Id != "usr_lmartin" {
			t.Fatalf("Léa for Thomas = %+v", got)
		}
		var req api.Request
		h.json("GET", "/api/v1/requests/"+got.RequestId, "tnguyen", nil, 200, &req)
		if req.Requester.Id != "usr_tnguyen" || req.Servicenow == nil || req.Servicenow.OrderedBy == nil ||
			req.Servicenow.OrderedBy.Id != "usr_lmartin" {
			t.Fatalf("the request = %+v", req.Servicenow)
		}
		var by, forID, given string
		if err := h.store.Pool.QueryRow(ctx, `select detail->'ordered_by'->>'id', detail->'on_behalf_of'->>'id',
			(select ordered_by_given from servicenow_requests where request_id = $1)
			from audit_events where action = 'servicenow.request' and target_id = $1`, got.RequestId).Scan(&by, &forID, &given); err != nil ||
			by != "usr_lmartin" || forID != "usr_tnguyen" || given != "lea.martin@corp.example" {
			t.Fatalf("the audit: by %q for %q, kept %q, %v", by, forID, given, err)
		}

		// Policies read how a request came and who ordered it.
		h.json("POST", "/api/v1/admin/approval-policies", "mkeller", api.ApprovalPolicyWrite{Name: "Ordered for others",
			Condition: `request.via == "servicenow" && request.ordered_by != user.id`,
			Levels:    []api.ApprovalLevel{{Name: "Infrastructure leads", Group: new("rvav-infra-leads")}}}, 201, nil)
		if got, _ := submit("thomas.nguyen@corp.example", "thomas.nguyen@corp.example", "dev", 202); got.Status == api.RequestStatusPendingApproval {
			t.Fatalf("his own in dev = %+v", got)
		}
		if got, _ := submit("thomas.nguyen@corp.example", "lea.martin@corp.example", "dev", 202); got.Status != api.RequestStatusPendingApproval {
			t.Fatalf("Léa's for Thomas in dev = %+v", got)
		}

		// A bare name is a whole user name, never the start of an email.
		var ent api.ServiceNowEntitlements
		h.json("GET", "/api/v1/servicenow/entitlements?user=TNguyen", "", nil, 200, &ent, token...)
		if ent.User.Id != "usr_tnguyen" {
			t.Fatalf("by user name = %+v", ent.User)
		}
		h.json("GET", "/api/v1/servicenow/entitlements?user=thomas.nguyen", "", nil, 404, nil, token...)
		if got, _ := submit("tnguyen", "lmartin", "dev", 202); got.User.Id != "usr_tnguyen" || got.OrderedBy == nil ||
			got.OrderedBy.Id != "usr_lmartin" {
			t.Fatalf("by user names = %+v", got)
		}

		// A sign-in older than the idle limit: its groups may be gone.
		exec(`update users set last_seen_at = now() - interval '400 days' where id = 'usr_tnguyen'`)
		h.json("GET", "/api/v1/servicenow/entitlements?user=thomas.nguyen@corp.example", "", nil, 422, nil, token...)
		if _, errs := submit("thomas.nguyen@corp.example", "thomas.nguyen@corp.example", "prd", 422); len(errs) != 1 ||
			errs[0].Field != "/user" || deref(errs[0].Code) != "idle" {
			t.Fatalf("an old sign-in = %+v", errs)
		}
		exec(`update users set last_seen_at = now() where id = 'usr_tnguyen'`)
		exec(`update users set last_seen_at = now() - interval '400 days' where id = 'usr_lmartin'`)
		if _, errs := submit("thomas.nguyen@corp.example", "lea.martin@corp.example", "prd", 422); len(errs) != 1 ||
			errs[0].Field != "/ordered_by" || deref(errs[0].Code) != "idle" {
			t.Fatalf("an orderer's old sign-in = %+v", errs)
		}
	})
}
