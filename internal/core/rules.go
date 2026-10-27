package core

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/policy"
)

// fieldStates evaluates every UI field's rules (ADR-0019): visible,
// required, read-only and a computed value, over the form's CEL variables.
// Without a rule: visible, required when the schema says so, read-only when
// the schema says so or a value rule computes it. A rule that fails to
// evaluate keeps the default and says why in Error.
func fieldStates(formSchema, uiSchema json.RawMessage, vars policy.Vars) api.FieldStates {
	var schema struct {
		Required   []string                  `json:"required"`
		Properties map[string]map[string]any `json:"properties"`
	}
	var ui api.UiSchema
	_ = json.Unmarshal(formSchema, &schema)
	_ = json.Unmarshal(uiSchema, &ui)
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
// submit alike: a hidden field's input is dropped; a read-only field gets
// its computed value, else its initial value, never the requester's; a
// computed value that fails is an error, as is an empty field a rule makes
// required. It returns those errors.
func applyStates(states api.FieldStates, ui api.UiSchema, initial map[string]any, inputs map[string]any) []api.FieldError {
	var errs []api.FieldError
	for _, name := range sortedKeys(states) {
		st := states[name]
		rules := ui.Fields[name].Rules
		switch {
		case !st.Visible:
			delete(inputs, name)
			continue
		case st.Value != nil:
			inputs[name] = st.Value
		case rules != nil && rules.Value != nil && *rules.Value != "":
			msg := "This value couldn't be computed"
			if st.Error != nil {
				msg += ": " + *st.Error
			}
			errs = append(errs, fieldErr("/inputs/"+name, "rule_failed", msg+"."))
		case st.ReadOnly:
			if v, ok := initial[name]; ok {
				inputs[name] = v
			} else {
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
