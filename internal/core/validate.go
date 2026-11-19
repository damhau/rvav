package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/damhau/rvav/internal/api"
)

var (
	schemaMu    sync.Mutex
	schemaCache = map[string]*jsonschema.Schema{}
	printer     = message.NewPrinter(language.English)
	boundsVocab = formatBoundsVocab()
)

// newCompiler compiles form schemas, which catalog admins write: a $ref may
// point inside the schema only, never to a file or a URL (the default
// loader reads local files). Formats are checked, and dates bounded.
func newCompiler() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.UseLoader(noLoader{})
	c.AssertFormat()
	c.AssertVocabs()
	c.RegisterVocabulary(boundsVocab)
	return c
}

type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("a form schema can't load %s: refer to its own definitions ($defs) only", url)
}

func compileSchema(versionID string, raw json.RawMessage) (*jsonschema.Schema, error) {
	schemaMu.Lock()
	defer schemaMu.Unlock()
	if s, ok := schemaCache[versionID]; ok {
		return s, nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("form schema of %s: %w", versionID, err)
	}
	c := newCompiler()
	url := "urn:rvav:form:" + versionID
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	s, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compile form schema of %s: %w", versionID, err)
	}
	schemaCache[versionID] = s
	return s, nil
}

// compileSchemaUncached compiles a form schema being edited (not cached:
// drafts change).
func compileSchemaUncached(raw json.RawMessage) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c := newCompiler()
	if err := c.AddResource("urn:rvav:form:draft", doc); err != nil {
		return nil, err
	}
	return c.Compile("urn:rvav:form:draft")
}

// validateInputs checks inputs against the form's JSON Schema and returns
// one error per problem, with fields as JSON pointers under /inputs.
func validateInputs(versionID string, schema json.RawMessage, inputs map[string]any) ([]api.FieldError, error) {
	s, err := compileSchema(versionID, schema)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(inputs)
	if err != nil {
		return nil, fmt.Errorf("inputs: %w", err)
	}
	return validateWith(s, b)
}

// validateWith checks a JSON document against a compiled schema.
func validateWith(s *jsonschema.Schema, b []byte) ([]api.FieldError, error) {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	err = s.Validate(inst)
	var ve *jsonschema.ValidationError
	if err == nil {
		return nil, nil
	}
	if !errors.As(err, &ve) {
		return nil, err
	}
	var out []api.FieldError
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		// "contains" explains itself through each item's mismatch; report it
		// once on the list instead.
		if _, ok := e.ErrorKind.(*kind.Contains); ok {
			out = append(out, fieldErr("/inputs"+pointer(e.InstanceLocation), "contains", "A required option is missing from this list."))
			return
		}
		if k, ok := e.ErrorKind.(*kind.PropertyNames); ok {
			msg := "Give every pair a key."
			if k.Property != "" {
				msg = fmt.Sprintf("%q can't be a key here.", k.Property)
			}
			out = append(out, fieldErr(keysAt(e.SchemaURL), "key", msg))
			return
		}
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		ptr := "/inputs" + pointer(e.InstanceLocation)
		switch k := e.ErrorKind.(type) {
		case *kind.Required:
			for _, m := range k.Missing {
				out = append(out, fieldErr(ptr+"/"+m, "required", "This field is required."))
			}
		case *kind.AdditionalProperties:
			for _, p := range k.Properties {
				out = append(out, fieldErr(ptr+"/"+p, "unknown_field", "This form has no field named "+p+"."))
			}
		case *kind.Enum:
			out = append(out, fieldErr(ptr, "enum", "Choose one of the listed values."))
		case *kind.Pattern:
			out = append(out, fieldErr(ptr, "pattern", "This value is not in the expected format."))
		case *kind.MinLength:
			out = append(out, fieldErr(ptr, "required", "This field is required."))
		case *kind.Contains:
			out = append(out, fieldErr(ptr, "contains", "A required option is missing from this list."))
		case *kind.Type:
			out = append(out, fieldErr(ptr, "type", typeMessage(k.Want)))
		case *kind.Format:
			out = append(out, fieldErr(ptr, "format", formatMessage(k.Want)))
		case *kind.Minimum:
			out = append(out, fieldErr(ptr, "minimum", "Enter "+ratText(k.Want)+" or more."))
		case *kind.Maximum:
			out = append(out, fieldErr(ptr, "maximum", "Enter "+ratText(k.Want)+" or less."))
		case *kind.MultipleOf:
			out = append(out, fieldErr(ptr, "multiple_of", "Enter a multiple of "+ratText(k.Want)+"."))
		case *kind.MinItems:
			out = append(out, fieldErr(ptr, "min_items", fmt.Sprintf("Add at least %d.", k.Want)))
		case *kind.MaxItems:
			out = append(out, fieldErr(ptr, "max_items", fmt.Sprintf("Keep it to %d at most.", k.Want)))
		case *kind.UniqueItems:
			out = append(out, fieldErr(ptr, "unique", "The same value is in the list twice."))
		case *tooEarly, *tooLate:
			out = append(out, fieldErr(ptr, "out_of_range", e.ErrorKind.LocalizedString(printer)))
		default:
			msg := e.ErrorKind.LocalizedString(printer)
			if msg != "" {
				msg = strings.ToUpper(msg[:1]) + msg[1:] + "."
			}
			out = append(out, fieldErr(ptr, "invalid", msg))
		}
	}
	walk(ve)
	return out, nil
}

func typeMessage(want []string) string {
	if len(want) == 1 {
		switch want[0] {
		case "integer":
			return "Enter a whole number."
		case "number":
			return "Enter a number."
		case "string":
			return "Enter text."
		case "boolean":
			return "Choose yes or no."
		case "array":
			return "Enter a list."
		case "object":
			return "Enter keys and values."
		}
	}
	return "This value has the wrong type: want " + strings.Join(want, " or ") + "."
}

func formatMessage(format string) string {
	switch format {
	case "date-time":
		return "Enter a date and time with its time zone, for example 2026-11-20T14:30:00+01:00."
	case "date":
		return "Enter a date as YYYY-MM-DD, for example 2026-11-20."
	default:
		return "This isn't a valid " + format + "."
	}
}

// ratText writes a schema's bound as it was written: 0.5, not 1/2.
func ratText(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	f, _ := r.Float64()
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// keysAt is where a propertyNames problem is, from its schema: v6.0.3 gives
// the error its location stack itself, which the properties after it then
// overwrite. A pair field is a top-level input, so its name is enough.
func keysAt(schemaURL string) string {
	_, frag, _ := strings.Cut(schemaURL, "#")
	if rest, ok := strings.CutPrefix(frag, "/properties/"); ok {
		name, _, _ := strings.Cut(rest, "/")
		return "/inputs/" + name
	}
	return "/inputs"
}

func pointer(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(p))
	}
	return b.String()
}
