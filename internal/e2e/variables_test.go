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
)

// TestSharedVariables: catalog managers keep named values that aren't
// secret, and a composite's steps and conditions read them as
// vars.<group>.<name> each time they run, the value of the moment. A
// version that reads one that doesn't exist isn't published, and one read
// by an item isn't removed. Each change is audited with its value
// (ADR-0057).
func TestSharedVariables(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const author, requester = "lmartin", "tnguyen"
		cmdb := newRESTCMDB(t)
		h.svc.REST.Hosts = []string{cmdb.URL}

		add := func(group, name string, value any, want int) api.Variable {
			t.Helper()
			var v api.Variable
			var out any = &v
			if want != 201 {
				out = nil
			}
			h.json("POST", "/api/v1/admin/variables", author, api.VariableCreate{Group: group, Name: name, Value: value}, want, out)
			return v
		}
		dns := add("network", "dns_servers", []any{"10.20.0.53", "10.20.0.54"}, 201)
		if dns.Key != "network.dns_servers" || len(dns.UsedBy) != 0 || valueOf(dns.UpdatedBy) == "" {
			t.Fatalf("the variable = %+v", dns)
		}
		add("cmdb", "site", "gva", 201)
		add("monitoring", "enabled", true, 201)
		add("monitoring", "spare", 3, 201)
		add("network", "dns_servers", "10.0.0.1", 409)
		add("Network", "dns", "10.0.0.1", 422)
		add("cmdb", "admin_password", "hunter2", 422)
		add("cmdb", "empty", nil, 422)
		h.json("GET", "/api/v1/admin/variables", requester, nil, 403, nil)
		h.json("PUT", "/api/v1/admin/variables/cmdb.site", requester, api.VariableUpdate{Value: "zrh"}, 403, nil)

		def := restDefinition(cmdb.URL)
		steps := []any{
			map[string]any{"key": "register", "title": "Register", "rest": map[string]any{
				"method": "POST", "url": cmdb.URL + "/api/ci/${inputs.name}?site=${vars.cmdb.site}",
				"body": "{'name': inputs.name, 'dns': vars.network.dns_servers}", "accept": []any{201}}},
			map[string]any{"key": "job", "title": "Monitoring", "awx": map[string]any{"job_template": "monitoring-agent"},
				"when": "vars.monitoring.enabled", "with": map[string]any{"target": "inputs.name", "dns": "vars.network.dns_servers"}},
		}
		def.Blueprint = map[string]any{"kind": "composite", "steps": append(slices.Clone(steps),
			map[string]any{"key": "typo", "title": "Typo", "awx": map[string]any{"job_template": "monitoring-agent"},
				"with": map[string]any{"target": "vars.cmdb.nope"}})}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", author, api.CatalogItemCreate{Slug: "shared-vars", Name: "Shared variables",
			Summary: "Shared variables.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
		publish := "/api/v1/admin/catalog/items/shared-vars/versions/" + item.DraftVersion.Id + "/publish"
		var refused struct{ Errors []api.FieldError }
		h.json("POST", publish, author, nil, 422, &refused)
		if len(refused.Errors) != 1 || valueOf(refused.Errors[0].Code) != "unknown_variable" || refused.Errors[0].Field != "/definition/blueprint/steps/2" ||
			!strings.Contains(refused.Errors[0].Message, "vars.cmdb.nope") {
			t.Fatalf("publishing with an unknown variable = %+v", refused.Errors)
		}
		def.Blueprint = map[string]any{"kind": "composite", "steps": steps}
		h.json("PUT", "/api/v1/admin/catalog/items/shared-vars/versions/"+item.DraftVersion.Id, author,
			api.CatalogItemVersionUpdate{Definition: def, BaseRevision: &item.DraftVersion.Revision}, 200, nil)
		h.json("POST", publish, author, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/shared-vars/entitlements", author, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		run := func() (api.Request, map[string]api.Step) {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", requester, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"name": "web01", "api_password": restInput}},
				202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, requester, nil, 200, &req)
			if req.Status != api.RequestStatusSucceeded {
				t.Fatalf("the request = %s %s", req.Status, errorOf(req))
			}
			var list struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps", requester, nil, 200, &list)
			out := map[string]api.Step{}
			for _, s := range list.Items {
				out[s.Key] = s
			}
			return req, out
		}
		inputsOf := func(req api.Request, s api.Step) map[string]any {
			t.Helper()
			var d api.StepData
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps/"+s.Id+"/data", author, nil, 200, &d)
			return valueOf(d.Inputs)
		}
		lastCall := func() restCall {
			t.Helper()
			cmdb.mu.Lock()
			defer cmdb.mu.Unlock()
			return cmdb.calls[len(cmdb.calls)-1]
		}

		req, got := run()
		if call := lastCall(); call.query != "site=gva" || len(call.body["dns"].([]any)) != 2 {
			t.Fatalf("the call = %+v", call)
		}
		if in := inputsOf(req, got["step:job"]); in["target"] != "web01" || len(in["dns"].([]any)) != 2 {
			t.Fatalf("the job's inputs = %v", in)
		}

		// A change is read by the next run, as it is then.
		h.json("PUT", "/api/v1/admin/variables/network.dns_servers", author, api.VariableUpdate{Value: []any{"10.20.0.99"},
			Description: new("The resolvers of every site.")}, 200, &dns)
		if dns.Description != "The resolvers of every site." || !slices.Equal(dns.UsedBy, []string{"item shared-vars"}) {
			t.Fatalf("the variable = %+v", dns)
		}
		h.json("PUT", "/api/v1/admin/variables/monitoring.enabled", author, api.VariableUpdate{Value: false}, 200, nil)
		h.json("PUT", "/api/v1/admin/variables/monitoring.enabled", author, api.VariableUpdate{}, 422, nil)
		h.json("PUT", "/api/v1/admin/variables/monitoring.gone", author, api.VariableUpdate{Value: 1}, 404, nil)
		_, got = run()
		if call := lastCall(); call.body["dns"].([]any)[0] != "10.20.0.99" {
			t.Fatalf("the call after the change = %+v", call)
		}
		if got["step:job"].Status != api.StepStatusSkipped {
			t.Fatalf("the job with monitoring off = %+v", got["step:job"])
		}

		// One an item reads stays; one none reads goes.
		h.json("DELETE", "/api/v1/admin/variables/cmdb.site", author, nil, 409, nil)
		h.json("DELETE", "/api/v1/admin/variables/monitoring.spare", requester, nil, 403, nil)
		h.json("DELETE", "/api/v1/admin/variables/monitoring.spare", author, nil, 204, nil)
		h.json("GET", "/api/v1/admin/variables/monitoring.spare", author, nil, 404, nil)
		var list api.VariableList
		h.json("GET", "/api/v1/admin/variables", author, nil, 200, &list)
		var keys []string
		for _, v := range list.Items {
			keys = append(keys, v.Key)
		}
		if !slices.Equal(keys, []string{"cmdb.site", "monitoring.enabled", "network.dns_servers"}) {
			t.Fatalf("the variables = %v", keys)
		}

		rows, err := h.store.Pool.Query(ctx, `select action, target_id, detail::text from audit_events where target_type = 'variable' order by id`)
		if err != nil {
			t.Fatal(err)
		}
		var audit []string
		for rows.Next() {
			var action, target, detail string
			if err := rows.Scan(&action, &target, &detail); err != nil {
				t.Fatal(err)
			}
			audit = append(audit, action+" "+target+" "+detail)
		}
		if len(audit) != 7 || !strings.Contains(audit[4], `variable.update network.dns_servers {"was": ["10.20.0.53", "10.20.0.54"], "value": ["10.20.0.99"]}`) ||
			audit[6] != "variable.delete monitoring.spare {}" {
			t.Fatalf("the audit = %q", audit)
		}
	})
}
