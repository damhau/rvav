package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestSearchFields: a search field finds its choices with the text typed,
// in a source that searches (REST with ${search}) or in one rvav filters
// (CEL), and the server refuses on submit a value the source doesn't find
// when searched for it (F1.2).
func TestSearchFields(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		apps := []map[string]string{{"id": "app-001", "name": "Payroll", "team": "Finance"},
			{"id": "app-002", "name": "Intranet", "team": "HR"}, {"id": "app-003", "name": "Pay slips", "team": "HR"}}
		var mu sync.Mutex
		var asked []string
		cmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query().Get("q")
			mu.Lock()
			asked = append(asked, q)
			mu.Unlock()
			found := []map[string]string{}
			for _, a := range apps {
				if q != "" && (a["id"] == q || strings.Contains(strings.ToLower(a["name"]), strings.ToLower(q))) {
					found = append(found, a)
				}
			}
			_ = json.NewEncoder(w).Encode(found)
		}))
		t.Cleanup(cmdb.Close)
		h.svc.REST.Hosts = []string{cmdb.URL}

		rest := map[string]any{"url": cmdb.URL + "/apps", "items": "$[*]", "value": "$.id", "label": "$.name", "meta": "$.team"}
		fields := map[string]api.UiField{
			"app": {Widget: api.UiWidgetSearch, Label: "Application", ValueSource: &api.ValueSource{Kind: api.Rest, Params: &rest, Search: new(true)}},
			"host": {Widget: api.UiWidgetSearch, Label: "Host", ValueSource: &api.ValueSource{Kind: api.Cel,
				Params: &map[string]any{"options": `["web-01", "web-02", "db-01"]`}}},
		}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []any{"app"}, "properties": map[string]any{
				"app": map[string]any{"type": "string"}, "host": map[string]any{"type": "string"}}},
			UiSchema:  api.UiSchema{Fields: fields},
			Blueprint: map[string]any{"kind": "awx", "awx": map[string]any{"title": "Register", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "App", Fields: []string{"app", "host"}}}
		create := api.CatalogItemCreate{Slug: "search", Name: "Search fields", Summary: "Found as you type.",
			Category: "compute", Type: api.AwxJobTemplate, Definition: &def}

		// A source said to search must read the text.
		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 422, &refused)
		if len(refused.Errors) != 1 || refused.Errors[0].Field != "/definition/ui_schema/fields/app/value_source/search" {
			t.Fatalf("a REST source that doesn't read ${search}: %+v", refused.Errors)
		}
		rest["url"] = cmdb.URL + "/apps?q=${search}"
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/search/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/search/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		found := func(field, text string) []string {
			t.Helper()
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", requester, api.FieldValuesQuery{VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: new(api.EnvironmentKey("dev")), Field: field, Inputs: map[string]any{}, Search: &text}, 200, &out)
			var values []string
			for _, o := range *out.Options {
				values = append(values, o.Value+" "+o.Label)
			}
			return values
		}
		if got, want := found("app", "pay"), []string{"app-001 Payroll", "app-003 Pay slips"}; !slices.Equal(got, want) {
			t.Errorf("searching applications for pay: %v, want %v", got, want)
		}
		if got, want := found("app", "pay roll"), []string(nil); !slices.Equal(got, want) {
			t.Errorf("searching applications for pay roll: %v, want %v", got, want)
		}
		if got, want := found("host", "WEB"), []string{"web-01 web-01", "web-02 web-02"}; !slices.Equal(got, want) {
			t.Errorf("searching hosts for WEB, which rvav filters: %v, want %v", got, want)
		}
		mu.Lock()
		if want := []string{"pay", "pay roll"}; !slices.Equal(asked, want) {
			t.Errorf("the CMDB was asked %q, want %q", asked, want)
		}
		asked = nil
		mu.Unlock()

		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: "dev", Inputs: inputs}
		}
		for _, c := range []struct{ field, value string }{{"app", "app-999"}, {"app", "pay"}, {"host", "dns-01"}} {
			inputs := map[string]any{"app": "app-001", "host": "web-02"}
			inputs[c.field] = c.value
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/requests", requester, draft(inputs), 422, &problem, "Idempotency-Key", uuid.NewString())
			want := c.value + " is not one of the choices for " + fields[c.field].Label + "."
			if len(problem.Errors) != 1 || problem.Errors[0].Field != "/inputs/"+c.field || problem.Errors[0].Message != want {
				t.Errorf("%s = %s, which its source doesn't find: %+v", c.field, c.value, problem.Errors)
			}
		}

		var req api.Request
		valid := map[string]any{"app": "app-003", "host": "web-02"}
		h.json("POST", "/api/v1/requests", requester, draft(valid), 202, &req, "Idempotency-Key", uuid.NewString())
		mu.Lock()
		if !slices.Contains(asked, "app-003") {
			t.Errorf("on submit, the CMDB was asked %q, not for the value app-003", asked)
		}
		mu.Unlock()
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, requester, nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("request = %s %s", req.Status, errorOf(req))
		}
		var timeline struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", requester, nil, 200, &timeline)
		i := slices.IndexFunc(timeline.Items, func(s api.Step) bool { return strings.HasPrefix(s.Key, "awx.job:") })
		if i < 0 || timeline.Items[i].ExternalRef == nil {
			t.Fatalf("no AWX job in %+v", timeline.Items)
		}
		job, _ := strconv.Atoi(timeline.Items[i].ExternalRef.Id)
		if vars := h.awx.Launched(job).ExtraVars; vars["app"] != "app-003" || vars["host"] != "web-02" {
			t.Errorf("AWX got %v, want %v", vars, valid)
		}
	})
}
