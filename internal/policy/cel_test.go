package policy

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCostLimit: an expression that would run too long fails (S14.6).
func TestCostLimit(t *testing.T) {
	list := make([]any, 200)
	for i := range list {
		list[i] = i
	}
	vars := Vars{"inputs": map[string]any{"l": list}}
	if ok, err := Bool(`inputs.l.all(a, a >= 0)`, vars); err != nil || !ok {
		t.Fatalf("a short comprehension = %v, %v", ok, err)
	}
	_, err := Bool(`inputs.l.all(a, inputs.l.all(b, inputs.l.all(c, a + b + c >= 0)))`, vars)
	if err == nil || !strings.Contains(err.Error(), "cost") {
		t.Fatalf("8 million steps ran: %v", err)
	}
}

// A map and a null come back as JSON's: a REST call's body is sent as JSON.
func TestCompositeEvalGivesJSONValues(t *testing.T) {
	v, err := CompositeEval(`{'name': inputs.name, 'tags': ['a', inputs.name], 'none': null, 'nested': {'n': 1, 2: true}}`,
		Vars{"inputs": map[string]any{"name": "web"}, "steps": map[string]any{}, "environment": "dev", "project": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"web","nested":{"2":true,"n":1},"none":null,"tags":["a","web"]}`; string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Every environment has CEL's string extensions, version 4 (T1.1).
func TestStringFunctions(t *testing.T) {
	vars := Vars{"inputs": map[string]any{"app": " Shop_API ", "list": []any{"a", "b"}}}
	for expr, want := range map[string]string{
		`inputs.app.trim().lowerAscii()`:                     `"shop_api"`,
		`inputs.app.trim().upperAscii()`:                     `"SHOP_API"`,
		`inputs.app.trim().replace('_', '-')`:                `"Shop-API"`,
		`inputs.app.trim().substring(0, 4)`:                  `"Shop"`,
		`inputs.app.trim().split('_')`:                       `["Shop","API"]`,
		`inputs.list.join(',')`:                              `"a,b"`,
		`'%s-%d'.format([inputs.list[0], 7])`:                `"a-7"`,
		`inputs.app.indexOf('_')`:                            `5`,
		`inputs.app.trim().lowerAscii().split('_').join('')`: `"shopapi"`,
	} {
		got, err := Eval(expr, vars)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if b, _ := json.Marshal(got); string(b) != want {
			t.Errorf("%s = %s, want %s", expr, b, want)
		}
	}
	steps := Vars{"steps": map[string]any{"db": map[string]any{"fqdn": "db01.example.org"}}}
	if v, err := CompositeEval(`steps.db.fqdn.split('.')[0].upperAscii()`, steps); err != nil || v != "DB01" {
		t.Errorf("composite: %v, %v", v, err)
	}
	if ok, err := Day2Bool(`resource.name.lowerAscii().startsWith('web')`, Vars{"resource": map[string]any{"name": "WEB01"}}); err != nil || !ok {
		t.Errorf("day-2: %v, %v", ok, err)
	}
	if ok, err := EventBool(`request.number.substring(0, 3) == 'REQ'`, Vars{"request": map[string]any{"number": "REQ-12"}}); err != nil || !ok {
		t.Errorf("events: %v, %v", ok, err)
	}
	if err := Check(`inputs.app.someFutureFunction()`); err == nil {
		t.Error("an unknown function compiled")
	}
}

// A binding evaluated as the VM is built reads the VM and the components
// built before it, nothing else.
func TestBuildBindings(t *testing.T) {
	vars := Vars{"inputs": map[string]any{"app": "Shop"},
		"self":       map[string]any{"index": 1, "name": "web02"},
		"components": map[string]any{"db": map[string]any{"addresses": []any{"10.0.0.5"}}}}
	got, err := EvalBuild(`['app/' + inputs.app.lowerAscii(), 'node/' + string(self.index), 'db/' + components.db.addresses[0]]`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(got); string(b) != `["app/shop","node/1","db/10.0.0.5"]` {
		t.Fatalf("got %s", b)
	}
	if err := CheckBuild(`sizes.small.vcpu > 1`); err == nil {
		t.Error("sizes compiled at build")
	}
	if err := Check(`self.index == 0`); err == nil {
		t.Error("self compiled in a form rule")
	}
}

func TestReadsOf(t *testing.T) {
	for expr, want := range map[string]Reads{
		`components.db.addresses[0] + inputs.x`:         {Idents: []string{"components", "inputs"}, Components: []string{"db"}, Inputs: []string{"x"}},
		`inputs[inputs.which]`:                          {Idents: []string{"inputs"}, Inputs: []string{"which"}, AllInputs: true},
		`'n' + string(self.index)`:                      {Idents: []string{"self"}},
		`components.db.count + components.cache.count`:  {Idents: []string{"components"}, Components: []string{"db", "cache"}},
		`components['db'].count`:                        {Idents: []string{"components"}, Loose: true},
		`has(components.db) ? components.db.names : []`: {Idents: []string{"components"}, Components: []string{"db"}},
		`inputs.l.map(x, x + components.web.names[0])`:  {Idents: []string{"inputs", "x", "components"}, Components: []string{"web"}, Inputs: []string{"l"}},
	} {
		got, err := ReadsOf(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		if string(g) != string(w) {
			t.Errorf("%s: %s, want %s", expr, g, w)
		}
	}
	if _, err := ReadsOf(`components.`); err == nil {
		t.Error("a broken expression parsed")
	}
}
