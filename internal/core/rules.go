package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// valueWidgets take one typed value rather than a choice: a `rest` or
// `cel` source gives them their default.
var valueWidgets = []api.UiWidget{api.UiWidgetText, api.UiWidgetTextarea, api.UiWidgetNumber, api.UiWidgetSlider,
	api.UiWidgetDate, api.UiWidgetDatetime, api.UiWidgetToggle}

// singleSource says whether a field's value source gives it one value: a
// computed field's value, another field's default. A secret takes none.
func singleSource(f api.UiField) bool {
	src := f.ValueSource
	if src == nil || src.Kind != api.Rest && src.Kind != api.Cel || f.Secret != nil && *f.Secret {
		return false
	}
	return f.Widget == api.UiWidgetComputed || slices.Contains(valueWidgets, f.Widget)
}

func valueRule(f api.UiField) string {
	if f.Rules == nil {
		return ""
	}
	return deref(f.Rules.Value)
}

// computedBy says whether a field's value is worked out rather than given:
// by a value rule, or by a computed field's value source.
func computedBy(f api.UiField) bool {
	return valueRule(f) != "" || f.Widget == api.UiWidgetComputed && singleSource(f)
}

func defaultRule(f api.UiField) string {
	if f.Rules == nil || computedBy(f) {
		return ""
	}
	return deref(f.Rules.Default)
}

// defaulted says whether a field gets a default while it has no value:
// from a default rule, or from its value source.
func defaulted(f api.UiField) bool {
	return !computedBy(f) && (defaultRule(f) != "" || singleSource(f))
}

// formStates works out a form's defaults and rule results for the inputs
// given (ADR-0019), as p requesting in proj and env. The default rules
// come first; then the value sources that give a field its default or its
// value, which read those defaults; then the default rules again, which so
// read the sources'; then the other rules, which see every default. A
// field with an input keeps it: `null` is the input of a field the
// requester emptied.
func (s *Service) formStates(ctx context.Context, p *auth.Principal, proj store.Project, env string, v store.ItemVersion,
	inputs map[string]any) api.FieldStates {
	var ui api.UiSchema
	_ = json.Unmarshal(v.UISchema, &ui)
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	_ = json.Unmarshal(v.FormSchema, &schema)
	given := map[string]any{}
	for k, val := range inputs {
		if f, ok := ui.Fields[k]; val != nil || !ok || !defaulted(f) {
			given[k] = val
		}
	}
	has := func(name string) bool { _, ok := inputs[name]; return ok }
	vars := formVars(given, proj, env, p, v)

	ruled, _ := ruleDefaults(ui, vars, given, has)
	sourced, values, failed := s.sourceDefaults(ctx, p, proj, env, v, ui, schema.Properties, filled(given, ruled, has), has)
	base := filled(given, sourced, has)
	maps.Copy(base, values)
	ruled, ruleFailed := ruleDefaults(ui, vars, base, has)
	maps.Copy(failed, ruleFailed)

	seen := maps.Clone(vars)
	seen["inputs"] = filled(base, ruled, has)
	states := fieldStates(v.FormSchema, ui, seen)
	for name, st := range states {
		if d, ok := ruled[name]; ok {
			st.Default = d
		} else if d, ok := sourced[name]; ok {
			st.Default = d
		}
		if val, ok := values[name]; ok {
			st.Value = val
		}
		if msg, ok := failed[name]; ok && st.Error == nil {
			st.Error = &msg
		}
		states[name] = st
	}
	return states
}

