package core

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
)

// References between components and the order they are built in are
// checked when the blueprint is saved (T1.1).
func TestCheckReferences(t *testing.T) {
	check := func(raw string) []string {
		t.Helper()
		b, err := blueprint.Parse(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range checkVMBlueprint(b, "/blueprint", false) {
			out = append(out, e.Field+" "+*e.Code)
		}
		slices.Sort(out)
		return out
	}
	good := `{"kind": "vm", "template": "rhel9", "guest": {"os_family": "linux"},
		"components": [
		  {"key": "db", "count": 2, "bindings": {"tags": "['role/db', 'node/' + string(self.index)]"}},
		  {"key": "cache"},
		  {"key": "web", "count": 2, "depends_on": ["cache"],
		   "naming": "{project}-{bindings.role}{seq:02}",
		   "bindings": {"role": "inputs.app.lowerAscii().replace('_', '')",
		     "tags": "['db/' + components.db.addresses[0]]"},
		   "guest": {"cloud_init": {"user_data": "#cloud-config\nwrite_files:\n- path: /etc/app\n  content: |\n    db={{ (index .components.db.vms 0).address }} me={{ .vm.index }}\n    peers={{ range .components.web.addresses }}{{ . }} {{ end }}\n"}}}]}`
	if errs := check(good); len(errs) > 0 {
		t.Fatalf("a good blueprint: %v", errs)
	}
	// Its tags reading its own component are refused: they are attached
	// before its VMs all have addresses. Its cloud-init may.
	ownTags := strings.Replace(good, `components.db.addresses[0]]`, `components.db.addresses[0], 'peers/' + string(components.web.count)]`, 1)
	if errs := check(ownTags); !slices.Equal(errs, []string{"/blueprint/components/2/bindings/tags invalid"}) {
		t.Fatalf("tags reading their own component: %v", errs)
	}
	b, _ := blueprint.Parse(json.RawMessage(good))
	web := b.ComponentList()[2]
	if deps := b.DependsOn(web); !slices.Equal(deps, []string{"db", "cache"}) {
		t.Errorf("web depends on %v", deps)
	}
	if reads := b.Reads(web); !slices.Equal(reads, []string{"db", "web"}) {
		t.Errorf("web reads %v", reads)
	}

	bad := `{"kind": "vm", "template": "rhel9", "guest": {"os_family": "linux"},
		"naming": "{project}-{bindings.nope}{seq:02}", "deployment_name": "{bindings.role}",
		"components": [
		  {"key": "db", "depends_on": ["web", "db", "ghost"], "bindings": {"vcpu": "self.index + 1"}},
		  {"key": "web", "bindings": {"tags": "[components['db'].names[0]]", "role": "'r'"},
		   "guest": {"cloud_init": {"user_data": "{{ range .components }}{{ . }}{{ end }} {{ .components.app.names }}"}}},
		  {"key": "app", "bindings": {"tags": "['x/' + components.cache.names[0]]"}}]}`
	want := []string{
		"/blueprint/components/0/bindings/vcpu invalid", // only tags is worked out as each VM is built
		"/blueprint/components/0/depends_on/1 invalid",  // itself
		"/blueprint/components/0/depends_on/2 unknown_component",
		"/blueprint/components/1/bindings/tags invalid", // components['db']
		"/blueprint/components/1/guest/cloud_init/user_data invalid",
		"/blueprint/components/2/bindings/tags unknown_component",
		"/blueprint/deployment_name invalid",
		"/blueprint/naming unknown_binding",
	}
	// The naming db and app inherit is said once, at the blueprint's.
	errs := check(bad)
	if !slices.Equal(errs, want) {
		t.Fatalf("bad:\n%s\nwant:\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"))
	}

	cycle := `{"kind": "vm", "template": "rhel9", "guest": {"os_family": "linux"},
		"components": [
		  {"key": "a", "depends_on": ["c"]},
		  {"key": "b", "depends_on": ["a"]},
		  {"key": "c", "guest": {"cloud_init": {"user_data": "x={{ .components.b.names }}"}}}]}`
	b, _ = blueprint.Parse(json.RawMessage(cycle))
	if got := b.Cycle(); !slices.Equal(got, []string{"a", "c", "b", "a"}) {
		t.Errorf("cycle %v", got)
	}
	if errs := check(cycle); !slices.Equal(errs, []string{"/blueprint/components/0/depends_on/0 cycle"}) {
		t.Errorf("a cycle: %v", errs)
	}
	// Found through a reference: said at the reference.
	cycle = `{"kind": "vm", "template": "rhel9", "guest": {"os_family": "linux"},
		"components": [
		  {"key": "a", "bindings": {"tags": "['n/' + components.b.names[0]]"}},
		  {"key": "b", "depends_on": ["a"]}]}`
	if errs := check(cycle); !slices.Equal(errs, []string{"/blueprint/components/0/bindings/tags cycle"}) {
		t.Errorf("a cycle through tags: %v", errs)
	}

	// A single VM has no components to read; self is its own.
	single := `{"kind": "vm", "template": "rhel9", "guest": {"os_family": "linux"},
		"bindings": {"tags": "['n/' + self.name, 'x/' + components.db.names[0]]"}}`
	if errs := check(single); !slices.Equal(errs, []string{"/blueprint/bindings/tags unknown_component"}) {
		t.Errorf("a single VM: %v", errs)
	}
}
