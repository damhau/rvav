package e2e

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// runAll runs each workflow started from now on, as Temporal's worker
// would, until the test ends: Terraform waits for the requests it makes.
func (h *harness) runAll() {
	n := h.wf.startedCount()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			h.wf.mu.Lock()
			started := slices.Clone(h.wf.started)
			h.wf.mu.Unlock()
			for ; n < len(started); n++ {
				if err := h.execute(started[n], nil); err != nil {
					h.t.Error(err)
				}
			}
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	})
	h.t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})
}

// deploymentConfig is TestTerraformDeployment's, with the placement alone
// and a size: rvav fills in the other inputs from the form (ADR-0055).
const deploymentConfig = `
resource "rvav_deployment" "app" {
  project_id    = "prj_web"
  environment   = "dev"
  catalog_item  = "rhel9-server"
  version       = %s
  description   = %q
  justification = "Terraform, O1.2."
  inputs = jsonencode({
    size      = %q
    cluster   = "GVA-DEV-CL01"
    datastore = "vsan-gva-dev-cl01"
    network   = "dvpg-app-dev-vl310"
  })
}
`

// TestTerraformDeployment requests a deployment with the provider and
// waits for it (O1.2), imports it, updates its inputs and description,
// sees changes made outside Terraform, updates it to a new version, makes
// it again once deleted outside, and destroys it with a delete request.
func TestTerraformDeployment(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		h.runAll()
		const owner = "tnguyen"
		tf := newTerraform(t, h, owner)
		var item api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", "mkeller", nil, 200, &item)
		var published struct {
			Definition map[string]any `json:"definition"`
		}
		h.json("GET", "/api/v1/admin/catalog/items/"+item.Id+"/versions/"+item.PublishedVersion.Id, "mkeller", nil, 200, &published)
		var next api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/"+item.Id+"/versions", "mkeller", map[string]any{"definition": published.Definition},
			201, &next)
		v1 := tf.config(deploymentConfig, "null", "Web front", "S")
		v2 := tf.config(deploymentConfig, "null", "Web front, resized", "M")
		v3 := tf.config(deploymentConfig, "null", "Web front, resized", "L")
		v4 := tf.config(deploymentConfig, strconv.Quote(next.Number), "Web front, resized", "L")
		deployment := func(id string) api.DeploymentDetail {
			var d api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+id, owner, nil, 200, &d)
			return d
		}
		// asks makes a request outside Terraform and waits for it.
		asks := func(path string, body any) {
			var r api.Request
			h.json("POST", path, owner, body, 202, &r, "Idempotency-Key", uuid.NewString())
			h.waitStatus(r.Id, owner, api.RequestStatusSucceeded)
		}
		var first, second string
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps: []resource.TestStep{{
				Config: v1,
				Check: resource.ComposeAggregateTestCheckFunc(
					grab("rvav_deployment.app", "id", &first),
					resource.TestCheckResourceAttr("rvav_deployment.app", "status", "ready"),
					resource.TestCheckResourceAttr("rvav_deployment.app", "owner_id", "usr_tnguyen"),
					resource.TestCheckResourceAttrSet("rvav_deployment.app", "version"),
					resource.TestCheckResourceAttrSet("rvav_deployment.app", "address"),
					func(*terraform.State) error {
						d := deployment(first)
						if d.Inputs["size"] != "S" || d.Inputs["role"] != "app" || d.Inputs["post_config"] == nil || d.Description != "Web front" {
							return fmt.Errorf("deployment in rvav: %q, inputs %v", d.Description, d.Inputs)
						}
						return nil
					},
				),
			}, {
				ResourceName: "rvav_deployment.app", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"inputs", "justification"},
			}, {
				Config:           v2,
				ConfigPlanChecks: expect("rvav_deployment.app", plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPtr("rvav_deployment.app", "id", &first),
					func(*terraform.State) error {
						if d := deployment(first); d.Inputs["size"] != "M" || d.Description != "Web front, resized" {
							return fmt.Errorf("after the update: %q, inputs %v", d.Description, d.Inputs)
						}
						return nil
					},
				),
			}, {
				// Changed outside Terraform: the plan sets both back, and
				// rvav refuses to shrink the disks.
				PreConfig: func() {
					d := deployment(first)
					in := maps.Clone(d.Inputs)
					in["size"] = "L"
					asks("/api/v1/deployments/"+first+"/update", api.UpdateDraft{VersionId: d.Version.Id, Inputs: in})
					asks("/api/v1/deployments/"+first+"/actions", map[string]any{"action": "edit",
						"inputs": map[string]any{"name": d.Name, "description": "Changed in the web app"}})
				},
				Config:           v2,
				ConfigPlanChecks: expect("rvav_deployment.app", plancheck.ResourceActionUpdate),
				ExpectError:      regexp.MustCompile(`can't shrink a disk`),
			}, {
				Config:           v3,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: func(*terraform.State) error {
					if d := deployment(first); d.Inputs["size"] != "L" || d.Description != "Web front, resized" {
						return fmt.Errorf("after the drift: %q, inputs %v", d.Description, d.Inputs)
					}
					return nil
				},
			}, {
				PreConfig: func() {
					h.json("POST", "/api/v1/admin/catalog/items/"+item.Id+"/versions/"+next.Id+"/publish", "mkeller", map[string]any{}, 200, nil)
				},
				Config:           v4,
				ConfigPlanChecks: expect("rvav_deployment.app", plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rvav_deployment.app", "version_id", next.Id),
					func(*terraform.State) error {
						if d := deployment(first); d.Version.Id != next.Id || d.Inputs["size"] != "L" {
							return fmt.Errorf("after the version's update: version %s, inputs %v", d.Version.Number, d.Inputs)
						}
						return nil
					},
				),
			}, {
				// Deleted outside Terraform: Terraform requests it again.
				PreConfig: func() {
					asks("/api/v1/deployments/"+first+"/actions", map[string]any{"action": "delete", "inputs": map[string]any{}})
				},
				Config:           v4,
				ConfigPlanChecks: expect("rvav_deployment.app", plancheck.ResourceActionCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					grab("rvav_deployment.app", "id", &second),
					resource.TestCheckResourceAttr("rvav_deployment.app", "status", "ready"),
				),
			}},
			CheckDestroy: func(*terraform.State) error {
				for _, id := range []string{first, second} {
					if d := deployment(id); d.Status != api.DeploymentStatusDeleted {
						return fmt.Errorf("deployment %s is %s after destroy", d.Name, d.Status)
					}
				}
				return nil
			},
		})
	})
}
