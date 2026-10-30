package vraimport

import (
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// TestCutover: each vRA project's checks before and after it moves, and
// when vRA can be switched off (P2.6).
func TestCutover(t *testing.T) {
	now := time.Now()
	e := vra.SampleEstate(now)
	e.Deployments[3].CreatedAt = now.AddDate(0, 0, -1) // vra-gone, made in vRA after Web Platform moved
	r := Rvav{Now: now, ProjectMap: map[string]string{"vra-prj-data": "prj_data"},
		Users: map[string]string{"thomas.nguyen@corp.example": "usr_t", "lea.martin@corp.example": "usr_l"},
		Projects: []RvavProject{{ID: "prj_web", Name: "Web Platform", Environments: []string{"dev"}, Members: []string{"usr_t", "usr_l"}},
			{ID: "prj_data", Name: "Data Engineering", Environments: []string{"dev"}, Members: []string{"usr_l"}}},
		VMs:   map[string]VMRef{"vra-web-001": {VCenter: "vc1", Moref: "vm-1", Name: "vra-web-001", Managed: true}},
		Items: map[string]string{}}
	plan0 := PlanImport(&e, r, ImportState{})
	hash := map[string]string{}
	for _, it := range plan0.Items {
		hash[it.BlueprintID] = it.Hash
	}
	st := ImportState{
		Items: map[string]ItemState{
			"itm_rhel": {ID: "itm_rhel", Slug: "rhel-9-server", Published: true, Projects: []string{"prj_web"}},
			"itm_win":  {ID: "itm_win", Slug: "windows-server-2022", HasDraft: true, Projects: []string{"prj_web"}},
			"itm_db":   {ID: "itm_db", Slug: "database-server", Published: true}},
		Links: map[string]Link{"bp-rhel9": {RvavID: "itm_rhel", Hash: hash["bp-rhel9"]}, "bp-win": {RvavID: "itm_win", Hash: hash["bp-win"]},
			"bp-db": {RvavID: "itm_db", Hash: hash["bp-db"]}},
		Slugs:   map[string]string{},
		Cutover: map[string]Moved{"vra-prj-web": {At: now.AddDate(0, 0, -10), By: "Marc Keller"}},
	}
	c := PlanImport(&e, r, st).Cutover
	if c.Moved != 1 || c.Total != 3 || c.SwitchOff || len(c.DeploymentsLeft) != 4 {
		t.Fatalf("cutover = %+v", c)
	}
	checks := func(p CutoverProject) map[string]Check {
		out := map[string]Check{}
		for _, ch := range p.Checks {
			out[ch.Key] = ch
		}
		return out
	}
	is := func(ch Check, state, text string) bool { return ch.State == state && strings.Contains(ch.Detail, text) }

	web := checks(c.Projects[0])
	if c.Projects[0].Ready || c.Projects[0].MovedBy != "Marc Keller" || !is(web["project"], CheckOK, "by name") ||
		!is(web["items"], CheckWarn, "1 of 3 published in rvav. Not yet: Windows Server 2022 (a draft), Three-tier application (not imported).") ||
		!is(web["sharing"], CheckOK, "") || !is(web["extensibility"], CheckManual, "Create AD group") ||
		!is(web["deployments"], CheckWarn, "1 of 3 adopted. Still in vRA: vra-web-pair") ||
		!is(web["vra_lease"], CheckWarn, "vra-web-001 (") || !is(web["people"], CheckOK, "All 2 have signed in") ||
		!is(web["people"], CheckOK, "rvav-web@corp.example") || !is(web["policies"], CheckManual, "Production needs a lead") ||
		!is(web["vra_offer"], CheckWarn, "still offers it 4 items") || !is(web["vra_new"], CheckWarn, "vra-gone") {
		t.Fatalf("web = %+v", c.Projects[0].Checks)
	}
	data := checks(c.Projects[1])
	if !is(data["project"], CheckOK, "mapped") || !is(data["items"], CheckOK, "All 2 published") ||
		!is(data["sharing"], CheckTodo, "Not yet offered to Data Engineering: RHEL 9 Server, Database server") ||
		!is(data["people"], CheckWarn, "Not yet: marc.keller@corp.example") || data["vra_offer"].Key != "" {
		t.Fatalf("data = %+v", c.Projects[1].Checks)
	}
	if legacy := checks(c.Projects[2]); !is(legacy["project"], CheckTodo, "Map it") || c.Projects[2].Ready {
		t.Fatalf("legacy = %+v", c.Projects[2].Checks)
	}

	// Every project moved and its deployment adopted: vRA can go.
	e.Deployments = e.Deployments[:1]
	st.Cutover["vra-prj-data"], st.Cutover["vra-prj-legacy"] = Moved{At: now}, Moved{At: now}
	if c := PlanImport(&e, r, st).Cutover; !c.SwitchOff || c.Moved != 3 || len(c.DeploymentsLeft) != 0 {
		t.Fatalf("all moved = %+v", c)
	}
}
