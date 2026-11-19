package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
)

// TestInfiniteRule: a rule that divides by zero fails on its field: a
// default gives none, and a computed value refuses the submit, rather
// than answer 500 (F1.6).
func TestInfiniteRule(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		num := func(label string) api.UiField { return api.UiField{Widget: api.UiWidgetNumber, Label: label} }
		ratio, share := num("Ratio"), num("Share")
		ratio.Rules = &api.UiRules{Default: new(`double(inputs.a) / double(inputs.b)`)}
		share.Rules = &api.UiRules{Value: new(`100.0 * double(inputs.a) / double(inputs.b)`)}
		types := map[string]string{"a": "number", "b": "number", "ratio": "number", "share": "number"}
		for _, c := range []struct {
			slug   string
			fields map[string]api.UiField
			submit int
		}{
			{"ratio", map[string]api.UiField{"a": num("A"), "b": num("B"), "ratio": ratio}, 202},
			{"share", map[string]api.UiField{"a": num("A"), "b": num("B"), "share": share}, 422},
		} {
			def := awxForm(c.fields, map[string]string{"a": "number", "b": "number", c.slug: types[c.slug]})
			var it api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", "lmartin", api.CatalogItemCreate{Slug: c.slug, Name: c.slug, Summary: "s",
				Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &it)
			h.json("POST", "/api/v1/admin/catalog/items/"+c.slug+"/versions/"+it.DraftVersion.Id+"/publish", "lmartin", nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+c.slug+"/entitlements", "lmartin",
				api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			d := api.RequestDraft{CatalogItemId: it.Id, VersionId: it.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"a": 1, "b": 0}}
			var p api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", "tnguyen", d, 200, &p)
			if st := (*p.Fields)[c.slug]; st.Error == nil || !strings.Contains(*st.Error, "isn't a number") || st.Default != nil || st.Value != nil {
				t.Errorf("%s: preview state = %+v", c.slug, st)
			}
			code, body, _ := h.do("POST", "/api/v1/requests", "tnguyen", d, "Idempotency-Key", uuid.NewString())
			if code != c.submit {
				t.Errorf("%s: submit = %d, want %d: %s", c.slug, code, c.submit, body)
			}
			if c.submit == 422 && !strings.Contains(string(body), `"/inputs/share"`) {
				t.Errorf("share: the refusal names no field: %s", body)
			}
			if c.submit == 202 && !strings.Contains(string(body), `"inputs":{"a":1,"b":0}`) {
				t.Errorf("ratio: the request's inputs aren't a and b alone: %s", body)
			}
		}
	})
}

// TestDefaultRuleBeatsSuggestion: a dropdown whose source suggests an
// option (the datastore with the most room) takes its default rule's
// instead; one without a rule keeps the suggestion (F1.6).
func TestDefaultRuleBeatsSuggestion(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		def := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		def.UiSchema.Fields["datastore"] = api.UiField{Widget: api.UiWidgetVsphereDatastore, Label: "Datastore",
			DependsOn:   &[]string{"cluster"},
			ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("vsphere.datastores"), Params: &map[string]any{"cluster": "${inputs.cluster}"}}}
		plain := publishItem(h, "ds-suggested", def)
		ruled := def.UiSchema.Fields["datastore"]
		ruled.Rules = &api.UiRules{Default: new(`"chosen-by-rule"`)}
		def.UiSchema.Fields["datastore"] = ruled
		withRule := publishItem(h, "ds-ruled", def)
		dev := api.EnvironmentKey("dev")
		for _, c := range []struct {
			item    api.AdminCatalogItem
			suggest bool
		}{{plain, true}, {withRule, false}} {
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", "tnguyen", api.FieldValuesQuery{VersionId: c.item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: &dev, Field: "datastore", Inputs: map[string]any{"cluster": "GVA-DEV-CL01"}}, 200, &out)
			if out.Options == nil || len(*out.Options) == 0 || (out.Default != nil) != c.suggest {
				t.Errorf("%s: options %v, suggested %v", c.item.Slug, out.Options, out.Default)
			}
		}
		var p api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", api.RequestDraft{CatalogItemId: withRule.Id, VersionId: withRule.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"cluster": "GVA-DEV-CL01"}}, 200, &p)
		if p.Fields == nil || (*p.Fields)["datastore"].Default != "chosen-by-rule" {
			t.Errorf("the rule's default in the preview = %+v", p.Fields)
		}
	})
}

