package blueprint

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// Bindings are CEL expressions by name. A number or a boolean written
// without quotes (memory_gb: 4) is the constant it means, as CEL reads its
// text: YAML gives one where a string was meant (#208). A type error names
// its field from bindings down: encoding/json adds no path to the error of
// a type's own decoder.
type Bindings map[string]string

func (b *Bindings) UnmarshalJSON(data []byte) error {
	if string(bytes.TrimSpace(data)) == "null" {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return &json.UnmarshalTypeError{Value: jsonKind(bytes.TrimSpace(data)), Type: reflect.TypeFor[Bindings](), Field: "bindings"}
	}
	out := make(Bindings, len(raw))
	for k, v := range raw {
		v = bytes.TrimSpace(v)
		switch kind := jsonKind(v); kind {
		case "string":
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			out[k] = s
		case "number", "bool":
			out[k] = string(v)
		case "null":
		default:
			return &json.UnmarshalTypeError{Value: kind, Type: reflect.TypeFor[string](), Field: "bindings." + k}
		}
	}
	*b = out
	return nil
}

// jsonKind is what a JSON value is, as json.UnmarshalTypeError names it.
func jsonKind(v []byte) string {
	if len(v) == 0 {
		return ""
	}
	switch v[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	}
	return "number"
}
