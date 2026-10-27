package day2

import (
	"fmt"

	"github.com/damhau/rvav/internal/api"
)

// field is one input of an action form.
type field struct {
	name   string
	schema map[string]any
	ui     api.UiField
	value  any
}

// form builds a one-section form; every field is required.
func form(title string, fields ...field) *api.ActionForm {
	props := map[string]any{}
	names := make([]string, len(fields))
	ui := api.UiSchema{Fields: map[string]api.UiField{}}
	initial := map[string]any{}
	for i, f := range fields {
		props[f.name] = f.schema
		names[i] = f.name
		ui.Fields[f.name] = f.ui
		if f.value != nil {
			initial[f.name] = f.value
		}
	}
	ui.Layout.Kind = api.Page
	ui.Layout.Sections = []api.UiSection{{Key: "action", Title: title, Fields: names}}
	return &api.ActionForm{
		Schema: map[string]any{"type": "object", "required": names, "properties": props,
			"additionalProperties": false},
		UiSchema:      ui,
		InitialValues: initial,
	}
}

func number(name, label, help string, min, max, value int) field {
	f := field{name: name, schema: map[string]any{"type": "integer", "minimum": min, "maximum": max},
		ui: api.UiField{Label: label, Widget: api.UiWidgetNumber}, value: value}
	if help != "" {
		f.ui.Help = &help
	}
	return f
}

func choice(name, label string, options []api.Option, value string) field {
	values := make([]string, len(options))
	for i, o := range options {
		values[i] = o.Value
	}
	f := field{name: name, schema: map[string]any{"type": "string", "enum": values},
		ui: api.UiField{Label: label, Widget: api.UiWidgetSelect, Options: &options}}
	if value != "" {
		f.value = value
	}
	return f
}

func toggle(name, label, help string) field {
	return field{name: name, schema: map[string]any{"type": "boolean"},
		ui: api.UiField{Label: label, Help: &help, Widget: api.UiWidgetToggle}, value: false}
}

func text(name, label string, maxLength int) field {
	return field{name: name, schema: map[string]any{"type": "string", "minLength": 1, "maxLength": maxLength},
		ui: api.UiField{Label: label, Widget: api.UiWidgetText}}
}

func intInput(inputs map[string]any, name string) int {
	switch v := inputs[name].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func strInput(inputs map[string]any, name string) string {
	s, _ := inputs[name].(string)
	return s
}

func boolInput(inputs map[string]any, name string) bool {
	b, _ := inputs[name].(bool)
	return b
}

func invalid(name, code, format string, args ...any) []api.FieldError {
	c := code
	return []api.FieldError{{Field: "/inputs/" + name, Code: &c, Message: fmt.Sprintf(format, args...)}}
}
