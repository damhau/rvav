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

// TestNamingCounterStart: a naming policy's counter starts where it says and
// moves by its increment, the preview showing the same names, and requests
// submitted together still get distinct names (ADR-0038).
func TestNamingCounterStart(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		if _, err := h.acts.IPAM.CreateRecord(ctx, efficientip.Record{FQDN: "web-dev-cc120.corp.example", Type: "A", Value: "10.9.9.9"}); err != nil {
			t.Fatal(err)
		}
		h.json("POST", "/api/v1/admin/naming-policies", admin, api.NamingPolicyWrite{Name: "Bad step",
			Template: "{project}-{environment}-{role}{seq:03}", Increment: new(0)}, 422, nil)
		var pol api.NamingPolicy
		h.json("POST", "/api/v1/admin/naming-policies", admin, api.NamingPolicyWrite{Name: "Hundreds",
			Template: "{project}-{environment}-{role}{seq:03}", Start: new(100), Increment: new(10),
			Offers: &[]api.Offer{{ProjectId: new("prj_web")}}}, 201, &pol)
		if pol.Start != 100 || pol.Increment != 10 {
			t.Fatalf("policy = %+v", pol)
		}
		var preview api.NamingPreview
		h.json("POST", "/api/v1/admin/naming-preview", admin, api.NamingPreviewQuery{Template: pol.Template, ProjectId: "prj_web",
			Environment: "dev", Inputs: &map[string]any{"role": "cc"}, Start: new(100), Increment: new(10), Count: new(3)}, 200, &preview)
		// The policy's preview doesn't look in DNS: a request does.
		if !slices.Equal(preview.Names, []string{"web-dev-cc100", "web-dev-cc110", "web-dev-cc120"}) {
			t.Fatalf("preview = %+v", preview)
		}

		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", "tnguyen", nil, 200, &form)
		draft := api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "cc", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		const n = 5
		var mu sync.Mutex
		var names []string
		var wg sync.WaitGroup
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
		slices.Sort(names)
		if want := []string{"web-dev-cc100", "web-dev-cc110", "web-dev-cc130", "web-dev-cc140", "web-dev-cc150"}; !slices.Equal(names, want) {
			t.Fatalf("names %v, want %v", names, want)
		}

		// Raising the start makes the counter jump; it never goes back.
		h.json("PUT", "/api/v1/admin/naming-policies/"+pol.Id, admin, api.NamingPolicyWrite{Name: pol.Name, Template: pol.Template,
			Start: new(500), Increment: new(1), Offers: &pol.Offers}, 200, nil)
		h.json("POST", "/api/v1/admin/naming-preview", admin, api.NamingPreviewQuery{Template: pol.Template, ProjectId: "prj_web",
			Environment: "dev", Inputs: &map[string]any{"role": "cc"}, Start: new(500), Count: new(2)}, 200, &preview)
		if !slices.Equal(preview.Names, []string{"web-dev-cc500", "web-dev-cc501"}) {
			t.Fatalf("preview after raising the start = %+v", preview)
		}
		h.json("POST", "/api/v1/admin/naming-preview", admin, api.NamingPreviewQuery{Template: pol.Template, ProjectId: "prj_web",
			Environment: "dev", Inputs: &map[string]any{"role": "cc"}, Start: new(1), Increment: new(10), Count: new(1)}, 200, &preview)
		if !slices.Equal(preview.Names, []string{"web-dev-cc160"}) {
			t.Fatalf("a lower start goes on from the last value: %+v", preview)
		}
	})
}
