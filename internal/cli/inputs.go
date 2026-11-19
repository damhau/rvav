package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/pkg/rvavclient"
)

// inputFlags are a form's values given on the command line: a file of
// them, then each --input over it.
type inputFlags struct {
	file   string
	values []string
}

// read builds the inputs, each --input taking the type the form's JSON
// Schema gives its property: "4" is a number for an integer, "a,b" a list
// for an array.
func (f inputFlags) read(stdin io.Reader, schema map[string]any) (map[string]any, error) {
	inputs := map[string]any{}
	if f.file != "" {
		var b []byte
		var err error
		if f.file == "-" {
			b, err = io.ReadAll(stdin)
		} else {
			b, err = os.ReadFile(f.file)
		}
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(b, &inputs); err != nil {
			return nil, fmt.Errorf("--inputs %s: %w", f.file, err)
		}
		if inputs == nil {
			inputs = map[string]any{}
		}
	}
	for _, kv := range f.values {
		name, raw, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("--input %q: write name=value", kv)
		}
		v, err := coerce(property(schema, name), raw)
		if err != nil {
			return nil, fmt.Errorf("--input %s: %w", name, err)
		}
		inputs[name] = v
	}
	return inputs, nil
}

func property(schema map[string]any, name string) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	p, _ := props[name].(map[string]any)
	return p
}

// kind is a schema's type, the first that isn't null.
func kind(p map[string]any) string {
	switch t := p["type"].(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if s, _ := x.(string); s != "" && s != "null" {
				return s
			}
		}
	}
	return ""
}

func coerce(p map[string]any, raw string) (any, error) {
	switch kind(p) {
	case "integer":
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", raw)
		}
		return n, nil
	case "number":
		n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return n, nil
	case "boolean":
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%q is not true or false", raw)
		}
		return b, nil
	case "array":
		if strings.HasPrefix(strings.TrimSpace(raw), "[") {
			return decodeJSON(raw)
		}
		out := []any{}
		if raw == "" {
			return out, nil
		}
		items, _ := p["items"].(map[string]any)
		for _, part := range strings.Split(raw, ",") {
			v, err := coerce(items, strings.TrimSpace(part))
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case "object":
		return decodeJSON(raw)
	}
	return raw, nil
}

func decodeJSON(raw string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("%q is not JSON: %v", raw, err)
	}
	return v, nil
}

// formInput is one input a form takes, with how to give it.
type formInput struct {
	name, kind, label, def string
	required               bool
	choices                []string
	// computed: rvav finds its choices or value (a value source).
	computed bool
}

// formInputs are the inputs a form takes, in the order its sections show
// them, then any other the schema has.
func formInputs(schema map[string]any, ui rvavclient.UiSchema, initial map[string]any) []formInput {
	props, _ := schema["properties"].(map[string]any)
	var req []string
	if r, ok := schema["required"].([]any); ok {
		for _, x := range r {
			if s, ok := x.(string); ok {
				req = append(req, s)
			}
		}
	}
	var names []string
	for _, sec := range ui.Layout.Sections {
		for _, n := range sec.Fields {
			if _, ok := props[n]; ok && !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	var rest []string
	for n := range props {
		if !slices.Contains(names, n) {
			rest = append(rest, n)
		}
	}
	slices.Sort(rest)
	var out []formInput
	for _, n := range append(names, rest...) {
		p, _ := props[n].(map[string]any)
		if p["readOnly"] == true {
			continue
		}
		f := ui.Fields[n]
		in := formInput{name: n, kind: kind(p), label: f.Label, required: slices.Contains(req, n), computed: f.ValueSource != nil}
		if in.kind == "array" {
			if items, ok := p["items"].(map[string]any); ok && kind(items) != "" {
				in.kind = "list of " + kind(items)
			}
		}
		if v, ok := initial[n]; ok && v != nil {
			in.def = shown(v)
		}
		for _, e := range enumOf(p) {
			in.choices = append(in.choices, shown(e))
		}
		if len(in.choices) == 0 && f.Options != nil && f.ValueSource == nil {
			for _, o := range *f.Options {
				in.choices = append(in.choices, o.Value)
			}
		}
		out = append(out, in)
	}
	return out
}

// printInputs writes a form's inputs as a table, after a blank line, and
// says whether rvav lists some of their choices; listed is what their
// choices column shows.
func (a *app) printInputs(schema map[string]any, ui rvavclient.UiSchema, initial map[string]any, listed string) (bool, error) {
	inputs := formInputs(schema, ui, initial)
	if len(inputs) == 0 {
		return false, nil
	}
	fmt.Fprintln(a.Out)
	t := newTable(a.Out, "INPUT", "TYPE", "REQUIRED", "DEFAULT", "CHOICES", "LABEL")
	some := false
	for _, in := range inputs {
		req := ""
		if in.required {
			req = "yes"
		}
		choices := strings.Join(in.choices, ",")
		if choices == "" && in.computed {
			choices, some = listed, true
		}
		t.row(in.name, in.kind, req, in.def, choices, in.label)
	}
	return some, t.flush()
}

func enumOf(p map[string]any) []any {
	if e, ok := p["enum"].([]any); ok {
		return e
	}
	if items, ok := p["items"].(map[string]any); ok {
		if e, ok := items["enum"].([]any); ok {
			return e
		}
	}
	return nil
}

// shown is a value as a person would type it.
func shown(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = shown(e)
		}
		return strings.Join(parts, ",")
	case map[string]any:
		b, _ := json.Marshal(x)
		return string(b)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}