// TestFormDefaults: a default rule fills a field from the others while it
// has no input, and stays editable; a request without the input gets the
// default, and one that emptied it (null) none. A CEL source gives a
// number field its default and a computed field its value, which the
// server works out whatever the client sends (F1.4).
func TestFormDefaults(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		text := map[string]any{"type": "string"}
		cores := map[string]any{"value": `environment == "prd" ? 4 : 2`}
		tag := map[string]any{"value": `"tag-" + inputs.app`}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []any{"app"}, "properties": map[string]any{
				"app": text, "host": text, "fqdn": text, "ticket": text, "tag": text, "cores": map[string]any{"type": "integer"}}},
			UiSchema: api.UiSchema{Fields: map[string]api.UiField{
				"app":    {Widget: api.UiWidgetText, Label: "Application"},
				"host":   {Widget: api.UiWidgetText, Label: "Host", Rules: &api.UiRules{Default: new(`inputs.app + "-" + environment`)}},
				"fqdn":   {Widget: api.UiWidgetText, Label: "FQDN", Rules: &api.UiRules{Default: new(`inputs.host + ".corp.example"`)}},
				"ticket": {Widget: api.UiWidgetText, Label: "Ticket", Rules: &api.UiRules{ReadOnly: new("true"), Default: new(`"CHG-" + inputs.app`)}},
				"cores": {Widget: api.UiWidgetNumber, Label: "Cores", DependsOn: &[]string{"$environment"},
					ValueSource: &api.ValueSource{Kind: api.Cel, Params: &cores}},
				"tag": {Widget: api.UiWidgetComputed, Label: "Tag", DependsOn: &[]string{"app"},
					ValueSource: &api.ValueSource{Kind: api.Cel, Params: &tag}},
			}},
			InitialValues: map[string]any{"host": "fixed"},
			Blueprint:     map[string]any{"kind": "awx", "awx": map[string]any{"title": "Register", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Host", Fields: []string{"app", "host", "fqdn", "ticket", "cores", "tag"}}}
		create := api.CatalogItemCreate{Slug: "host-register", Name: "Register a host", Summary: "Defaults from the application.",
			Category: "compute", Type: api.AwxJobTemplate, Definition: &def}

		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 422, &refused)
		if len(refused.Errors) != 1 || refused.Errors[0].Field != "/definition/ui_schema/fields/host/rules/default" || deref(refused.Errors[0].Code) != "conflict" {
			t.Fatalf("a default rule and an initial value: %+v", refused.Errors)
		}
		def.InitialValues = map[string]any{}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/host-register/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/host-register/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev", Inputs: inputs}
		}
		preview := func(inputs map[string]any) api.FieldStates {
			t.Helper()
			var out api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", requester, draft(inputs), 200, &out)
			if !out.Valid || out.Fields == nil {
				t.Fatalf("preview of %v = %+v", inputs, out)
			}
			return *out.Fields
		}
		st := preview(map[string]any{"app": "web"})
		if st["host"].Default != "web-dev" || st["fqdn"].Default != "web-dev.corp.example" || st["ticket"].Default != "CHG-web" ||
			st["cores"].Default != float64(2) || st["tag"].Value != "tag-web" || st["host"].ReadOnly {
			t.Fatalf("defaults = %+v", st)
		}
		// An edited field keeps its value; the defaults that read it follow it.
		if st := preview(map[string]any{"app": "web", "host": "mine"}); st["fqdn"].Default != "mine.corp.example" || st["host"].Default != "web-dev" {
			t.Fatalf("with host edited = %+v", st)
		}

		submit := func(inputs map[string]any) map[string]any {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", requester, draft(inputs), 202, &req, "Idempotency-Key", uuid.NewString())
			return *req.Inputs
		}
		got := submit(map[string]any{"app": "web", "ticket": "CHG-other", "tag": "forged"})
		if got["host"] != "web-dev" || got["fqdn"] != "web-dev.corp.example" || got["ticket"] != "CHG-web" || got["cores"] != float64(2) || got["tag"] != "tag-web" {
			t.Fatalf("a request without the inputs = %v", got)
		}
		got = submit(map[string]any{"app": "web", "host": nil, "cores": 8})
		if _, ok := got["host"]; ok || got["cores"] != float64(8) {
			t.Fatalf("a request that emptied host and changed cores = %v", got)
		}
		if _, ok := got["fqdn"]; ok {
			t.Fatalf("fqdn's default reads host, emptied: %v", got)
		}

		// The designer's preview works the defaults out the same way.
		var designed api.DesignerPreview
		env := api.EnvironmentKey("dev")
		h.json("POST", "/api/v1/admin/catalog/items/host-register/versions/"+item.DraftVersion.Id+"/preview", admin,
			api.DesignerPreviewQuery{ProjectId: "prj_web", Environment: &env, Inputs: map[string]any{"app": "db"}}, 200, &designed)
		if designed.Fields["host"].Default != "db-dev" || designed.Fields["tag"].Value != "tag-db" {
			t.Fatalf("designer preview = %+v", designed.Fields)
		}
	})
}

