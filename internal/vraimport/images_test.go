package vraimport

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// TestPlanTemplates: each image name of vRA's image profiles becomes a
// catalogue template with a source in each region's vCenter rvav manages,
// its cloud-init and constraints; one rvav has is never changed (T1.4,
// ADR-0037).
func TestPlanTemplates(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	geneva := Rvav{VCenters: []RvavVCenter{{Name: "", Host: "vc01.corp.example"}}}
	by := func(list []TemplateImport) map[string]TemplateImport {
		out := map[string]TemplateImport{}
		for _, ti := range list {
			out[ti.Key] = ti
		}
		return out
	}
	notes := func(ti TemplateImport, level, text string) bool {
		return slices.ContainsFunc(ti.Notes, func(n Note) bool { return n.Level == level && strings.Contains(n.Text, text) })
	}

	// Only Geneva managed: Zurich's mappings are left out, and said so.
	got := by(PlanTemplates(&e, geneva))
	win := got["win2022"]
	if win.Action != ActionCreate || win.OSFamily != "windows" || !slices.Equal(win.Sources, []TemplateSource{{"", "tpl-win2025-std-2026.09"}}) ||
		!slices.Equal(win.Constraints, []string{"license/windows"}) || !notes(win, Unmapped, "vCenter vc02.corp.example, which rvav doesn't manage") {
		t.Fatalf("win2022 = %+v", win)
	}

	// Both: a source in each, by its name in rvav.
	both := Rvav{VCenters: []RvavVCenter{{Name: "vc01.corp.example", Host: "vc01.corp.example"}, {Name: "vc02.corp.example", Host: "vcsa-zrh.corp.example"}}}
	got = by(PlanTemplates(&e, both))
	if ub := got["ubuntu24"]; ub.Action != ActionCreate || ub.OSFamily != "linux" || ub.UserData != "#cloud-config\npackages: [qualys-agent]" ||
		!slices.Equal(ub.Sources, []TemplateSource{{"vc01.corp.example", "tpl-ubuntu24.04-2026.10"}, {"vc02.corp.example", "tpl-ubuntu24.04-zrh"}}) ||
		len(ub.Notes) != 0 {
		t.Fatalf("ubuntu24 = %+v", ub)
	}
	if rh := got["rhel9"]; rh.Action != ActionCreate || len(rh.Sources) != 2 || rh.UserData != "" || len(rh.Constraints) != 0 {
		t.Fatalf("rhel9 = %+v", rh)
	}

	// What rvav has: the same is unchanged, another is left as it is.
	both.Templates = map[string]RvavTemplate{
		"ubuntu24": {Sources: []TemplateSource{{"vc02.corp.example", "tpl-ubuntu24.04-zrh"}, {"vc01.corp.example", "tpl-ubuntu24.04-2026.10"}},
			UserData: "#cloud-config\npackages: [qualys-agent]\n"},
		"win2022": {Sources: []TemplateSource{{"vc01.corp.example", "tpl-win2025-std-2026.09"}}},
	}
	got = by(PlanTemplates(&e, both))
	if got["ubuntu24"].Action != ActionUnchanged || got["win2022"].Action != ActionDiffers || !notes(got["win2022"], Warning, "leaves it as it is") {
		t.Fatalf("with rvav's = %+v / %+v", got["ubuntu24"], got["win2022"])
	}

	// Regions differ, are unknown, or are in no vCenter rvav manages.
	gva, zrh := &e.Images[0], &e.Images[1]
	zrh.ImageMappings.Mapping["ubuntu24"] = vra.ImageMapping{Name: "tpl-ubuntu24.04-zrh", CloudConfig: "#cloud-config\npackages: [other]\n",
		Constraints: []vra.ImageConstraint{{Mandatory: false, Expression: "rvav-tier:gold"}}}
	gva.ImageMappings.Mapping["win2022"] = vra.ImageMapping{Name: "tpl-win2025-std-2026.09", CloudConfig: "#cloud-config\n",
		Constraints: []vra.ImageConstraint{{Mandatory: true, Expression: "!rvav-zone:pci"}, {Mandatory: false, Expression: "license:${input.licence}"}, {Expression: "solo"}}}
	got = by(PlanTemplates(&e, both))
	if ub := got["ubuntu24"]; !notes(ub, Warning, "Region Zurich's cloud-init differs from region Geneva's") ||
		!notes(ub, Warning, "Region Zurich's constraints differ") || ub.UserData != "#cloud-config\npackages: [qualys-agent]" {
		t.Fatalf("differing regions = %+v", ub)
	}
	if w := got["win2022"]; !slices.Equal(w.Constraints, []string{"!rvav-zone/pci"}) || w.UserData != "" ||
		!notes(w, Unmapped, "cloudbase-init") || !notes(w, Unmapped, "depends on the request") || !notes(w, Unmapped, `"solo"`) {
		t.Fatalf("win2022 constraints = %+v", w)
	}
	e.CloudAccounts = nil
	if rh := by(PlanTemplates(&e, both))["rhel9"]; rh.Action != ActionSkip || !notes(rh, Unmapped, "Region Geneva isn't in a vSphere cloud account read") {
		t.Fatalf("no cloud accounts = %+v", rh)
	}

	for in, want := range map[string]string{"rhel9": "rhel9", "Ubuntu 24.04 LTS": "ubuntu-24-04-lts", "2022-core": "img-2022-core", "x": "x-img"} {
		if got := ImageKey(in); got != want {
			t.Errorf("ImageKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestImageKeyClashes: two image names that make the same key get keys of
// their own, said in the plan, and an item binds its image's (T1.7).
func TestImageKeyClashes(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	const v1, v2 = "Windows Server 2019 Datacenter Core English v1", "Windows Server 2019 Datacenter Core English v2"
	for i, p := range e.Images {
		for _, name := range []string{v1, v2, "Ubuntu 22.04", "ubuntu-22-04"} {
			e.Images[i].ImageMappings.Mapping[name] = vra.ImageMapping{Name: "tpl-" + ImageKey(name) + "-" + p.RegionID}
		}
	}
	keys := map[string]string{}
	var notes []Note
	for _, ti := range PlanTemplates(&e, Rvav{}) {
		keys[ti.Image] = ti.Key
		notes = append(notes, ti.Notes...)
	}
	want := map[string]string{v1: "windows-server-2019-datacenter-core-engli", v2: "windows-server-2019-datacenter-core-eng-2",
		"Ubuntu 22.04": "ubuntu-22-04", "ubuntu-22-04": "ubuntu-22-04-2"}
	for name, key := range want {
		if keys[name] != key {
			t.Errorf("%s = %q, want %q", name, keys[name], key)
		}
	}
	if !slices.ContainsFunc(notes, func(n Note) bool {
		return n.Level == Warning && strings.Contains(n.Text, "Images "+v1+" and "+v2+" would both be template windows-server-2019-datacenter-core-engli")
	}) {
		t.Errorf("notes = %+v", notes)
	}

	content := strings.Replace(e.Blueprints[0].Content, "image: ", "image: "+v2+" #", 1)
	bp := vra.Blueprint{ID: "bp-v2", Name: "v2", ProjectID: "vra-prj-web", Status: "RELEASED", Content: content,
		Versions: []vra.BlueprintVersion{{ID: "bp-v2-v1", BlueprintID: "bp-v2", Version: "1", Status: "RELEASED", Content: content}}}
	if p := MapTemplate(&e, bp); !slices.Equal(p.Templates, []string{want[v2]}) {
		t.Errorf("the v2 item binds %v", p.Templates)
	}
}

// TestImageBinding: a machine whose image is more than a template's name
// binds its catalogue template; one that is the same template everywhere
// keeps naming it; the plan says when the template won't be made.
func TestImageBinding(t *testing.T) {
	ps := plans(t)
	win := ps["bp-win"].Doc.Versions[0].Blueprint
	if win["template"] != nil || win["bindings"].(map[string]any)["template"] != `"win2022"` ||
		!slices.Equal(ps["bp-win"].Templates, []string{"win2022"}) || !has(ps["bp-win"], Info, "resources.Cloud_vSphere_Machine_1.image", "catalogue template win2022") {
		t.Fatalf("windows = %+v / %v", win, ps["bp-win"].Templates)
	}
	if rhel := ps["bp-rhel9"]; rhel.Doc.Versions[0].Blueprint["template"] != "tpl-rhel9.4-2026.10" || len(rhel.Templates) != 0 {
		t.Fatalf("rhel = %+v", rhel.Doc.Versions[0].Blueprint)
	}

	e := vra.SampleEstate(time.Now())
	r := Rvav{Now: time.Now(), ProjectMap: map[string]string{}}
	st := ImportState{Links: map[string]Link{}, Items: map[string]ItemState{}, Slugs: map[string]string{}}
	plan := PlanImport(&e, r, st)
	i := slices.IndexFunc(plan.Items, func(it ItemImport) bool { return it.BlueprintID == "bp-win" })
	if plan.Summary.TemplatesCreate != 0 || len(plan.Templates) != 3 || !slices.ContainsFunc(plan.Items[i].Notes, func(n Note) bool {
		return n.Level == Warning && strings.Contains(n.Text, "Catalogue template win2022 has no source in a vCenter rvav manages")
	}) {
		t.Fatalf("no vCenter = %+v / %+v", plan.Summary, plan.Items[i].Notes)
	}
	r.VCenters = []RvavVCenter{{Host: "vc01.corp.example"}}
	if plan := PlanImport(&e, r, st); plan.Summary.TemplatesCreate != 3 {
		t.Fatalf("Geneva = %+v", plan.Summary)
	}
}
