package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/trust"
)

// TestServiceNowContract: a submission is held to the API's schema (no
// field it doesn't list, a sys_id and a number of their pattern), a
// requested item asked for again is a read that leaves the room for new
// orders, and a 409 that passes says so, for ServiceNow to order again
// later rather than close the item.
func TestServiceNowContract(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		snow, srv := snowInstance(t)
		h.svc.Config.ServiceNow = config.ServiceNowConfig{URL: srv.URL, Auth: servicenow.AuthBasic, Username: snow.Username,
			Secret: snow.Password, CertSHA256: trust.Fingerprint(srv.Certificate()), RequestsPerMinute: 30}
		var made api.ServiceNowTokenMade
		h.json("POST", "/api/v1/admin/integrations/servicenow/token", "mkeller", nil, 201, &made)
		token := []string{"Authorization", "Bearer " + made.Secret}
		n := 0
		draft := func() map[string]any {
			n++
			return map[string]any{"ritm_sys_id": fmt.Sprintf("%032x", 0xc0de00+n), "ritm_number": fmt.Sprintf("RITM%07d", 30000+n),
				"user": "thomas.nguyen@corp.example", "ordered_by": "thomas.nguyen@corp.example", "catalog_item_id": "rhel9-server",
				"project_id": "web", "environment": "dev", "inputs": map[string]any{"role": "app", "size": "S",
					"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}
		}
		with := func(d map[string]any, k string, v any) map[string]any {
			d[k] = v
			return d
		}
		post := func(d map[string]any, want int) (api.ServiceNowRequest, api.Problem) {
			t.Helper()
			code, b, _ := h.do("POST", "/api/v1/servicenow/requests", "", d, token...)
			if code != want {
				t.Fatalf("%v = %d %s, want %d", d, code, b, want)
			}
			var out api.ServiceNowRequest
			var p api.Problem
			_ = json.Unmarshal(b, &out)
			_ = json.Unmarshal(b, &p)
			return out, p
		}
		fields := func(p api.Problem) []string {
			if p.Errors == nil {
				return nil
			}
			return fieldsOf(*p.Errors)
		}

		// Out of the schema: refused before anything is looked up or kept.
		for _, c := range []struct {
			d     map[string]any
			field string
		}{
			{with(draft(), "ritm_sys_id", ""), "/ritm_sys_id"},
			{with(draft(), "ritm_sys_id", "../../sys_user/"+fmt.Sprintf("%032x", 1)), "/ritm_sys_id"},
			{with(draft(), "ritm_sys_id", fmt.Sprintf("%032X", 0xabc)), "/ritm_sys_id"},
			{with(draft(), "ritm_number", "x"), "/ritm_number"},
			{with(draft(), "user", " "), "/user"},
			{with(draft(), "ordered_by", fmt.Sprintf("%0300d@corp.example", 0)), "/ordered_by"},
		} {
			if _, p := post(c.d, http.StatusUnprocessableEntity); !slices.Equal(fields(p), []string{c.field}) {
				t.Errorf("%v = %+v, want %s", c.d, p.Errors, c.field)
			}
		}
		post(with(draft(), "on_behalf_of", "usr_mkeller"), http.StatusBadRequest)
		post(with(draft(), "scheduled_at", "2030-01-01T00:00:00Z"), http.StatusBadRequest)
		var stored int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from servicenow_requests where ritm_sys_id !~ '^[0-9a-f]{32}$'`).
			Scan(&stored); err != nil || stored != 0 {
			t.Fatalf("sys_ids out of the pattern kept: %d, %v", stored, err)
		}
		if code, b, _ := h.do("GET", "/api/v1/servicenow/requests/..%2F..%2Fsys_user", "", nil, token...); code != http.StatusBadRequest &&
			code != http.StatusNotFound {
			t.Fatalf("a path for a sys_id = %d %s", code, b)
		}
		if code, b, _ := h.do("GET", "/api/v1/servicenow/requests/NOT-A-SYS-ID", "", nil, token...); code != http.StatusBadRequest {
			t.Fatalf("a sys_id out of the pattern = %d %s", code, b)
		}

		// Asked again, a requested item is a read: it doesn't use up the
		// minute's room for new orders.
		h.svc.Config.ServiceNow.RequestsPerMinute = 2
		first := draft()
		got, _ := post(first, http.StatusAccepted)
		for range 4 {
			if again, _ := post(first, http.StatusOK); again.RequestId != got.RequestId {
				t.Fatalf("again = %+v, first %+v", again, got)
			}
		}
		limited := false
		for range 4 {
			code, b, _ := h.do("POST", "/api/v1/servicenow/requests", "", with(draft(), "user", "nobody@corp.example"), token...)
			if code == http.StatusTooManyRequests {
				limited = true
				break
			}
			if code != http.StatusUnprocessableEntity {
				t.Fatalf("a new order = %d %s", code, b)
			}
		}
		if !limited {
			t.Fatal("new orders never limited")
		}
		h.svc.Config.ServiceNow.RequestsPerMinute = 200

		// rvav's connection to ServiceNow isn't set yet: that passes.
		h.svc.Config.ServiceNow.URL = ""
		if _, p := post(draft(), http.StatusConflict); p.Retry == nil || !*p.Retry {
			t.Fatalf("no connection = %+v", p)
		}
	})
}
