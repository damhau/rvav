package e2e

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/vra"
)

// TestVRAImport: the importer through its API (P2.5). vRA read, or the
// file vra-export wrote; the dry run writes nothing; a vRA project mapped;
// the import makes the items as drafts with their sharing; run again it
// changes nothing; a template's new version gives its item a new draft,
// and one more while that draft is open is a conflict; deployments are
// adopted once their item is published, and refused while it isn't.
func TestVRAImport(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		e := vra.SampleEstate(time.Now())

		var st api.VraImportStatus
		h.json("GET", "/api/v1/admin/vra", "tnguyen", nil, 403, nil)
		h.json("GET", "/api/v1/admin/vra", admin, nil, 200, &st)
		if st.Configured || st.Snapshot != nil {
			t.Fatalf("status = %+v", st)
		}
		h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 404, nil)
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, nil, 422, nil)
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, map[string]any{"hello": "world"}, 422, nil)

		// Read from vRA itself (the fake), then from vra-export's file.
		fake := httptest.NewServer(vra.NewFakeServer(e))
		defer fake.Close()
		h.svc.Config.VRA.URL, h.svc.Config.VRA.Token = fake.URL, vra.FakeRefreshToken
		var snap api.VraSnapshot
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, nil, 201, &snap)
		if snap.Source != "vra" || snap.Counts.Templates != 5 || snap.Counts.VroWorkflows != 7 || snap.CreatedBy == nil {
			t.Fatalf("read from vRA = %+v", snap)
		}
		upload := func(e vra.Estate) {
			t.Helper()
			h.json("POST", "/api/v1/admin/vra/snapshots", admin, e, 201, &snap)
			if snap.Source != "file" {
				t.Fatalf("upload = %+v", snap)
			}
		}
		upload(e)
		h.json("GET", "/api/v1/admin/vra", admin, nil, 200, &st)
		if !st.Configured || st.Snapshot == nil || st.Snapshot.Id != snap.Id || st.Snapshot.Counts.Deployments != 5 {
			t.Fatalf("status = %+v", st)
		}

		plan := func() api.VraPlan {
			t.Helper()
			var p api.VraPlan
			h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
			return p
		}
		item := func(p api.VraPlan, id string) api.VraItemPlan {
			t.Helper()
			i := slices.IndexFunc(p.Items, func(x api.VraItemPlan) bool { return x.BlueprintId == id })
			if i < 0 {
				t.Fatalf("no plan for %s", id)
			}
			return p.Items[i]
		}

		// The dry run: Web Platform by name, the others not yet.
		p := plan()
		proj := map[string]api.VraProjectPlan{}
		for _, x := range p.Projects {
			proj[x.VraId] = x
		}
		if proj["vra-prj-web"].How != "same_name" || deref(proj["vra-prj-web"].RvavProjectId) != "prj_web" ||
			proj["vra-prj-data"].How != "none" || p.Summary.ItemsCreate != 4 || p.Summary.ItemsBlocked != 1 ||
			len(p.Policies) != 5 || p.Triage.Groups["C"] != 1 || p.Summary.DeploymentsAdopt != 0 {
			t.Fatalf("plan = %+v %+v", p.Projects, p.Summary)
		}
		if r := item(p, "bp-rhel9"); r.Action != "create" || !slices.Equal(r.ShareAdd, []string{"prj_web"}) ||
			!slices.Equal(r.ShareUnmapped, []string{"Data Services", "Legacy Apps"}) {
			t.Fatalf("rhel = %+v", r)
		}
		var n int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from catalog_items where slug = 'rhel-9-server'`).Scan(&n)
		if n != 0 {
			t.Fatal("the dry run made an item")
		}

		// Data Services is Data Engineering.
		h.json("PUT", "/api/v1/admin/vra/projects/vra-prj-data", admin, api.VraProjectMapping{RvavProjectId: ptr("prj_nope")}, 422, nil)
		h.json("PUT", "/api/v1/admin/vra/projects/vra-prj-nope", admin, api.VraProjectMapping{RvavProjectId: ptr("prj_data")}, 404, nil)
		h.json("PUT", "/api/v1/admin/vra/projects/vra-prj-data", "tnguyen", api.VraProjectMapping{RvavProjectId: ptr("prj_data")}, 403, nil)
		h.json("PUT", "/api/v1/admin/vra/projects/vra-prj-data", admin, api.VraProjectMapping{RvavProjectId: ptr("prj_data")}, 204, nil)
		if r := item(plan(), "bp-rhel9"); !slices.Equal(r.ShareAdd, []string{"prj_data", "prj_web"}) {
			t.Fatalf("rhel mapped = %+v", r)
		}

		// The import.
		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if len(res.Created) != 4 || len(res.Updated) != 0 || res.Shared != 5 || len(res.Failed) != 0 ||
			res.Plan.Summary.ItemsUnchanged != 4 || res.Plan.Summary.SharesAdd != 0 {
			t.Fatalf("import = %+v %+v", res, res.Plan.Summary)
		}
		rhel := deref(item(res.Plan, "bp-rhel9").RvavItemId)
		var ents api.CatalogEntitlements
		h.json("GET", "/api/v1/admin/catalog/items/"+rhel+"/entitlements", admin, nil, 200, &ents)
		if ents.AllProjects || !slices.Equal(ents.ProjectIds, []string{"prj_data", "prj_web"}) {
			t.Fatalf("rhel entitlements = %+v", ents)
		}
		var it api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/"+rhel, admin, nil, 200, &it)
		if len(it.Versions) != 1 || it.Versions[0].State != "draft" || it.Versions[0].Number != "2.0" {
			t.Fatalf("rhel versions = %+v", it.Versions)
		}

		// Again: nothing changes.
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if len(res.Created)+len(res.Updated)+res.Shared+len(res.Failed) != 0 {
			t.Fatalf("again = %+v", res)
		}

		// vRA releases version 3 of the RHEL template: its item gets a new
		// draft once the earlier one is published; while a draft is open,
		// the import doesn't touch it.
		publishVRAItem(h, api.CatalogImportResult{ItemId: rhel, Versions: it.Versions})
		bp := &e.Blueprints[0]
		v3 := bp.Versions[1]
		v3.ID, v3.Version, v3.VersionDescription = "bp-rhel9-v3", "3", "Larger default"
		v3.Content = strings.Replace(v3.Content, "default: small", "default: medium", 1)
		bp.Versions = append(bp.Versions, v3)
		upload(e)
		if r := item(plan(), "bp-rhel9"); r.Action != "update" || deref(r.Version) != "3.0" {
			t.Fatalf("v3 = %+v", r)
		}
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if !slices.Equal(res.Updated, []string{"RHEL 9 Server"}) || len(res.Created) != 0 {
			t.Fatalf("v3 import = %+v", res)
		}
		h.json("GET", "/api/v1/admin/catalog/items/"+rhel, admin, nil, 200, &it)
		if len(it.Versions) != 2 || !slices.ContainsFunc(it.Versions, func(v api.CatalogItemVersion) bool { return v.State == "draft" && v.Number == "3.0" }) {
			t.Fatalf("rhel versions after v3 = %+v", it.Versions)
		}
		v4 := v3
		v4.ID, v4.Version = "bp-rhel9-v4", "4"
		v4.Content = strings.Replace(v4.Content, "default: medium", "default: large", 1)
		bp.Versions = append(bp.Versions, v4)
		upload(e)
		if r := item(plan(), "bp-rhel9"); r.Action != "conflict" || !strings.Contains(deref(r.Reason), "has a draft") {
			t.Fatalf("v4 with a draft open = %+v", r)
		}

		// Deployments: the VMs vRA built, in vCenter; the database's item
		// isn't published, so onboarding refuses it.
		for i, name := range []string{"vra-web-001", "vra-db-001"} {
			existingVM(t, ctx, h, c, model, name, "10.31.10."+string(rune('1'+i))+"1")
		}
		if err := (&inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}).SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		h.json("PUT", "/api/v1/projects/prj_data/members/usr_lmartin", admin, api.ProjectMemberWrite{Role: api.Member}, 200, nil)
		if s := plan().Summary; s.DeploymentsAdopt != 2 {
			t.Fatalf("deployments to adopt = %+v", s)
		}
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{Catalog: ptr(false), Deployments: ptr(true)}, 200, &res)
		if !slices.Equal(res.Adopted, []string{"vra-web-001"}) || len(res.Failed) != 1 || res.Failed[0].Name != "vra-db-001" ||
			res.Plan.Summary.DeploymentsAdopted != 1 {
			t.Fatalf("deployments = %+v %+v", res, res.Plan.Summary)
		}
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from vra_links where kind = 'deployment' and vra_id = 'dep-vra-1'`).Scan(&n)
		if n != 1 {
			t.Fatalf("%d links for dep-vra-1", n)
		}

		// The vRO triage, as Markdown.
		code, body, hdr := h.do("GET", "/api/v1/admin/vra/triage", admin, nil)
		if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/markdown") || !strings.Contains(string(body), "## C. Custom Temporal workflow") {
			t.Fatalf("triage = %d %s: %.200s", code, hdr.Get("Content-Type"), body)
		}
	}, model)
}

func ptr[T any](v T) *T { return &v }
