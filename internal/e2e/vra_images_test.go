package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/vra"
)

// TestVRAImageMappings: vRA's image mappings, a template per region, are
// catalogue templates with a source in each region's vCenter, their
// cloud-init and constraints; the item of an image that is more than a
// template's name binds it. Run again, nothing changes, and a template
// rvav has is never changed (T1.4, ADR-0037).
func TestVRAImageMappings(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		zurichVCenter(t, h, ctx)
		e := vra.SampleEstate(time.Now())
		var snap api.VraSnapshot
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, e, 201, &snap)

		// An admin's own ubuntu24, unlike vRA's: left as it is.
		h.json("POST", "/api/v1/admin/templates", admin, api.VmTemplateWrite{Key: "ubuntu24", Label: "Ubuntu 24.04",
			OsFamily: api.VmTemplateWriteOsFamilyLinux, Sources: sourceOn("", "tpl-ubuntu24.04-ours"), Offers: &[]api.Offer{{}}}, 201, nil)

		var p api.VraPlan
		h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
		plans := map[string]api.VraTemplatePlan{}
		for _, x := range p.Templates {
			plans[x.Key] = x
		}
		win := plans["win2022"]
		if p.Summary.TemplatesCreate != 2 || len(p.Templates) != 3 || plans["ubuntu24"].Action != api.VraTemplatePlanActionDiffers ||
			win.Action != api.VraTemplatePlanActionCreate || len(win.Sources) != 2 || win.Sources[0].Vcenter != "vc01.corp.example" ||
			win.Sources[1].Template != "tpl-win2025-std-zrh" || !slices.Equal(win.Constraints, []string{"license/windows"}) {
			t.Fatalf("plan = %+v / %+v", p.Summary, p.Templates)
		}
		i := slices.IndexFunc(p.Items, func(x api.VraItemPlan) bool { return x.BlueprintId == "bp-win" })
		if i < 0 || !slices.ContainsFunc(p.Items[i].Notes, func(n api.VraNote) bool { return strings.Contains(n.Text, "catalogue template win2022") }) {
			t.Fatalf("windows item = %+v", p.Items)
		}

		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if !slices.Equal(res.TemplatesCreated, []string{"rhel9", "win2022"}) || len(res.Failed) != 0 || res.Plan.Summary.TemplatesCreate != 0 {
			t.Fatalf("import = %+v / %+v", res.TemplatesCreated, res.Failed)
		}
		var list struct{ Items []api.VmTemplate }
		h.json("GET", "/api/v1/admin/templates", admin, nil, 200, &list)
		got := map[string]api.VmTemplate{}
		for _, x := range list.Items {
			got[x.Key] = x
		}
		if w := got["win2022"]; w.OsFamily != api.VmTemplateOsFamilyWindows || len(w.Sources) != 2 || w.Sources[1].Vcenter != vc02 ||
			!slices.Equal(w.Constraints.Compute, []string{"license/windows"}) || len(w.Offers) != 1 || w.Offers[0].ProjectId != nil {
			t.Fatalf("win2022 = %+v", w)
		}
		if u := got["ubuntu24"]; len(u.Sources) != 1 || u.Sources[0].Template != "tpl-ubuntu24.04-ours" || u.UserData != "" {
			t.Fatalf("ubuntu24 changed = %+v", u)
		}

		// Again: the templates are what vRA's mappings give.
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		for _, x := range res.Plan.Templates {
			if x.Key != "ubuntu24" && x.Action != api.VraTemplatePlanActionUnchanged {
				t.Fatalf("again = %+v", x)
			}
		}
		if len(res.TemplatesCreated) != 0 || len(res.Failed) != 0 {
			t.Fatalf("again = %+v", res)
		}
	})
}
