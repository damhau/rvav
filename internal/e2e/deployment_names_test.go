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
// (ADR-0046).
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

// TestDeploymentEdit: the owner and the project admins edit a
// deployment's name and description with the day-2 action Edit
// deployment, a request like any other, checked as a request's name is;
// a deployment can't move to a project where its name is taken
// (ADR-0046).
func TestDeploymentEdit(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, member, projectAdmin, web = "mkeller", "tnguyen", "afavre", "lmartin", "prj_web"
		if _, err := h.store.Pool.Exec(ctx, `insert into project_members (project_id, user_id, role) values ('prj_web', 'usr_afavre', 'member')`); err != nil {
			t.Fatal(err)
		}

		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, owner, nil, 200, &form)
		request := func(name, description string) api.DeploymentDetail {
			t.Helper()
			d := api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
				DeploymentName: present.Ptr(name), DeploymentDescription: present.Ptr(description),
				Inputs: map[string]any{"role": "app", "size": "M", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
					"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
			var req api.Request
			h.json("POST", "/api/v1/requests", owner, d, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			req = h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
			var dep api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+(*req.DeploymentIds)[0], owner, nil, 200, &dep)
			return dep
		}
		actions := func(id, user string) map[string]api.Day2Action {
			t.Helper()
			var out struct{ Items []api.Day2Action }
			h.json("GET", "/api/v1/deployments/"+id+"/actions", user, nil, 200, &out)
			m := map[string]api.Day2Action{}
			for _, a := range out.Items {
				if a.ResourceId == nil {
					m[a.Key] = a
				}
			}
			return m
		}
		draft := func(name, description string) api.Day2Draft {
			return api.Day2Draft{Action: "edit", Inputs: map[string]any{"name": name, "description": description}}
		}
		edit := func(id, user, name, description string) api.Request {
			t.Helper()
			var r api.Request
			h.json("POST", "/api/v1/deployments/"+id+"/actions", user, draft(name, description), 202, &r, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(r.Id, nil)
			return h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
		}
		refused := func(id, name, description string) string {
			t.Helper()
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/deployments/"+id+"/actions", owner, draft(name, description), 422, &problem, "Idempotency-Key", uuid.NewString())
			if len(problem.Errors) != 1 {
				t.Fatalf("edit to %q = %+v", name, problem.Errors)
			}
			return problem.Errors[0].Field + " " + deref(problem.Errors[0].Code)
		}
		vmOf := func(id string) string {
			t.Helper()
			var dep struct{ Resources []struct{ Type, Name string } }
			h.json("GET", "/api/v1/deployments/"+id, owner, nil, 200, &dep)
			for _, r := range dep.Resources {
				if r.Type == "vsphere.vm" {
					return r.Name
				}
			}
			t.Fatal("no VM")
			return ""
		}

		billing, invoices := request("Billing", "Bills."), request("Invoices", "")
		a := actions(billing.Id, owner)["edit"]
		if !a.Enabled || a.Form == nil || a.Form.InitialValues["name"] != "Billing" || a.Form.InitialValues["description"] != "Bills." {
			t.Fatalf("edit for the owner = %+v", a)
		}
		if m := actions(billing.Id, member)["edit"]; m.Enabled ||
			deref(m.DisabledReason) != "Only the owner and the project admins can edit this deployment, delete it or hand it over." {
			t.Fatalf("edit for a member who doesn't own it = %+v", m)
		}

		// Refused as a request's name is, and when nothing changes.
		vm := vmOf(billing.Id)
		for _, c := range []struct{ name, description, want string }{
			{"Billing", " Bills. ", "/inputs/name no_change"},
			{"INVOICES", "", "/inputs/name taken"},
			{strings.TrimRight(vm, "0123456789") + "999", "", "/inputs/name naming"},
			{"Bill\tting", "", "/inputs/name invalid"},
			{" ", "", "/inputs/name required"},
			// The form's schema refuses what is too long.
			{strings.Repeat("é", 81), "", "/inputs/name invalid"},
			{"Billing", strings.Repeat("ü", 2001), "/inputs/description invalid"},
		} {
			if got := refused(billing.Id, c.name, c.description); got != c.want {
				t.Fatalf("edit to %q = %s, want %s", c.name, got, c.want)
			}
		}

		// A request with its step; the deployment shows the new name and
		// description, its VM keeps its own.
		r := edit(billing.Id, owner, " Billing runs ", "Bills, monthly.")
		if r.Title != "Edit deployment · Billing" {
			t.Fatalf("title = %q", r.Title)
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+r.Id+"/steps", owner, nil, 200, &steps)
		if i := slices.IndexFunc(steps.Items, func(s api.Step) bool { return s.Key == "rvav.details" }); i < 0 ||
			steps.Items[i].Title != "Rename Billing to Billing runs" ||
			deref(steps.Items[i].Detail) != "From Billing to Billing runs, with a new description" {
			t.Fatalf("steps = %+v", steps.Items)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+billing.Id, owner, nil, 200, &dep)
		if dep.Name != "Billing runs" || dep.Description != "Bills, monthly." || vmOf(billing.Id) != vm {
			t.Fatalf("after the edit = %q %q %q", dep.Name, dep.Description, vmOf(billing.Id))
		}
		// Its own VM's name, and the old one, are its to take; the project
		// admin edits it too.
		edit(billing.Id, projectAdmin, vm, "")
		edit(billing.Id, owner, "Billing", "")

		// A project where its name is taken is offered disabled.
		move := func(id string) api.Option {
			t.Helper()
			m := actions(id, admin)["change_project"]
			if m.Form == nil {
				t.Fatalf("change_project = %+v", m)
			}
			for _, o := range *m.Form.UiSchema.Fields["project"].Options {
				if o.Value == "prj_data" {
					return o
				}
			}
			t.Fatalf("no Data Engineering among %+v", *m.Form.UiSchema.Fields["project"].Options)
			return api.Option{}
		}
		if o := move(invoices.Id); o.Disabled != nil && *o.Disabled {
			t.Fatalf("data for invoices = %+v", o)
		}
		h.json("POST", "/api/v1/deployments/"+invoices.Id+"/actions", admin, api.Day2Draft{Action: "change_project",
			Inputs: map[string]any{"project": "prj_data"}}, 202, &r, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, admin, api.RequestStatusSucceeded)
		again := request("Invoices", "")
		if o := move(again.Id); o.Disabled == nil || !*o.Disabled ||
			deref(o.DisabledReason) != "Data Engineering already has a deployment named Invoices. Rename this one first (Edit deployment)." {
			t.Fatalf("data for the second invoices = %+v", o)
		}
		h.json("POST", "/api/v1/deployments/"+again.Id+"/actions", admin, api.Day2Draft{Action: "change_project",
			Inputs: map[string]any{"project": "prj_data"}}, 422, nil, "Idempotency-Key", uuid.NewString())

		// Two live deployments may share a name, from before migration
		// 00080: a new description keeps it, another case too.
		if _, err := h.store.Pool.Exec(ctx, `update deployments set name = 'Billing' where id = $1`, again.Id); err != nil {
			t.Fatal(err)
		}
		edit(again.Id, owner, "Billing", "Billing too.")
		edit(again.Id, owner, "BILLING", "Billing too.")
		h.json("GET", "/api/v1/deployments/"+again.Id, owner, nil, 200, &dep)
		if dep.Name != "BILLING" || dep.Description != "Billing too." {
			t.Fatalf("a shared name after an edit = %q %q", dep.Name, dep.Description)
		}
	})
}
