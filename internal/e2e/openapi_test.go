package e2e

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
)

// TestOpenAPI: the API serves the description it was built from (S14.10),
// to anyone signed in.
func TestOpenAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		var spec struct {
			OpenAPI string `json:"openapi"`
			Info    struct{ Title string }
			Paths   map[string]any `json:"paths"`
		}
		h.json("GET", "/api/v1/openapi.json", "tnguyen", nil, 200, &spec)
		if spec.OpenAPI != "3.1.0" || spec.Info.Title != "rvav API" || spec.Paths["/requests"] == nil || spec.Paths["/openapi.json"] == nil {
			t.Fatalf("spec %s %q with %d paths", spec.OpenAPI, spec.Info.Title, len(spec.Paths))
		}
	})
}
