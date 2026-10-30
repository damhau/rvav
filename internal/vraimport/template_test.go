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
