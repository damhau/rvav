package vraimport

import (
	"slices"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// TestPlanImport: what each template's import does, from what earlier
// imports made and what is in the way.
func TestPlanImport(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	r := Rvav{Now: time.Now(), Projects: []RvavProject{{ID: "prj_web", Name: "Web Platform", Environments: []string{"dev"}}},
		ProjectMap: map[string]string{}}
	st := ImportState{Links: map[string]Link{}, Items: map[string]ItemState{}, Slugs: map[string]string{}}
	by := func(p ImportPlan) map[string]ItemImport {
		out := map[string]ItemImport{}
		for _, it := range p.Items {
			out[it.BlueprintID] = it
		}
		return out
	}

	first := by(PlanImport(&e, r, st))
	if first["bp-rhel9"].Action != ActionCreate || first["bp-draft"].Action != ActionSkip || first["bp-rhel9"].Hash == "" {
		t.Fatalf("first = %+v", first)
	}

	// The RHEL item imported, open to every project; the Windows one
	// imported and changed since; an rvav item has the app's slug.
	st.Items["itm_rhel"] = ItemState{ID: "itm_rhel", Slug: "rhel-9-server", AllProjects: true}
	st.Links["bp-rhel9"] = Link{RvavID: "itm_rhel", Hash: first["bp-rhel9"].Hash}
	st.Items["itm_win"] = ItemState{ID: "itm_win", Slug: "windows-server-2022", Projects: []string{"prj_web"}}
	st.Links["bp-win"] = Link{RvavID: "itm_win", Hash: "older"}
	st.Items["itm_other"] = ItemState{ID: "itm_other", Slug: "three-tier-application"}
	st.Slugs["three-tier-application"] = "itm_other"
	// The database's item was deleted in rvav: imported again.
	st.Links["bp-db"] = Link{RvavID: "itm_gone", Hash: first["bp-db"].Hash}

	p := PlanImport(&e, r, st)
	got := by(p)
	if it := got["bp-rhel9"]; it.Action != ActionUnchanged || len(it.ShareAdd) != 0 {
		t.Fatalf("rhel = %+v", it)
	}
	if it := got["bp-win"]; it.Action != ActionUpdate || len(it.ShareAdd) != 0 {
		t.Fatalf("win = %+v", it)
	}
	if it := got["bp-app"]; it.Action != ActionConflict || it.RvavItemID != "" || it.Reason == "" {
		t.Fatalf("app = %+v", it)
	}
	if it := got["bp-db"]; it.Action != ActionCreate || !slices.Equal(it.ShareUnmapped, []string{"Data Services", "Legacy Apps"}) {
		t.Fatalf("db = %+v", it)
	}
	// The Windows item has a draft: the template's change waits.
	st.Items["itm_win"] = ItemState{ID: "itm_win", Slug: "windows-server-2022", HasDraft: true}
	if it := by(PlanImport(&e, r, st))["bp-win"]; it.Action != ActionConflict || !slices.Equal(it.ShareAdd, []string{"prj_web"}) {
		t.Fatalf("win with a draft = %+v", it)
	}
	if s := p.Summary; s.ItemsCreate != 1 || s.ItemsUpdate != 1 || s.ItemsUnchanged != 1 || s.ItemsBlocked != 2 {
		t.Fatalf("summary = %+v", s)
	}
}

// TestPlanDeploymentSharing: vRA's Deployment sharing, on unless turned
// off, is the rvav project's deployment sharing to set (ADR-0045).
func TestPlanDeploymentSharing(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	r := Rvav{Now: time.Now(), ProjectMap: map[string]string{}, Projects: []RvavProject{
		{ID: "prj_web", Name: "Web Platform", DeploymentSharing: "all_members"},
		{ID: "prj_data", Name: "Data Services", DeploymentSharing: "all_members"}}}
	p := PlanImport(&e, r, ImportState{Links: map[string]Link{}, Items: map[string]ItemState{}, Slugs: map[string]string{}})
	got := map[string][2]string{}
	for _, pp := range p.Projects {
		got[pp.VRAID] = [2]string{pp.DeploymentSharing, pp.RvavDeploymentSharing}
	}
	want := map[string][2]string{"vra-prj-web": {"all_members", "all_members"}, "vra-prj-data": {"owner_only", "all_members"},
		"vra-prj-legacy": {"all_members", ""}}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %v, want %v", id, got[id], w)
		}
	}
}
