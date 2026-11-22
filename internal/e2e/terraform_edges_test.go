package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
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

// requestBuild requests rhel9-server in prj_web as owner, and leaves its
// workflow to the test: the deployment is being built until it runs it.
func requestBuild(h *harness, owner string) (api.Request, string) {
	var item api.CatalogItem
	h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", owner, nil, 200, &item)
	var form api.RequestForm
	h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id=prj_web", owner, nil, 200, &form)
	var r api.Request
	h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId,
		ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"size": "S", "cluster": "GVA-DEV-CL01",
			"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}},
		202, &r, "Idempotency-Key", uuid.NewString())
	return r, (*r.DeploymentIds)[0]
}

// TestTerraformDestroyWhileBuilding destroys a deployment that its request
// still builds, as after an apply stopped while it waited (O1.7): the
// destroy waits for the build, then deletes it.
func TestTerraformDestroyWhileBuilding(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const owner = "tnguyen"
		tf := newTerraform(t, h, owner)
		r, dep := requestBuild(h, owner)
		built := make(chan error, 1)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps: []resource.TestStep{{
				Config: tf.config(deploymentConfig, "null", "Web front", "S"), ResourceName: "rvav_deployment.app",
				ImportState: true, ImportStateId: dep, ImportStatePersist: true,
				ImportStateCheck: func(s []*terraform.InstanceState) error {
					if got := s[0].Attributes["status"]; got != "provisioning" {
						t.Errorf("imported while building: status %s", got)
					}
					return nil
				},
			}, {
				PreConfig: func() {
					h.runAll()
					time.AfterFunc(300*time.Millisecond, func() { built <- h.execute(r.Id, nil) })
				},
				Config: tf.provider,
			}},
		})
		if err := <-built; err != nil {
			t.Fatal(err)
		}
		var d api.Deployment
		h.json("GET", "/api/v1/deployments/"+dep, owner, nil, 200, &d)
		if d.Status != api.DeploymentStatusDeleted {
			t.Errorf("after the destroy, the deployment is %s", d.Status)
		}
	})
}

// TestTerraformImportByItemID imports a deployment whose configuration
// names its item by ID rather than by slug: the plan replaces nothing.
func TestTerraformImportByItemID(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const owner = "tnguyen"
		tf := newTerraform(t, h, owner)
		r, dep := requestBuild(h, owner)
		h.runWorkflow(r.Id, nil)
		h.runAll()
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", owner, nil, 200, &item)
		byID := tf.config(`
resource "rvav_deployment" "app" {
  project_id   = "prj_web"
  environment  = "dev"
  catalog_item = %q
}
`, item.Id)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps: []resource.TestStep{{
				Config: byID, ResourceName: "rvav_deployment.app", ImportState: true, ImportStateId: dep,
				ImportStatePersist: true,
			}, {
				Config:           byID,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			}, {
				// And back to the slug, once the state has the ID.
				Config: strings.Replace(byID, item.Id, "rhel9-server", 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("rvav_deployment.app", plancheck.ResourceActionNoop)}},
			}, {
				// Another item still replaces it: the check stops the apply.
				Config:      strings.Replace(byID, item.Id, "win2022-server", 1),
				ExpectError: regexp.MustCompile(`planned a replacement`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					stopOn{"rvav_deployment.app", plancheck.ResourceActionDestroyBeforeCreate}}},
			}},
		})
	})
}

// stopOn fails the step, before its apply, when the plan has action for
// the resource as it should: an error that the step expects.
type stopOn struct {
	address string
	action  plancheck.ResourceActionType
}

func (s stopOn) CheckPlan(ctx context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	plancheck.ExpectResourceAction(s.address, s.action).CheckPlan(ctx, req, resp)
	if resp.Error == nil {
		resp.Error = errors.New("planned a replacement, as expected")
	}
}

// withSecret adds a secret field, join_password, to a definition's form.
func withSecret(def map[string]any) {
	form, _ := def["form_schema"].(map[string]any)
	props, _ := form["properties"].(map[string]any)
	props["join_password"] = map[string]any{"type": "string"}
	ui, _ := def["ui_schema"].(map[string]any)
	fields, _ := ui["fields"].(map[string]any)
	fields["join_password"] = map[string]any{"widget": "secret", "label": "Join password"}
	layout, _ := ui["layout"].(map[string]any)
	sections, _ := layout["sections"].([]any)
	first, _ := sections[0].(map[string]any)
	first["fields"] = append(first["fields"].([]any), "join_password")
}

