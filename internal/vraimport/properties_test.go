package vraimport

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/vra"
)

const keptTemplate = `formatVersion: 1
inputs:
  environment:
    type: string
    enum: [dev, prd]
  app:
    type: string
    title: Application
  apiKey:
    type: string
    encrypted: true
resources:
  Cloud_vSphere_Machine_1:
    type: Cloud.vSphere.Machine
    preventDelete: true
    ignoreChanges: true
    createTimeout: 2h
    metadata:
      layoutPosition: [0, 0]
    properties:
      image: rhel9
      flavor: medium
      folderName: apps/${input.app}-${input.environment}
      costCenter: CC-2002
      owner: ${input.app}-team
      apiAuth: Bearer ${input.apiKey}
      replicas: 3
      backupPolicies: [daily, weekly]
      peer: ${self.resourceName}
      "bad key": x
      remoteAccess:
        authentication: usernamePassword
      cloudConfig: |
        #cloud-config
        chpasswd:
          list: |
            root:${secret.Root_Password}
        runcmd:
          - echo ${secret.Root_Password} ${secret.api-token} > /etc/creds
`

// keptEstate is the sample with a template that keeps its VMs, places
// them in a folder, gives them custom properties and opens secrets.
func keptEstate() vra.Estate {
	e := vra.SampleEstate(time.Now())
	e.Blueprints = append(e.Blueprints, vra.Blueprint{ID: "bp-kept", Name: "Kept server", ProjectID: "vra-prj-web",
		Status: "RELEASED", Content: keptTemplate, Versions: []vra.BlueprintVersion{
			{ID: "bp-kept-v1", BlueprintID: "bp-kept", Version: "1", Status: "RELEASED", Content: keptTemplate}}})
	return e
}

// TestMapKeptTemplate: preventDelete, folderName, custom properties and
// secrets in cloud-init, and the resource flags rvav has none of (T1.5).
func TestMapKeptTemplate(t *testing.T) {
	e := keptEstate()
	p := MapTemplate(&e, e.Blueprints[len(e.Blueprints)-1])
	if p.Status != Partial || p.Doc == nil {
		t.Fatalf("kept = %s %+v", p.Status, p.Notes)
	}
	bp := p.Doc.Versions[0].Blueprint
	if bp["prevent_delete"] != true || bp["folder"] != `apps/${inputs.app}-${environment}` {
		t.Fatalf("blueprint = %+v", bp)
	}
	props, _ := bp["properties"].(map[string]string)
	if want := []string{"costCenter", "owner", "replicas"}; !slices.Equal(slices.Sorted(maps.Keys(props)), want) ||
		props["costCenter"] != "CC-2002" || props["owner"] != "${inputs.app}-team" || props["replicas"] != "3" {
		t.Fatalf("properties = %+v", props)
	}
	ud := bp["guest"].(map[string]any)["cloud_init"].(map[string]any)["user_data"].(string)
	if !strings.Contains(ud, `root:{{ secret "root-password" }}`) || !strings.Contains(ud, `{{ secret "api-token" }} > /etc/creds`) {
		t.Fatalf("user data = %q", ud)
	}
	if !slices.Equal(p.Secrets, []string{"root-password", "api-token"}) {
		t.Fatalf("secrets = %v", p.Secrets)
	}
	for _, n := range []struct{ level, where, text string }{
		{Info, "resources.Cloud_vSphere_Machine_1.preventDelete", "kept from deletion"},
		{Unmapped, "resources.Cloud_vSphere_Machine_1.ignoreChanges", "no rvav equivalent"},
		{Info, "resources.Cloud_vSphere_Machine_1.createTimeout", "rvav's own timeouts"},
		{Info, "resources.Cloud_vSphere_Machine_1.folderName", "apps/"},
		{Unmapped, "resources.Cloud_vSphere_Machine_1.backupPolicies", "a list or a map"},
		{Unmapped, "resources.Cloud_vSphere_Machine_1.peer", "reads the machines"},
		{Unmapped, "resources.Cloud_vSphere_Machine_1.bad key", "A key of up to 64"},
		{Unmapped, "resources.Cloud_vSphere_Machine_1.apiAuth", "reads secret input apiKey"},
		{Info, "resources.Cloud_vSphere_Machine_1.properties", "costCenter, owner, replicas"},
		{Warning, "resources.Cloud_vSphere_Machine_1.cloudConfig", "secret root-password"},
		{Warning, "resources.Cloud_vSphere_Machine_1.cloudConfig", "secret api-token"},
		{Unmapped, "resources.Cloud_vSphere_Machine_1.remoteAccess.username", "No user name"},
	} {
		if !has(p, n.level, n.where, n.text) {
			t.Errorf("no %s note at %s with %q in %+v", n.level, n.where, n.text, p.Notes)
		}
	}
	for _, n := range p.Notes {
		if strings.Contains(n.Where, "metadata") {
			t.Errorf("note on what isn't a custom property: %+v", n)
		}
	}
}

