package vraimport

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// The cases P2's review found (P2.7).

func pint(n int) *int { return &n }

// An estate without deployments or vRO gives empty lists, never null.
func TestPlanEmptyLists(t *testing.T) {
	e := vra.Estate{Projects: []vra.Project{{ID: "p", Name: "P"}}}
	b, _ := json.Marshal(PlanImport(&e, Rvav{}, ImportState{}))
	for _, k := range []string{`"deployments":null`, `"items":null`, `"notes":null`, `"checks":null`, `"policies":null`} {
		if strings.Contains(string(b), k) {
			t.Fatalf("%s in %s", k, b)
		}
	}
}

// Two templates of the same name get slugs of their own, and each one's
// deployments its own item; vRA's patch versions stay apart.
func TestTemplateSlugsAndVersions(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	twin := e.Blueprints[0]
	twin.ID, twin.ProjectID = "bp-rhel9-data", "vra-prj-data"
	e.Blueprints = append(e.Blueprints, twin)
	slugs := TemplateSlugs(&e)
	if slugs["bp-rhel9"] != "rhel-9-server-web-platform" || slugs["bp-rhel9-data"] != "rhel-9-server-data-services" || slugs["bp-win"] != "windows-server-2022" {
		t.Fatalf("slugs = %v", slugs)
	}
	r := Rvav{Now: time.Now(), Items: map[string]string{"rhel-9-server-web-platform": "itm_web", "rhel-9-server-data-services": "itm_data"},
		Users: map[string]string{"lea.martin@corp.example": "usr_l"}, VMs: map[string]VMRef{"vra-db-001": {Name: "vra-db-001", Moref: "vm-9"}},
		Projects: []RvavProject{{ID: "prj_data", Name: "Data Services", Environments: []string{"dev"}, Members: []string{"usr_l"}}}}
	d := e.Deployments[2] // vra-db-001, in Data Services
	d.BlueprintID = "bp-rhel9-data"
	if p := PlanDeployments(&vra.Estate{Projects: e.Projects, Blueprints: e.Blueprints, Deployments: []vra.Deployment{d}}, r)[0]; p.CatalogItemID != "itm_data" {
		t.Fatalf("the twin's deployment = %+v", p)
	}

	bp := e.Blueprints[1]
	v := bp.Versions[0]
	bp.Versions = []vra.BlueprintVersion{v, v}
	bp.Versions[0].Version, bp.Versions[1].Version = "1.0.1", "1.0.2"
	m := MapTemplate(&e, bp)
	if len(m.Doc.Versions) != 2 || m.Doc.Versions[0].Number != "1.0" || m.Doc.Versions[1].Number != "1.1" || !has(m, Info, "versions", "1.0.2 is 1.1") {
		t.Fatalf("versions = %+v %+v", m.Doc.Versions, m.Notes)
	}
}

// Shared by content source only; a share the import made and an
// administrator removed isn't made again.
func TestSharingBySourceAndRemoved(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	for i := range e.CatalogItems {
		e.CatalogItems[i].ProjectIDs = nil
	}
	e.Entitlements = nil
	var en vra.Entitlement
	en.ProjectID, en.Definition.Type, en.Definition.ID = "vra-prj-web", "CatalogSourceIdentifier", "cs-templates"
	e.Entitlements = append(e.Entitlements, en)
	r := Rvav{Now: time.Now(), Projects: []RvavProject{{ID: "prj_web", Name: "Web Platform"}}}
	st := ImportState{Items: map[string]ItemState{"itm_win": {ID: "itm_win", Slug: "windows-server-2022"}},
		Links: map[string]Link{"bp-win": {RvavID: "itm_win"}}, Slugs: map[string]string{}, Shared: map[string]bool{"itm_win/prj_web": true}}
	p := PlanImport(&e, r, st)
	got := map[string]ItemImport{}
	for _, it := range p.Items {
		got[it.BlueprintID] = it
	}
	if !slices.Equal(got["bp-rhel9"].ShareAdd, []string{"prj_web"}) || p.Projects[0].Items != 4 {
		t.Fatalf("by source: rhel %+v, web %+v", got["bp-rhel9"].ShareAdd, p.Projects[0])
	}
	if it := got["bp-win"]; len(it.ShareAdd) != 0 || !slices.ContainsFunc(it.Notes, func(n Note) bool { return strings.Contains(n.Text, "taken it away") }) {
		t.Fatalf("removed share = %+v", it)
	}
	web := p.Cutover.Projects[0].Checks
	if i := slices.IndexFunc(web, func(c Check) bool { return c.Key == "items" }); web[i].State != CheckWarn {
		t.Fatalf("cutover items by source = %+v", web[i])
	}
}

