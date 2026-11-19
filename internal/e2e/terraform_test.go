package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	install "github.com/hashicorp/hc-install"
	"github.com/hashicorp/hc-install/checkpoint"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/src"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/tfprovider"
)

var terraformOnce sync.Once

// needTerraform makes sure the tests have a Terraform CLI: the one on PATH,
// else the latest release, installed once for every test (hc-install),
// rather than once per test case as terraform-plugin-testing would.
func needTerraform(t *testing.T) {
	t.Helper()
	terraformOnce.Do(func() {
		if os.Getenv("TF_ACC_TERRAFORM_PATH") != "" {
			return
		}
		if _, err := exec.LookPath("terraform"); err == nil {
			return
		}
		dir, err := os.MkdirTemp("", "rvav-terraform-")
		if err != nil {
			t.Fatal(err)
		}
		path, err := install.NewInstaller().Ensure(context.Background(),
			[]src.Source{&checkpoint.LatestVersion{Product: product.Terraform, InstallDir: dir}})
		if err != nil {
			t.Fatalf("installing Terraform: %v", err)
		}
		t.Logf("Terraform installed in %s", path)
		os.Setenv("TF_ACC_TERRAFORM_PATH", path)
	})
}

// terraformAs is a test case's provider: rvav's, in this process, calling
// the harness with an API token of user.
type terraformAs struct {
	factories map[string]func() (tfprotov6.ProviderServer, error)
	provider  string
}

func newTerraform(t *testing.T, h *harness, user string) terraformAs {
	needTerraform(t)
	var made api.ApiTokenCreated
	h.json("POST", "/api/v1/tokens", user, api.ApiTokenCreate{Name: "terraform"}, 201, &made)
	return terraformAs{
		factories: map[string]func() (tfprotov6.ProviderServer, error){
			"rvav": providerserver.NewProtocol6WithError(tfprovider.New("test")()),
		},
		provider: fmt.Sprintf("provider \"rvav\" {\n  url   = %q\n  token = %q\n}\n", h.srv.URL, made.Token),
	}
}

// config is a configuration with the provider's block.
func (tf terraformAs) config(hcl string, args ...any) string {
	return tf.provider + fmt.Sprintf(hcl, args...)
}

// grab keeps an attribute's value for later steps.
func grab(name, key string, into *string) resource.TestCheckFunc {
	return resource.TestCheckResourceAttrWith(name, key, func(v string) error {
		*into = v
		return nil
	})
}

func expect(name string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, action)}}
}

