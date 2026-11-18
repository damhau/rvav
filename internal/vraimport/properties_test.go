package vraimport

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

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
