package blueprint

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestUserDataComponents(t *testing.T) {
	for text, want := range map[string][]string{
		`{{ .vm.name }}`: nil,
		`{{ (index .components.db.vms 0).address }} {{ .components.cache.names | json }}`:       {"db", "cache"},
		`{{ range .components.web.addresses }}{{ . }}{{ end }}`:                                 {"web"},
		`{{ if .components.db.count }}{{ with $.components.db }}{{ .names }}{{ end }}{{ end }}`: {"db"},
		`{{ define "x" }}{{ .components.lb.fqdns }}{{ end }}{{ template "x" . }}`:               {"lb"},
	} {
		got, err := UserDataComponents(text)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: %v, %v; want %v", text, got, err, want)
		}
	}
	for _, text := range []string{`{{ range .components }}{{ . }}{{ end }}`, `{{ index .components "db" }}`} {
		if _, err := UserDataComponents(text); err == nil {
			t.Errorf("%s: read components without naming one", text)
		}
	}
}

// A tags binding reading self or components is left to the clone; the
// others are worked out when the request is submitted.
func TestBuildTimeTags(t *testing.T) {
	b, err := Parse(json.RawMessage(`{"kind": "vm", "bindings": {"tags": "['app/' + inputs.app.lowerAscii()]"},
		"components": [
		  {"key": "db"},
		  {"key": "web", "bindings": {"tags": "['node/' + string(self.index), 'db/' + components.db.names[0]]"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	comps := b.ComponentList()
	db, _, missing := b.EvaluateComponent(comps[0], map[string]any{"app": "Shop"})
	if !slices.Equal(db.Tags, []string{"app/shop"}) || len(missing) > 0 {
		t.Errorf("db's tags %v, missing %v", db.Tags, missing)
	}
	web, _, missing := b.EvaluateComponent(comps[1], map[string]any{"app": "Shop"})
	if len(web.Tags) > 0 || len(missing) > 0 {
		t.Errorf("web's tags %v, missing %v", web.Tags, missing)
	}
	if r := comps[1].Refs(); !r.BuildTags || !slices.Equal(r.Tags, []string{"db"}) {
		t.Errorf("web's refs %+v", r)
	}
	if r := comps[0].Refs(); r.BuildTags {
		t.Errorf("db's refs %+v", r)
	}
}

// The topology says which components each is built after, by key only.
func TestTopologyDependsOn(t *testing.T) {
	raw := json.RawMessage(`{"kind": "vm", "guest": {"cloud_init": {"user_data": "db={{ .components.db.addresses }}"}},
		"components": [
		  {"key": "db", "guest": {"cloud_init": {"user_data": "#cloud-config"}}},
		  {"key": "cache"},
		  {"key": "web", "depends_on": ["cache"]}]}`)
	got, _ := json.Marshal(Topology(raw)["components"])
	if want := `[{"guest":{},"key":"db"},{"depends_on":["db"],"key":"cache"},{"depends_on":["db","cache"],"key":"web"}]`; string(got) != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}
