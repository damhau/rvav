package vraimport

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/vra"
)

// A fake vRA's template whose machines read each other, with count.index,
// dependsOn and string functions (T1.1).
const referencesTemplate = `formatVersion: 1
inputs:
  app:
    type: string
  webCount:
    type: integer
    default: 2
  ha:
    type: boolean
    default: false
resources:
  db:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: large
      count: '${input.ha ? 2 : 1}'
      tags:
        - key: app
          value: ${to_lower(input.app)}
        - key: backup
          value: daily
  web_server:
    type: Cloud.vSphere.Machine
    dependsOn: [db, lb, app_net]
    properties:
      image: rhel9
      flavor: small
      count: ${input.webCount}
      networks:
        - network: ${resource.app_net.id}
      tags:
        - key: node
          value: web-${count.index}
        - key: db
          value: ${replace(resource.db[0].address, '.', '-')}
        - key: peers
          value: ${join(resource.web_server[*].address, ',')}
        - key: env
          value: ${input.environment}
        - key: short
          value: ${substring(to_upper(input.app), 0, 3)}
        - key: owner
          value: ${env.requestedBy}
      cloudConfig: |
        #cloud-config
        write_files:
          - path: /etc/app.conf
            content: |
              db=${resource.db[0].address}
              dbs=${resource.db.address}
              db_names=${resource.db.resourceName}
              me=${self.resourceName} ${count.index}
              peer=${resource.web_server[count.index].address}
              app=${input.app}
              up=${to_upper(input.app)}
              lb=${resource.lb.address}
  lb:
    type: Cloud.NSX.LoadBalancer
    properties: {}
  app_net:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
`

func TestMapReferences(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	p := MapTemplate(&e, vra.Blueprint{ID: "bp-refs", Name: "Shop", Versions: []vra.BlueprintVersion{
		{Version: "1", Status: "RELEASED", Content: referencesTemplate}}})
	if p.Doc == nil {
		t.Fatalf("plan = %s %+v", p.Status, p.Notes)
	}
	comps := p.Doc.Versions[0].Blueprint["components"].([]map[string]any)
	db, web := comps[0], comps[1]
	if db["key"] != "db" || web["key"] != "webserver" || web["naming"] != "{project}-{environment}-webserver{seq:02}" {
		t.Fatalf("keys %v %v, naming %v", db["key"], web["key"], web["naming"])
	}
	if db["count"] != "inputs.ha ? 2 : 1" || web["count"] != "inputs.webCount" || db["depends_on"] != nil ||
		!slices.Equal(web["depends_on"].([]string), []string{"db"}) {
		t.Fatalf("counts %v %v, depends_on %v %v", db["count"], web["count"], db["depends_on"], web["depends_on"])
	}
	dbTags := db["bindings"].(map[string]any)["tags"]
	if !slices.Equal(db["tags"].([]string), []string{"backup/daily"}) || dbTags != "['app/' + inputs.app.lowerAscii()]" {
		t.Fatalf("db's tags %v, binding %v", db["tags"], dbTags)
	}
	webTags := web["bindings"].(map[string]any)["tags"].(string)
	if want := "['node/web-' + string(self.index), 'db/' + components.db.vms[0].address.replace('.', '-'), " +
		"'short/' + inputs.app.upperAscii().substring(0, 3)]"; webTags != want {
		t.Fatalf("web's tags\n%s\nwant\n%s", webTags, want)
	}
	for _, expr := range []string{dbTags.(string), webTags, db["count"].(string)} {
		if err := policy.CheckBuild(expr); err != nil {
			t.Errorf("%s: %v", expr, err)
		}
	}
	// The block holds an input: it is one text printed through json.
	ud := web["guest"].(map[string]any)["cloud_init"].(map[string]any)["user_data"].(string)
	if want := `content: {{ printf "db=%v\ndbs=%v\ndb_names=%v\nme=%v %v\npeer=%v\napp=%v\nup=${to_upper(input.app)}\nlb=${resource.lb.address}\n" ` +
		"(index .components.db.vms 0).address (.components.db.addresses | json) (.components.db.names | json) .vm.name .vm.index " +
		"(index .components.webserver.vms .vm.index).address .inputs.app | json }}\n"; !strings.HasSuffix(ud, want) {
		t.Errorf("user data\n%s\nwant it to end\n%s", ud, want)
	}
	if err := blueprint.CheckUserData(ud); err != nil {
		t.Errorf("user data: %v", err)
	}
	if read, err := blueprint.UserDataComponents(ud); err != nil || !slices.Equal(read, []string{"db", "webserver"}) {
		t.Errorf("user data reads %v, %v", read, err)
	}
	for _, want := range []struct{ level, where, text string }{
		{Unmapped, "resources.web_server.tags[2]", "reads its own machines"},
		{Unmapped, "resources.web_server.tags[3]", "the environment"},
		{Unmapped, "resources.web_server.tags[5]", "env.requestedBy has no rvav equivalent"},
		{Unmapped, "resources.web_server.dependsOn", "lb (Cloud.NSX.LoadBalancer)"},
		{Info, "resources.web_server.dependsOn", "Built after db"},
		{Warning, "resources.web_server.cloudConfig", "${to_upper(input.app)}: rvav's user data prints values"},
		{Warning, "resources.web_server.cloudConfig", "reads lb, which isn't a machine rvav builds"},
		{Warning, "resources.db.tags", "the categories must exist in vCenter"},
	} {
		if !has(p, want.level, want.where, want.text) {
			t.Errorf("no %s note at %s with %q: %+v", want.level, want.where, want.text, p.Notes)
		}
	}
	if has(p, Unmapped, "resources.web_server.dependsOn", "app_net") {
		t.Error("a dependency on a network, which rvav makes first, is reported")
	}
}

func TestTranslateExpressions(t *testing.T) {
	s := scope{machines: map[string]machineRef{"db": {key: "db"}, "web": {key: "web", many: true}}, self: "web"}
	for src, want := range map[string]string{
		"input.a + '-' + to_lower(input.b)":      "inputs.a + '-' + inputs.b.lowerAscii()",
		"to_upper(input.a + input.b)":            "(inputs.a + inputs.b).upperAscii()",
		"split(input.list, ',')[0]":              "inputs.list.split(',')[0]",
		"input.n > 2 && input.on == true":        "inputs.n > 2 && inputs.on == true",
		"resource.web.address":                   "components.web.addresses",
		"resource.db.address":                    "components.db.addresses[0]",
		"resource.db.networks[0].address":        "components.db.addresses[0]",
		"resource.web[count.index].resourceName": "components.web.vms[self.index].name",
		"resource.db.count":                      "components.db.count",
	} {
		got, err := s.cel(src)
		if err != nil || got.cel != want {
			t.Errorf("%s: %q, %v; want %q", src, got.cel, err, want)
		}
	}
	for _, src := range []string{"length(input.a)", "resource.disk1.capacityGb", "resource.db.cpuCount", "env.projectName", "'open",
		"to_lower()", "replace(, 'a', 'b')", "to_upper(input.a"} {
		if got, err := s.cel(src); err == nil {
			t.Errorf("%s translated as %q", src, got.cel)
		}
	}
}
