package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/vra"
)

// TestVRAFormLibrary: the vRO action a vRA form takes a field's choices
// from, calling a REST service, becomes a form library entry once rvav may
// call its host; the imported item's field names it, and gives the
// service's choices. The others are reported with the action's name
// (F1.5, ADR-0043).
func TestVRAFormLibrary(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester = "mkeller", "tnguyen"
		dbaas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string][]string{"dev": {"15", "16"}, "prd": {"15"}}[r.URL.Query().Get("env")])
		}))
		t.Cleanup(dbaas.Close)

		e := vra.SampleEstate(time.Now())
		e.Actions[0].Script = `var host = RESTHostManager.createTransientHostFrom(RESTHostManager.createHost("dbaas"));
host.url = "` + dbaas.URL + `";
return JSON.parse(host.createRequest("GET", "/versions?env=" + environment).execute().contentAsString);`
		bp, form := vra.SampleFieldTypes()
		e.Blueprints, e.Forms = append(e.Blueprints, bp), append(e.Forms, form)
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, e, 201, nil)

		entry := func(p api.VraPlan, key string) api.VraFormLibraryPlan {
			t.Helper()
			i := slices.IndexFunc(p.FormLibrary, func(x api.VraFormLibraryPlan) bool { return x.Key == key })
			if i < 0 {
				t.Fatalf("no %s in the plan: %+v", key, p.FormLibrary)
			}
			return p.FormLibrary[i]
		}
		noted := func(notes []api.VraNote, level, where, text string) bool {
			return slices.ContainsFunc(notes, func(n api.VraNote) bool {
				return string(n.Level) == level && n.Where == where && strings.Contains(n.Text, text)
			})
		}
		var p api.VraPlan
		h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
		if v := entry(p, "getVersions"); v.Action != api.VraFormLibraryPlanActionSkip || !noted(v.Notes, "unmapped", "script", "rvav may not call 127.0.0.1:") {
			t.Fatalf("a host rvav may not call = %+v", v)
		}
		if u := entry(p, "getUsers"); u.VroAction != "com.corp.ad/getUsers" || u.Action != api.VraFormLibraryPlanActionSkip {
			t.Fatalf("an action vRO doesn't have = %+v", u)
		}

		h.svc.REST.Hosts = []string{dbaas.URL}
		h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
		v := entry(p, "getVersions")
		if v.Action != api.VraFormLibraryPlanActionCreate || v.Version != 1 || deref(v.Url) != dbaas.URL+"/versions?env=${params.environment}" ||
			!slices.Equal(v.Parameters, []string{"environment"}) || p.Summary.FormLibraryCreate != 1 {
			t.Fatalf("getVersions = %+v, summary %+v", v, p.Summary)
		}

		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if !slices.Equal(res.FormLibraryCreated, []string{"getVersions"}) || len(res.Failed) != 0 {
			t.Fatalf("import = form library %v, failed %+v", res.FormLibraryCreated, res.Failed)
		}
		if v := entry(res.Plan, "getVersions"); v.Action != api.VraFormLibraryPlanActionUnchanged || v.Version != 1 {
			t.Fatalf("after the import = %+v", v)
		}
		items := map[string]api.VraItemPlan{}
		for _, it := range res.Plan.Items {
			items[it.BlueprintId] = it
		}
		if db := items["bp-db"]; !noted(db.Notes, "info", "form.dbVersion", "from form library entry getVersions (version 1)") ||
			noted(db.Notes, "unmapped", "form.dbVersion", "") {
			t.Errorf("dbVersion notes = %+v", db.Notes)
		}
		if types := items["bp-types"]; !noted(types.Notes, "unmapped", "form.owners", "com.corp.ad/getUsers in vRA") ||
			!noted(types.Notes, "unmapped", "form.owners", "no form library entry of it: vRO has no such action") {
			t.Errorf("owners notes = %+v", types.Notes)
		}

		var saved api.FormLibraryEntry
		h.json("GET", "/api/v1/admin/form-library/getVersions", admin, nil, 200, &saved)
		if saved.Kind != api.Rest || saved.Use != api.FormLibraryPurposeSource || saved.Params["url"] != dbaas.URL+"/versions?env=${params.environment}" ||
			len(saved.UsedBy) != 1 || saved.UsedBy[0].State != "draft" {
			t.Fatalf("the entry = %+v", saved)
		}
		id := deref(items["bp-db"].RvavItemId)
		var it api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/"+id, admin, nil, 200, &it)
		var draft api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/"+id+"/versions/"+it.Versions[0].Id, admin, nil, 200, &draft)
		f := draft.Definition.UiSchema.Fields["dbVersion"]
		if f.Widget != api.UiWidgetSelect || f.ValueSource == nil || f.ValueSource.Kind != api.Library || deref(f.ValueSource.Name) != "getVersions" ||
			f.ValueSource.Version == nil || *f.ValueSource.Version != 1 || (*f.ValueSource.Params)["environment"] != "environment" {
			t.Fatalf("dbVersion = %+v", f)
		}

		var out api.FieldValues
		h.json("POST", "/api/v1/forms/values", admin, api.FieldValuesQuery{VersionId: draft.Id, ProjectId: "prj_web",
			Environment: new(api.EnvironmentKey("dev")), Field: "dbVersion", Inputs: map[string]any{}, AsUserId: new(seed.UserID(requester))}, 200, &out)
		var got []string
		for _, o := range *out.Options {
			got = append(got, o.Value)
		}
		if !slices.Equal(got, []string{"15", "16"}) {
			t.Fatalf("dbVersion's choices in dev = %v", got)
		}

		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?action=form_library.create", admin, nil, 200, &audit)
		if len(audit.Items) != 1 || audit.Items[0].TargetId != "getVersions" {
			t.Fatalf("audit = %+v", audit.Items)
		}
	})
}
