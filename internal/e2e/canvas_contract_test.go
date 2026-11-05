package e2e

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
)

// TestCanvasContract: the routes D1 added answer only what the spec says
// they do, as TestAuthContract checks sign-in's, so the generated client's
// types hold: each status sent is one the operation lists, and each listed
// is one it sends.
func TestCanvasContract(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	listed := func(method, path string) []int {
		t.Helper()
		item := spec.Paths.Find(path)
		if item == nil || item.GetOperation(method) == nil {
			t.Fatalf("no %s %s in the spec", method, path)
		}
		var out []int
		for code := range item.GetOperation(method).Responses.Map() {
			n, _ := strconv.Atoi(code)
			out = append(out, n)
		}
		slices.Sort(out)
		return out
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		sent := map[string][]int{}
		// No user: a token that isn't one (without it, dev mode signs the
		// default user in).
		const nobody = ""
		send := func(op, path, user string, want int) {
			t.Helper()
			var headers []string
			if user == nobody {
				headers = []string{"Authorization", "Bearer " + auth.TokenPrefix + "notatoken"}
			}
			code, out, _ := h.do("GET", "/api/v1"+path, user, nil, headers...)
			if code != want {
				t.Fatalf("GET %s as %q = %d, want %d: %s", path, user, code, want, out)
			}
			if !slices.Contains(listed("GET", op), code) {
				t.Errorf("GET %s sent %d, which the spec doesn't list (%v)", path, code, listed("GET", op))
			}
			sent[op] = append(sent[op], code)
		}

		// What a composite's steps can run: catalog admins and administrators.
		const steps = "/admin/catalog/composite-steps"
		send(steps, steps, nobody, http.StatusUnauthorized)
		send(steps, steps, "tnguyen", http.StatusForbidden)
		send(steps, steps, "lmartin", http.StatusOK)
		send(steps, steps, "mkeller", http.StatusOK)

		// A request's topology: whoever may read the request.
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/postgres-cluster/form?project_id=prj_data", "sbrunner", nil, 200, &form)
		var req api.Request
		h.json("POST", "/api/v1/requests", "sbrunner", api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId,
			ProjectId: "prj_data", Environment: "dev", Inputs: map[string]any{"name": "ledger", "pg_version": "16.6", "size": "S",
				"data_gb": 50, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-db-dev-vl320"}},
			202, &req, "Idempotency-Key", uuid.NewString())
		const topology = "/requests/{requestId}/topology"
		send(topology, "/requests/"+req.Id+"/topology", nobody, http.StatusUnauthorized)
		send(topology, "/requests/"+req.Id+"/topology", "tnguyen", http.StatusNotFound)
		send(topology, "/requests/req_doesnotexist/topology", "sbrunner", http.StatusNotFound)
		send(topology, "/requests/"+req.Id+"/topology", "sbrunner", http.StatusOK)
		send(topology, "/requests/"+req.Number+"/topology", "mkeller", http.StatusOK)

		for op := range sent {
			got := slices.Compact(slices.Sorted(slices.Values(sent[op])))
			if want := listed("GET", op); !slices.Equal(got, want) {
				t.Errorf("GET %s sends %v; the spec lists %v", op, got, want)
			}
		}
	})
}
