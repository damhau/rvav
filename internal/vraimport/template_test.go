package vraimport

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

func TestFitWidget(t *testing.T) {
	for _, c := range []struct {
		widget string
		prop   map[string]any
		want   string
		fits   bool
		after  map[string]any
	}{
		{"datetime", map[string]any{"type": "string", "format": "date"}, "date", true, map[string]any{"type": "string", "format": "date"}},
		{"datetime", map[string]any{"type": "string"}, "datetime", true, map[string]any{"type": "string", "format": "date-time"}},
		{"datetime", map[string]any{"type": "integer"}, "datetime", false, nil},
		{"number", map[string]any{"type": "number"}, "number", true, nil},
		{"toggle", map[string]any{"type": "string"}, "toggle", false, nil},
		{"tags", map[string]any{"type": "array"}, "tags", true, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
		{"tags", map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "tags", false, nil},
		{"tags", map[string]any{"type": "string"}, "tags", false, nil},
		{"multiselect", map[string]any{"type": "array"}, "multiselect", true, nil},
		{"multiselect", map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "multiselect", false, nil},
		{"datagrid", map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "datagrid", true, nil},
		{"datagrid", map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "datagrid", false, nil},
		{"secret", map[string]any{"type": "integer"}, "secret", false, nil},
		{"text", map[string]any{"type": "array"}, "text", false, nil},
	} {
		got, fits := fitWidget(c.widget, c.prop)
		if got != c.want || fits != c.fits {
			t.Errorf("%s on %v = %s %v, want %s %v", c.widget, c.prop, got, fits, c.want, c.fits)
		}
		if c.after != nil && !reflect.DeepEqual(c.prop, c.after) {
			t.Errorf("%s: the input became %v, want %v", c.widget, c.prop, c.after)
		}
	}
}

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
	// Placed by its constraint tags (ADR-0035): the form asks for no
	// cluster, datastore or network.
	cons := bp["constraints"].(map[string]any)
	if !slices.Equal(cons["compute"].([]any), []any{"env/${environment}", "!rvav-zone/pci"}) ||
		!slices.Equal(cons["storage"].([]any), []any{"rvav-tier/gold:soft"}) || !slices.Equal(cons["network"].([]any), []any{"net/app"}) {
		t.Fatalf("rhel constraints = %+v", cons)
	}
	for _, f := range []string{"cluster", "datastore", "network"} {
		if _, ok := props[f]; ok || bp["bindings"].(map[string]any)[f] != nil {
			t.Fatalf("rhel still asks for its %s: %+v, %+v", f, props, bp["bindings"])
		}
	}
	if !has(rhel, Info, "resources.Cloud_vSphere_Machine_1.constraints", "Placed by its constraint tags") {
		t.Fatalf("rhel notes = %+v", rhel.Notes)
	}
	for _, s := range v.UISchema["layout"].(map[string]any)["sections"].([]map[string]any) {
		if s["key"] == "placement" {
			t.Fatalf("rhel's form has a placement section: %+v", s)
		}
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
		comps[0]["disks"].([]map[string]any)[0]["size_gb"] != 200 || !has(app, Unmapped, "resources.lb", "Cloud.NSX.LoadBalancer") ||
		!has(app, Warning, "naming", "Named after each component ({project}-{environment}-db{seq:02}, {project}-{environment}-web{seq:02}), not by") {
		t.Fatalf("app = %s %+v %+v", app.Status, comps, app.Notes)
	}
	// The database's disk has its own constraint; its network none, so the
	// form still asks where the VMs go.
	if d := comps[0]["disks"].([]map[string]any)[0]; !slices.Equal(d["constraints"].([]any), []any{"rvav-tier/gold"}) ||
		comps[0]["bindings"].(map[string]any)["cluster"] != "inputs.cluster" || !has(app, Warning, "resources.db.constraints", "no constraint tags") {
		t.Fatalf("app's db = %+v, notes %+v", comps[0], app.Notes)
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

// TestMapFieldTypes: each of vRA's form widgets on an input of each type
// becomes rvav's field, or is reported; a secret keeps no default (F1.1).
// A dual list, an object, and the text, image and link elements map to
// rvav's own (F1.2).
func TestMapFieldTypes(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	bp, form := vra.SampleFieldTypes()
	e.Blueprints, e.Forms = append(e.Blueprints, bp), append(e.Forms, form)
	p := MapTemplate(&e, bp)
	if p.Doc == nil {
		t.Fatalf("plan = %s %+v", p.Status, p.Notes)
	}
	v := p.Doc.Versions[0]
	props := v.FormSchema["properties"].(map[string]any)
	fields := v.UISchema["fields"].(map[string]any)
	prop := func(name string) map[string]any { p, _ := props[name].(map[string]any); return p }
	field := func(name string) map[string]any { f, _ := fields[name].(map[string]any); return f }
	widget := func(name string) any { return field(name)["widget"] }

	if widget("window") != "datetime" || prop("window")["format"] != "date-time" {
		t.Errorf("window = %v %v", field("window"), prop("window"))
	}
	if widget("ratio") != "number" || prop("ratio")["type"] != "number" || prop("ratio")["minimum"] != 0.5 {
		t.Errorf("ratio = %v %v", field("ratio"), prop("ratio"))
	}
	required, _ := v.FormSchema["required"].([]string)
	for _, s := range []string{"adminPassword", "apiToken"} {
		if widget(s) != "secret" || field(s)["secret"] != true || v.InitialValues[s] != nil || !slices.Contains(required, s) {
			t.Errorf("%s = %v, initial %v, required %v", s, field(s), v.InitialValues[s], required)
		}
	}
	if items := prop("labels")["items"].(map[string]any); widget("labels") != "tags" || items["enum"] != nil || len(field("labels")["options"].([]map[string]any)) != 2 {
		t.Errorf("labels = %v %v", field("labels"), prop("labels"))
	}
	features := prop("features")["items"].(map[string]any)
	if widget("features") != "multiselect" || !reflect.DeepEqual(features["enum"], []any{"backup", "monitoring"}) ||
		field("features")["options"].([]map[string]any)[0]["label"] != "Back up" || !reflect.DeepEqual(v.InitialValues["features"], []any{"backup"}) {
		t.Errorf("features = %v %v, initial %v", field("features"), prop("features"), v.InitialValues["features"])
	}
	if widget("owners") != "tags" {
		t.Errorf("owners = %v", field("owners"))
	}
	teams := prop("teams")["items"].(map[string]any)
	if widget("teams") != "dual_list" || !reflect.DeepEqual(teams["enum"], []any{"web", "net"}) || field("teams")["options"].([]map[string]any)[1]["label"] != "Network team" {
		t.Errorf("teams = %v %v", field("teams"), prop("teams"))
	}
	if zones := prop("zones")["items"].(map[string]any); widget("zones") != "multiselect" || !reflect.DeepEqual(zones["enum"], []any{"dmz", "lan"}) {
		t.Errorf("zones = %v %v", field("zones"), prop("zones"))
	}
	var subs []string
	for _, c := range field("backup")["columns"].([]map[string]any) {
		subs = append(subs, fmt.Sprintf("%s:%s:%s", c["key"], c["label"], c["widget"]))
	}
	backup := prop("backup")
	if widget("backup") != "object" || !slices.Equal(subs, []string{"policy:Policy:select", "keepDays:Keep (days):number"}) ||
		backup["type"] != "object" || !reflect.DeepEqual(backup["required"], []any{"policy"}) {
		t.Errorf("backup = %v, sub-fields %v", backup, subs)
	}
	for name, want := range map[string]map[string]any{
		"text_notice": {"widget": "info", "label": "Text", "help": "Ask the network team before you open a port."},
		"image_zones": {"widget": "image", "label": "Network zones", "url": "https://wiki.corp.example/img/zones.png", "alt": "Network zones"},
		"link_sizes":  {"widget": "link", "label": "VM sizes explained", "url": "https://wiki.corp.example/vm-sizes"},
	} {
		if _, ok := props[name]; ok || !reflect.DeepEqual(field(name), want) {
			t.Errorf("%s = %v (a property: %v), want %v", name, field(name), ok, want)
		}
	}
	if _, ok := fields["image_logo"]; ok {
		t.Errorf("a data: image became a field: %v", field("image_logo"))
	}
	var shown []string
	for _, s := range v.UISchema["layout"].(map[string]any)["sections"].([]map[string]any) {
		shown = append(shown, s["fields"].([]string)...)
	}
	if !slices.Contains(shown, "image_zones") || slices.Contains(shown, "image_logo") {
		t.Errorf("the layout shows %v", shown)
	}
	var cols []string
	for _, c := range field("disks")["columns"].([]map[string]any) {
		cols = append(cols, fmt.Sprintf("%s:%s:%s", c["key"], c["label"], c["widget"]))
	}
	rows := prop("disks")["items"].(map[string]any)
	if widget("disks") != "datagrid" || !slices.Equal(cols, []string{"size:Size (GB):number", "tier:Tier:select", "label:Label:text", "thin:Thin:toggle"}) ||
		!reflect.DeepEqual(rows["required"], []any{"size"}) || prop("disks")["maxItems"] != 4 ||
		!reflect.DeepEqual(rows["properties"].(map[string]any)["tier"], map[string]any{"type": "string", "enum": []any{"gold", "silver"}}) {
		t.Errorf("disks = %v, columns %v, rows %v", prop("disks"), cols, rows)
	}
	for _, gone := range []string{"ports", "meta"} {
		if _, ok := props[gone]; ok {
			t.Errorf("%s became a field: %v", gone, props[gone])
		}
		for _, s := range v.UISchema["layout"].(map[string]any)["sections"].([]map[string]any) {
			if slices.Contains(s["fields"].([]string), gone) {
				t.Errorf("section %s names %s, which is left out", s["key"], gone)
			}
		}
	}
	for _, want := range []struct{ level, where, text string }{
		{Info, "inputs.adminPassword", "Encrypted in vRA"},
		{Warning, "inputs.adminPassword", "default isn't kept"},
		{Warning, "form.apiToken", "default isn't kept"},
		{Unmapped, "form.owners", "make it a dual list again"},
		{Unmapped, "form.catalog", "rvav's equivalent is a search field"},
		{Warning, "form.image_zones", `its label, "Network zones"`},
		{Unmapped, "form.image_logo", "a data: URI"},
		{Info, "form.text_notice", "information text"},
		{Unmapped, "form.catalog", "com.corp.cmdb/search"},
		{Unmapped, "inputs.ports", "A list of integer values"},
		{Unmapped, "inputs.meta", "An object without properties"},
	} {
		if !has(p, want.level, want.where, want.text) {
			t.Errorf("no %s note at %s with %q: %+v", want.level, want.where, want.text, p.Notes)
		}
	}
	if has(p, Warning, "form.adminPassword", "default isn't kept") {
		t.Errorf("the dropped default is reported twice: %+v", p.Notes)
	}
}

// TestMapFormPages: a form with several pages gets the tabs layout, as vRA
// shows them, one tab per page with the fields of its sections; inputs the
// form doesn't show come after, in a tab of their own (F1.3).
func TestMapFormPages(t *testing.T) {
	form := &vra.Form{Form: `{"layout":{"pages":[
{"id":"page_general","title":"General","sections":[
 {"id":"s_role","fields":[{"id":"role","display":"textField"}]},
 {"id":"s_size","fields":[{"id":"size","display":"dropDown"}]}]},
{"id":"page_owner","title":"","sections":[
 {"id":"s_owner","fields":[{"id":"owner","display":"textField"}]}]}]},
"schema":{"role":{"label":"Role"},"size":{"label":"Size"},"owner":{"label":"Owner"}}}`}
	props := map[string]any{"role": map[string]any{"type": "string"}, "size": map[string]any{"type": "string"},
		"owner": map[string]any{"type": "string"}, "notes": map[string]any{"type": "string"}}
	fields := map[string]any{"role": map[string]any{"widget": "text", "label": "role"}, "size": map[string]any{"widget": "text", "label": "size"},
		"owner": map[string]any{"widget": "text", "label": "owner"}, "notes": map[string]any{"widget": "text", "label": "notes"}}
	ui := map[string]any{"fields": fields, "layout": map[string]any{"kind": "page", "sections": []map[string]any{
		{"key": "inputs", "title": "Request", "columns": 2, "fields": []string{"role", "size", "owner", "notes"}}}}}
	var notes []string
	required := []string{}
	applyForm(form, props, fields, &required, map[string]any{}, ui, func(level, where, text string) {
		notes = append(notes, level+" "+where+": "+text)
	})
	layout := ui["layout"].(map[string]any)
	var got []string
	for _, s := range layout["sections"].([]map[string]any) {
		got = append(got, fmt.Sprintf("%s %s %v", s["key"], s["title"], s["fields"]))
	}
	want := []string{"p1 General [role size]", "p2 Page 2 [owner]", "more More [notes]"}
	if layout["kind"] != "tabs" || !slices.Equal(got, want) {
		t.Errorf("layout %v %v, want tabs %v", layout["kind"], got, want)
	}
	if !slices.ContainsFunc(notes, func(n string) bool { return strings.Contains(n, "Its pages are tabs") }) {
		t.Errorf("notes = %v", notes)
	}
}
