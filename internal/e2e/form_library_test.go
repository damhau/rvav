package e2e

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/seed"
)

// awxForm is the definition of an AWX item whose form has these inputs,
// all strings but those typed in types, its fields and its validations.
func awxForm(fields map[string]api.UiField, types map[string]string, validations ...api.FormValidation) api.CatalogVersionDefinition {
	props := map[string]any{}
	names := make([]string, 0, len(fields))
	for name := range fields {
		props[name] = map[string]any{"type": cmp.Or(types[name], "string")}
		names = append(names, name)
	}
	slices.Sort(names)
	def := api.CatalogVersionDefinition{
		FormSchema: map[string]any{"type": "object", "properties": props},
		UiSchema:   api.UiSchema{Fields: fields},
		Blueprint:  map[string]any{"kind": "awx", "awx": map[string]any{"title": "Register", "job_template": "pg-create-role"}},
	}
	if len(validations) > 0 {
		def.UiSchema.Validations = &validations
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Main", Fields: names}}
	return def
}

// TestFormLibrary: a library REST source two items use; a new version
// gives each a draft naming it, while what they published keeps the
// version it names; an entry a published version uses can't be deleted
// (F1.5, ADR-0043).
func TestFormLibrary(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		var mu sync.Mutex
		var asked []string
		dbaas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			asked = append(asked, r.URL.RequestURI())
			mu.Unlock()
			versions := map[string][]string{"dev": {"15", "16"}, "prd": {"15"}}[r.URL.Query().Get("env")]
			if r.URL.Query().Get("beta") == "1" {
				versions = append(versions, "17")
			}
			out := []map[string]string{}
			for _, v := range versions {
				out = append(out, map[string]string{"v": v, "name": "PostgreSQL " + v})
			}
			_ = json.NewEncoder(w).Encode(out)
		}))
		t.Cleanup(dbaas.Close)
		h.svc.REST.Hosts = []string{dbaas.URL}

		var problem struct{ Errors []api.FieldError }
		yes := true
		entry := api.FormLibraryEntryWrite{Key: present.Ptr("dbVersions"), Name: "Database versions", Use: api.Source, Kind: api.Rest,
			Params:     map[string]any{"url": dbaas.URL + "/versions?env=${inputs.environment}", "items": "$[*]", "value": "$.v", "label": "$.name"},
			Parameters: []api.FormLibraryParameter{{Name: "env", Label: "Environment", Required: &yes}}}
		h.json("POST", "/api/v1/admin/form-library", admin, entry, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Equal(f, []string{"/params/url"}) {
			t.Fatalf("an entry reading a form's inputs = %+v", problem.Errors)
		}
		entry.Params["url"] = dbaas.URL + "/versions?env=${params.env}"
		var saved api.FormLibraryEntry
		h.json("POST", "/api/v1/admin/form-library", admin, entry, 201, &saved)
		if saved.Version != 1 || saved.Use != api.Source || len(saved.UsedBy) != 0 {
			t.Fatalf("created = %+v", saved)
		}
		h.json("POST", "/api/v1/admin/form-library", admin, entry, 409, nil)
		h.json("GET", "/api/v1/admin/form-library", requester, nil, 403, nil)

		var tested api.FormLibraryTestResult
		h.json("POST", "/api/v1/admin/form-library/test", admin, api.FormLibraryTest{Entry: entry, Params: map[string]any{"env": "prd"}}, 200, &tested)
		if tested.Options == nil || len(*tested.Options) != 1 || (*tested.Options)[0].Label != "PostgreSQL 15" {
			t.Fatalf("tested with env prd = %+v", tested)
		}

		// Two items name it, their environment input its parameter.
		uses := func(version int, binding string) api.UiField {
			return api.UiField{Widget: api.UiWidgetSelect, Label: "Version", DependsOn: &[]string{"environment"},
				ValueSource: &api.ValueSource{Kind: api.Library, Name: present.Ptr("dbVersions"), Version: &version,
					Params: &map[string]any{"env": binding}}}
		}
		env := api.UiField{Widget: api.UiWidgetSelect, Label: "Environment", Options: &[]api.Option{{Value: "dev", Label: "dev"}, {Value: "prd", Label: "prd"}}}
		item := func(slug string, f api.UiField) api.AdminCatalogItem {
			t.Helper()
			def := awxForm(map[string]api.UiField{"environment": env, "version": f}, nil)
			var it api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: "Database " + slug, Summary: "A database.",
				Category: "databases", Type: api.AwxJobTemplate, Definition: &def}, 201, &it)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+it.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			h.json("GET", "/api/v1/admin/catalog/items/"+slug, admin, nil, 200, &it)
			return it
		}
		bad := awxForm(map[string]api.UiField{"environment": env, "version": uses(1, "inputs.nope +")}, nil)
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "db-bad", Name: "d", Summary: "d",
			Category: "databases", Type: api.AwxJobTemplate, Definition: &bad}, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Equal(f, []string{"/definition/ui_schema/fields/version/value_source/params/env"}) {
			t.Fatalf("a binding that isn't CEL = %+v", problem.Errors)
		}
		bad = awxForm(map[string]api.UiField{"environment": env, "version": uses(3, "inputs.environment")}, nil)
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "db-bad", Name: "d", Summary: "d",
			Category: "databases", Type: api.AwxJobTemplate, Definition: &bad}, 422, &problem)
		if len(problem.Errors) != 1 || !strings.Contains(problem.Errors[0].Message, "its latest is 1") {
			t.Fatalf("a version that doesn't exist = %+v", problem.Errors)
		}
		alpha := item("db-alpha", uses(1, "inputs.environment"))
		beta := item("db-beta", uses(1, "inputs.environment"))

		options := func(it api.AdminCatalogItem, versionID string, inputs map[string]any) []string {
			t.Helper()
			var out api.FieldValues
			// A draft's as the designer previews it, as the requester.
			who, q := requester, api.FieldValuesQuery{VersionId: versionID, ProjectId: "prj_web",
				Environment: new(api.EnvironmentKey("dev")), Field: "version", Inputs: inputs}
			if versionID != it.PublishedVersion.Id {
				who, q.AsUserId = admin, new(seed.UserID(requester))
			}
			h.json("POST", "/api/v1/forms/values", who, q, 200, &out)
			var values []string
			for _, o := range *out.Options {
				values = append(values, o.Value)
			}
			return values
		}
		for _, it := range []api.AdminCatalogItem{alpha, beta} {
			if got := options(it, it.PublishedVersion.Id, map[string]any{"environment": "dev"}); !slices.Equal(got, []string{"15", "16"}) {
				t.Errorf("%s's versions in dev = %v", it.Slug, got)
			}
		}
		if got := options(alpha, alpha.PublishedVersion.Id, map[string]any{}); len(got) != 0 {
			t.Errorf("without its required parameter, the source gave %v", got)
		}

		h.json("DELETE", "/api/v1/admin/form-library/dbVersions", admin, nil, 409, &problem)
		h.json("GET", "/api/v1/admin/form-library/dbVersions", admin, nil, 200, &saved)
		if len(saved.UsedBy) != 2 || saved.UsedBy[0].Slug != "db-alpha" || saved.UsedBy[0].State != "published" || saved.UsedBy[0].EntryVersion != 1 {
			t.Fatalf("used by = %+v", saved.UsedBy)
		}

		// beta has a draft already: the new version changes it; alpha gets one.
		var betaDraft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/db-beta/versions", admin, api.CatalogItemVersionCreate{}, 201, &betaDraft)
		entry.Key = nil
		entry.Params["url"] = dbaas.URL + "/versions?env=${params.env}&beta=1"
		h.json("PUT", "/api/v1/admin/form-library/dbVersions", admin, entry, 200, &saved)
		if saved.Version != 2 || saved.Drafts == nil || len(*saved.Drafts) != 2 {
			t.Fatalf("saved version 2 = %+v", saved)
		}
		drafts := *saved.Drafts
		if drafts[0].Slug != "db-alpha" || !drafts[0].Created || drafts[0].Number != "1.1" || drafts[0].Problem != nil ||
			drafts[1].Slug != "db-beta" || drafts[1].Created || drafts[1].Number != betaDraft.Number {
			t.Fatalf("drafts = %+v", drafts)
		}
		h.json("GET", "/api/v1/admin/catalog/items/db-alpha", admin, nil, 200, &alpha)
		var detail api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/db-alpha/versions/"+alpha.DraftVersion.Id, admin, nil, 200, &detail)
		if v := detail.Definition.UiSchema.Fields["version"].ValueSource.Version; v == nil || *v != 2 {
			t.Fatalf("alpha's draft names %v", v)
		}
		if got := options(alpha, alpha.PublishedVersion.Id, map[string]any{"environment": "dev"}); !slices.Equal(got, []string{"15", "16"}) {
			t.Errorf("published alpha, on version 1 still: %v", got)
		}
		if got := options(alpha, alpha.DraftVersion.Id, map[string]any{"environment": "dev"}); !slices.Equal(got, []string{"15", "16", "17"}) {
			t.Errorf("alpha's draft, on version 2: %v", got)
		}
		h.json("GET", "/api/v1/admin/catalog/items/db-beta/versions/"+betaDraft.Id, admin, nil, 200, &detail)
		if v := detail.Definition.UiSchema.Fields["version"].ValueSource.Version; v == nil || *v != 2 || detail.Revision != betaDraft.Revision+1 {
			t.Fatalf("beta's draft names %v at revision %d", v, detail.Revision)
		}
		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?action=form_library.", "mkeller", nil, 200, &audit)
		var actions []string
		for _, e := range audit.Items {
			actions = append(actions, e.Action)
		}
		if !slices.Contains(actions, "form_library.create") || !slices.Contains(actions, "form_library.update") || !slices.Contains(actions, "form_library.test") {
			t.Errorf("audited %v", actions)
		}
		mu.Lock()
		if !slices.Contains(asked, "/versions?env=dev&beta=1") {
			t.Errorf("the service was asked %v", asked)
		}
		mu.Unlock()
	})
}

