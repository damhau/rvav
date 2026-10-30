package vraimport

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

func plans(t *testing.T) map[string]ItemPlan {
	t.Helper()
	e := vra.SampleEstate(time.Now())
	out := map[string]ItemPlan{}
	for _, bp := range e.Blueprints {
		out[bp.ID] = MapTemplate(&e, bp)
	}
	return out
}

func has(p ItemPlan, level, where, text string) bool {
	return slices.ContainsFunc(p.Notes, func(n Note) bool {
		return n.Level == level && strings.HasPrefix(n.Where, where) && strings.Contains(n.Text, text)
	})
}

// TestMapTemplates: what each sample template becomes, and what it couldn't
// carry over (P2.2).
func TestMapTemplates(t *testing.T) {
	ps := plans(t)

	rhel := ps["bp-rhel9"]
	if rhel.Status != Complete || rhel.Slug != "rhel-9-server" || len(rhel.Doc.Versions) != 1 || rhel.Doc.Versions[0].Number != "2.0" {
		t.Fatalf("rhel = %s %s %+v", rhel.Status, rhel.Slug, rhel.Doc.Versions)
	}
	v := rhel.Doc.Versions[0]
	bp := v.Blueprint
	if bp["template"] != "tpl-rhel9.4-2026.10" || bp["naming"] != "web-{environment}-{inputs.role}{seq:03}" ||
		bp["bindings"].(map[string]any)["vcpu"] != "sizes[inputs.size].vcpu" {
		t.Fatalf("rhel blueprint = %+v", bp)
	}
	if s := bp["sizes"].(map[string]any)["medium"].(map[string]any); s["vcpu"] != 4 || s["memory_gb"] != 16 {
		t.Fatalf("medium = %+v", s)
	}
	ud := bp["guest"].(map[string]any)["cloud_init"].(map[string]any)["user_data"].(string)
	if !strings.Contains(ud, "hostname: {{ .vm.name }}") {
		t.Fatalf("user data = %q", ud)
	}
	props := v.FormSchema["properties"].(map[string]any)
	if _, ok := props["environment"]; ok {
		t.Fatal("the environment input became a field")
	}
	size := v.UISchema["fields"].(map[string]any)["size"].(map[string]any)
	if size["widget"] != "radio_cards" || size["options"].([]map[string]any)[0]["label"] != "Small (2 vCPU, 4 GB)" ||
		v.InitialValues["size"] != "small" {
		t.Fatalf("size field = %+v, initial %v", size, v.InitialValues)
	}
	if !has(rhel, Warning, "resources.Cloud_vSphere_Machine_1.constraints", "env:${input.environment}") {
		t.Fatalf("rhel notes = %+v", rhel.Notes)
	}

	win := ps["bp-win"]
	g := win.Doc.Versions[0].Blueprint["guest"].(map[string]any)
	if win.Status != Complete || g["os_family"] != "windows" || g["customization_spec"] != "win-domain-join" ||
		win.Doc.Metadata.Icon != "monitor" || !has(win, Warning, "naming", "can't be translated") {
		t.Fatalf("win = %s %+v %+v", win.Status, g, win.Notes)
	}

	app := ps["bp-app"]
	comps := app.Doc.Versions[0].Blueprint["components"].([]map[string]any)
	if app.Status != Partial || len(comps) != 2 || comps[1]["count"] != "inputs.webCount" ||
		comps[0]["disks"].([]map[string]any)[0]["size_gb"] != 200 || !has(app, Unmapped, "resources.lb", "Cloud.NSX.LoadBalancer") {
		t.Fatalf("app = %s %+v %+v", app.Status, comps, app.Notes)
	}

	db := ps["bp-db"]
	if db.Status != Partial || !has(db, Unmapped, "form.dbVersion", "com.corp.db/getVersions") {
		t.Fatalf("db = %s %+v", db.Status, db.Notes)
	}
	if req := db.Doc.Versions[0].FormSchema["required"].([]string); !slices.Contains(req, "dbVersion") {
		t.Fatalf("db required = %v", req)
	}

	if d := ps["bp-draft"]; d.Status != Skipped || d.Doc != nil || !has(d, Unmapped, "versions", "No released version") {
		t.Fatalf("draft = %+v", d)
	}
}