// TestRESTSingleValues: a REST source gives a text or number field its
// default and a computed field its value, through the same machinery as
// options: allowed hosts, a stored secret, JSONPath and the cache. A source
// that fails gives no default and holds nothing up (F1.4).
func TestRESTSingleValues(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "lmartin", "tnguyen"
		var calls atomic.Int32
		cmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Header.Get("Authorization") != "Bearer cmdb-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			app := r.URL.Query().Get("app")
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/owners":
				_, _ = fmt.Fprintf(w, `{"data": [{"login": "%s-owner", "cores": 4}, {"login": "spare"}]}`, app)
			case "/next":
				_, _ = fmt.Fprintf(w, `"%s-07"`, app)
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer cmdb.Close()
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb", Kind: api.SecretWriteKindToken, Value: new("cmdb-token")}, 201, nil)

		rest := func(path, items, value string) *api.ValueSource {
			params := map[string]any{"url": cmdb.URL + path + "?app=${inputs.app}", "secret": "cmdb"}
			if items != "" {
				params["items"] = items
			}
			if value != "" {
				params["value"] = value
			}
			return &api.ValueSource{Kind: api.Rest, Params: &params}
		}
		text := map[string]any{"type": "string"}
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "properties": map[string]any{
				"app": text, "owner": text, "serial": text, "broken": text, "cores": map[string]any{"type": "integer"}}},
			UiSchema: api.UiSchema{Fields: map[string]api.UiField{
				"app":    {Widget: api.UiWidgetText, Label: "Application"},
				"owner":  {Widget: api.UiWidgetText, Label: "Owner", DependsOn: &[]string{"app"}, ValueSource: rest("/owners", "$.data", "$.login")},
				"cores":  {Widget: api.UiWidgetNumber, Label: "Cores", DependsOn: &[]string{"app"}, ValueSource: rest("/owners", "$.data", "$.cores")},
				"serial": {Widget: api.UiWidgetComputed, Label: "Serial", DependsOn: &[]string{"app"}, ValueSource: rest("/next", "", "")},
				"broken": {Widget: api.UiWidgetText, Label: "Broken", DependsOn: &[]string{"app"}, ValueSource: rest("/down", "", "")},
			}},
			Blueprint: map[string]any{"kind": "awx", "awx": map[string]any{"title": "Register", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Owner", Fields: []string{"app", "owner", "cores", "serial", "broken"}}}
		create := api.CatalogItemCreate{Slug: "owner-register", Name: "Register an owner", Summary: "Defaults from the CMDB.",
			Category: "compute", Type: api.AwxJobTemplate, Definition: &def}
		var refused struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 422, &refused)
		if len(refused.Errors) != 4 || !strings.HasSuffix(refused.Errors[0].Field, "/value_source/params/url") {
			t.Fatalf("hosts not allowed = %+v", refused.Errors)
		}
		h.svc.REST.Hosts = []string{"http://" + strings.TrimPrefix(cmdb.URL, "http://")}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, create, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/owner-register/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/owner-register/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		var owner api.FieldValues
		h.json("POST", "/api/v1/forms/values", requester, api.FieldValuesQuery{VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
			Environment: new(api.EnvironmentKey("dev")), Field: "owner", Inputs: map[string]any{"app": "web"}}, 200, &owner)
		if owner.Value != "web-owner" || owner.Options != nil {
			t.Fatalf("a text field's REST value = %+v", owner)
		}

		draft := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev", Inputs: inputs}
		}
		before := calls.Load()
		var preview api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", requester, draft(map[string]any{"app": "web"}), 200, &preview)
		st := *preview.Fields
		if !preview.Valid || st["owner"].Default != "web-owner" || st["cores"].Default != float64(4) || st["serial"].Value != "web-07" ||
			st["broken"].Default != nil || st["broken"].Error == nil {
			t.Fatalf("preview = %+v", preview)
		}
		// owner's answer is cached; cores reads the same call another way, serial and broken call.
		if n := calls.Load() - before; n != 3 {
			t.Fatalf("the preview called the CMDB %d times", n)
		}

		var req api.Request
		h.json("POST", "/api/v1/requests", requester, draft(map[string]any{"app": "web", "serial": "forged", "cores": 2}), 202, &req, "Idempotency-Key", uuid.NewString())
		got := *req.Inputs
		if got["owner"] != "web-owner" || got["cores"] != float64(2) || got["serial"] != "web-07" {
			t.Fatalf("submitted = %v", got)
		}
		if _, ok := got["broken"]; ok {
			t.Fatalf("a failed source gave a value: %v", got)
		}
	})
}

