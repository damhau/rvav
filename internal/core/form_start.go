package core

import (
	"encoding/json"
	"maps"

	"github.com/damhau/rvav/internal/api"
)

// StartValues are the values a form starts with (ADR-0055): its initial
// values, and for an input without one the default its JSON Schema gives.
func StartValues(schema, initial map[string]any) map[string]any {
	out := map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	for name, p := range props {
		if prop, ok := p.(map[string]any); ok {
			if d, ok := prop["default"]; ok {
				out[name] = d
			}
		}
	}
	maps.Copy(out, initial)
	return out
}

// startOf is StartValues of a version stored as JSON.
func startOf(schema, initial json.RawMessage) map[string]any {
	var s, i map[string]any
	_ = json.Unmarshal(schema, &s)
	_ = json.Unmarshal(initial, &i)
	return StartValues(s, i)
}

// withStart gives each input left out its start value, as a client that
// shows the form sends it. An input given stays: a null is a field the
// requester emptied, which so gets no value. That null is dropped here
// unless the field has a default too, a rule's or a source's, which
// applyStates then keeps from it.
func withStart(ui api.UiSchema, start, inputs map[string]any) {
	for name, v := range start {
		given, ok := inputs[name]
		switch {
		case !ok:
			inputs[name] = jsonCopy(v)
		case given == nil && !defaulted(ui.Fields[name]):
			delete(inputs, name)
		}
	}
}

// startForm is an action's form as it is served: its start values are its
// initial values.
func startForm(f *api.ActionForm) *api.ActionForm {
	if f == nil {
		return nil
	}
	c := *f
	c.InitialValues = StartValues(f.Schema, f.InitialValues)
	return &c
}

// jsonCopy is v as decoded from a request body: numbers are float64, and
// no list or object is shared with the form it came from.
func jsonCopy(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	return out
}