// writeDefinition writes rhel9-server's published definition, changed by
// change, as a JSON file for a configuration's file().
func writeDefinition(t *testing.T, h *harness, name string, change func(def map[string]any)) string {
	t.Helper()
	var item api.AdminCatalogItem
	h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", "mkeller", nil, 200, &item)
	var v struct {
		Definition map[string]any `json:"definition"`
	}
	h.json("GET", "/api/v1/admin/catalog/items/"+item.Id+"/versions/"+item.PublishedVersion.Id, "mkeller", nil, 200, &v)
	if change != nil {
		change(v.Definition)
	}
	b, _ := json.MarshalIndent(v.Definition, "", "  ")
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// roleLabel changes the label of the form's role input.
func roleLabel(label string) func(map[string]any) {
	return func(def map[string]any) {
		ui, _ := def["ui_schema"].(map[string]any)
		fields, _ := ui["fields"].(map[string]any)
		role, _ := fields["role"].(map[string]any)
		role["label"] = label
	}
}

// TestTerraformProjectsAndItems manages a project and a catalog item with
// the provider (O1.2): create, read through data sources, import, change,
// drift (archived outside Terraform, a version published in the web app)
// and destroy.
func TestTerraformProjectsAndItems(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		tf := newTerraform(t, h, "mkeller")
		first := writeDefinition(t, h, "first.json", roleLabel("Server role"))
		second := writeDefinition(t, h, "second.json", roleLabel("What the server does"))
		const resources = `
resource "rvav_project" "web" {
  code         = "tfweb"
  name         = %q
  description  = %s
  environments = %s
  properties   = { owner = "web-team" }
  %s
}

resource "rvav_catalog_item" "app" {
  slug        = "tf-app-server"
  name        = "App server"
  summary     = "A RHEL 9 server for the web team."
  category    = "compute"
  type        = "vm_blueprint"
  tags        = ["linux", "web"]
  definition  = file(%q)
  changelog   = "From Terraform."
  project_ids = [rvav_project.web.id]
}

data "rvav_project" "web" {
  code = rvav_project.web.code
}

data "rvav_catalog_item" "app" {
  slug       = rvav_catalog_item.app.slug
  project_id = data.rvav_project.web.id
}
`
		v1 := tf.config(resources, "Web front", "null", `["dev", "tst"]`, "", first)
		v2 := tf.config(resources, "Web platform", `"The web team's servers."`, `["dev", "tst", "prd"]`,
			`deployment_sharing = "owner_only"`, second)
		var projectID, itemID, versionID string
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps: []resource.TestStep{
				{
					Config: v1,
					Check: resource.ComposeAggregateTestCheckFunc(
						grab("rvav_project.web", "id", &projectID),
						grab("rvav_catalog_item.app", "id", &itemID),
						resource.TestCheckResourceAttr("rvav_project.web", "deployment_sharing", "all_members"),
						resource.TestCheckResourceAttr("rvav_project.web", "environments.#", "2"),
						resource.TestCheckResourceAttr("rvav_catalog_item.app", "version", "1.0"),
						resource.TestCheckResourceAttr("rvav_catalog_item.app", "icon", "server"),
						resource.TestCheckResourceAttr("rvav_catalog_item.app", "publish", "true"),
						resource.TestCheckResourceAttr("rvav_catalog_item.app", "all_projects", "false"),
						resource.TestCheckResourceAttrPair("data.rvav_project.web", "id", "rvav_project.web", "id"),
						resource.TestCheckResourceAttr("data.rvav_project.web", "name", "Web front"),
						resource.TestCheckResourceAttrPair("data.rvav_catalog_item.app", "version_id", "rvav_catalog_item.app", "version_id"),
						resource.TestCheckResourceAttr("data.rvav_catalog_item.app", "type", "vm_blueprint"),
						func(*terraform.State) error {
							var item api.AdminCatalogItem
							h.json("GET", "/api/v1/admin/catalog/items/tf-app-server", "mkeller", nil, 200, &item)
							if item.PublishedVersion == nil || item.PublishedVersion.Changelog != "From Terraform." ||
								len(item.Entitlements.ProjectIds) != 1 || item.Entitlements.ProjectIds[0] != projectID {
								return fmt.Errorf("item in rvav: %+v", item)
							}
							return nil
						},
					),
				},
				{ResourceName: "rvav_project.web", ImportState: true, ImportStateVerify: true},
				{ResourceName: "rvav_catalog_item.app", ImportState: true, ImportStateVerify: true,
					ImportStateVerifyIgnore: []string{"changelog", "definition"}},
				{
					Config:           v2,
					ConfigPlanChecks: expect("rvav_catalog_item.app", plancheck.ResourceActionUpdate),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttrPtr("rvav_project.web", "id", &projectID),
						resource.TestCheckResourceAttr("rvav_project.web", "deployment_sharing", "owner_only"),
						resource.TestCheckResourceAttr("rvav_project.web", "description", "The web team's servers."),
						resource.TestCheckResourceAttr("rvav_catalog_item.app", "version", "1.1"),
						grab("rvav_catalog_item.app", "version_id", &versionID),
						func(*terraform.State) error {
							var form api.RequestForm
							h.json("GET", "/api/v1/catalog/items/tf-app-server/form?project_id="+projectID, "mkeller", nil, 200, &form)
							if form.VersionId != versionID || form.UiSchema.Fields["role"].Label != "What the server does" {
								return fmt.Errorf("request form: %s %+v", form.VersionId, form.UiSchema.Fields["role"])
							}
							return nil
						},
					),
				},
				{
					// Changed outside Terraform: the project archived, another
					// version of the item published.
					PreConfig: func() {
						h.json("PATCH", "/api/v1/admin/projects/"+projectID, "mkeller", map[string]any{"archived": true}, 200, nil)
						var draft api.CatalogItemVersionDetail
						def := map[string]any{}
						b, _ := os.ReadFile(first)
						_ = json.Unmarshal(b, &def)
						h.json("POST", "/api/v1/admin/catalog/items/"+itemID+"/versions", "mkeller",
							map[string]any{"definition": def}, 201, &draft)
						h.json("POST", "/api/v1/admin/catalog/items/"+itemID+"/versions/"+draft.Id+"/publish", "mkeller",
							map[string]any{}, 200, nil)
					},
					Config: v2,
					ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("rvav_project.web", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("rvav_catalog_item.app", plancheck.ResourceActionUpdate),
					}},
					Check: resource.ComposeAggregateTestCheckFunc(
						// The archived project comes back, with its ID.
						resource.TestCheckResourceAttrPtr("rvav_project.web", "id", &projectID),
						resource.TestCheckResourceAttr("rvav_catalog_item.app", "version", "1.3"),
					),
				},
			},
			CheckDestroy: func(*terraform.State) error {
				if code, b, _ := h.do("GET", "/api/v1/admin/catalog/items/"+itemID, "mkeller", nil); code != 404 {
					return fmt.Errorf("item after destroy: %d %s", code, b)
				}
				var p api.AdminProject
				h.json("GET", "/api/v1/admin/projects/"+projectID, "mkeller", nil, 200, &p)
				if !p.Archived {
					return fmt.Errorf("project after destroy isn't archived")
				}
				return nil
			},
		})
		if !strings.HasPrefix(projectID, "prj_") {
			t.Fatalf("project ID %q", projectID)
		}
	})
}
