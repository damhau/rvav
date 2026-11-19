package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// governance is the configuration of TestTerraformGovernance, %s being
// what changes between its steps.
const governance = `
data "rvav_user" "marc" {
  email = "marc.keller@corp.example"
}

data "rvav_user" "alain" {
  email = "alain.favre@corp.example"
}

resource "rvav_approval_policy" "prd" {
  name      = "Production web"
  condition = "environment == \"prd\""
  project_id = "prj_web"
  levels = [
    { name = "Leads", approvers = [data.rvav_user.marc.id], mode = "all" },
    %s
  ]
  priority = %d
}

resource "rvav_day2_policy" "delete" {
  name    = "Deleting needs the manager"
  actions = ["delete"]
  effect  = "approval"
  levels  = [{ name = "Manager", manager = true }]
}

resource "rvav_naming_policy" "web" {
  name     = "Web names"
  template = "{project}-{environment}-{role}{seq:03}"
  start    = %d
  offers   = [{ project_id = "prj_web", environment = "dev" }]
}

resource "rvav_lease_policy" "web_dev" {
  environment    = "dev"
  project_id     = "prj_web"
  default_days   = 7
  max_days       = 30
  max_extensions = 2
  grace_days     = 3
  expiry_action  = "delete"
}

resource "rvav_lease_policy" "tst" {
  environment    = "tst"
  default_days   = 14
  max_extensions = 1
  grace_days     = 2
  expiry_action  = "power_off_then_delete"
}

resource "rvav_lease_rule" "blueprints" {
  name           = "Blueprints"
  condition      = "item.type == \"vm_blueprint\""
  max_extensions = 1
  grace_days     = 1
  expiry_action  = "notify_only"
}

resource "rvav_deployment_limit_policy" "web" {
  name                 = "Web limits"
  project_id           = "prj_web"
  deployments_per_user = %d
}

resource "rvav_project_quotas" "web" {
  project_id = "prj_web"
  # In rvav's order, the project's own first, so that an import plans no
  # change of order.
  limits = [
    { resource = "vm_count", limit = 20, enforcement = "soft" },
    { environment = "prd", resource = "vcpu", limit = %d },
  ]
  member_limits = [{ resource = "vcpu", limit = 16 }]
}

resource "rvav_global_quotas" "all" {
  limits = [{ resource = "vm_count", per_user = true, limit = 10 }]
}

resource "rvav_role" "ops" {
  key         = "tf-operator"
  name        = "Operator"
  permissions = %s
}

resource "rvav_role_assignment" "catalog" {
  user_id = data.rvav_user.alain.id
  role    = "catalog-admin"
}

resource "rvav_project_member" "alain" {
  project_id = "prj_web"
  user_id    = data.rvav_user.alain.id
  roles      = %s
}

resource "rvav_group_mapping" "ops" {
  oidc_group = "tf-operators"
  project_id = "prj_web"
  role       = rvav_role.ops.key
}

resource "rvav_secret" "hook" {
  name  = "tf-hook"
  kind  = "token"
  value = "hook-secret"
}

resource "rvav_subscription" "cmdb" {
  name      = "CMDB"
  topic     = "provisioning.post"
  condition = "environment == \"prd\""
  enabled   = %t
  target    = { webhook = { url = "http://hooks.corp.example/rvav", secret = rvav_secret.hook.name } }
}
`

