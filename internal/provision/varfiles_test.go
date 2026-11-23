package provision

import (
	"reflect"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/policy"
)

func TestVariableKeys(t *testing.T) {
	got := variableKeys("vars.linux.motd.text + vars.cmdb.site", map[string]any{"body": "{'ca': vars.pki.ca.text, 'm': vars.linux.motd}"}, nil)
	if !slices.Equal(got, []string{"linux.motd", "cmdb.site", "pki.ca"}) {
		t.Fatalf("keys = %v", got)
	}
}

// A binding that is a file's content whole is given on as its marker, at
// any depth; one built from it, or a text equal to it read elsewhere,
// isn't.
func TestByReference(t *testing.T) {
	shared := map[string]any{
		"linux": map[string]any{"motd": map[string]any{"name": "motd.txt", "text": "Welcome\n"}},
		"cmdb":  map[string]any{"greeting": "Welcome\n"},
	}
	with := map[string]any{
		"motd":    "vars.linux.motd.text",
		"files":   "[{'path': '/etc/motd', 'content': vars.linux.motd.text}]",
		"banner":  "vars.linux.motd.text + 'and bye'",
		"same":    "vars.cmdb.greeting",
		"name":    "vars.linux.motd.name",
		"literal": 3,
	}
	vars := policy.Vars{"vars": shared, "inputs": map[string]any{}, "steps": map[string]any{}}
	r, err := resolveStep(ResolveInput{With: with}, vars)
	if err != nil {
		t.Fatal(err)
	}
	byReference(r.Values, with, shared)
	marker := map[string]any{fileKey: "linux.motd"}
	want := map[string]any{
		"motd":    marker,
		"files":   []any{map[string]any{"path": "/etc/motd", "content": marker}},
		"banner":  "Welcome\nand bye",
		"same":    "Welcome\n",
		"name":    "motd.txt",
		"literal": 3,
	}
	if !reflect.DeepEqual(r.Values, want) {
		t.Fatalf("values = %#v", r.Values)
	}
}