// What onboarding would refuse, the plan says first: a draft item, one not
// offered to the project; names made valid; leases held to the policy; a
// project kept in vRA; a deployment adopted by hand counts as adopted.
func TestPlanDeploymentRefusals(t *testing.T) {
	now := time.Now()
	e := vra.SampleEstate(now)
	e.Deployments[0].Name = "Web Server for Thomas"
	r := Rvav{Now: now, Users: map[string]string{"thomas.nguyen@corp.example": "usr_t", "lea.martin@corp.example": "usr_l"},
		Projects: []RvavProject{{ID: "prj_web", Name: "Web Platform", Environments: []string{"dev"}, Members: []string{"usr_t", "usr_l"}}},
		VMs: map[string]VMRef{"vra-web-001": {Name: "vra-web-001", Moref: "vm-1"}, "vra-api-001": {Name: "vra-api-001", Moref: "vm-2"},
			"vra-api-002": {Name: "vra-api-002", Moref: "vm-3"}},
		TemplateItems: map[string]string{"bp-rhel9": "itm_rhel"},
		ItemStates:    map[string]ItemState{"itm_rhel": {ID: "itm_rhel", Slug: "rhel-9-server"}},
		Leases:        map[string]Lease{"prj_web/dev": {Default: pint(30), Max: pint(30)}}}
	plan := func() []DeploymentPlan { return PlanDeployments(&e, r) }
	if p := plan()[0]; p.Status != Skipped || !strings.Contains(p.Notes[len(p.Notes)-1].Text, "isn't published") {
		t.Fatalf("a draft item: %+v", p)
	}
	r.ItemStates["itm_rhel"] = ItemState{ID: "itm_rhel", Slug: "rhel-9-server", Published: true}
	if p := plan()[0]; p.Status != Skipped || !strings.Contains(p.Notes[len(p.Notes)-1].Text, "isn't offered to Web Platform") {
		t.Fatalf("not offered: %+v", p)
	}
	r.ItemStates["itm_rhel"] = ItemState{ID: "itm_rhel", Slug: "rhel-9-server", Published: true, Projects: []string{"prj_web"}}
	ps := plan()
	if p := ps[0]; p.Status != Adopt || p.RvavName != "web-server-for-thomas" || !hasNote(p.Notes, "name", "Named web-server-for-thomas") {
		t.Fatalf("a vRA name: %+v", p)
	}
	if p := ps[1]; p.Status != Adopt || *p.LeaseDays != 30 || !hasNote(p.Notes, "lease", "allows at most 30") {
		t.Fatalf("a lease longer than the policy: %+v", p)
	}
	e.Deployments[0].LeaseExpireAt = nil
	if p := plan()[0]; !slices.ContainsFunc(p.Notes, func(n Note) bool { return n.Level == Warning && strings.Contains(n.Text, "gives it 30 days") }) {
		t.Fatalf("no lease in vRA: %+v", p)
	}
	if RvavName("--ÉTÉ 2026!") != "t-2026" || RvavName("Ünïcödé") != "n-c-d" || RvavName("***") != "" {
		t.Fatalf("names: %q %q %q", RvavName("--ÉTÉ 2026!"), RvavName("Ünïcödé"), RvavName("***"))
	}

	// Kept in vRA, though rvav has a project of its name.
	r.ProjectMap = map[string]string{"vra-prj-web": ""}
	if p := plan()[0]; p.Status != Skipped || !hasNote(p.Notes, "project", "stays in vRA") {
		t.Fatalf("kept in vRA: %+v", p)
	}
	// Adopted by hand (a link), its second VM not with it.
	r.ProjectMap, r.Adopted = nil, map[string][]string{"dep-vra-2": {"dep_x"}}
	if p := plan()[1]; p.Status != Adopted || len(p.VMs) != 2 || !hasNote(p.Notes, "vms", "Not adopted with it: vra-api-001, vra-api-002") {
		t.Fatalf("adopted by hand: %+v", p)
	}
}