// TestFormValidations: REST, CEL and library validations refuse a request
// with their messages on its fields, on submit and on the review step,
// then pass; a REST validation whose service is down refuses it, saying
// so (F1.5, ADR-0043).
func TestFormValidations(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		var mu sync.Mutex
		calls := 0
		budget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Budget float64 `json:"budget"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			mu.Lock()
			calls++
			mu.Unlock()
			if in.Budget > 100 {
				_ = json.NewEncoder(w).Encode(map[string]any{"valid": false, "errors": map[string]string{"budget": "Over the team's budget of 100."}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"valid": true})
		}))
		t.Cleanup(budget.Close)
		down := httptest.NewServer(http.NotFoundHandler())
		downURL := down.URL
		down.Close()
		h.svc.REST.Hosts = []string{budget.URL, downURL}

		yes := true
		openCC := api.FormLibraryEntryWrite{Key: present.Ptr("costCentreOpen"), Name: "Cost centre open", Use: api.Validation, Kind: api.Cel,
			Params:     map[string]any{"check": `params.cc.startsWith("CC-") ? {} : {"cc": "Cost centre " + params.cc + " is closed."}`},
			Parameters: []api.FormLibraryParameter{{Name: "cc", Label: "Cost centre", Required: &yes}}}
		h.json("POST", "/api/v1/admin/form-library", admin, openCC, 201, nil)

		validations := []api.FormValidation{
			{Key: "budget", Title: present.Ptr("Budget"), Kind: api.Rest, Params: &map[string]any{"url": budget.URL + "/check", "method": "POST",
				"body": `{"budget": ${inputs.budget}}`}},
			{Key: "size", Kind: api.Cel, Params: &map[string]any{"check": `inputs.cpu <= 8.0 ? {} : {"cpu": "At most 8 CPUs for this team."}`}},
			{Key: "cc", Kind: api.Library, Name: present.Ptr("costCentreOpen"), Version: new(1), Params: &map[string]any{"cc": "inputs.costCentre"}},
			{Key: "pair", Kind: api.Cel, Fields: &[]string{"owner"}, Message: present.Ptr("Name someone other than yourself."),
				Params: &map[string]any{"check": `inputs.owner != user.id`}},
		}
		text := func(label string) api.UiField { return api.UiField{Widget: api.UiWidgetText, Label: label} }
		num := func(label string) api.UiField { return api.UiField{Widget: api.UiWidgetNumber, Label: label} }
		def := awxForm(map[string]api.UiField{"budget": num("Budget"), "cpu": num("CPUs"), "costCentre": text("Cost centre"), "owner": text("Owner")},
			map[string]string{"budget": "number", "cpu": "number"}, validations...)
		var it api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "checked", Name: "Checked", Summary: "Checked on submit.",
			Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &it)
		h.json("POST", "/api/v1/admin/catalog/items/checked/versions/"+it.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/checked/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: it.Id, VersionId: it.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev", Inputs: inputs}
		}

		wrong := map[string]any{"budget": 250, "cpu": 16, "costCentre": "X-9", "owner": seed.UserID(requester)}
		want := map[string]string{
			"/inputs/budget":     "Over the team's budget of 100.",
			"/inputs/cpu":        "At most 8 CPUs for this team.",
			"/inputs/costCentre": "Cost centre X-9 is closed.",
			"/inputs/owner":      "Name someone other than yourself.",
		}
		errorsOf := func(list []api.FieldError) map[string]string {
			out := map[string]string{}
			for _, e := range list {
				out[e.Field] = e.Message
			}
			return out
		}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/requests", requester, draft(wrong), 422, &problem, "Idempotency-Key", uuid.NewString())
		if got := errorsOf(problem.Errors); !maps.Equal(got, want) {
			t.Errorf("submitted wrong = %v, want %v", got, want)
		}
		var preview api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", requester, draft(wrong), 200, &preview)
		if len(preview.Errors) != 0 {
			t.Errorf("the preview ran the validations: %+v", preview.Errors)
		}
		h.json("POST", "/api/v1/requests/preview?validate=true", requester, draft(wrong), 200, &preview)
		if got := errorsOf(preview.Errors); preview.Valid || !maps.Equal(got, want) {
			t.Errorf("the review step = %v, want %v", got, want)
		}
		// The form's own checks first: a validation doesn't see a form that fails them.
		mu.Lock()
		before := calls
		mu.Unlock()
		h.json("POST", "/api/v1/requests/preview?validate=true", requester, draft(map[string]any{"budget": "lots"}), 200, &preview)
		mu.Lock()
		if calls != before {
			t.Errorf("a form failing its own checks was sent to the budget service")
		}
		mu.Unlock()

		right := map[string]any{"budget": 80, "cpu": 4, "costCentre": "CC-1001", "owner": seed.UserID(admin)}
		var req api.Request
		h.json("POST", "/api/v1/requests", requester, draft(right), 202, &req, "Idempotency-Key", uuid.NewString())

		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?action=form_validation.run&limit=200", admin, nil, 200, &audit)
		outcomes := map[string][]string{}
		for _, e := range audit.Items {
			key, _ := e.Detail["validation"].(string)
			outcome, _ := e.Detail["outcome"].(string)
			outcomes[key] = append(outcomes[key], outcome)
			if e.TargetId != it.Id || e.Project == nil || e.Project.Id != "prj_web" {
				t.Errorf("audited %+v", e)
			}
		}
		if got := outcomes["budget"]; !slices.Contains(got, "refused") || !slices.Contains(got, "ok") {
			t.Errorf("budget's runs audited %v", got)
		}
		if got := outcomes["cc"]; !slices.Contains(got, "refused") || !slices.Contains(got, "ok") {
			t.Errorf("cc's runs audited %v", got)
		}

		// A binding reads only the form's inputs. One that fails, other than
		// for an input not given yet, refuses the request: the entry never
		// passes unrun.
		validations[2].Params = &map[string]any{"cc": "inputs.costCentr"}
		def.UiSchema.Validations = &validations
		h.json("POST", "/api/v1/admin/catalog/items/checked/versions", admin, api.CatalogItemVersionCreate{Definition: &def}, 422, &problem)
		if len(problem.Errors) != 1 || !strings.HasSuffix(problem.Errors[0].Field, "/ui_schema/validations/2/params/cc") ||
			deref(problem.Errors[0].Code) != "unknown_input" {
			t.Fatalf("a binding reading an input the form doesn't have = %+v", problem.Errors)
		}
		validations[2].Params = &map[string]any{"cc": "inputs.costCentre + 1"}
		var typed api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/checked/versions", admin, api.CatalogItemVersionCreate{Definition: &def}, 201, &typed)
		h.json("POST", "/api/v1/admin/catalog/items/checked/versions/"+typed.Id+"/publish", admin, nil, 200, nil)
		d := draft(right)
		d.VersionId = typed.Id
		h.json("POST", "/api/v1/requests", requester, d, 422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/validations/cc" || deref(problem.Errors[0].Code) != "validation_failed" {
			t.Fatalf("a binding that fails = %+v", problem.Errors)
		}
		delete(d.Inputs, "costCentre")
		h.json("POST", "/api/v1/requests", requester, d, 202, &req, "Idempotency-Key", uuid.NewString())
		validations[2].Params = &map[string]any{"cc": "inputs.costCentre"}

		// A service that doesn't answer refuses the request: it never passes unchecked.
		validations[0].Params = &map[string]any{"url": downURL + "/check", "method": "POST", "body": `{"budget": ${inputs.budget}}`}
		def.UiSchema.Validations = &validations
		var v2 api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/checked/versions", admin, api.CatalogItemVersionCreate{Definition: &def}, 201, &v2)
		h.json("POST", "/api/v1/admin/catalog/items/checked/versions/"+v2.Id+"/publish", admin, nil, 200, nil)
		d = draft(right)
		d.VersionId = v2.Id
		h.json("POST", "/api/v1/requests", requester, d, 422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/validations/budget" || deref(problem.Errors[0].Code) != "validation_failed" ||
			!strings.HasPrefix(problem.Errors[0].Message, "Budget couldn't check the request (REST validation ") {
			t.Fatalf("a validation whose service is down = %+v", problem.Errors)
		}
	})
}
