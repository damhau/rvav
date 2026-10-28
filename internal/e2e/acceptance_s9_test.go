package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestAdministrationAcceptance is the acceptance scenario of slice 9: a new
// project set up through the administration API only, with the calls the
// UI makes (members, placement, quota, naming, templates and sizes, the
// catalog entitlement), requests a VM without any SQL or YAML. The VM is
// named by the project's naming policy, placed by its rule in its folder,
// built from the offered template and size; a second VM is refused by the
// quota, and the explain query says what decided (S9.11, ADR-0020).
func TestAdministrationAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }

		// The project, its admin, and a member the project admin adds.
		var pay api.AdminProject
		h.json("POST", "/api/v1/admin/projects", admin, api.AdminProjectCreate{Code: "pay", Name: "Payments",
			Environments: []string{"dev"}, ProjectAdmins: &[]string{seed.UserID("sbrunner")}}, 201, &pay)
		h.json("PUT", "/api/v1/projects/"+pay.Id+"/members/"+seed.UserID("tnguyen"), "sbrunner",
			api.ProjectMemberWrite{Role: api.Member}, 200, nil)

		// Placement: bronze clusters by vCenter tag, one datastore by name, the dev app networks.
		h.json("POST", "/api/v1/admin/placement-rules", admin, api.PlacementRuleWrite{ProjectId: pay.Id, Environment: "dev",
			Clusters: []string{}, ClusterTags: &[]string{"rvav-tier/bronze"}, Datastores: []string{"vsan-gva-dev-cl01"},
			Networks: []string{"dvpg-app-dev-*"}, Folder: "/GVA/vm/rvav/pay/dev"}, 201, nil)
		var preview api.PlacementPreview
		h.json("GET", "/api/v1/admin/placement-preview?project_id="+pay.Id+"&environment=dev", admin, nil, 200, &preview)
		if len(preview.Clusters) != 1 || preview.Clusters[0].Name != "GVA-DEV-CL01" || len(preview.Datastores) != 1 ||
			len(preview.Networks) == 0 || len(preview.Problems) != 0 {
			t.Fatalf("Payments' placement = %+v", preview)
		}

		// A quota of one VM, a naming policy, a template and a size offered to the project.
		h.json("PUT", "/api/v1/admin/projects/"+pay.Id+"/quotas", admin, api.QuotaLimitList{Items: []api.QuotaLimit{
			{Environment: "dev", Resource: api.VmCount, Limit: new(1), Enforcement: api.QuotaLimitEnforcementHard},
			{Environment: "", Resource: api.Vcpu, Limit: new(8), Enforcement: api.QuotaLimitEnforcementHard},
		}}, 200, nil)
		offer := &[]api.Offer{{ProjectId: new(pay.Id)}}
		h.json("POST", "/api/v1/admin/naming-policies", admin, api.NamingPolicyWrite{Name: "Payments names",
			Template: "{project}{environment}{role}{seq:02}", Offers: offer}, 201, nil)
		h.json("POST", "/api/v1/admin/templates", admin, api.VmTemplateWrite{Key: "ubuntu", Label: "Ubuntu 24.04",
			VcenterTemplate: "tpl-ubuntu24.04-2026.10", OsFamily: api.VmTemplateWriteOsFamilyLinux, Offers: offer}, 201, nil)
		h.json("POST", "/api/v1/admin/sizes", admin, api.SizePresetWrite{Key: "M", Label: "Medium", Vcpu: 4, MemoryGb: 8,
			DiskGb: 60, Offers: offer}, 201, nil)

		// A VM item whose operating system and size are chosen from what is offered, entitled to Payments.
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		var published api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, admin, nil, 200, &published)
		def := published.Definition
		props := def.FormSchema["properties"].(map[string]any)
		props["size"] = map[string]any{"type": "string"}
		props["template"] = map[string]any{"type": "string"}
		def.FormSchema["required"] = append(def.FormSchema["required"].([]any), "template")
		def.UiSchema.Fields["size"] = api.UiField{Widget: api.UiWidgetSelect, Label: "Size",
			ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("rvav.sizes")}}
		def.UiSchema.Fields["template"] = api.UiField{Widget: api.UiWidgetSelect, Label: "Operating system",
			ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("rvav.templates")}}
		def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "template")
		delete(def.Blueprint, "sizes")
		def.Blueprint["template"] = ""
		def.Blueprint["bindings"].(map[string]any)["template"] = "inputs.template"
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pay-vm", Name: "Payments VM",
			Summary: "A VM from the offered templates and sizes.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/pay-vm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/pay-vm/entitlements", admin,
			api.CatalogEntitlements{ProjectIds: []string{pay.Id}}, 200, nil)

		// The member sees the item in Payments' catalog, with what is offered there.
		var catalog api.CatalogItemPage
		h.json("GET", "/api/v1/catalog/items?project_id="+pay.Id, "tnguyen", nil, 200, &catalog)
		if !slices.ContainsFunc(catalog.Items, func(i api.CatalogItem) bool { return i.Slug == "pay-vm" }) {
			t.Fatalf("Payments' catalog = %+v", catalog.Items)
		}
		options := func(field string) []string {
			t.Helper()
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", "tnguyen", api.FieldValuesQuery{VersionId: item.DraftVersion.Id, ProjectId: pay.Id,
				Environment: new(api.EnvironmentKey("dev")), Field: field, Inputs: map[string]any{}}, 200, &out)
			keys := []string{}
			for _, o := range *out.Options {
				keys = append(keys, o.Value)
			}
			return keys
		}
		if got := fmt.Sprint(options("template"), options("size"), options("cluster")); got != "[ubuntu] [M] [GVA-DEV-CL01]" {
			t.Fatalf("Payments' choices = %s", got)
		}

		// Outside the placement rule: refused.
		inputs := map[string]any{"role": "api", "size": "M", "template": "ubuntu", "cluster": "GVA-PRD-CL01",
			"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: pay.Id, Environment: "dev", Inputs: inputs}
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 422, &problem, "Idempotency-Key", uuid.NewString())
		if !slices.Contains(fieldsOf(problem.Errors), "/inputs/cluster") {
			t.Fatalf("a cluster outside the rule = %+v", problem.Errors)
		}

		// Inside it: the VM is built, named by the policy, in the rule's folder.
		inputs["cluster"] = "GVA-DEV-CL01"
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded || req.DeploymentIds == nil {
			t.Fatalf("request = %s", req.Status)
		}
		var dep api.Deployment
		h.json("GET", "/api/v1/deployments/"+(*req.DeploymentIds)[0], "sbrunner", nil, 200, &dep)
		if dep.Name != "paydevapi01" || dep.Project.Id != pay.Id {
			t.Fatalf("deployment = %s in %s", dep.Name, dep.Project.Name)
		}
		var plan struct {
			Folder string `json:"folder"`
			VMs    []struct {
				Template string `json:"template"`
				VCPU     int    `json:"vcpu"`
			} `json:"vms"`
		}
		var raw []byte
		if err := h.store.Pool.QueryRow(ctx, `select plan from requests where id = $1`, req.Id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(raw, &plan)
		if len(plan.VMs) != 1 || plan.VMs[0].Template != "tpl-ubuntu24.04-2026.10" || plan.VMs[0].VCPU != 4 {
			t.Fatalf("plan = %s", raw)
		}
		if _, err := find.NewFinder(c).VirtualMachine(ctx, "/GVA/vm/rvav/pay/dev/paydevapi01"); err != nil {
			t.Fatalf("the VM in vCenter: %v", err)
		}

		// A second VM is over the quota, which says what is left.
		inputs["role"] = "web"
		status, body, _ := h.do("POST", "/api/v1/requests", "tnguyen", draft, "Idempotency-Key", uuid.NewString())
		if status != 422 || !strings.Contains(string(body), "Payments in Development has 0 left") {
			t.Fatalf("over the quota: %d %s", status, body)
		}

		// What decided, for the project admin.
		var why api.PolicyExplanation
		h.json("POST", "/api/v1/admin/policies/explain", admin, api.PolicyExplainQuery{ProjectId: pay.Id, Environment: "dev",
			ItemId: &item.Id}, 200, &why)
		if why.Naming.DecidedBy == nil || *why.Naming.DecidedBy != "Payments names" || why.Approval == nil {
			t.Fatalf("explain = %+v", why)
		}
		i := slices.IndexFunc(why.Quota, func(l api.PolicyQuotaLine) bool { return l.Resource == api.VmCount })
		if i < 0 || why.Quota[i].Used != 1 {
			t.Fatalf("quota lines = %+v", why.Quota)
		}

		// The audit log has the whole setup, for the project admin too.
		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?project_id="+pay.Id, "sbrunner", nil, 200, &audit)
		actions := []string{}
		for _, e := range audit.Items {
			actions = append(actions, e.Action)
		}
		for _, want := range []string{"project.create", "member.set", "placement.rule.create", "policy.quota.set"} {
			if !slices.Contains(actions, want) {
				t.Fatalf("audit for Payments = %v, no %s", actions, want)
			}
		}
	})
}
