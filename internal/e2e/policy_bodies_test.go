package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
)

// TestPolicyBodies: C2's writes replace what they write, so a field they
// don't take, such as a misspelt one, is refused rather than read as
// leaving the meant one empty; a limit policy's name is at most 120
// characters; and publishing reads its body without a Content-Length
// (#160).
func TestPolicyBodies(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, catalogAdmin = "mkeller", "lmartin"
		var limits api.DeploymentLimitPolicyList
		h.json("GET", "/api/v1/admin/deployment-limit-policies", admin, nil, 200, &limits)
		before := len(limits.Items)
		var created api.DeploymentLimitPolicy
		h.json("POST", "/api/v1/admin/deployment-limit-policies", admin,
			api.DeploymentLimitPolicyWrite{Name: "Five each", DeploymentsPerUser: new(5)}, 201, &created)

		for _, w := range []struct{ method, path, body string }{
			{"POST", "/api/v1/admin/deployment-limit-policies", `{"name": "Typo", "deployments_per_usr": 5}`},
			{"PUT", "/api/v1/admin/deployment-limit-policies/" + created.Id, `{"name": "Five each", "deployments_per_usr": 5}`},
			{"POST", "/api/v1/admin/lease-rules", `{"name": "Typo", "condition": "true", "expiry_action": "delete", "max_day": 3}`},
			{"PUT", "/api/v1/admin/quotas", `{"items": [{"resource": "vcpu", "per_user": false, "limit": 5, "enforcement": "hard", "limt": 4}]}`},
			{"PUT", "/api/v1/admin/projects/prj_web/user-quotas", `{"items": [{"resource": "vcpu", "limit": 5, "enforcement": "hard"}], "item": []}`},
		} {
			var problem api.Problem
			h.json(w.method, w.path, admin, rawYAML(w.body), 400, &problem)
			if problem.Title != "The request body has a field this operation doesn't take" || !strings.Contains(deref(problem.Detail), "unknown field") {
				t.Fatalf("%s %s with a misspelt field: %+v", w.method, w.path, problem)
			}
		}
		var got api.DeploymentLimitPolicy
		h.json("GET", "/api/v1/admin/deployment-limit-policies", admin, nil, 200, &limits)
		for _, p := range limits.Items {
			if p.Id == created.Id {
				got = p
			}
		}
		if len(limits.Items) != before+1 || got.DeploymentsPerUser == nil || *got.DeploymentsPerUser != 5 {
			t.Fatalf("after the refused writes: %d policies, %+v", len(limits.Items), got)
		}

		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/deployment-limit-policies", admin,
			api.DeploymentLimitPolicyWrite{Name: strings.Repeat("é", 121), DeploymentsPerUser: new(5)}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/name" || deref(problem.Errors[0].Code) != "too_long" {
			t.Fatalf("a name of 121 characters: %+v", problem.Errors)
		}
		h.json("PUT", "/api/v1/admin/deployment-limit-policies/"+created.Id, admin,
			api.DeploymentLimitPolicyWrite{Name: strings.Repeat("é", 120), DeploymentsPerUser: new(5)}, 200, nil)

		// A chunked body has no Content-Length: keep_published still counts.
		var item api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", catalogAdmin, nil, 200, &item)
		v1 := item.PublishedVersion.Id
		var draft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions", catalogAdmin, api.CatalogItemVersionCreate{Changelog: new("Newer.")}, 201, &draft)
		req, _ := http.NewRequestWithContext(ctx, "POST", h.srv.URL+"/api/v1/admin/catalog/items/rhel9-server/versions/"+draft.Id+"/publish",
			io.MultiReader(strings.NewReader(`{"keep_published": true}`)))
		req.Header.Set(auth.DevUserHeader, catalogAdmin)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 || req.ContentLength > 0 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("publish with a chunked body = %d (length %d) %s", resp.StatusCode, req.ContentLength, b)
		}
		if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
			t.Fatal(err)
		}
		if item.PublishedVersion.Id != draft.Id || stateOf(item, v1) != "published" {
			t.Fatalf("published beside 1 with a chunked body: default %s, state of 1 %s", item.PublishedVersion.Id, stateOf(item, v1))
		}
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions/"+v1+"/deprecate", catalogAdmin, nil, 200, &item)
		if stateOf(item, v1) != "deprecated" {
			t.Fatalf("a deprecation without a body: state of 1 %s", stateOf(item, v1))
		}
	})
}
