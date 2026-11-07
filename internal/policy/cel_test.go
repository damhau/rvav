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