// ruleDefaults works out the default rules over base, the inputs given and
// the defaults found so far. A rule never sees its own field's input, and
// sees the others' defaults when their fields have no input (has), until
// the defaults stop changing. It returns the defaults, without those that
// are null, and the rules that failed.
func ruleDefaults(ui api.UiSchema, vars policy.Vars, base map[string]any, has func(string) bool) (map[string]any, map[string]string) {
	var names []string
	for _, n := range sortedKeys(ui.Fields) {
		if defaultRule(ui.Fields[n]) != "" {
			names = append(names, n)
		}
	}
	out, failed := map[string]any{}, map[string]string{}
	work := maps.Clone(base)
	set := func(name string, v any, ok bool) {
		if has(name) {
			return
		}
		if ok && v != nil {
			work[name] = v
		} else {
			delete(work, name)
		}
	}
	for range len(names) + 1 {
		changed := false
		for _, name := range names {
			in := maps.Clone(work)
			delete(in, name)
			own := maps.Clone(vars)
			own["inputs"] = in
			v, err := policy.Eval(defaultRule(ui.Fields[name]), own)
			prev, had := out[name]
			if err != nil {
				failed[name] = "default: " + err.Error()
				if had {
					delete(out, name)
					set(name, nil, false)
					changed = true
				}
				continue
			}
			delete(failed, name)
			if had && reflect.DeepEqual(prev, v) {
				continue
			}
			out[name], changed = v, true
			set(name, v, true)
		}
		if !changed {
			break
		}
	}
	maps.DeleteFunc(out, func(_ string, v any) bool { return v == nil })
	return out, failed
}

// sourceDefaults resolves the value sources that give a field one value,
// over inputs: a field's default (whether it has an input or not, as a
// read-only field takes it), a computed field's value. A source reads the
// values of those its depends_on names, which are resolved before it.
func (s *Service) sourceDefaults(ctx context.Context, p *auth.Principal, proj store.Project, env string, v store.ItemVersion,
	ui api.UiSchema, props map[string]map[string]any, inputs map[string]any, has func(string) bool,
) (defaults, values map[string]any, failed map[string]string) {
	defaults, values, failed = map[string]any{}, map[string]any{}, map[string]string{}
	var pending []string
	for _, n := range sortedKeys(ui.Fields) {
		if f := ui.Fields[n]; singleSource(f) && valueRule(f) == "" {
			pending = append(pending, n)
		}
	}
	work := maps.Clone(inputs)
	for len(pending) > 0 {
		ready := slices.DeleteFunc(slices.Clone(pending), func(n string) bool {
			deps := ui.Fields[n].DependsOn
			return deps != nil && slices.ContainsFunc(*deps, func(d string) bool { return d != n && slices.Contains(pending, d) })
		})
		if len(ready) == 0 {
			ready = pending
		}
		for _, name := range ready {
			f := ui.Fields[name]
			out, err := s.sourceValues(ctx, p, proj, env, v, name, f, work, "")
			switch {
			case err != nil:
				failed[name] = "value source: " + err.Error()
			case out.Value == nil:
			case f.Widget == api.UiWidgetComputed:
				values[name] = typed(out.Value, props[name])
				work[name] = values[name]
			default:
				defaults[name] = typed(out.Value, props[name])
				if !has(name) {
					work[name] = defaults[name]
				}
			}
		}
		pending = slices.DeleteFunc(pending, func(n string) bool { return slices.Contains(ready, n) })
	}
	return defaults, values, failed
}

// filled is base with the defaults of the fields that have no input.
func filled(base, defaults map[string]any, has func(string) bool) map[string]any {
	out := make(map[string]any, len(base)+len(defaults))
	maps.Copy(out, base)
	for k, v := range defaults {
		if !has(k) {
			out[k] = v
		}
	}
	return out
}

// typed is a source's value as its input's type takes it: a text input
// takes a number as its digits, a number input a number's text.
func typed(v any, prop map[string]any) any {
	switch prop["type"] {
	case "string":
		switch x := v.(type) {
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case int64, int, uint64, bool, json.Number:
			return fmt.Sprint(x)
		}
	case "number", "integer":
		if t, ok := v.(string); ok {
			if n, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
				return n
			}
		}
	case "boolean":
		if t, ok := v.(string); ok {
			if b, err := strconv.ParseBool(strings.TrimSpace(t)); err == nil {
				return b
			}
		}
	}
	return v
}

