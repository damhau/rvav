package core

import (
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/policy"
)

func TestRuleDefaults(t *testing.T) {
	ui := api.UiSchema{Fields: map[string]api.UiField{
		"app":    {Widget: api.UiWidgetText},
		"host":   {Widget: api.UiWidgetText, Rules: &api.UiRules{Default: new(`inputs.app + "-" + environment`)}},
		"fqdn":   {Widget: api.UiWidgetText, Rules: &api.UiRules{Default: new(`inputs.host + ".corp.example"`)}},
		"self":   {Widget: api.UiWidgetText, Rules: &api.UiRules{Default: new(`has(inputs.self) ? "seen" : "unseen"`)}},
		"none":   {Widget: api.UiWidgetText, Rules: &api.UiRules{Default: new(`null`)}},
		"locked": {Widget: api.UiWidgetText, Rules: &api.UiRules{Value: new(`"v"`), Default: new(`"d"`)}},
	}}
	vars := policy.Vars{"environment": "dev"}
	for _, c := range []struct {
		what   string
		inputs map[string]any
		want   map[string]any
		failed []string
	}{
		{"defaults chain, in any order", map[string]any{"app": "web"},
			map[string]any{"host": "web-dev", "fqdn": "web-dev.corp.example", "self": "unseen"}, nil},
		{"an edited field keeps its input for the others", map[string]any{"app": "web", "host": "mine"},
			map[string]any{"host": "web-dev", "fqdn": "mine.corp.example", "self": "unseen"}, nil},
		{"a rule never sees its own input", map[string]any{"app": "web", "self": "typed"},
			map[string]any{"host": "web-dev", "fqdn": "web-dev.corp.example", "self": "unseen"}, nil},
		{"a rule that fails gives no default", map[string]any{},
			map[string]any{"self": "unseen"}, []string{"fqdn", "host"}},
	} {
		has := func(n string) bool { _, ok := c.inputs[n]; return ok }
		got, failed := ruleDefaults(ui, vars, c.inputs, has)
		if !mapsEqual(got, c.want) {
			t.Errorf("%s: defaults %v, want %v", c.what, got, c.want)
		}
		if names := sortedKeys(failed); !slices.Equal(names, c.failed) {
			t.Errorf("%s: failed %v, want %v", c.what, failed, c.failed)
		}
	}
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestApplyDefaults(t *testing.T) {
	rule := `"x"`
	ui := api.UiSchema{Fields: map[string]api.UiField{
		"absent": {Rules: &api.UiRules{Default: &rule}}, "emptied": {Rules: &api.UiRules{Default: &rule}},
		"edited": {Rules: &api.UiRules{Default: &rule}}, "locked": {Rules: &api.UiRules{Default: &rule}},
		"required": {Rules: &api.UiRules{Default: &rule}}, "plain": {},
	}}
	states := api.FieldStates{
		"absent":   {Visible: true, Default: "web-dev"},
		"emptied":  {Visible: true, Default: "web-dev"},
		"edited":   {Visible: true, Default: "web-dev"},
		"locked":   {Visible: true, ReadOnly: true, Default: "web-dev"},
		"required": {Visible: true, Required: true, Default: nil},
		"plain":    {Visible: true},
	}
	inputs := map[string]any{"emptied": nil, "edited": "mine", "locked": "sneaked", "plain": nil}
	errs := applyStates(states, ui, map[string]any{}, inputs)
	want := map[string]any{"absent": "web-dev", "edited": "mine", "locked": "web-dev", "plain": nil}
	if !mapsEqual(inputs, want) {
		t.Fatalf("inputs = %v, want %v", inputs, want)
	}
	if len(errs) != 1 || errs[0].Field != "/inputs/required" {
		t.Fatalf("errors = %v", errs)
	}
}

func TestComputedSourceFails(t *testing.T) {
	msg := "value source: down"
	ui := api.UiSchema{Fields: map[string]api.UiField{
		"tag": {Widget: api.UiWidgetComputed, ValueSource: &api.ValueSource{Kind: api.Cel, Params: &map[string]any{"value": `"a"`}}},
	}}
	inputs := map[string]any{"tag": "mine"}
	errs := applyStates(api.FieldStates{"tag": {Visible: true, Error: &msg}}, ui, nil, inputs)
	if len(errs) != 1 || deref(errs[0].Code) != "source_failed" || !strings.Contains(errs[0].Message, "down") {
		t.Fatalf("errors = %v", errs)
	}
}

func TestTyped(t *testing.T) {
	str, num, boolean := map[string]any{"type": "string"}, map[string]any{"type": "integer"}, map[string]any{"type": "boolean"}
	for _, c := range []struct {
		v    any
		prop map[string]any
		want any
	}{
		{float64(1234567), str, "1234567"},
		{int64(8), str, "8"},
		{true, str, "true"},
		{"16", num, float64(16)},
		{"sixteen", num, "sixteen"},
		{"true", boolean, true},
		{"web", nil, "web"},
	} {
		if got := typed(c.v, c.prop); got != c.want {
			t.Errorf("typed(%v, %v) = %v (%T), want %v", c.v, c.prop, got, got, c.want)
		}
	}
}

func TestCheckDefaults(t *testing.T) {
	cel := func(params map[string]any) *api.ValueSource { return &api.ValueSource{Kind: api.Cel, Params: &params} }
	rule := &api.UiRules{Default: new(`inputs.app + "-01"`)}
	for _, c := range []struct {
		what    string
		f       api.UiField
		initial bool
		want    []string
	}{
		{"a default rule", api.UiField{Widget: api.UiWidgetText, Rules: rule}, false, nil},
		{"a default rule and an initial value", api.UiField{Widget: api.UiWidgetText, Rules: rule}, true,
			[]string{"/ui_schema/fields/f/rules/default conflict"}},
		{"a default rule and a value rule", api.UiField{Widget: api.UiWidgetText, Rules: &api.UiRules{Default: rule.Default, Value: new(`"a"`)}}, false,
			[]string{"/ui_schema/fields/f/rules/default conflict"}},
		{"a default rule and a source's default", api.UiField{Widget: api.UiWidgetText, Rules: rule, ValueSource: cel(map[string]any{"value": `"a"`})}, false,
			[]string{"/ui_schema/fields/f/rules/default conflict"}},
		{"a default on a computed field", api.UiField{Widget: api.UiWidgetComputed, Rules: rule}, false,
			[]string{"/ui_schema/fields/f/rules/default widget_mismatch"}},
		{"a default on a file", api.UiField{Widget: api.UiWidgetFile, Rules: rule}, false,
			[]string{"/ui_schema/fields/f/rules/default widget_mismatch"}},
		{"a source's default and an initial value", api.UiField{Widget: api.UiWidgetNumber, ValueSource: cel(map[string]any{"value": `4`})}, true,
			[]string{"/ui_schema/fields/f/value_source conflict"}},
		{"a CEL source with only options on a text field", api.UiField{Widget: api.UiWidgetText, ValueSource: cel(map[string]any{"options": `["a"]`})}, false,
			[]string{"/ui_schema/fields/f/value_source/params/value required"}},
		{"a CEL source with options on a select", api.UiField{Widget: api.UiWidgetSelect, ValueSource: cel(map[string]any{"options": `["a"]`})}, true, nil},
		{"a computed field's source and an initial value", api.UiField{Widget: api.UiWidgetComputed, ValueSource: cel(map[string]any{"value": `"a"`})}, true, nil},
	} {
		var got []string
		for _, e := range checkDefaults("f", c.f, c.initial, "") {
			got = append(got, e.Field+" "+deref(e.Code))
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.what, got, c.want)
		}
	}
}
