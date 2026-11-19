package vraimport

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/vra"
)

// TestMapFormDefaults: a custom form's bound and computed defaults become
// default rules, or a value rule on a field read-only in vRA; constants stay
// initial values; an action's or a conditional default is reported (F1.4).
func TestMapFormDefaults(t *testing.T) {
	ids := []string{"app", "host", "fqdn", "cores", "double", "label", "owner", "size", "tier", "weird"}
	var layout []string
	for _, id := range ids {
		state := `{"visible":true,"read-only":false}`
		if id == "fqdn" {
			state = `{"visible":true,"read-only":true}`
		}
		layout = append(layout, `{"id":"`+id+`","display":"textField","state":`+state+`}`)
	}
	form := &vra.Form{Form: `{"layout":{"pages":[{"id":"p","title":"General","sections":[{"id":"s","fields":[` +
		strings.Join(layout, ",") + `]}]}]},"schema":{
"app":{"label":"Application"},
"host":{"label":"Host","default":{"bind":"app"}},
"fqdn":{"label":"FQDN","default":{"bind":{"values":["host","` + "`.corp.example`" + `"],"operator":"concatenate"}}},
"cores":{"label":"Cores","default":2},
"double":{"label":"Twice","default":{"bind":{"values":["cores","` + "`2`" + `"],"operator":"multiply"}}},
"label":{"label":"Label","default":{"bind":{"values":["environment","` + "`-`" + `","cores"],"operator":"concatenate"}}},
"owner":{"label":"Owner","default":{"id":"com.corp.cmdb/getOwner","type":"scriptAction","parameters":[]}},
"size":{"label":"Size","default":[{"equals":{"environment":"prd"},"value":"large"},{"value":"small"}]},
"tier":{"label":"Tier","default":{"bind":{"values":["app","` + "`x`" + `"],"operator":"ratio"}}},
"weird":{"label":"Weird","default":{"bind":"nothere"}}}}`}
	props := map[string]any{"cores": map[string]any{"type": "integer"}, "double": map[string]any{"type": "number"}}
	fields := map[string]any{}
	for _, id := range ids {
		if _, ok := props[id]; !ok {
			props[id] = map[string]any{"type": "string"}
		}
		fields[id] = map[string]any{"widget": "text", "label": id}
	}
	initial := map[string]any{"host": "web01"}
	ui := map[string]any{"fields": fields, "layout": map[string]any{"kind": "page", "sections": []map[string]any{
		{"key": "inputs", "title": "Request", "columns": 2, "fields": ids}}}}
	var notes []string
	required := []string{}
	applyForm(form, props, fields, &required, initial, ui, nil, func(level, where, text string) {
		notes = append(notes, level+" "+where+": "+text)
	})

	rule := func(id, kind string) any {
		r, _ := fields[id].(map[string]any)["rules"].(map[string]any)
		return r[kind]
	}
	for _, c := range []struct{ id, kind, want string }{
		{"host", "default", "inputs.app"},
		{"fqdn", "value", `inputs.host + ".corp.example"`},
		{"double", "default", "double(inputs.cores) * 2.0"},
		{"label", "default", `environment + "-" + string(inputs.cores)`},
	} {
		if got := rule(c.id, c.kind); got != c.want {
			t.Errorf("%s's %s rule = %v, want %s", c.id, c.kind, got, c.want)
		} else if err := policy.Check(c.want); err != nil {
			t.Errorf("%s's %s rule %s: %v", c.id, c.kind, c.want, err)
		}
	}
	for _, id := range []string{"owner", "size", "tier", "weird"} {
		if r := fields[id].(map[string]any)["rules"]; r != nil {
			t.Errorf("%s has rules %v", id, r)
		}
	}
	if !maps.Equal(initial, map[string]any{"cores": float64(2)}) {
		t.Errorf("initial values = %v, want only cores 2", initial)
	}

	// What they give, as the server works them out.
	vars := policy.Vars{"environment": "prd", "inputs": map[string]any{"app": "web", "host": "web", "cores": float64(4)}}
	for expr, want := range map[string]any{
		rule("fqdn", "value").(string):     "web.corp.example",
		rule("double", "default").(string): 8.0,
		rule("label", "default").(string):  "prd-4",
	} {
		if got, err := policy.Eval(expr, vars); err != nil || got != want {
			t.Errorf("%s = %v (%v), want %v", expr, got, err, want)
		}
	}

	for _, want := range []string{
		"info form.host: Its default is worked out from other fields in vRA: its default rule is inputs.app",
		"info form.fqdn: Read-only in vRA's form, its value worked out from other fields",
		"unmapped form.owner: Its default comes from action com.corp.cmdb/getOwner",
		"unmapped form.size: Its default depends on other fields in vRA (a condition)",
		`unmapped form.tier: Its default is worked out in vRA (operator "ratio")`,
		"unmapped form.weird: Its default is worked out in vRA (it reads nothere, which is no input of the template)",
	} {
		if !slices.ContainsFunc(notes, func(n string) bool { return strings.HasPrefix(n, want) }) {
			t.Errorf("no note %q in %v", want, notes)
		}
	}
	if slices.ContainsFunc(notes, func(n string) bool { return strings.Contains(n, "Read-only in vRA's form: editable") }) {
		t.Errorf("the computed read-only field is reported editable: %v", notes)
	}
}
