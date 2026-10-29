package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
)

// TestConcurrentNames: requests submitted together for one name prefix get
// distinct names, skip those in DNS, and look their names up before the
// name counter is locked (S14.7).
func TestConcurrentNames(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		for _, fqdn := range []string{"web-dev-cc002.corp.example", "web-dev-cc005.corp.example"} {
			if _, err := h.acts.IPAM.CreateRecord(ctx, efficientip.Record{FQDN: fqdn, Type: "A", Value: "10.9.9.9"}); err != nil {
				t.Fatal(err)
			}
		}
		// As a SOLIDserver answers.
		h.ipam.Latency = 50 * time.Millisecond
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", "tnguyen", nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "cc", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		const n = 8
		var mu sync.Mutex
		var names []string
		var wg sync.WaitGroup
		start := time.Now()
		for range n {
			wg.Go(func() {
				code, b, _ := h.do("POST", "/api/v1/requests", "tnguyen", draft, "Idempotency-Key", uuid.NewString())
				var req api.Request
				if code != 202 || json.Unmarshal(b, &req) != nil {
					t.Errorf("submit = %d: %s", code, b)
					return
				}
				dep, err := h.store.GetDeployment(ctx, (*req.DeploymentIds)[0])
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				names = append(names, dep.Name)
				mu.Unlock()
			})
		}
		wg.Wait()
		took := time.Since(start)
		slices.Sort(names)
		want := []string{"web-dev-cc001", "web-dev-cc003", "web-dev-cc004", "web-dev-cc006", "web-dev-cc007", "web-dev-cc008",
			"web-dev-cc009", "web-dev-cc010"}
		if !slices.Equal(names, want) {
			t.Fatalf("names %v", names)
		}
		t.Logf("%d submitted together in %v", n, took)
	})
}