// TestMapNSXTemplate: a template's network made for each deployment is an
// NSX segment, its new security groups groups made for each deployment
// with the inbound rules rvav can write; what it can't is reported (N1.4).
func TestMapNSXTemplate(t *testing.T) {
	const content = `formatVersion: 1
resources:
  web:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
      count: 2
      networks:
        - network: ${resource.app_net.id}
          securityGroups: ['${resource.web_sg.id}', '${resource.backup_sg.id}']
  db:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: large
      networks:
        - network: ${resource.app_net.id}
          securityGroups: ['${resource.db_sg.id}']
        - network: ${resource.app_net.id}
  app_net:
    type: Cloud.NSX.Network
    properties:
      networkType: outbound
  web_sg:
    type: Cloud.SecurityGroup
    properties:
      securityGroupType: new
      rules:
        - name: https
          direction: inbound
          source: ANY
          protocol: TCP
          ports: 443
          access: Allow
        - name: out
          direction: outbound
          destination: ANY
          service: HTTPS
        - name: office
          direction: inbound
          source: 10.0.0.0/8
          service: SSH
  db_sg:
    type: Cloud.SecurityGroup
    properties:
      securityGroupType: new
      rules:
        - name: pg
          source: ${resource.web_sg.id}
          protocol: TCP
          ports: "5432"
        - name: nope
          source: ANY
          protocol: TCP
          ports: "8443"
          access: Drop
  backup_sg:
    type: Cloud.SecurityGroup
    properties:
      securityGroupType: existing
      constraints:
        - tag: sg:backup
  lb:
    type: Cloud.NSX.LoadBalancer
    properties:
      instances: ${resource.web[*].id}
`
	e := vra.SampleEstate(time.Now())
	p := MapTemplate(&e, vra.Blueprint{ID: "bp-nsx", Name: "Three tier", Versions: []vra.BlueprintVersion{
		{Version: "1", Status: "RELEASED", Content: content}}})
	if p.Status != Partial || p.Doc == nil {
		t.Fatalf("plan = %s %+v", p.Status, p.Notes)
	}
	v := p.Doc.Versions[0]
	bp := v.Blueprint
	segs, _ := bp["segments"].([]any)
	if len(segs) != 1 || segs[0].(map[string]any)["key"] != "appnet" {
		t.Fatalf("segments = %+v", bp["segments"])
	}
	comps := bp["components"].([]map[string]any)
	for _, c := range comps {
		if c["bindings"].(map[string]any)["network"] != "segments.appnet" {
			t.Fatalf("%s network = %v", c["key"], c["bindings"])
		}
	}
	if g := comps[1]["groups"]; !slices.Equal(g.([]any), []any{"websg"}) || !slices.Equal(comps[0]["groups"].([]any), []any{"dbsg"}) {
		t.Fatalf("groups = %v, %v", comps[0]["groups"], comps[1]["groups"])
	}
	// db's second adapter, on the same network: an added adapter on the segment.
	if nics, _ := comps[0]["nics"].([]any); len(nics) != 1 || nics[0].(map[string]any)["network"] != "segments.appnet" ||
		nics[0].(map[string]any)["key"] != "nic2" {
		t.Fatalf("db's nics = %v", comps[0]["nics"])
	}
	if has(p, Unmapped, "resources.db.networks", "") {
		t.Fatalf("db's adapter reported: %+v", p.Notes)
	}
	sgs := bp["security_groups"].([]any)
	if len(sgs) != 2 {
		t.Fatalf("security groups = %+v", sgs)
	}
	db, web := sgs[0].(map[string]any), sgs[1].(map[string]any)
	dbRules, webRules := db["rules"].([]any), web["rules"].([]any)
	if db["key"] != "dbsg" || len(dbRules) != 1 || !slices.Equal(dbRules[0].(map[string]any)["from"].([]any), []any{"websg"}) ||
		!slices.Equal(dbRules[0].(map[string]any)["services"].([]any), []any{"PostgreSQL"}) {
		t.Fatalf("db_sg = %+v", db)
	}
	if web["key"] != "websg" || len(webRules) != 1 || !slices.Equal(webRules[0].(map[string]any)["services"].([]any), []any{"HTTPS"}) ||
		!slices.Equal(webRules[0].(map[string]any)["from"].([]any), []any{"any"}) {
		t.Fatalf("web_sg = %+v", web)
	}
	// Every VM on the segment: the form asks for no network.
	if _, ok := v.FormSchema["properties"].(map[string]any)["network"]; ok {
		t.Fatal("the form still asks for a network")
	}
	for _, want := range []struct{ level, where, text string }{
		{Info, "resources.app_net", "NSX segment made for each deployment (appnet)"},
		{Info, "resources.web_sg", "with 1 of its 3 rules"},
		{Unmapped, "resources.web_sg.rules[1]", "outbound"},
		{Unmapped, "resources.web_sg.rules[2]", "from addresses (10.0.0.0/8)"},
		{Unmapped, "resources.db_sg.rules[1]", "TCP port 8443"},
		{Unmapped, "resources.backup_sg", "can't tell which NSX group"},
		{Unmapped, "resources.lb", "Cloud.NSX.LoadBalancer"},
	} {
		if !has(p, want.level, want.where, want.text) {
			t.Errorf("no %s note at %s with %q: %+v", want.level, want.where, want.text, p.Notes)
		}
	}
}
