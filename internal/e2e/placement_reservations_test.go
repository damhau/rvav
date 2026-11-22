package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/provision"
)

// TestPlacementReservations sends requests at once onto two clusters, one
// with room for only some of them: each counts what the others reserved,
// so they spread over both instead of all choosing the one with the most
// free memory. A reservation ends when it expires, its VM is in the
// inventory, its request is cancelled, or its build fails or is rolled
// back; a retry and a build starting reserve again (#189, ADR-0036).
func TestPlacementReservations(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		syncer := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := syncer.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		rule := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })]
		strategy := api.MostFreeMemory
		h.json("PUT", "/api/v1/admin/placement-rules/"+rule.Id, admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Clusters: []string{"GVA-PRD-*"}, Datastores: []string{"vsan-gva-prd-*"}, Networks: []string{"dvpg-app-prd-*"},
			Folder: "/GVA/vm/rvav/web/dev", Strategy: &strategy}, 200, &rule)
		// GVA-PRD-CL01 has room for four VMs of 8 GB, GVA-PRD-CL02 for two,
		// as their clusters say: their hosts' figures left out.
		if _, err := h.store.Pool.Exec(ctx, `update inv_hosts set memory_total_mb = 0, memory_used_mb = 0`); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Pool.Exec(ctx, `update inv_clusters set memory_total_mb = 100 * 1024,
			memory_used_mb = 100 * 1024 - case name when 'GVA-PRD-CL01' then 38 * 1024 else 21 * 1024 end
			where name like 'GVA-PRD-%'`); err != nil {
			t.Fatal(err)
		}

		def := targetItem("dvpg-app-prd-vl220", map[string]any{"memory_gb": "8"}, nil)
		def.Blueprint["rollback_on_failure"] = true
		var it api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "i189-app", Name: "Reserved app",
			Summary: "Placement reservations.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &it)
		h.json("POST", "/api/v1/admin/catalog/items/i189-app/versions/"+it.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/i189-app/entitlements", admin,
			api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		draft := func(role string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: it.Id, VersionId: it.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"role": role}}
		}

		// Six at once: all accepted, four on GVA-PRD-CL01 and two on CL02.
		var mu sync.Mutex
		var wg sync.WaitGroup
		var ids, refused []string
		for _, role := range []string{"a", "b", "c", "d", "e", "f"} {
			wg.Go(func() {
				code, body, _ := h.do("POST", "/api/v1/requests", "tnguyen", draft(role), "Idempotency-Key", uuid.NewString())
				var req api.Request
				_ = json.Unmarshal(body, &req)
				mu.Lock()
				defer mu.Unlock()
				if code != 202 {
					refused = append(refused, string(body))
				} else {
					ids = append(ids, req.Id)
				}
			})
		}
		wg.Wait()
		if len(refused) > 0 {
			t.Fatalf("refused: %v", refused)
		}
		on := map[string][]string{}
		for _, id := range ids {
			var cluster string
			if err := h.store.Pool.QueryRow(ctx, `select plan->'vms'->0->>'cluster' from requests where id = $1`, id).Scan(&cluster); err != nil {
				t.Fatal(err)
			}
			on[cluster] = append(on[cluster], id)
		}
		if len(on["GVA-PRD-CL01"]) != 4 || len(on["GVA-PRD-CL02"]) != 2 {
			t.Fatalf("placed at once: %v", on)
		}
		reserved := func(want1, want2 int) {
			t.Helper()
			var inv api.VsphereInventory
			h.json("GET", "/api/v1/admin/inventory", admin, nil, 200, &inv)
			got := map[string]int{}
			for _, c := range inv.Clusters {
				got[c.Name] = c.MemoryReservedGb
			}
			if got["GVA-PRD-CL01"] != want1 || got["GVA-PRD-CL02"] != want2 {
				t.Fatalf("reserved GB = %v, want %d on GVA-PRD-CL01 and %d on CL02", got, want1, want2)
			}
		}
		reserved(32, 16)
		one, two := on["GVA-PRD-CL01"], on["GVA-PRD-CL02"]

		// Expired: it no longer counts, and the sweep deletes it.
		if _, err := h.store.Pool.Exec(ctx, `update placement_reservations set expires_at = now() - interval '1 minute'
			where request_id = $1`, one[0]); err != nil {
			t.Fatal(err)
		}
		reserved(24, 16)
		if n, err := h.acts.SweepReservations(ctx); err != nil || n != 1 {
			t.Fatalf("swept %d (%v), want the expired one", n, err)
		}

		// Its VM in the inventory, as vCenter's change notification puts it.
		if _, err := h.store.Pool.Exec(ctx, `insert into inv_vms (vcenter, moref, name, cluster)
			select vcenter, 'vm-i189', vm_name, cluster from placement_reservations where request_id = $1`, one[1]); err != nil {
			t.Fatal(err)
		}
		reserved(16, 16)

		// Cancelled before it ran.
		h.json("POST", "/api/v1/requests/"+two[0]+"/cancel", "tnguyen", nil, 202, nil)
		reserved(16, 8)

		// Its clone failing: nothing built, nothing held.
		h.runWorkflow(two[1], func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(h.acts.CloneVM, mock.Anything, mock.Anything).Return("", temporal.NewNonRetryableApplicationError(
				"vCenter refused the clone", "test", nil))
		})
		var failed api.Request
		h.json("GET", "/api/v1/requests/"+two[1], "tnguyen", nil, 200, &failed)
		if failed.Status != api.RequestStatusFailed {
			t.Fatalf("clone failing: %s", failed.Status)
		}
		reserved(16, 0)

		// Built, then rolled back: its VM gone, its reservation doesn't come
		// back while it is failed; retried, it holds its room again.
		h.runWorkflow(one[2], func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(h.acts.PowerOn, mock.Anything, mock.Anything).Return("", temporal.NewNonRetryableApplicationError(
				"the VM doesn't start", "test", nil))
		})
		var rolled api.Request
		h.json("GET", "/api/v1/requests/"+one[2], "tnguyen", nil, 200, &rolled)
		steps := timelineOf(h, "tnguyen", one[2])
		if rolled.Status != api.RequestStatusFailed || steps["rollback"].Status != api.StepStatusDone {
			t.Fatalf("rolled back = %s (steps %v)", rolled.Status, keysOf(steps))
		}
		reserved(8, 0)
		h.json("POST", "/api/v1/requests/"+one[2]+"/retry", "tnguyen", nil, 202, nil)
		reserved(16, 0)

		// A build starting holds its room again, the expired one too, until
		// its VM is built.
		for _, id := range []string{one[0], one[2]} {
			var heldWhileCloning bool
			h.runWorkflow(id, func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity(h.acts.CloneVM, mock.Anything, mock.Anything).Return(func(ctx context.Context, in provision.VMInput) (string, error) {
					var n int
					_ = h.store.Pool.QueryRow(ctx, `select count(*) from placement_reservations where request_id = $1
						and expires_at > now()`, id).Scan(&n)
					heldWhileCloning = n == 1
					return h.acts.CloneVM(ctx, in)
				})
			})
			var built api.Request
			h.json("GET", "/api/v1/requests/"+id, "tnguyen", nil, 200, &built)
			if built.Status != api.RequestStatusSucceeded || !heldWhileCloning {
				t.Fatalf("%s: %s %s, held while cloning %v", id, built.Status, errorOf(built), heldWhileCloning)
			}
		}
		reserved(8, 0)
		if n, err := h.acts.SweepReservations(ctx); err != nil || n != 5 {
			t.Fatalf("swept %d (%v), want all but the one still queued", n, err)
		}
		var left []string
		rows, _ := h.store.Pool.Query(ctx, `select request_id from placement_reservations`)
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			left = append(left, id)
		}
		if rows.Close(); !slices.Equal(left, []string{one[3]}) {
			t.Fatalf("left after the sweep: %v, want %s", left, one[3])
		}
	})
}
