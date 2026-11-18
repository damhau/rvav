package vraimport

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

const groupedTemplate = `formatVersion: 1
inputs:
  environment:
    type: string
    enum: [dev, prd]
  corp:
    type: object
    $ref: /ref/property-groups/corpInputs
  gone:
    type: object
    $ref: /ref/property-groups/nowhere
resources:
  Cloud_vSphere_Machine_1:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: medium
      costCenter: ${input.corp.costCenter}
      dnsDomain: ${propgroup.dnsSettings.domain}
      cloudConfig: |
        #cloud-config
        fqdn: ${self.resourceName}.${propgroup.dnsSettings.domain}
        write_files:
          - content: ${input.corp.costCenter}
`

func groupedEstate() vra.Estate {
	e := vra.SampleEstate(time.Now())
	e.Blueprints = append(e.Blueprints, vra.Blueprint{ID: "bp-grouped", Name: "Grouped server", ProjectID: "vra-prj-web",
		Status: "RELEASED", Content: groupedTemplate, Versions: []vra.BlueprintVersion{
			{ID: "bp-grouped-v1", BlueprintID: "bp-grouped", Version: "1", Status: "RELEASED", Content: groupedTemplate}}})
	return e
}

func TestGroupKey(t *testing.T) {
	for in, want := range map[string]string{"corpInputs": "corpInputs", "dns-settings v2": "dns_settings_v2", "9lives": "g_9lives",
		"x": "x_group", "--": "g_"} {
		if got := GroupKey(in); got != want {
			t.Errorf("GroupKey(%q) = %q, want %q", in, got, want)
		}
	}
	e := vra.Estate{PropertyGroups: []vra.PropertyGroup{{ID: "b", Name: "net", ProjectID: "p2"}, {ID: "a", Name: "net", ProjectID: "p1"},
		{ID: "c", Name: "net"}}}
	if keys := GroupKeys(&e); keys["c"] != "net" || keys["a"] != "net_2" || keys["b"] != "net_3" {
		t.Fatalf("keys = %v", keys)
	}
	long := strings.Repeat("a", 70)
	long2 := GroupKeys(&vra.Estate{PropertyGroups: []vra.PropertyGroup{{ID: "a", Name: long}, {ID: "b", Name: long, ProjectID: "p"}}})["b"]
	if len(long2) != 63 || !strings.HasSuffix(long2, "a_2") {
		t.Fatalf("a long name's second key = %q", long2)
	}
	if g := e.PropertyGroupFor("net", "p2"); g == nil || g.ID != "b" {
		t.Fatalf("the project's own = %+v", g)
	}
	if g := e.PropertyGroupFor("net", "p9"); g == nil || g.ID != "c" {
		t.Fatalf("the organisation's = %+v", g)
	}
}

// TestPlanPropertyGroups: vRA's property groups as rvav's, made once and
// never changed (T1.5).
func TestPlanPropertyGroups(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	e.PropertyGroups = append(e.PropertyGroups, vra.PropertyGroup{ID: "pg-odd", Name: "odd", Type: "INPUT",
		Properties: map[string]map[string]any{
			"disks":  {"type": "array"},
			"size":   {"type": "integer", "enum": []any{1, 2}, "default": 1, "minimum": 1},
			"secret": {"type": "string", "encrypted": true, "default": "x"},
			"bad-1":  {"type": "string"},
		}})
	by := func(gs []GroupImport) map[string]GroupImport {
		out := map[string]GroupImport{}
		for _, g := range gs {
			out[g.Key] = g
		}
		return out
	}
	gs := by(PlanPropertyGroups(&e, Rvav{}))
	corp, dns, odd := gs["corpInputs"], gs["dnsSettings"], gs["odd"]
	if corp.Action != ActionCreate || corp.Version != 1 || corp.Type != "input" || corp.Label != "Corporate inputs" || len(corp.Inputs) != 2 {
		t.Fatalf("corp = %+v", corp)
	}
	if b, cc := corp.Inputs[0], corp.Inputs[1]; b.Name != "backup" || b.Type != "boolean" || b.Default != true || b.Required ||
		cc.Name != "costCenter" || !cc.Required || !slices.Equal(cc.Options, []string{"CC-1001", "CC-2002"}) {
		t.Fatalf("corp inputs = %+v", corp.Inputs)
	}
	if c, _ := json.Marshal(dns.Constants); dns.Type != "constant" || string(c) != `{"domain":"corp.example","ttl":300}` {
		t.Fatalf("dns = %+v", dns)
	}
	names := func(g GroupImport) []string {
		var out []string
		for _, in := range g.Inputs {
			out = append(out, in.Name)
		}
		return out
	}
	if !slices.Equal(names(odd), []string{"secret", "size"}) || !odd.Inputs[0].Secret || odd.Inputs[0].Default != nil ||
		odd.Inputs[1].Options != nil || odd.Inputs[1].Default != float64(1) {
		t.Fatalf("odd = %+v", odd.Inputs)
	}
	for _, n := range []struct{ level, where, text string }{
		{Unmapped, "properties.disks", "An input of type array"},
		{Unmapped, "properties.bad-1", "letters, digits and underscores"},
		{Unmapped, "properties.size.enum", "Only a string input"},
		{Unmapped, "properties.size.minimum", "no such check"},
		{Info, "properties.secret.default", "no default"},
	} {
		if !slices.ContainsFunc(odd.Notes, func(x Note) bool {
			return x.Level == n.level && x.Where == n.where && strings.Contains(x.Text, n.text)
		}) {
			t.Errorf("no %s note at %s with %q in %+v", n.level, n.where, n.text, odd.Notes)
		}
	}

	// Made earlier: unchanged; changed in rvav since: left as it is.
	r := Rvav{PropertyGroups: map[string]RvavGroup{
		"corpInputs":  {Version: 1, Inputs: corp.Inputs},
		"dnsSettings": {Version: 3, Constants: map[string]any{"domain": "corp.example"}},
	}}
	gs = by(PlanPropertyGroups(&e, r))
	if gs["corpInputs"].Action != ActionUnchanged || gs["corpInputs"].Version != 1 {
		t.Fatalf("corp again = %+v", gs["corpInputs"])
	}
	if d := gs["dnsSettings"]; d.Action != ActionDiffers || d.Version != 3 || !strings.Contains(d.Notes[len(d.Notes)-1].Text, "leaves it as it is") {
		t.Fatalf("dns changed in rvav = %+v", d)
	}
}

