package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/vra"
)

// TestVRAFormFieldTypes: a vRA form's date and time, decimal, password,
// tag, multi-select, dual list and datagrid fields import as rvav's, which
// the server accepts as they are; a secret's default stays out of the item,
// and widgets rvav has none of are reported (F1.1).
func TestVRAFormFieldTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		e := vra.SampleEstate(time.Now())
		bp, form := vra.SampleFieldTypes()
		e.Blueprints, e.Forms = append(e.Blueprints, bp), append(e.Forms, form)
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, e, 201, nil)

		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if !slices.Contains(res.Created, "Field types") || len(res.Failed) != 0 {
			t.Fatalf("import = created %v, failed %+v", res.Created, res.Failed)
		}
		i := slices.IndexFunc(res.Plan.Items, func(x api.VraItemPlan) bool { return x.BlueprintId == "bp-types" })
		plan := res.Plan.Items[i]
		for _, want := range []struct{ level, where, text string }{
			{"unmapped", "form.catalog", "valuePicker"},
			{"unmapped", "form.owners", "it is tags"},
			{"unmapped", "inputs.ports", "A list of integer values"},
			{"warning", "form.apiToken", "default isn't kept"},
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
		var v api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/"+id+"/versions/"+it.Versions[0].Id, admin, nil, 200, &v)
		widgets := map[string]api.UiWidget{}
		for name, f := range v.Definition.UiSchema.Fields {
			widgets[name] = f.Widget
		}
		for name, want := range map[string]api.UiWidget{"window": api.UiWidgetDatetime, "ratio": api.UiWidgetNumber,
			"adminPassword": api.UiWidgetSecret, "apiToken": api.UiWidgetSecret, "labels": api.UiWidgetTags,
			"features": api.UiWidgetMultiselect, "owners": api.UiWidgetTags, "disks": api.UiWidgetDatagrid, "catalog": api.UiWidgetText} {
			if widgets[name] != want {
				t.Errorf("%s is a %s field, want %s", name, widgets[name], want)
			}
		}
		if cols := v.Definition.UiSchema.Fields["disks"].Columns; cols == nil || len(*cols) != 4 || (*cols)[0].Label != "Size (GB)" {
			t.Errorf("disks columns = %+v", cols)
		}
		body, _ := json.Marshal(v)
		for _, secret := range []string{"changeme", "tok-123"} {
			if strings.Contains(string(body), secret) {
				t.Errorf("the item holds the secret default %q: %s", secret, body)
			}
		}
	})
}
