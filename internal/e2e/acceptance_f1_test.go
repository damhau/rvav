package e2e

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/vra"
)

const f1Template = `formatVersion: 1
inputs:
  environment:
    type: string
    enum: [dev, prd]
  role:
    type: string
    pattern: ^[a-z]{2,12}$
  hostName:
    type: string
  fqdn:
    type: string
  costCode:
    type: string
  application:
    type: string
  disks:
    type: array
    minItems: 1
    items:
      type: object
      required: [size]
      properties:
        size:
          type: integer
          minimum: 10
        tier:
          type: string
          enum: [gold, silver]
resources:
  Cloud_vSphere_Machine_1:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
      networks:
        - network: ${resource.Cloud_vSphere_Network_1.id}
      constraints:
        - tag: env:${input.environment}
      cloudConfig: |
        #cloud-config
        hostname: ${self.resourceName}
  Cloud_vSphere_Network_1:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
      constraints:
        - tag: net:app
`

// f1Form is vRA's custom form of f1Template: two pages, a default bound to
// the host name, a read-only cost code bound to the role, a value picker
// whose choices come from a vRO action, a table of disks, and an external
// validation of the disks.
const f1Form = `{"layout":{"pages":[
{"id":"page_server","title":"Server","sections":[
 {"id":"s_env","fields":[{"id":"environment","display":"dropDown","state":{"visible":true,"read-only":false}}]},
 {"id":"s_names","fields":[
  {"id":"role","display":"textField","state":{"visible":true,"read-only":false}},
  {"id":"hostName","display":"textField","state":{"visible":true,"read-only":false}},
  {"id":"fqdn","display":"textField","state":{"visible":true,"read-only":false}},
  {"id":"costCode","display":"textField","state":{"visible":true,"read-only":true}}]}]},
{"id":"page_app","title":"Application","sections":[
 {"id":"s_app","fields":[
  {"id":"application","display":"valuePicker","state":{"visible":true,"read-only":false}},
  {"id":"disks","display":"datagrid","state":{"visible":true,"read-only":false}}]}]}]},
"schema":{
"environment":{"label":"Environment","type":{"dataType":"string","isMultiple":false},"valueList":[{"label":"Development","value":"dev"},{"label":"Production","value":"prd"}],"constraints":{"required":true}},
"role":{"label":"Role","type":{"dataType":"string","isMultiple":false},"constraints":{"required":true,"pattern":"^[a-z]{2,12}$"}},
"hostName":{"label":"Host name","type":{"dataType":"string","isMultiple":false},"constraints":{"required":true}},
"fqdn":{"label":"FQDN","type":{"dataType":"string","isMultiple":false},"default":{"bind":{"values":["hostName","` + "`.corp.example`" + `"],"operator":"concatenate"}}},
"costCode":{"label":"Cost code","type":{"dataType":"string","isMultiple":false},"default":{"bind":{"values":["` + "`CC-`" + `","role"],"operator":"concatenate"}}},
"application":{"label":"Application","type":{"dataType":"string","isMultiple":false},"valueList":{"id":"com.corp.cmdb/findApplications","type":"scriptAction","parameters":[{"$type":"field","value":"environment"}]},"constraints":{"required":true}},
"disks":{"label":"Disks","type":{"dataType":"complex","isMultiple":true,"fields":[{"id":"size","label":"Size (GB)","type":{"dataType":"integer"}},{"id":"tier","label":"Tier","type":{"dataType":"string"}}]}}},
"options":{"externalValidations":[{"label":"Gold disks","source":{"id":"com.corp.storage/checkGoldDisks","type":"scriptAction","parameters":[{"$type":"field","value":"disks"}]},"target":["disks"]}]}}`

