package blueprint

import (
	"maps"
	"testing"

	"github.com/damhau/rvav/internal/policy"
)

func TestFolderUnder(t *testing.T) {
	for _, c := range []struct{ rule, folder, want string }{
		{"/GVA/vm/rvav/web/dev", "", "/GVA/vm/rvav/web/dev"},
		{"/GVA/vm/rvav/web/dev", "apps/shop", "/GVA/vm/apps/shop"},
		{"/GVA/vm/rvav/web/dev", "/ZRH/vm/shop/", "/ZRH/vm/shop"},
		{"/Lab/rvav", "shop", "/Lab/rvav/shop"},
	} {
		if got := FolderUnder(c.rule, c.folder); got != c.want {
			t.Errorf("FolderUnder(%q, %q) = %q, want %q", c.rule, c.folder, got, c.want)
		}
	}
	if CheckFolder("apps/../../x") == "" || CheckFolder("apps/${inputs.app") == "" || CheckFolder("apps/${inputs.app}") != "" {
		t.Fatal("folder checks")
	}
	if CheckProperty("cost-center.1", "x") != "" || CheckProperty("1cost", "x") == "" || CheckProperty("a b", "x") == "" {
		t.Fatal("property key checks")
	}
}

// A component's properties go over the blueprint's, and are worked out
// with the project's as the context; its folder and prevent_delete replace
// the blueprint's.
func TestComponentProperties(t *testing.T) {
	b, err := Parse([]byte(`{"kind": "vm", "template": "t", "folder": "apps", "prevent_delete": true,
		"properties": {"owner": "web team", "tier": "${inputs.tier}"},
		"bindings": {"cluster": "'c'", "datastore": "'d'", "network": "'n'"},
		"components": [
			{"key": "db", "folder": "/GVA/vm/db", "prevent_delete": false,
			 "properties": {"tier": "gold", "cost": "${project.properties.cc}"}},
			{"key": "web", "properties": {"app": "${inputs.app}"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	b = b.WithContext(policy.Vars{"project": map[string]any{"code": "web", "properties": map[string]any{"cc": "CC-1"}}})
	list := b.ComponentList()
	db, _, missing := b.EvaluateComponent(list[0], map[string]any{"tier": "silver"})
	if len(missing) != 0 || db.Folder != "/GVA/vm/db" || db.PreventDelete ||
		!maps.Equal(db.Properties, map[string]string{"owner": "web team", "tier": "gold", "cost": "CC-1"}) {
		t.Fatalf("db = %+v, missing %v", db, missing)
	}
	web, _, missing := b.EvaluateComponent(list[1], map[string]any{"tier": "silver"})
	if web.Folder != "apps" || !web.PreventDelete || len(missing) != 1 || missing[0] != "properties/app" ||
		!maps.Equal(web.Properties, map[string]string{"owner": "web team", "tier": "silver"}) {
		t.Fatalf("web = %+v, missing %v", web, missing)
	}
}