// TestTerraformSecretInputs requests a deployment with a secret input in
// secret_inputs (O1.7): rvav gets it, and neither the plan nor the state
// holds it. A new value alone makes no request; an update sends it again.
func TestTerraformSecretInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		h.runAll()
		path := writeDefinition(t, h, "secret.json", withSecret)
		var def map[string]any
		b, _ := os.ReadFile(path)
		_ = json.Unmarshal(b, &def)
		var made api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", "mkeller", map[string]any{"slug": "joined-server", "name": "Joined server",
			"summary": "A server joined with a password.", "category": "compute", "type": "vm_blueprint", "definition": def}, 201, &made)
		h.json("POST", "/api/v1/admin/catalog/items/"+made.Id+"/versions/"+made.DraftVersion.Id+"/publish", "mkeller",
			map[string]any{"changelog": "First."}, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/"+made.Id+"/entitlements", "mkeller",
			api.CatalogEntitlements{ProjectIds: []string{"prj_web"}}, 200, nil)
		tf := newTerraform(t, h, "tnguyen")
		const cfg = `
resource "rvav_deployment" "app" {
  project_id   = "prj_web"
  environment  = "dev"
  catalog_item = "joined-server"
  inputs = jsonencode({
    size      = %q
    cluster   = "GVA-DEV-CL01"
    datastore = "vsan-gva-dev-cl01"
    network   = "dvpg-app-dev-vl310"
  })
  secret_inputs = jsonencode({ join_password = %q })
}
`
		var dep string
		sent := func(n int) resource.TestCheckFunc {
			return func(*terraform.State) error {
				var d api.DeploymentDetail
				h.json("GET", "/api/v1/deployments/"+dep, "tnguyen", nil, 200, &d)
				if d.Inputs["join_password"] != "••••" {
					t.Errorf("rvav's deployment has join_password %v", d.Inputs["join_password"])
				}
				var reqs api.RequestPage
				h.json("GET", "/api/v1/requests?project_id=prj_web", "tnguyen", nil, 200, &reqs)
				made := 0
				for _, r := range reqs.Items {
					if slicesContains(r.DeploymentIds, dep) && r.Inputs != nil && (*r.Inputs)["join_password"] == "••••" {
						made++
					}
				}
				if made != n {
					t.Errorf("%d requests with join_password, want %d", made, n)
				}
				return nil
			}
		}
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps: []resource.TestStep{{
				Config: tf.config(cfg, "S", "s3cret-first"),
				Check: resource.ComposeAggregateTestCheckFunc(
					grab("rvav_deployment.app", "id", &dep),
					resource.TestCheckNoResourceAttr("rvav_deployment.app", "secret_inputs"),
					noneHolds("s3cret-first"),
					sent(1),
				),
			}, {
				Config:           tf.config(cfg, "S", "s3cret-second"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			}, {
				Config:           tf.config(cfg, "M", "s3cret-second"),
				ConfigPlanChecks: expect("rvav_deployment.app", plancheck.ResourceActionUpdate),
				Check:            resource.ComposeAggregateTestCheckFunc(noneHolds("s3cret-second"), sent(2)),
			}},
		})
	})
}

// noneHolds checks that no attribute in the state holds secret.
func noneHolds(secret string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for name, r := range s.RootModule().Resources {
			for k, v := range r.Primary.Attributes {
				if strings.Contains(v, secret) {
					return &stateError{name + "." + k}
				}
			}
		}
		return nil
	}
}

type stateError struct{ at string }

func (e *stateError) Error() string { return "the state holds the secret in " + e.at }

func slicesContains(ids *[]string, id string) bool {
	if ids == nil {
		return false
	}
	for _, x := range *ids {
		if x == id {
			return true
		}
	}
	return false
}