// TestApproverEditKeepsEmptied: an approver's edit evaluates the request's
// inputs again, and a default the requester emptied stays empty, in what
// is stored and in what AWX gets.
func TestApproverEditKeepsEmptied(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-emptied-e2e"
		h.svc.Workflows = &provision.Temporal{Client: ts.Client(), TaskQueue: taskQueue}
		w := provision.NewWorker(ts.Client(), taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		const admin = "lmartin"
		def := awxForm(map[string]api.UiField{
			"role":  {Widget: api.UiWidgetText, Label: "Role", ApproverEditable: new(true)},
			"proxy": {Widget: api.UiWidgetText, Label: "Proxy", Rules: &api.UiRules{Default: new(`"proxy.corp:3128"`)}},
		}, nil)
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pg-proxy", Name: "Role", Summary: "A role.",
			Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/pg-proxy/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/pg-proxy/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		h.json("POST", "/api/v1/admin/approval-policies", "mkeller", api.ApprovalPolicyWrite{Name: "Roles in production",
			Condition: `environment == "prd"`,
			Levels:    []api.ApprovalLevel{{Name: "Infrastructure leads", Group: new("rvav-infra-leads")}}, Priority: new(5)}, 201, nil)

		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "prd", Inputs: map[string]any{"role": "app", "proxy": nil},
			Justification: new("A role.")}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusPendingApproval)
		var level string
		for deadline := time.Now().Add(30 * time.Second); level == ""; time.Sleep(50 * time.Millisecond) {
			_ = h.store.Pool.QueryRow(ctx, `select id from approvals where request_id = $1 and status = 'pending'`, req.Id).Scan(&level)
			if time.Now().After(deadline) {
				t.Fatal("no approval level opened")
			}
		}
		h.json("POST", "/api/v1/approvals/"+level+"/decision", "mkeller", api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove,
			Inputs: &map[string]any{"role": "ops"}}, 200, nil)
		done := h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		if got := *done.Inputs; got["role"] != "ops" || got["proxy"] != nil {
			t.Fatalf("stored after the edit: %v", got)
		}
		step, err := h.store.GetStepByKey(ctx, req.Id, "awx.job:pg-create-role")
		if err != nil || step.ExternalID == nil {
			t.Fatalf("job step = %+v, %v", step, err)
		}
		var jobID int
		_, _ = fmt.Sscan(*step.ExternalID, &jobID)
		if vars := h.awx.Launched(jobID).ExtraVars; vars["role"] != "ops" || vars["proxy"] != nil {
			t.Errorf("AWX got %v", vars)
		}
	})
}
