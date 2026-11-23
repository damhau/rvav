package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/policy"
)

// parseProblem says why a blueprint doesn't parse, at the value that
// doesn't fit, in words: encoding/json's name Go's types (#208).
func parseProblem(bp map[string]any, ptr string, err error) api.FieldError {
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) || te.Field == "" {
		return fieldErr(ptr, "invalid", "The blueprint can't be read: "+err.Error())
	}
	given, _, _ := strings.Cut(te.Value, " ")
	path := strings.Split(te.Field, ".")
	at, ok := findValue(bp, path, given)
	if !ok {
		at = "/" + strings.Join(path, "/")
	}
	want := typeWords(te.Type)
	switch {
	case path[len(path)-1] == "bindings":
		want = `bindings by name, as {memory_gb: "4"}`
	case len(path) > 1 && path[len(path)-2] == "bindings":
		want = `CEL in quotes ("inputs.network") or a number`
	}
	return fieldErr(ptr+at, "invalid", fmt.Sprintf("This is %s, where the blueprint takes %s.", kindWords[given], want))
}

// findValue is the JSON pointer of the first value at path, from v or from
// a value under it, whose JSON kind is given: the error of a type's own
// decoder names its path from that type down.
func findValue(v any, path []string, given string) (string, bool) {
	if at, ok := valueAt(v, path, given); ok {
		return at, true
	}
	switch v := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(v) {
			if at, ok := findValue(v[k], path, given); ok {
				return "/" + k + at, true
			}
		}
	case []any:
		for i, e := range v {
			if at, ok := findValue(e, path, given); ok {
				return "/" + strconv.Itoa(i) + at, true
			}
		}
	}
	return "", false
}

// valueAt is the JSON pointer of the value at path whose JSON kind is
// given. A list's element is the one the path's index names or, when the
// path names none, the first that fits.
func valueAt(v any, path []string, given string) (string, bool) {
	if len(path) == 0 {
		return "", jsonKindOf(v) == given
	}
	switch v := v.(type) {
	case []any:
		if i, err := strconv.Atoi(path[0]); err == nil {
			if i < 0 || i >= len(v) {
				return "", false
			}
			at, ok := valueAt(v[i], path[1:], given)
			return "/" + path[0] + at, ok
		}
		for i, e := range v {
			if at, ok := valueAt(e, path, given); ok {
				return "/" + strconv.Itoa(i) + at, true
			}
		}
	case map[string]any:
		if next, ok := v[path[0]]; ok {
			at, ok := valueAt(next, path[1:], given)
			return "/" + path[0] + at, ok
		}
	}
	return "", false
}

func jsonKindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case float64, json.Number, int, int64:
		return "number"
	case []any:
		return "array"
	}
	return "object"
}

var kindWords = map[string]string{"string": "text", "number": "a number", "bool": "true or false",
	"array": "a list", "object": "keys and values", "null": "null"}

func typeWords(t reflect.Type) string {
	if t == nil {
		return "something else"
	}
	switch t.Kind() {
	case reflect.String:
		return "text"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
		reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "a list"
	}
	return "keys and values"
}

// checkInputReads refuses CEL in a VM blueprint that reads an input the
// form doesn't have: requests would fail on it (#208). inputs are the
// form's, its property groups' among them.
func checkInputReads(b blueprint.Blueprint, inputs map[string]bool, ptr string) []api.FieldError {
	var errs []api.FieldError
	read := func(at, expr string) {
		rd, err := policy.ReadsOf(expr)
		if err != nil {
			return
		}
		for _, name := range rd.Inputs {
			if !inputs[name] {
				errs = append(errs, fieldErr(at, "unknown_input",
					fmt.Sprintf("This reads inputs.%s, and %q is not an input of the form's schema.", name, name)))
				return
			}
		}
	}
	parts := func(at string, bindings map[string]string, disks []blueprint.Disk, nics []blueprint.NIC) {
		for _, k := range sortedKeys(bindings) {
			read(at+"/bindings/"+k, bindings[k])
		}
		for i, d := range disks {
			if s, ok := d.SizeGB.(string); ok {
				read(fmt.Sprintf("%s/disks/%d/size_gb", at, i), s)
			}
			read(fmt.Sprintf("%s/disks/%d/datastore", at, i), d.Datastore)
		}
		for i, n := range nics {
			read(fmt.Sprintf("%s/nics/%d/network", at, i), n.Network)
		}
	}
	parts(ptr, b.Bindings, b.Disks, b.NICs)
	for i, c := range b.Components {
		at := fmt.Sprintf("%s/components/%d", ptr, i)
		if s, ok := c.Count.(string); ok {
			read(at+"/count", s)
		}
		parts(at, c.Bindings, c.Disks, c.NICs)
	}
	for i, r := range b.Resources {
		for _, k := range sortedKeys(r.With) {
			if s, ok := r.With[k].(string); ok {
				read(fmt.Sprintf("%s/resources/%d/with/%s", ptr, i, k), s)
			}
		}
	}
	return errs
}