// fieldStates evaluates every UI field's rules (ADR-0019): visible,
// required, read-only and a computed value, over the form's CEL variables.
// Without a rule: visible, required when the schema says so, read-only when
// the schema says so or a value rule computes it. A rule that fails to
// evaluate keeps the default and says why in Error. Defaults are
// formStates'.
func fieldStates(formSchema json.RawMessage, ui api.UiSchema, vars policy.Vars) api.FieldStates {
	var schema struct {
		Required   []string                  `json:"required"`
		Properties map[string]map[string]any `json:"properties"`
	}
	_ = json.Unmarshal(formSchema, &schema)
	out := api.FieldStates{}
	for name, f := range ui.Fields {
		readOnly, _ := schema.Properties[name]["readOnly"].(bool)
		st := api.FieldState{Visible: true, ReadOnly: readOnly}
		for _, r := range schema.Required {
			if r == name {
				st.Required = true
			}
		}
		if f.Rules != nil {
			fail := func(rule string, err error) {
				if st.Error == nil {
					msg := rule + ": " + err.Error()
					st.Error = &msg
				}
			}
			for _, b := range []struct {
				rule string
				expr *string
				to   *bool
			}{{"visible", f.Rules.Visible, &st.Visible}, {"required", f.Rules.Required, &st.Required}, {"read_only", f.Rules.ReadOnly, &st.ReadOnly}} {
				if b.expr == nil || *b.expr == "" {
					continue
				}
				if v, err := policy.Bool(*b.expr, vars); err != nil {
					fail(b.rule, err)
				} else {
					*b.to = v
				}
			}
			if f.Rules.Value != nil && *f.Rules.Value != "" {
				st.ReadOnly = true
				if v, err := policy.Eval(*f.Rules.Value, vars); err != nil {
					fail("value", err)
				} else {
					st.Value = v
				}
			}
		}
		out[name] = st
	}
	return out
}

// applyStates makes the inputs what the rules say, in the preview and on
// submit alike: a hidden field's input is dropped; a computed field gets
// its computed value, never the requester's; a read-only field gets its
// default, else its initial value; a field without an input gets its
// default, and one the requester emptied (null) none. A computed value
// that fails is an error, as is an empty field a rule makes required. It
// returns those errors.
func applyStates(states api.FieldStates, ui api.UiSchema, initial map[string]any, inputs map[string]any) []api.FieldError {
	var errs []api.FieldError
	for _, name := range sortedKeys(states) {
		st := states[name]
		f := ui.Fields[name]
		switch {
		case !st.Visible:
			delete(inputs, name)
			continue
		case st.Value != nil:
			inputs[name] = st.Value
		case computedBy(f):
			code, msg := "rule_failed", "This value couldn't be computed"
			if valueRule(f) == "" {
				code = "source_failed"
			}
			if st.Error != nil {
				msg += ": " + *st.Error
			}
			errs = append(errs, fieldErr("/inputs/"+name, code, msg+"."))
		case st.ReadOnly && st.Default != nil:
			inputs[name] = st.Default
		case st.ReadOnly:
			if v, ok := initial[name]; ok {
				inputs[name] = v
			} else {
				delete(inputs, name)
			}
		case defaulted(f):
			if v, ok := inputs[name]; !ok && st.Default != nil {
				inputs[name] = st.Default
			} else if ok && v == nil {
				delete(inputs, name)
			}
		}
		if st.Required && empty(inputs[name]) {
			errs = append(errs, fieldErr("/inputs/"+name, "required", "This field is required."))
		}
	}
	return errs
}

// hiddenErrors drops schema errors about fields a rule hides: their inputs
// are not sent.
func hiddenErrors(states api.FieldStates, errs []api.FieldError) []api.FieldError {
	return slices.DeleteFunc(errs, func(e api.FieldError) bool {
		name, ok := strings.CutPrefix(e.Field, "/inputs/")
		if !ok {
			return false
		}
		name, _, _ = strings.Cut(name, "/")
		st, known := states[name]
		return known && !st.Visible
	})
}

func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	}
	return false
}
