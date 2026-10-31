package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
)

// TestInventoryToken (U1.4): Administration makes the token AWX's inventory
// source reads rvav with and puts it in AWX, for rvav's own inventory user;
// a new one keeps AWX's address for rvav and revokes the old; AWX refusing
// it leaves the old one working; RVAV_INVENTORY_TOKEN locks it.
func TestInventoryToken(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		reads := func(token string) int {
			code, _, _ := h.do("GET", "/api/v1/ansible/inventory", "", nil, "Authorization", "Bearer "+token)
			return code
		}

		var st api.InventoryToken
		h.json("GET", "/api/v1/admin/integrations/awx/inventory-token", admin, nil, 200, &st)
		if st.Locked || st.SourceSet || st.Token != nil {
			t.Fatalf("before: %+v", st)
		}
		// AWX's inventory source isn't set yet.
		h.json("POST", "/api/v1/admin/integrations/awx/inventory-token", admin, api.InventoryTokenWrite{}, 409, nil)
		h.svc.Config.AWX.InventorySourceID = 1
		h.svc.Config.PublicURL = "https://rvav.corp.example"
		bad := "ftp://rvav.corp.example"
		h.json("POST", "/api/v1/admin/integrations/awx/inventory-token", admin, api.InventoryTokenWrite{Url: &bad}, 422, nil)
		h.json("POST", "/api/v1/admin/integrations/awx/inventory-token", "tnguyen", api.InventoryTokenWrite{}, 403, nil)
		h.json("GET", "/api/v1/admin/integrations/awx/inventory-token", "tnguyen", nil, 403, nil)

		// Made with rvav's public URL, the credential set on the source.
		var made api.InventoryTokenMade
		h.json("POST", "/api/v1/admin/integrations/awx/inventory-token", admin, api.InventoryTokenWrite{}, 201, &made)
		url, first := h.awx.RvavCredentialOf(1)
		if url != "https://rvav.corp.example" || made.Url != url || !made.Attached || made.Credential != "rvav API" ||
			made.Token.User.Id != core.InventoryUserID || made.Token.Scope != api.ApiTokenScopeInventory ||
			made.Token.ExpiresAt == nil || !strings.Contains(deref(made.Update), "syncing") {
			t.Fatalf("made %+v; AWX has %q", made, url)
		}
		if code := reads(first); code != http.StatusOK {
			t.Fatalf("the token AWX has reads the inventory: %d", code)
		}
		if code, _, _ := h.do("GET", "/api/v1/requests?project_id=prj_web", "", nil, "Authorization", "Bearer "+first); code != http.StatusForbidden {
			t.Fatalf("the token AWX has read requests: %d", code)
		}
		h.json("GET", "/api/v1/admin/integrations/awx/inventory-token", admin, nil, 200, &st)
		if st.Token == nil || st.Token.Id != made.Token.Id || st.Token.LastUsedAt == nil || !st.SourceSet || deref(st.PublicUrl) != "https://rvav.corp.example" {
			t.Fatalf("after: %+v", st)
		}
		// rvav's inventory user is no one to pick, nor to sign in as.
		var people api.UserList
		h.json("GET", "/api/v1/users?q=inventory", admin, nil, 200, &people)
		for _, u := range people.Items {
			if u.Id == core.InventoryUserID {
				t.Fatalf("users found: %+v", people.Items)
			}
		}

		// Another address: the old token is revoked.
		inCluster := "http://rvav-api.rvav.svc.cluster.local:8080"
		h.json("POST", "/api/v1/admin/integrations/awx/inventory-token", admin, api.InventoryTokenWrite{Url: &inCluster}, 201, &made)
		url, second := h.awx.RvavCredentialOf(1)
		if url != inCluster || made.Attached || made.Revoked == nil || *made.Revoked != 1 || reads(first) != http.StatusUnauthorized || reads(second) != http.StatusOK {
			t.Fatalf("again: %+v; AWX has %q; old %d, new %d", made, url, reads(first), reads(second))
		}
		// None given: AWX's address for rvav is kept.
		h.json("POST", "/api/v1/admin/integrations/awx/inventory-token", admin, api.InventoryTokenWrite{}, 201, &made)
		url, third := h.awx.RvavCredentialOf(1)
		if url != inCluster || reads(second) != http.StatusUnauthorized || reads(third) != http.StatusOK {
			t.Fatalf("kept address: AWX has %q", url)
		}

		// AWX refuses it (another source, no address anywhere): the new token
		// is revoked, AWX's stays.
		h.svc.Config.AWX.InventorySourceID, h.svc.Config.PublicURL = 2, ""
		h.json("POST", "/api/v1/admin/integrations/awx/inventory-token", admin, api.InventoryTokenWrite{}, 502, nil)
		var n int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from api_tokens where user_id = $1`, core.InventoryUserID).Scan(&n); err != nil || n != 1 || reads(third) != http.StatusOK {
			t.Fatalf("after AWX refused: %d tokens, %v; AWX's reads %d", n, err, reads(third))
		}

		// Each is audited, without the token.
		var audited, leaked int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) filter (where action = 'awx.inventory_token'),
			count(*) filter (where detail::text like '%'||$1||'%') from audit_events`, third).Scan(&audited, &leaked)
		if audited != 3 || leaked != 0 {
			t.Fatalf("audited %d, the token in %d events", audited, leaked)
		}

		// RVAV_INVENTORY_TOKEN sets it: rvav makes none.
		t.Setenv("RVAV_INVENTORY_TOKEN", "rvav_pat_from_env")
		h.svc.Config.AWX.InventorySourceID = 1
		h.json("GET", "/api/v1/admin/integrations/awx/inventory-token", admin, nil, 200, &st)
		if !st.Locked {
			t.Fatalf("locked: %+v", st)
		}
		h.json("POST", "/api/v1/admin/integrations/awx/inventory-token", admin, api.InventoryTokenWrite{}, 409, nil)
	})
}