// TestTerraformItemImportAndDraft imports an item made in the web app,
// whose definition file is formatted otherwise than rvav's (O1.7): the plan
// is empty. With publish = false, an edit of the draft in the web app shows
// in the plan, and leaving icon out sets the type's icon again.
func TestTerraformItemImportAndDraft(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		tf := newTerraform(t, h, "mkeller")
		path := writeDefinition(t, h, "def.json", roleLabel("Imported role"))
		edited := writeDefinition(t, h, "edited.json", roleLabel("Edited in the web app"))
		var def map[string]any
		b, _ := os.ReadFile(path)
		_ = json.Unmarshal(b, &def)
		var made api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", "mkeller", map[string]any{"slug": "imported", "name": "Imported",
			"summary": "Made in the web app.", "category": "compute", "type": "vm_blueprint", "icon": "database",
			"definition": def}, 201, &made)
		h.json("POST", "/api/v1/admin/catalog/items/"+made.Id+"/versions/"+made.DraftVersion.Id+"/publish", "mkeller",
			map[string]any{"changelog": "From the web app."}, 200, nil)
		const item = `
resource "rvav_catalog_item" "app" {
  slug       = "imported"
  name       = "Imported"
  summary    = "Made in the web app."
  category   = "compute"
  type       = "vm_blueprint"
  %s
  definition = file(%q)
}
`
		draft := writeDefinition(t, h, "draft.json", roleLabel("Draft role"))
		var versionID string
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps: []resource.TestStep{{
				Config: tf.config(item, `icon = "database"`, path), ResourceName: "rvav_catalog_item.app", ImportState: true,
				ImportStateId: made.Id, ImportStatePersist: true,
			}, {
				Config:           tf.config(item, `icon = "database"`, path),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			}, {
				// Without icon, the type's again.
				Config:           tf.config(item, "", path),
				ConfigPlanChecks: expect("rvav_catalog_item.app", plancheck.ResourceActionUpdate),
				Check:            resource.TestCheckResourceAttr("rvav_catalog_item.app", "icon", "server"),
			}, {
				// A change of definition with publish = false makes a draft.
				Config: tf.config(item, `publish = false`, draft),
				Check:  grab("rvav_catalog_item.app", "version_id", &versionID),
			}, {
				PreConfig: func() {
					var d map[string]any
					b, _ := os.ReadFile(edited)
					_ = json.Unmarshal(b, &d)
					h.json("PUT", "/api/v1/admin/catalog/items/"+made.Id+"/versions/"+versionID, "mkeller",
						map[string]any{"definition": d}, 200, nil)
				},
				Config:           tf.config(item, `publish = false`, draft),
				ConfigPlanChecks: expect("rvav_catalog_item.app", plancheck.ResourceActionUpdate),
				Check: func(*terraform.State) error {
					var v struct {
						Definition struct {
							UiSchema struct {
								Fields map[string]struct{ Label string } `json:"fields"`
							} `json:"ui_schema"`
						} `json:"definition"`
					}
					h.json("GET", "/api/v1/admin/catalog/items/"+made.Id+"/versions/"+versionID, "mkeller", nil, 200, &v)
					if got := v.Definition.UiSchema.Fields["role"].Label; got != "Draft role" {
						t.Errorf("the draft's role label after the apply: %q", got)
					}
					return nil
				},
			}},
		})
	})
}

// TestTerraformWithdrawnItemAgain destroys an item someone requested, which
// rvav withdraws, and applies it again (O1.7): the error says to import it,
// and once imported, the apply publishes it again.
func TestTerraformWithdrawnItemAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		tf := newTerraform(t, h, "mkeller")
		path := writeDefinition(t, h, "def.json", nil)
		cfg := tf.config(`
resource "rvav_catalog_item" "app" {
  slug        = "again"
  name        = "Again"
  summary     = "Requested, then destroyed."
  category    = "compute"
  type        = "vm_blueprint"
  definition  = file(%q)
  project_ids = ["prj_web"]
}
`, path)
		var itemID string
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps: []resource.TestStep{{
				Config: cfg,
				Check:  grab("rvav_catalog_item.app", "id", &itemID),
			}, {
				PreConfig: func() {
					var form api.RequestForm
					h.json("GET", "/api/v1/catalog/items/again/form?project_id=prj_web", "tnguyen", nil, 200, &form)
					h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: itemID, VersionId: form.VersionId,
						ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"size": "S", "cluster": "GVA-DEV-CL01",
							"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}},
						202, nil, "Idempotency-Key", uuid.NewString())
				},
				Config: tf.provider,
			}, {
				Config:      cfg,
				ExpectError: regexp.MustCompile(`(?s)taken.*terraform\s+import\s+rvav_catalog_item\.NAME\s+itm_`),
			}, {
				Config: cfg, ResourceName: "rvav_catalog_item.app", ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) {
					return itemID, nil
				}, ImportStatePersist: true,
			}, {
				Config: cfg,
				// Requestable again.
				Check: func(*terraform.State) error {
					h.json("GET", "/api/v1/catalog/items/again?project_id=prj_web", "tnguyen", nil, 200, nil)
					return nil
				},
			}},
		})
	})
}

// TestTerraformUnencrypted gives the provider an http:// address of another
// machine (O1.7): it refuses to send the token there, as rvavctl does,
// unless insecure_http. The harness's own loopback address needs nothing.
func TestTerraformUnencrypted(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		tf := newTerraform(t, h, "mkeller")
		const project = `
data "rvav_project" "web" {
  id = "prj_web"
}
`
		elsewhere := strings.Replace(tf.provider, h.srv.URL, "http://rvav.corp.example", 1)
		allowed := strings.Replace(tf.provider, "}\n", "  insecure_http = true\n}\n", 1)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps: []resource.TestStep{{
				Config:      elsewhere + project,
				ExpectError: regexp.MustCompile(`(?s)http://rvav\.corp\.example\s+would\s+get\s+the\s+token\s+unencrypted.*insecure_http\s+=\s+true`),
			}, {
				Config: tf.provider + project,
				Check:  resource.TestCheckResourceAttr("data.rvav_project.web", "id", "prj_web"),
			}, {
				Config: allowed + project,
				Check:  resource.TestCheckResourceAttr("data.rvav_project.web", "id", "prj_web"),
			}},
		})
	})
}