// TestMapGroupedTemplate: a template's property group inputs and
// constants: the item names the groups, reads their inputs as its own and
// their constants as propgroup; cloud-init prints a constant.
func TestMapGroupedTemplate(t *testing.T) {
	e := groupedEstate()
	p := MapTemplate(&e, e.Blueprints[len(e.Blueprints)-1])
	v := p.Doc.Versions[0]
	bp := v.Blueprint
	refs, _ := json.Marshal(bp["property_groups"])
	if string(refs) != `[{"key":"corpInputs","version":1},{"key":"dnsSettings","version":1}]` {
		t.Fatalf("property groups = %s", refs)
	}
	props := bp["properties"].(map[string]string)
	if props["costCenter"] != "${inputs.costCenter}" || props["dnsDomain"] != "${propgroup.dnsSettings.domain}" {
		t.Fatalf("properties = %v", props)
	}
	ud := bp["guest"].(map[string]any)["cloud_init"].(map[string]any)["user_data"].(string)
	if !strings.Contains(ud, "fqdn: {{ .vm.name }}.corp.example") || !strings.Contains(ud, "content: {{ .inputs.costCenter | json }}") {
		t.Fatalf("user data = %q", ud)
	}
	if fs := v.FormSchema["properties"].(map[string]any); fs["corp"] != nil || fs["gone"] != nil {
		t.Fatalf("a group input became a field: %v", fs)
	}
	if !has(p, Info, "inputs.corp", "rvav's corpInputs") || !has(p, Unmapped, "inputs.gone", "nowhere isn't in what was read") ||
		!has(p, Info, "resources.Cloud_vSphere_Machine_1.cloudConfig", "${propgroup.dnsSettings.domain} is the constant's value") {
		t.Fatalf("notes = %+v", p.Notes)
	}

	// rvav's dnsSettings differs from vRA's: the item names its latest.
	r := Rvav{Now: time.Now(), ProjectMap: map[string]string{}, PropertyGroups: map[string]RvavGroup{"dnsSettings": {Version: 3}}}
	st := ImportState{Links: map[string]Link{}, Items: map[string]ItemState{}, Slugs: map[string]string{}}
	plan := PlanImport(&e, r, st)
	i := slices.IndexFunc(plan.Items, func(x ItemImport) bool { return x.BlueprintID == "bp-grouped" })
	it := plan.Items[i]
	refs, _ = json.Marshal(it.Doc.Versions[0].Blueprint["property_groups"])
	if string(refs) != `[{"key":"corpInputs","version":1},{"key":"dnsSettings","version":3}]` ||
		!slices.ContainsFunc(it.Notes, func(n Note) bool { return n.Level == Warning && strings.Contains(n.Text, "differs in rvav") }) {
		t.Fatalf("item = %s %+v", refs, it.Notes)
	}
	if plan.Summary.PropertyGroupsCreate != 1 {
		t.Fatalf("summary = %+v", plan.Summary)
	}

	// Imported while dnsSettings was at version 1: a new draft, said on
	// the group.
	st.Links["bp-grouped"] = Link{RvavID: "itm_grouped", Version: "1", Hash: "the hash with version 1"}
	st.Items["itm_grouped"] = ItemState{ID: "itm_grouped", Slug: it.Slug, Published: true}
	plan = PlanImport(&e, r, st)
	g := plan.PropertyGroups[slices.IndexFunc(plan.PropertyGroups, func(g GroupImport) bool { return g.Key == "dnsSettings" })]
	if !slices.ContainsFunc(g.Notes, func(n Note) bool {
		return n.Level == Warning && n.Text == "The import makes a new draft of "+it.Slug+", naming version 3: publish it for requests to use that version."
	}) || !strings.Contains(g.Notes[0].Text, "gets a new draft, even when its template hasn't changed") {
		t.Fatalf("dnsSettings notes = %+v", g.Notes)
	}
}