// TestF1Acceptance is the Forms milestone's acceptance (F1.6): a vRA custom
// form with two pages, a table, a value picker whose choices come from a
// vRO action calling a REST service, a default bound to another field and
// a read-only field bound to one, imported through the vRA import. The
// action becomes a form library entry, the value picker a search field.
// vRA's external validation, a vRO action run on submit, is reported, and
// the administrator writes it as an rvav validation (CEL) before
// publishing. The requester's submit is refused once on the disks, then
// accepted with the table's rows, the application found by search, and
// the default kept, then changed; the cost code is the rule's whatever the
// requester sends.
func TestF1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		apps := map[string][]map[string]string{
			"dev": {{"value": "app-001", "label": "Payroll"}, {"value": "app-002", "label": "Intranet"}, {"value": "app-003", "label": "Pay slips"}},
			"prd": {{"value": "app-001", "label": "Payroll"}},
		}
		cmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(apps[r.URL.Query().Get("env")])
		}))
		t.Cleanup(cmdb.Close)
		h.svc.REST.Hosts = []string{cmdb.URL}

		e := vra.SampleEstate(time.Now())
		e.Actions = append(e.Actions, vra.VROAction{ID: "act-apps", Name: "findApplications", Module: "com.corp.cmdb", Version: "1.0.0",
			OutputType: "Array/Properties", Inputs: []vra.VROParam{{Name: "environment", Type: "string"}},
			Script: `var host = RESTHostManager.createTransientHostFrom(RESTHostManager.createHost("cmdb"));
host.url = "` + cmdb.URL + `";
return JSON.parse(host.createRequest("GET", "/apps?env=" + environment).execute().contentAsString);`})
		e.Blueprints = append(e.Blueprints, vra.Blueprint{ID: "bp-f1", Name: "Application server", ProjectID: "vra-prj-web",
			Status: "RELEASED", Content: f1Template,
			Versions: []vra.BlueprintVersion{{ID: "bp-f1-v1", BlueprintID: "bp-f1", Version: "1", Status: "RELEASED", Content: f1Template}}})
		e.Forms = append(e.Forms, vra.Form{ID: "form-f1", Name: "Application server", SourceID: "bp-f1", SourceType: "com.vmw.blueprint",
			Type: "requestForm", Status: "ON", Form: f1Form})
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, e, 201, nil)

		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if !slices.Contains(res.Created, "Application server") || !slices.Contains(res.FormLibraryCreated, "findApplications") ||
			len(res.Failed) != 0 {
			t.Fatalf("import = created %v, form library %v, failed %+v", res.Created, res.FormLibraryCreated, res.Failed)
		}
		plan := res.Plan.Items[slices.IndexFunc(res.Plan.Items, func(x api.VraItemPlan) bool { return x.BlueprintId == "bp-f1" })]
		for _, want := range []struct{ level, where, text string }{
			{"info", "form", "Its pages are tabs"},
			{"info", "form.application", "a search field in rvav"},
			{"info", "form.application", "from form library entry findApplications (version 1)"},
			{"info", "form.fqdn", `its default rule is inputs.hostName + ".corp.example"`},
			{"info", "form.costCode", "its value rule is"},
			{"unmapped", "form.validations", `"Gold disks" on submit with action com.corp.storage/checkGoldDisks`},
		} {
			if !slices.ContainsFunc(plan.Notes, func(n api.VraNote) bool {
				return string(n.Level) == want.level && n.Where == want.where && strings.Contains(n.Text, want.text)
			}) {
				t.Errorf("no %s note at %s with %q: %+v", want.level, want.where, want.text, plan.Notes)
			}
		}

		id := deref(plan.RvavItemId)
		var it api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/"+id, admin, nil, 200, &it)
		var draft api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/"+id+"/versions/"+it.Versions[0].Id, admin, nil, 200, &draft)
		ui := draft.Definition.UiSchema
		var tabs []string
		for _, s := range ui.Layout.Sections {
			tabs = append(tabs, s.Title)
		}
		if string(ui.Layout.Kind) != "tabs" || len(tabs) < 2 || tabs[0] != "Server" || tabs[1] != "Application" {
			t.Fatalf("layout = %s %v", ui.Layout.Kind, tabs)
		}
		app := ui.Fields["application"]
		if app.Widget != api.UiWidgetSearch || app.ValueSource == nil || app.ValueSource.Kind != api.Library ||
			deref(app.ValueSource.Name) != "findApplications" || (*app.ValueSource.Params)["environment"] != "environment" {
			t.Fatalf("application = %+v", app)
		}
		if ui.Fields["disks"].Widget != api.UiWidgetDatagrid {
			t.Fatalf("disks = %+v", ui.Fields["disks"])
		}

		// vRA's external validation, written as the form's own.
		def := draft.Definition
		def.UiSchema.Validations = &[]api.FormValidation{{Key: "gold_disks", Title: new("Gold disks"), Kind: api.Cel,
			Params: &map[string]any{"check": `has(inputs.disks) && inputs.disks.exists(d, has(d.tier) && d.tier == "gold" && double(d.size) > 100.0)` +
				` ? {"disks": "A gold disk is at most 100 GB: ask the storage team for a larger one."} : {}`}}}
		h.json("PUT", "/api/v1/admin/catalog/items/"+id+"/versions/"+draft.Id, admin,
			api.CatalogItemVersionUpdate{Definition: def, BaseRevision: &draft.Revision}, 200, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/"+id+"/versions/"+draft.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/"+id+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		found := func(env, text string) []string {
			t.Helper()
			var out api.FieldValues
			h.json("POST", "/api/v1/forms/values", requester, api.FieldValuesQuery{VersionId: draft.Id, ProjectId: "prj_web",
				Environment: new(api.EnvironmentKey(env)), Field: "application", Inputs: map[string]any{}, Search: &text}, 200, &out)
			var got []string
			for _, o := range *out.Options {
				got = append(got, o.Value+" "+o.Label)
			}
			return got
		}
		if got, want := found("dev", "pay"), []string{"app-001 Payroll", "app-003 Pay slips"}; !slices.Equal(got, want) {
			t.Errorf("searching applications in dev for pay: %v, want %v", got, want)
		}
		if got, want := found("prd", "pay"), []string{"app-001 Payroll"}; !slices.Equal(got, want) {
			t.Errorf("searching applications in prd for pay: %v, want %v", got, want)
		}

		disks := []any{map[string]any{"size": 50, "tier": "gold"}, map[string]any{"size": 20, "tier": "silver"}}
		draftOf := func(inputs map[string]any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: it.Id, VersionId: draft.Id, ProjectId: "prj_web", Environment: "dev", Inputs: inputs}
		}
		inputs := map[string]any{"role": "api", "hostName": "web01", "costCode": "CC-free", "application": "app-003", "disks": disks}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", requester, draftOf(inputs), 200, &pv)
		if !pv.Valid || pv.Fields == nil {
			t.Fatalf("preview: valid %v, errors %+v", pv.Valid, pv.Errors)
		}
		if fqdn, cost := (*pv.Fields)["fqdn"], (*pv.Fields)["costCode"]; fqdn.Default != "web01.corp.example" || fqdn.ReadOnly ||
			cost.Value != "CC-api" || !cost.ReadOnly {
			t.Fatalf("fqdn = %+v, costCode = %+v", fqdn, cost)
		}

		big := maps.Clone(inputs)
		big["disks"] = []any{map[string]any{"size": 200, "tier": "gold"}}
		const message = "A gold disk is at most 100 GB: ask the storage team for a larger one."
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/requests", requester, draftOf(big), 422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/inputs/disks" || problem.Errors[0].Message != message {
			t.Fatalf("a 200 GB gold disk = %+v", problem.Errors)
		}
		h.json("POST", "/api/v1/requests/preview?validate=true", requester, draftOf(big), 200, &pv)
		if pv.Valid || len(pv.Errors) != 1 || pv.Errors[0].Field != "/inputs/disks" || pv.Errors[0].Message != message {
			t.Fatalf("the review step with a 200 GB gold disk = %v %+v", pv.Valid, pv.Errors)
		}

		stored := func(inputs map[string]any) map[string]any {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", requester, draftOf(inputs), 202, &req, "Idempotency-Key", uuid.NewString())
			h.json("GET", "/api/v1/requests/"+req.Id, requester, nil, 200, &req)
			if req.Inputs == nil {
				t.Fatalf("request %s has no inputs", req.Id)
			}
			return *req.Inputs
		}
		kept := stored(inputs)
		rows, _ := json.Marshal(kept["disks"])
		if string(rows) != `[{"size":50,"tier":"gold"},{"size":20,"tier":"silver"}]` || kept["application"] != "app-003" ||
			kept["fqdn"] != "web01.corp.example" || kept["costCode"] != "CC-api" {
			t.Errorf("the default kept: inputs %v, disks %s", kept, rows)
		}
		changed := maps.Clone(inputs)
		changed["fqdn"] = "web01.dmz.example"
		if got := stored(changed); got["fqdn"] != "web01.dmz.example" || got["costCode"] != "CC-api" {
			t.Errorf("the default changed: inputs %v", got)
		}
	})
}
