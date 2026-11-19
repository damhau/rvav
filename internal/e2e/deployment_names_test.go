package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
)

// TestDeploymentNames: a request names its deployments and says what they
// are for. The name is unique among the project's live deployments,
// whatever the case, and not a VM name of the request's naming; several
// deployments are numbered; an item that makes none refuses both. A
// blueprint's deployment_name already taken gets the request number
// (ADR-0019).
func TestDeploymentNames(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"

		var rhel api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, owner, nil, 200, &rhel)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, owner, nil, 200, &form)
		if form.NamesDeployments == nil || !*form.NamesDeployments {
			t.Fatalf("a VM item's form names its deployments: %v", form.NamesDeployments)
		}
		draft := func(name, description string, count int) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: rhel.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
				Count: present.Ptr(count), DeploymentName: present.Ptr(name), DeploymentDescription: present.Ptr(description),
				Inputs: map[string]any{"role": "app", "size": "M", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
					"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
		}
		preview := func(d api.RequestDraft) api.RequestPreview {
			t.Helper()
			var pv api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", owner, d, 200, &pv)
			return pv
		}
		refused := func(d api.RequestDraft, field, code string) {
			t.Helper()
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/requests", owner, d, 422, &problem, "Idempotency-Key", uuid.NewString())
			if len(problem.Errors) != 1 || problem.Errors[0].Field != field || deref(problem.Errors[0].Code) != code {
				t.Fatalf("%s %s: %+v", field, code, problem.Errors)
			}
		}
		submit := func(d api.RequestDraft) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", owner, d, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, owner, nil, 200, &req)
			return req
		}
		deployment := func(id string) api.DeploymentDetail {
			t.Helper()
			var d api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+id, owner, nil, 200, &d)
			return d
		}

		// Two deployments of one name are numbered; both say what they are for.
		pv := preview(draft("  Payroll API ", "Payroll's API servers.", 2))
		if !pv.Valid || pv.DeploymentNames == nil || !slices.Equal(*pv.DeploymentNames, []string{"Payroll API 1", "Payroll API 2"}) {
			t.Fatalf("preview = %v %+v", pv.DeploymentNames, pv.Errors)
		}
		vmNames := *pv.Names
		req := submit(draft("  Payroll API ", " Payroll's API servers. ", 2))
		if req.Status != api.RequestStatusSucceeded || req.DeploymentIds == nil || len(*req.DeploymentIds) != 2 {
			t.Fatalf("request = %s %v", req.Status, req.DeploymentIds)
		}
		// The title still names the VMs; the reason stays the approvers'.
		if !strings.Contains(req.Title, vmNames[0]) || deref(req.Justification) != "" {
			t.Fatalf("title %q, justification %q", req.Title, deref(req.Justification))
		}
		var named []string
		for _, id := range *req.DeploymentIds {
			d := deployment(id)
			if d.Description != "Payroll's API servers." {
				t.Fatalf("description = %q", d.Description)
			}
			named = append(named, d.Name)
		}
		slices.Sort(named)
		if !slices.Equal(named, []string{"Payroll API 1", "Payroll API 2"}) {
			t.Fatalf("names = %v", named)
		}
		var list api.DeploymentPage
		h.json("GET", "/api/v1/deployments?project_id="+web+"&q=Payroll", owner, nil, 200, &list)
		if len(list.Items) != 2 || list.Items[0].Description != "Payroll's API servers." {
			t.Fatalf("listed = %+v", list.Items)
		}

		// Taken whatever the case; free again once deleted.
		refused(draft("payroll api 1", "", 1), "/deployment_name", "taken")
		if pv := preview(draft("PAYROLL API 2", "", 1)); pv.Valid || !slices.Contains(codesOf(pv.Errors), "taken") {
			t.Fatalf("preview of a taken name = %+v", pv.Errors)
		}
		if _, err := h.store.Pool.Exec(ctx, `update deployments set status = 'deleted' where id = $1`, (*req.DeploymentIds)[0]); err != nil {
			t.Fatal(err)
		}
		if pv := preview(draft("Payroll API 1", "", 1)); !pv.Valid {
			t.Fatalf("a deleted deployment's name = %+v", pv.Errors)
		}

		// Too long, on two lines, or a VM name the naming gives.
		refused(draft(strings.Repeat("é", 81), "", 1), "/deployment_name", "too_long")
		refused(draft("Payroll\nAPI", "", 1), "/deployment_name", "invalid")
		refused(draft("Payroll", strings.Repeat("ü", 2001), 1), "/deployment_description", "too_long")
		refused(draft(strings.ToUpper(vmNames[0][:len(vmNames[0])-1])+"9", "", 1), "/deployment_name", "naming")
		if pv := preview(draft(strings.Repeat("é", 80), strings.Repeat("ü", 2000), 1)); !pv.Valid {
			t.Fatalf("at the limits = %+v", pv.Errors)
		}

		// With no name, the deployment is its VM's, as before.
		one := submit(draft("", "", 1))
		if d := deployment((*one.DeploymentIds)[0]); d.Name != strings.TrimPrefix(one.Title, rhel.Name+" · ") || d.Description != "" {
			t.Fatalf("unnamed = %q %q (title %q)", d.Name, d.Description, one.Title)
		}

		// An AWX item makes no deployment to name.
		var dns api.RequestForm
		h.json("GET", "/api/v1/catalog/items/dns-record/form?project_id="+web, owner, nil, 200, &dns)
		if dns.NamesDeployments != nil {
			t.Fatalf("an AWX item's form = %v", *dns.NamesDeployments)
		}
		awx := api.RequestDraft{CatalogItemId: dns.ItemId, VersionId: dns.VersionId, ProjectId: web, Environment: "dev",
			Inputs: dns.InitialValues, DeploymentName: present.Ptr("Records"), DeploymentDescription: present.Ptr("Some")}
		if pv := preview(awx); !slices.Contains(fieldsOf(pv.Errors), "/deployment_name") || !slices.Contains(fieldsOf(pv.Errors), "/deployment_description") ||
			!slices.Contains(codesOf(pv.Errors), "no_deployment") {
			t.Fatalf("an AWX item named = %+v", pv.Errors)
		}

		// A multi-VM deployment takes the name given; the blueprint's own,
		// taken, gets the request number.
		def := shopApp()
		var shop api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "shop-app", Name: "Shop application",
			Summary: "Web servers and a database.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &shop)
		h.json("POST", "/api/v1/admin/catalog/items/shop-app/versions/"+shop.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/shop-app/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		shopDraft := func(name string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: shop.Id, VersionId: shop.DraftVersion.Id, ProjectId: web, Environment: "dev",
				DeploymentName: present.Ptr(name),
				Inputs: map[string]any{"app": "shop", "web_nodes": 1, "size": "S", "web_cluster": "GVA-DEV-CL01",
					"web_datastore": "vsan-gva-dev-cl01", "db_cluster": "GVA-DEV-CL01", "db_datastore": "nfs-gva-bronze-01",
					"network": "dvpg-app-dev-vl310", "db_post": []string{}}}
		}
		if pv := preview(shopDraft("Shop")); !pv.Valid || !slices.Equal(*pv.DeploymentNames, []string{"Shop"}) {
			t.Fatalf("multi-VM preview = %v %+v", pv.DeploymentNames, pv.Errors)
		}
		given := submit(shopDraft("Shop"))
		if d := deployment((*given.DeploymentIds)[0]); d.Name != "Shop" || given.Title != "Shop application · Shop" {
			t.Fatalf("named multi-VM = %q, title %q", d.Name, given.Title)
		}
		first, second := submit(shopDraft("")), submit(shopDraft(""))
		if d := deployment((*first.DeploymentIds)[0]); d.Name != "web-dev-shop" {
			t.Fatalf("blueprint's name = %q", d.Name)
		}
		if d := deployment((*second.DeploymentIds)[0]); d.Name != "web-dev-shop "+second.Number || second.Title != "Shop application · "+d.Name {
			t.Fatalf("blueprint's name taken = %q, title %q", d.Name, second.Title)
		}
	})
}
