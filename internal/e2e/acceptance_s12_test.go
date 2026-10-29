package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
)

// TestAdvancedProvisioningAcceptance is the acceptance scenario of slice
// 12, on a real Temporal server: a three-VM application blueprint, two
// Linux nodes kept on separate hosts and a Windows node joined to the
// domain, two disks each, provisions in production across two clusters,
// once the infrastructure leads approve it (S12.8, ADR-0024). The real
// domain join is for Lab acceptance.
func TestAdvancedProvisioningAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-s12-acceptance"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		const admin = "mkeller"

		// The domain join's account and the local administrator's password.
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "ad-join", Kind: api.SecretWriteKindBasic,
			Username: present.Ptr("svc-join@corp.example"), Value: present.Ptr("J0in-S3cret!")}, 201, nil)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "win-admin", Kind: api.SecretWriteKindToken,
			Value: present.Ptr("L0cal-S3cret!")}, 201, nil)

		// The blueprint: an application tier and its Windows node.
		fields := []string{"app", "app_cluster", "app_datastore", "win_cluster", "win_datastore", "network"}
		props, ui := map[string]any{}, map[string]api.UiField{}
		for _, f := range fields {
			props[f] = map[string]any{"type": "string", "minLength": 1}
			ui[f] = api.UiField{Widget: api.UiWidgetText, Label: f}
		}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": fields, "properties": props},
			UiSchema:   api.UiSchema{Fields: ui}, InitialValues: map[string]any{},
			Blueprint: map[string]any{
				"kind": "vm", "naming": "{project}-{environment}-{inputs.app}{component}{seq:02}",
				"deployment_name": "{project}-{environment}-{inputs.app}", "rollback_on_failure": true,
				"guest":    map[string]any{"os_family": "linux", "domain": "corp.example"},
				"bindings": map[string]any{"vcpu": "2", "memory_gb": "8", "disk_gb": "60", "network": "inputs.network"},
				"components": []any{
					map[string]any{"key": "app", "title": "Application servers", "count": 2, "anti_affinity": true,
						"template": "tpl-rhel9.4-2026.10", "disks": []any{map[string]any{"size_gb": 50}},
						"bindings": map[string]any{"cluster": "inputs.app_cluster", "datastore": "inputs.app_datastore"}},
					map[string]any{"key": "win", "title": "Windows server", "template": "tpl-win2025-std-2026.09",
						"guest": map[string]any{"os_family": "windows", "admin_password": "win-admin",
							"domain_join": map[string]any{"domain": "corp.example", "ou": "OU=Servers,DC=corp,DC=example", "credential": "ad-join"}},
						"disks":    []any{map[string]any{"size_gb": 100}},
						"bindings": map[string]any{"cluster": "inputs.win_cluster", "datastore": "inputs.win_datastore", "memory_gb": "16"}},
				},
			},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Application", Fields: fields}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "erp", Name: "ERP application",
			Summary: "Two application servers and a Windows server.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/erp/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/erp/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		// Requested in production: approval first.
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "prd", Inputs: map[string]any{"app": "erp",
				"app_cluster": "GVA-PRD-CL01", "app_datastore": "vsan-gva-prd-cl01",
				"win_cluster": "GVA-PRD-CL02", "win_datastore": "vsan-gva-prd-cl02", "network": "dvpg-web-prd-vl210"}},
			202, &req, "Idempotency-Key", uuid.NewString())
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusPendingApproval)
		var level string
		for deadline := time.Now().Add(30 * time.Second); level == ""; time.Sleep(50 * time.Millisecond) {
			_ = h.store.Pool.QueryRow(ctx, `select id from approvals where request_id = $1 and status = 'pending'`, req.Id).Scan(&level)
			if time.Now().After(deadline) {
				t.Fatal("no approval level opened")
			}
		}
		h.json("POST", "/api/v1/approvals/"+level+"/decision", admin, api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove,
			Comment: present.Ptr("ERP go-live")}, 200, nil)
		req = h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)

		// One deployment of three VMs across two clusters, two disks each;
		// the application servers on separate hosts, kept apart by a rule.
		if req.DeploymentIds == nil || len(*req.DeploymentIds) != 1 {
			t.Fatalf("deployments = %v", req.DeploymentIds)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+(*req.DeploymentIds)[0], "tnguyen", nil, 200, &dep)
		clusters, hosts := map[string]string{}, map[string]string{}
		var rule api.DrsRuleResource
		for _, r := range dep.Resources {
			switch v, _ := r.ValueByDiscriminator(); v := v.(type) {
			case api.VmResource:
				clusters[v.Name], hosts[v.Name] = deref(v.Vm.Cluster), deref(v.Vm.Host)
				if len(v.Vm.Disks) != 2 {
					t.Fatalf("%s has %d disks", v.Name, len(v.Vm.Disks))
				}
			case api.DrsRuleResource:
				rule = v
			}
		}
		if dep.Name != "web-prd-erp" || len(clusters) != 3 || clusters["web-prd-erpapp01"] != "GVA-PRD-CL01" ||
			clusters["web-prd-erpapp02"] != "GVA-PRD-CL01" || clusters["web-prd-erpwin01"] != "GVA-PRD-CL02" {
			t.Fatalf("deployment %s: clusters %v", dep.Name, clusters)
		}
		if hosts["web-prd-erpapp01"] == hosts["web-prd-erpapp02"] || rule.Rule.Cluster != "GVA-PRD-CL01" ||
			!slices.Equal(rule.Rule.Vms, []string{"web-prd-erpapp01", "web-prd-erpapp02"}) {
			t.Fatalf("hosts %v, rule %+v", hosts, rule.Rule)
		}
		if inVC, err := h.vc.AntiAffinityRule(ctx, "GVA-PRD-CL01", rule.Name); err != nil || len(inVC) != 2 {
			t.Fatalf("rule in vCenter = %v (%v)", inVC, err)
		}

		// The Windows node is customized to join the domain; no secret is
		// anywhere rvav keeps or shows.
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &steps)
		var winCustomize api.Step
		for _, s := range steps.Items {
			for _, ch := range s.Children {
				if ch.Key == "component:win/vsphere.customize@web-prd-erpwin01" {
					winCustomize = ch
				}
			}
		}
		if winCustomize.Status != api.StepStatusDone || !strings.Contains(deref(winCustomize.Detail), "joins corp.example in OU=Servers,DC=corp,DC=example") {
			t.Fatalf("Windows customization = %s %q", winCustomize.Status, deref(winCustomize.Detail))
		}
		var leaked int
		_ = h.store.Pool.QueryRow(ctx, `select (select count(*) from requests where plan::text like '%S3cret%' or inputs::text like '%S3cret%')
			+ (select count(*) from request_steps where detail like '%S3cret%' or coalesce(error, '') like '%S3cret%')
			+ (select count(*) from audit_events where detail::text like '%S3cret%')`).Scan(&leaked)
		if leaked != 0 {
			t.Fatalf("a secret is in %d rows", leaked)
		}
	})
}
