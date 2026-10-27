package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
)

// newCompiler compiles form schemas, which catalog admins write: a $ref may
// point inside the schema only, never to a file or a URL (the default
// loader reads local files).
func newCompiler() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.UseLoader(noLoader{})
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
	b, _ := json.Marshal(inputs)
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

func pointer(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(p))
	}
	return b.String()
}