// TestTerraformGovernance manages every kind of policy, quotas, roles and
// their bindings, a secret and a subscription with the provider (O1.2):
// create, import, change in place, drift (deleted outside Terraform) and
// destroy.
func TestTerraformGovernance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		h.allowWebhooks("hooks.corp.example")
		tf := newTerraform(t, h, "mkeller")
		security := `{ name = "Security", group = "rvav-security", timeout = "48h", on_timeout = "escalate",
      escalate_to = { approvers = [data.rvav_user.marc.id] } },`
		v1 := tf.config(governance, security, 100, 1, 5, 64, `["project.see", "day2.power:power_on"]`, `["viewer"]`, true)
		v2 := tf.config(governance, "", 50, 100, 3, 96, `["project.see"]`, `["member", rvav_role.ops.key]`, false)

		var policyID, subscriptionID string
		steps := []resource.TestStep{{
			Config: v1,
			Check: resource.ComposeAggregateTestCheckFunc(
				grab("rvav_approval_policy.prd", "id", &policyID),
				grab("rvav_subscription.cmdb", "id", &subscriptionID),
				resource.TestCheckResourceAttr("data.rvav_user.marc", "id", "usr_mkeller"),
				resource.TestCheckResourceAttr("rvav_approval_policy.prd", "enabled", "true"),
				resource.TestCheckResourceAttr("rvav_approval_policy.prd", "levels.1.escalate_to.approvers.0", "usr_mkeller"),
				resource.TestCheckResourceAttr("rvav_day2_policy.delete", "condition", "true"),
				resource.TestCheckResourceAttr("rvav_day2_policy.delete", "enforcement", "hard"),
				resource.TestCheckResourceAttr("rvav_naming_policy.web", "counter", "prefix"),
				resource.TestCheckResourceAttr("rvav_lease_policy.web_dev", "id", "dev/prj_web"),
				resource.TestCheckResourceAttr("rvav_lease_policy.tst", "id", "tst"),
				resource.TestCheckResourceAttr("rvav_project_quotas.web", "limits.0.environment", ""),
				resource.TestCheckResourceAttr("rvav_project_member.alain", "id", "prj_web/usr_afavre"),
				resource.TestCheckResourceAttr("rvav_secret.hook", "id", "tf-hook"),
				func(*terraform.State) error {
					var pols api.ApprovalPolicyList
					h.json("GET", "/api/v1/admin/approval-policies", "mkeller", nil, 200, &pols)
					for _, p := range pols.Items {
						if p.Id == policyID {
							if len(p.Levels) != 2 || p.Levels[1].Group == nil || *p.Levels[1].Group != "rvav-security" ||
								p.ProjectId == nil || *p.ProjectId != "prj_web" {
								return fmt.Errorf("approval policy in rvav: %+v", p)
							}
							return nil
						}
					}
					return fmt.Errorf("no approval policy %s in rvav", policyID)
				},
			),
		}}
		for _, name := range []string{"approval_policy.prd", "day2_policy.delete", "naming_policy.web", "lease_policy.web_dev",
			"lease_policy.tst", "lease_rule.blueprints", "deployment_limit_policy.web", "project_quotas.web", "global_quotas.all",
			"role.ops", "role_assignment.catalog", "project_member.alain", "group_mapping.ops", "subscription.cmdb"} {
			steps = append(steps, resource.TestStep{ResourceName: "rvav_" + name, ImportState: true, ImportStateVerify: true})
		}
		steps = append(steps, resource.TestStep{ResourceName: "rvav_secret.hook", ImportState: true, ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"value"}})
		updated := []string{"approval_policy.prd", "naming_policy.web", "deployment_limit_policy.web", "project_quotas.web",
			"role.ops", "project_member.alain", "subscription.cmdb"}
		var inPlace []plancheck.PlanCheck
		for _, name := range updated {
			inPlace = append(inPlace, plancheck.ExpectResourceAction("rvav_"+name, plancheck.ResourceActionUpdate))
		}
		steps = append(steps, resource.TestStep{
			Config:           v2,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: inPlace},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrPtr("rvav_approval_policy.prd", "id", &policyID),
				resource.TestCheckResourceAttr("rvav_approval_policy.prd", "levels.#", "1"),
				resource.TestCheckResourceAttr("rvav_naming_policy.web", "start", "100"),
				resource.TestCheckResourceAttr("rvav_project_member.alain", "roles.#", "2"),
				func(*terraform.State) error {
					var q api.QuotaLimitList
					h.json("GET", "/api/v1/admin/projects/prj_web/quotas", "mkeller", nil, 200, &q)
					var role api.Role
					h.json("GET", "/api/v1/admin/roles/tf-operator", "mkeller", nil, 200, &role)
					var sub api.Subscription
					h.json("GET", "/api/v1/admin/subscriptions/"+subscriptionID, "mkeller", nil, 200, &sub)
					if len(q.Items) != 2 || len(role.Permissions) != 1 || sub.Enabled {
						return fmt.Errorf("after the change: quotas %+v, role %+v, subscription enabled %v", q.Items, role.Permissions, sub.Enabled)
					}
					return nil
				},
			),
		}, resource.TestStep{
			// Deleted outside Terraform: Terraform makes them again.
			PreConfig: func() {
				h.json("DELETE", "/api/v1/admin/approval-policies/"+policyID, "mkeller", nil, 204, nil)
				h.json("DELETE", "/api/v1/admin/subscriptions/"+subscriptionID, "mkeller", nil, 204, nil)
				h.json("DELETE", "/api/v1/projects/prj_web/members/usr_afavre", "mkeller", nil, 204, nil)
			},
			Config: v2,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("rvav_approval_policy.prd", plancheck.ResourceActionCreate),
				plancheck.ExpectResourceAction("rvav_subscription.cmdb", plancheck.ResourceActionCreate),
				plancheck.ExpectResourceAction("rvav_project_member.alain", plancheck.ResourceActionCreate),
				plancheck.ExpectResourceAction("rvav_day2_policy.delete", plancheck.ResourceActionNoop),
			}},
		})
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps:                    steps,
			CheckDestroy: func(*terraform.State) error {
				var left []string
				var pols api.ApprovalPolicyList
				h.json("GET", "/api/v1/admin/approval-policies", "mkeller", nil, 200, &pols)
				for _, p := range pols.Items {
					if p.Name == "Production web" {
						left = append(left, "approval policy")
					}
				}
				var subs api.SubscriptionList
				h.json("GET", "/api/v1/admin/subscriptions", "mkeller", nil, 200, &subs)
				if len(subs.Items) != 0 {
					left = append(left, "subscriptions")
				}
				if code, _, _ := h.do("GET", "/api/v1/admin/roles/tf-operator", "mkeller", nil); code != 404 {
					left = append(left, "role")
				}
				var leases api.LeasePolicyList
				h.json("GET", "/api/v1/admin/lease-policies", "mkeller", nil, 200, &leases)
				for _, l := range leases.Items {
					if l.ProjectId != nil {
						left = append(left, "lease override")
					}
				}
				var q api.QuotaLimitList
				h.json("GET", "/api/v1/admin/projects/prj_web/quotas", "mkeller", nil, 200, &q)
				if len(q.Items) != 0 {
					left = append(left, "project quotas")
				}
				var members api.ProjectMemberList
				h.json("GET", "/api/v1/projects/prj_web/members", "mkeller", nil, 200, &members)
				for _, m := range members.Items {
					if m.User.Id == "usr_afavre" {
						left = append(left, "project member")
					}
				}
				if len(left) > 0 {
					return fmt.Errorf("left after destroy: %s", strings.Join(left, ", "))
				}
				return nil
			},
		})
	})
}