// TestSecretOutsideCloudInit: rvav opens a stored secret in cloud-init
// only; anywhere else it is reported, never given as text.
func TestSecretOutsideCloudInit(t *testing.T) {
	if _, err := (scope{environment: true}).interp("${secret.token}"); err == nil || !strings.Contains(err.Error(), "cloud-init") {
		t.Fatalf("a secret in a property = %v", err)
	}
	for in, want := range map[string]string{"Root_Password": "root-password", "x": "x-secret", "9lives": "s-9lives", "--": ""} {
		if got := SecretName(in); got != want {
			t.Errorf("SecretName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSecretInputsLeftOut: a folder or a custom property reading a secret
// input is left out (T1.7).
func TestSecretInputsLeftOut(t *testing.T) {
	var notes []Note
	note := func(level, where, text string) { notes = append(notes, Note{level, where, text}) }
	c := map[string]any{}
	sc := scope{secrets: map[string]bool{"pw": true, "app": false}}
	mapFolderAndProperties(sc, map[string]any{"folderName": "apps/${input.pw}", "auth": "${input.pw}", "app": "${input.app}"},
		"m", c, note)
	if _, ok := c["folder"]; ok {
		t.Errorf("folder = %v", c["folder"])
	}
	if props, _ := c["properties"].(map[string]string); len(props) != 1 || props["app"] != "${inputs.app}" {
		t.Errorf("properties = %v", c["properties"])
	}
	for _, where := range []string{"m.folderName", "m.auth"} {
		if !slices.ContainsFunc(notes, func(n Note) bool {
			return n.Level == Unmapped && n.Where == where && strings.Contains(n.Text, "reads secret input pw")
		}) {
			t.Errorf("no note at %s in %+v", where, notes)
		}
	}

	// A property group's secret input.
	e := groupedEstate()
	for i, g := range e.PropertyGroups {
		if g.Name == "corpInputs" {
			e.PropertyGroups[i].Properties["token"] = map[string]any{"type": "string", "encrypted": true}
		}
	}
	bp := &e.Blueprints[len(e.Blueprints)-1]
	bp.Versions[0].Content = strings.Replace(bp.Versions[0].Content, "      flavor: medium\n",
		"      flavor: medium\n      auth: ${input.corp.token}\n", 1)
	p := MapTemplate(&e, *bp)
	if props := p.Doc.Versions[0].Blueprint["properties"].(map[string]string); props["auth"] != "" || props["costCenter"] == "" {
		t.Errorf("properties = %v", props)
	}
	if !has(p, Unmapped, "resources.Cloud_vSphere_Machine_1.auth", "reads secret input token") {
		t.Errorf("notes = %+v", p.Notes)
	}
}

// TestMapCustomNaming: a project's custom naming wins over its naming
// template; a counter other than vRA's default is reported.
func TestMapCustomNaming(t *testing.T) {
	ps := plans(t)
	db := ps["bp-db"]
	if db.Doc.Versions[0].Blueprint["naming"] != "db-{environment}-{seq:03}" {
		t.Fatalf("db naming = %v", db.Doc.Versions[0].Blueprint["naming"])
	}
	if !has(db, Unmapped, "naming", "counts from 100 by 10") || !has(db, Info, "naming", "custom naming Database servers") {
		t.Fatalf("db notes = %+v", db.Notes)
	}
	// Web Platform has no custom naming: its own template still names.
	if rhel := ps["bp-rhel9"]; !has(rhel, Info, "naming", "from the project's naming template") {
		t.Fatalf("rhel notes = %+v", rhel.Notes)
	}

	// The organisation's default applies to a project without its own,
	// and counting from 1 by 1 is what rvav does.
	e := vra.SampleEstate(time.Now())
	e.Namings = append(e.Namings, vra.CustomNaming{ID: "cn-org", Name: "Org default",
		Projects:  []vra.NamingProject{{OrgDefault: true}},
		Templates: []vra.NamingTemplate{{ResourceType: "COMPUTE", Pattern: "org-${###}", StartCounter: 1, IncrementStep: 1}}})
	rhel := MapTemplate(&e, e.Blueprints[0])
	if rhel.Doc.Versions[0].Blueprint["naming"] != "org-{seq:03}" || has(rhel, Unmapped, "naming", "") {
		t.Fatalf("rhel = %v %+v", rhel.Doc.Versions[0].Blueprint["naming"], rhel.Notes)
	}
	if db := MapTemplate(&e, e.Blueprints[3]); db.Doc.Versions[0].Blueprint["naming"] != "db-{environment}-{seq:03}" {
		t.Fatalf("db with an org default = %v", db.Doc.Versions[0].Blueprint["naming"])
	}
}

// TestPlanSecretsAndProjectProperties: an item whose cloud-init opens a
// secret rvav lacks waits for it; a vRA project's custom properties the
// rvav project lacks are added, its own kept.
func TestPlanSecretsAndProjectProperties(t *testing.T) {
	e := keptEstate()
	e.Projects[0].Properties = map[string]string{"costCenter": "CC-1001", "tier": "gold", "bad key": "x"}
	r := Rvav{Now: time.Now(), Projects: []RvavProject{{ID: "prj_web", Name: "Web Platform", Environments: []string{"dev"},
		Properties: map[string]string{"costCenter": "CC-9999"}}}, ProjectMap: map[string]string{}, Secrets: map[string]bool{"api-token": true}}
	st := ImportState{Links: map[string]Link{}, Items: map[string]ItemState{}, Slugs: map[string]string{}}
	item := func(p ImportPlan) ItemImport {
		i := slices.IndexFunc(p.Items, func(x ItemImport) bool { return x.BlueprintID == "bp-kept" })
		return p.Items[i]
	}

	p := PlanImport(&e, r, st)
	if it := item(p); it.Action != ActionConflict || !strings.Contains(it.Reason, "root-password") || strings.Contains(it.Reason, "api-token") {
		t.Fatalf("kept without its secret = %s %q", it.Action, it.Reason)
	}
	web := p.Projects[slices.IndexFunc(p.Projects, func(x ProjectPlan) bool { return x.VRAID == "vra-prj-web" })]
	if !maps.Equal(web.PropertiesAdd, map[string]string{"tier": "gold"}) || !slices.Equal(web.PropertiesUnmapped, []string{"bad key"}) {
		t.Fatalf("web properties = %v, unmapped %v", web.PropertiesAdd, web.PropertiesUnmapped)
	}

	r.Secrets["root-password"] = true
	if it := item(PlanImport(&e, r, st)); it.Action != ActionCreate {
		t.Fatalf("kept with its secrets = %s %q", it.Action, it.Reason)
	}
}

// TestMapActiveDirectory: a machine's relative DN is its guest's
// active_directory.ou, ignoreActiveDirectory its skip; neither is a custom
// property, and a relative DN that isn't a DN is left out (ADR-0064).
func TestMapActiveDirectory(t *testing.T) {
	for _, c := range []struct {
		props map[string]any
		want  any
		note  string
	}{
		{map[string]any{"activeDirectory": map[string]any{"relativeDN": "OU=Linux,OU=Web"}}, map[string]any{"ou": "OU=Linux,OU=Web"}, Info},
		{map[string]any{"ignoreActiveDirectory": true, "activeDirectory": map[string]any{"relativeDN": "OU=Linux"}}, map[string]any{"skip": true}, Info},
		{map[string]any{"ignoreActiveDirectory": "true"}, map[string]any{"skip": true}, Info},
		{map[string]any{"activeDirectory": map[string]any{"relativeDN": "${input.ou}"}}, nil, Unmapped},
	} {
		var notes []Note
		out := map[string]any{}
		mapActiveDirectory(c.props, "m", out, func(level, where, text string) { notes = append(notes, Note{level, where, text}) })
		g, _ := out["guest"].(map[string]any)
		if got := g["active_directory"]; !reflect.DeepEqual(got, c.want) && (c.want != nil || got != nil) {
			t.Errorf("%v: active_directory = %v, want %v", c.props, got, c.want)
		}
		if len(notes) != 1 || notes[0].Level != c.note {
			t.Errorf("%v: notes = %+v", c.props, notes)
		}
		props := map[string]any{}
		mapFolderAndProperties(scope{}, c.props, "m", props, func(string, string, string) {})
		if props["properties"] != nil {
			t.Errorf("%v: custom properties %v", c.props, props["properties"])
		}
	}

	// In a template, beside its cloud-init.
	e := vra.SampleEstate(time.Now())
	bp := e.Blueprints[slices.IndexFunc(e.Blueprints, func(b vra.Blueprint) bool { return b.ID == "bp-rhel9" })]
	bp.Versions[1].Content = strings.Replace(bp.Versions[1].Content, "      image: rhel9\n",
		"      image: rhel9\n      activeDirectory:\n        relativeDN: OU=Linux,OU=Web\n", 1)
	p := MapTemplate(&e, bp)
	raw, _ := json.Marshal(p.Doc.Versions[len(p.Doc.Versions)-1].Blueprint)
	b, err := blueprint.Parse(raw)
	if err != nil || b.Guest.ActiveDirectory == nil || b.Guest.ActiveDirectory.OU != "OU=Linux,OU=Web" || b.Guest.CloudInit == nil {
		t.Fatalf("guest = %+v, %v", b.Guest, err)
	}
	if !has(p, Info, "resources.Cloud_vSphere_Machine_1.activeDirectory.relativeDN", "OU=Linux,OU=Web") {
		t.Errorf("notes = %+v", p.Notes)
	}
}

// TestPlanProjectDirectories: each project's plan says the OU vRA gives
// its computer objects, whether its templates may choose theirs, and when
// vRA makes none.
func TestPlanProjectDirectories(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	plan := PlanImport(&e, Rvav{Now: time.Now(), ProjectMap: map[string]string{}},
		ImportState{Links: map[string]Link{}, Items: map[string]ItemState{}, Slugs: map[string]string{}})
	type ad struct {
		ou        string
		blueprint bool
		none      bool
	}
	want := map[string]ad{"vra-prj-web": {"OU=Web,OU=Servers", true, false}, "vra-prj-legacy": {"", false, true}}
	for _, pp := range plan.Projects {
		if got := (ad{pp.ADOU, pp.ADBlueprintOU, pp.ADNone}); got != want[pp.VRAID] {
			t.Errorf("%s: %+v, want %+v", pp.VRAID, got, want[pp.VRAID])
		}
	}
}
