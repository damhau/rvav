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
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/vra"
)

// TestVRAImportSharing: a vRA content sharing policy that shares the
// Windows item in Data Services with Sophie, a group and roles becomes the
// item's sharing in Data Engineering (G1.2): Sophie and the group, not the
// other members; what doesn't map is in the plan's notes. Web Platform,
// entitled in vRA, has it for all its members.
func TestVRAImportSharing(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, web, data = "mkeller", "prj_web", "prj_data"
		if err := h.store.SetMemberRoles(ctx, data, seed.UserID("tnguyen"), []string{"member"}); err != nil {
			t.Fatal(err)
		}
		e := vra.SampleEstate(time.Now())
		def, _ := json.Marshal(vra.SharingDefinition{EntitledUsers: []vra.EntitledUsers{{UserType: "USER",
			Principals: []vra.SharePrincipal{{Type: "USER", ReferenceID: "sophie.brunner@corp.example"},
				{Type: "USER", ReferenceID: "gone@corp.example"}, {Type: "GROUP", ReferenceID: "rvav-dba"},
				{Type: "ROLE", ReferenceID: "auditor"}},
			Items: []vra.SharedReference{{Type: "CATALOG_ITEM_IDENTIFIER", ID: "bp-win"}}}}})
		e.Policies = append(e.Policies, vra.Policy{ID: "pol-share-win", Name: "Windows for the DBAs", TypeID: vra.PolicySharing,
			EnforcementType: "HARD", ProjectID: "vra-prj-data", Definition: def})
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, e, 201, nil)
		h.json("PUT", "/api/v1/admin/vra/projects/vra-prj-data", admin, api.VraProjectMapping{RvavProjectId: ptr(data)}, 204, nil)

		item := func(p api.VraPlan, id string) api.VraItemPlan {
			t.Helper()
			return p.Items[slices.IndexFunc(p.Items, func(x api.VraItemPlan) bool { return x.BlueprintId == id })]
		}
		var p api.VraPlan
		h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
		win := item(p, "bp-win")
		dba := api.CatalogProjectAudience{ProjectId: data, UserIds: []string{seed.UserID("sbrunner")}, Groups: []string{"rvav-dba"}, Roles: []string{}}
		if !slices.Equal(win.ShareAdd, []string{data, web}) || win.ShareAudiences == nil || len(*win.ShareAudiences) != 1 ||
			!slices.Equal((*win.ShareAudiences)[0].UserIds, dba.UserIds) || !slices.Equal((*win.ShareAudiences)[0].Groups, dba.Groups) {
			t.Fatalf("windows plan: add %v, audiences %+v", win.ShareAdd, win.ShareAudiences)
		}
		var unmapped []string
		for _, n := range win.Notes {
			if n.Where == "sharing" && n.Level == "unmapped" {
				unmapped = append(unmapped, n.Text)
			}
		}
		if len(unmapped) != 2 || !strings.Contains(unmapped[0], "gone@corp.example") || !strings.Contains(unmapped[1], "auditor") {
			t.Fatalf("unmapped = %q", unmapped)
		}
		if i := slices.IndexFunc(p.Policies, func(x api.VraPolicyPlan) bool { return x.VraId == "pol-share-win" }); i < 0 || p.Policies[i].Type != "sharing" {
			t.Fatalf("policies = %+v", p.Policies)
		}

		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		id := deref(item(res.Plan, "bp-win").RvavItemId)
		var ents api.CatalogEntitlements
		h.json("GET", "/api/v1/admin/catalog/items/"+id+"/entitlements", admin, nil, 200, &ents)
		if !slices.Equal(ents.ProjectIds, []string{data, web}) || len(*ents.Audiences) != 1 || (*ents.Audiences)[0].ProjectId != data ||
			!slices.Equal((*ents.Audiences)[0].UserIds, dba.UserIds) || !slices.Equal((*ents.Audiences)[0].Groups, dba.Groups) {
			t.Fatalf("windows sharing = %+v", ents)
		}
		var it api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/"+id, admin, nil, 200, &it)
		publishDraft(h, api.CatalogImportResult{ItemId: id, Versions: it.Versions})

		for _, c := range []struct {
			user, project string
			shared        bool
		}{{"sbrunner", data, true}, {"tnguyen", data, false}, {"tnguyen", web, true}} {
			var page api.CatalogItemPage
			h.json("GET", "/api/v1/catalog/items?limit=200&project_id="+c.project, c.user, nil, 200, &page)
			want := 404
			if c.shared {
				want = 200
			}
			if slices.ContainsFunc(page.Items, func(i api.CatalogItem) bool { return i.Id == id }) != c.shared {
				t.Fatalf("%s in %s: listed %v", c.user, c.project, !c.shared)
			}
			h.json("GET", "/api/v1/catalog/items/"+it.Slug+"?project_id="+c.project, c.user, nil, want, nil)
		}

		// Again: nothing more is shared, and an administrator's change stays.
		h.json("PUT", "/api/v1/admin/catalog/items/"+id+"/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{data, web},
			Audiences: &[]api.CatalogProjectAudience{{ProjectId: data, UserIds: []string{}, Groups: []string{}, Roles: []string{"project-admin"}}}}, 200, nil)
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		h.json("GET", "/api/v1/admin/catalog/items/"+id+"/entitlements", admin, nil, 200, &ents)
		if res.Shared != 0 || len(*ents.Audiences) != 1 || !slices.Equal((*ents.Audiences)[0].Roles, []string{"project-admin"}) {
			t.Fatalf("again: shared %d, sharing %+v", res.Shared, ents)
		}
	})
}
