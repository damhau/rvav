package vraimport

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

const gapsTemplate = `formatVersion: 1
inputs:
  backup:
    type: boolean
  primary:
    type: object
    $ref: /ref/property-groups/corpInputs
  secondary:
    type: object
    $ref: /ref/property-groups/corpInputs
  alt:
    type: object
    $ref: /ref/property-groups/altInputs
resources:
  vm:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel-twin
      flavor: medium
      linkedClone: true
      provisioningType: thin
      storagePolicy: gold
`

// What the review of T1 found the importer lost without a word (T1.7):
// group inputs that clash, machine properties rvav has, Windows told by a
// word of the image's name.
func TestImporterGaps(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	e.PropertyGroups = append(e.PropertyGroups, vra.PropertyGroup{ID: "pg-alt", Name: "altInputs", Type: "INPUT",
		Properties: map[string]map[string]any{"costCenter": {"type": "string"}}})
	bp := vra.Blueprint{ID: "bp-gaps", Name: "Gaps", ProjectID: "vra-prj-web", Status: "RELEASED", Content: gapsTemplate,
		Versions: []vra.BlueprintVersion{{ID: "bp-gaps-v1", BlueprintID: "bp-gaps", Version: "1", Status: "RELEASED", Content: gapsTemplate}}}
	e.Blueprints = append(e.Blueprints, bp)
	p := MapTemplate(&e, bp)
	for _, want := range []struct{ where, text string }{
		{"inputs.primary", "Property group corpInputs has input backup, as the form has"},
		{"inputs.primary", "Property groups altInputs and corpInputs both have input costCenter"},
		{"inputs.secondary", "Inputs primary and secondary are both property group corpInputs"},
	} {
		if !has(p, Unmapped, want.where, want.text) {
			t.Errorf("no note %q at %s: %+v", want.text, want.where, p.Notes)
		}
	}
	if p.Status != Partial {
		t.Errorf("status = %s, want partial", p.Status)
	}
	b := p.Doc.Versions[0].Blueprint
	if b["linked_clone"] != true || b["provisioning"] != "thin" || b["storage_policy"] != "gold" {
		t.Errorf("machine options = %v %v %v", b["linked_clone"], b["provisioning"], b["storage_policy"])
	}
	if os := b["guest"].(map[string]any)["os_family"]; os != "linux" {
		t.Errorf("rhel-twin is %v", os)
	}
	for name, want := range map[string]bool{"win2022": true, "Windows-2019-std": true, "tpl-WIN11": true, "rhel-twin": false, "darwin": false} {
		if windowsImage(name) != want {
			t.Errorf("windowsImage(%q) = %v", name, !want)
		}
	}
}

// A template whose cloud-init opens a secret rvav doesn't have isn't made
// (its save would be refused), and its items say why (T1.7).
func TestTemplateSecrets(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	for i := range e.Images {
		if m, ok := e.Images[i].ImageMappings.Mapping["ubuntu24"]; ok {
			m.CloudConfig = "#cloud-config\nchpasswd:\n  list: |\n    ubuntu:${secret.ubuntu_pw}"
			e.Images[i].ImageMappings.Mapping["ubuntu24"] = m
		}
	}
	r := Rvav{VCenters: []RvavVCenter{{Name: "vc01.corp.example", Host: "vc01.corp.example"}, {Name: "vc02.corp.example", Host: "vcsa-zrh.corp.example"}}}
	ub := func() TemplateImport {
		list := PlanTemplates(&e, r)
		return list[slices.IndexFunc(list, func(ti TemplateImport) bool { return ti.Key == "ubuntu24" })]
	}
	if got := ub(); got.Action != ActionSkip || !slices.ContainsFunc(got.Notes, func(n Note) bool {
		return n.Level == Unmapped && strings.Contains(n.Text, "store ubuntu-pw in Catalog items › Secrets")
	}) {
		t.Fatalf("without the secret = %+v", got)
	}
	r.Secrets = map[string]bool{"ubuntu-pw": true}
	if got := ub(); got.Action != ActionCreate {
		t.Fatalf("with the secret = %+v", got)
	}
}
