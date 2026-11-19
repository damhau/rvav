package vraimport

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

func sharingPolicy(id, project string, items []vra.SharedReference, principals ...vra.SharePrincipal) vra.Policy {
	def, _ := json.Marshal(vra.SharingDefinition{EntitledUsers: []vra.EntitledUsers{{UserType: "USER", Principals: principals, Items: items}}})
	return vra.Policy{ID: id, Name: id, TypeID: vra.PolicySharing, EnforcementType: "HARD", ProjectID: project, Definition: def}
}

func item(id string) []vra.SharedReference {
	return []vra.SharedReference{{Type: "CATALOG_ITEM_IDENTIFIER", ID: id}}
}

func principal(typ, ref string) vra.SharePrincipal {
	return vra.SharePrincipal{Type: typ, ReferenceID: ref}
}

// TestPlanSharing: vRA's content sharing policies become each item's
// sharing: with all a project's members when an entitlement or a PROJECT
// principal says so, else with the users, groups and roles they name that
// map; what doesn't is in the notes, and a project where nothing maps
// isn't shared at all.
func TestPlanSharing(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	e.Policies = append(e.Policies,
		sharingPolicy("pol-share-win", "vra-prj-data", item("bp-win"), principal("USER", "Lea.Martin@corp.example"),
			principal("USER", "gone@corp.example"), principal("GROUP", "rvav-data"), principal("ROLE", "administrator"),
			principal("ROLE", "auditor")),
		sharingPolicy("pol-share-app", "", item("bp-app"), principal("ROLE", "member")),
		sharingPolicy("pol-share-db", "vra-prj-web", item("bp-db"), principal("USER", "nobody@corp.example")))
	r := Rvav{Now: time.Now(), Projects: []RvavProject{{ID: "prj_web", Name: "Web Platform"}, {ID: "prj_data", Name: "Data Services"}},
		ProjectMap: map[string]string{}, Users: map[string]string{"lea.martin@corp.example": "usr_lea"}}
	st := ImportState{Links: map[string]Link{}, Items: map[string]ItemState{}, Slugs: map[string]string{}}
	plan := func() map[string]ItemImport {
		out := map[string]ItemImport{}
		for _, it := range PlanImport(&e, r, st).Items {
			out[it.BlueprintID] = it
		}
		return out
	}
	notes := func(it ItemImport, level string) []string {
		var out []string
		for _, n := range it.Notes {
			if n.Where == "sharing" && n.Level == level {
				out = append(out, n.Text)
			}
		}
		return out
	}

	got := plan()
	win := got["bp-win"]
	if !slices.Equal(win.ShareAdd, []string{"prj_data", "prj_web"}) || !reflect.DeepEqual(win.ShareAudiences, []ShareAudience{
		{ProjectID: "prj_data", UserIDs: []string{"usr_lea"}, Groups: []string{"rvav-data"}, Roles: []string{"project-admin"}}}) {
		t.Fatalf("win: add %v, audiences %+v", win.ShareAdd, win.ShareAudiences)
	}
	if u := notes(win, Unmapped); len(u) != 2 || !strings.Contains(u[0], "gone@corp.example") || !strings.Contains(u[1], "auditor") {
		t.Fatalf("win unmapped = %q", u)
	}
	if w := notes(win, Warning); !slices.ContainsFunc(w, func(s string) bool { return strings.Contains(s, "the groups rvav-data") }) {
		t.Fatalf("win warnings = %q", w)
	}
	// Org-wide: in each project; Web Platform has it already for all.
	app := got["bp-app"]
	if !slices.Equal(app.ShareAdd, []string{"prj_data", "prj_web"}) || !reflect.DeepEqual(app.ShareAudiences, []ShareAudience{
		{ProjectID: "prj_data", UserIDs: []string{}, Groups: []string{}, Roles: []string{"member"}}}) {
		t.Fatalf("app: add %v, audiences %+v", app.ShareAdd, app.ShareAudiences)
	}
	db := got["bp-db"]
	if !slices.Equal(db.ShareAdd, []string{"prj_data"}) || len(db.ShareAudiences) != 0 {
		t.Fatalf("db: add %v, audiences %+v", db.ShareAdd, db.ShareAudiences)
	}
	if w := notes(db, Warning); !slices.ContainsFunc(w, func(s string) bool { return strings.HasPrefix(s, "Not shared with Web Platform:") }) {
		t.Fatalf("db warnings = %q", w)
	}

	// A PROJECT principal shares with all the project's members.
	e.Policies = append(e.Policies, sharingPolicy("pol-share-app-data", "vra-prj-data", item("bp-app"), principal("PROJECT", "vra-prj-data")))
	if app := plan()["bp-app"]; !slices.Equal(app.ShareAdd, []string{"prj_data", "prj_web"}) || len(app.ShareAudiences) != 0 {
		t.Fatalf("app for all: add %v, audiences %+v", app.ShareAdd, app.ShareAudiences)
	}

	// An item shared with a project already keeps its sharing there.
	st.Items["itm_win"] = ItemState{ID: "itm_win", Slug: "windows-server-2022", Projects: []string{"prj_data", "prj_web"}}
	st.Links["bp-win"] = Link{RvavID: "itm_win", Hash: win.Hash}
	if w := plan()["bp-win"]; len(w.ShareAdd) != 0 || len(w.ShareAudiences) != 0 {
		t.Fatalf("win shared already: add %v, audiences %+v", w.ShareAdd, w.ShareAudiences)
	}

	pols := PlanPolicies(&e)
	i := slices.IndexFunc(pols, func(p PolicyPlan) bool { return p.VRAID == "pol-share-win" })
	if i < 0 || pols[i].Type != "sharing" || pols[i].VRAProject != "Data Services" || !strings.Contains(pols[i].Becomes, "sharing") {
		t.Fatalf("policies = %+v", pols)
	}
}
