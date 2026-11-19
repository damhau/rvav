// Package secretinput keeps a request's secret inputs out of its inputs,
// plan and Temporal's history (S14.6): a marker, {"$secret": "<field>"},
// stands for each, and the value is sealed with the secrets key.
package secretinput

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

const key = "$secret"

// Name is what a request's secret input is sealed under.
func Name(requestID, field string) string { return "request:" + requestID + ":" + field }

// Marker stands for a secret input.
func Marker(field string) map[string]any { return map[string]any{key: field} }

// IsMarker reports whether v is a marker.
func IsMarker(v any) bool {
	_, ok := field(v)
	return ok
}

func field(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return "", false
	}
	f, ok := m[key].(string)
	return f, ok
}

// Encode is a value as it is sealed (JSON, so a number stays a number).
func Encode(v any) ([]byte, error) { return json.Marshal(v) }

// Open replaces the markers in v (maps and lists, at any depth) with the
// sealed values they stand for, in place where v is a map. A marker whose
// value isn't sealed is an error, never passed on as it is.
func Open(box *secrets.Box, requestID string, sealed []store.SealedInput, v map[string]any) error {
	if box == nil && len(sealed) > 0 {
		return errors.New("rvav has no secrets key (RVAV_SECRETS_KEY) to open a secret input")
	}
	values := map[string]any{}
	for _, s := range sealed {
		plain, err := box.Open(Name(requestID, s.Field), s.KeyID, s.Nonce, s.Ciphertext)
		if err != nil {
			return fmt.Errorf("open %s: %w", s.Field, err)
		}
		var val any
		if err := json.Unmarshal(plain, &val); err != nil {
			return err
		}
		values[s.Field] = val
	}
	var missing error
	var walk func(any) any
	walk = func(x any) any {
		if f, ok := field(x); ok {
			val, ok := values[f]
			if !ok {
				missing = fmt.Errorf("the secret input %s isn't sealed with the request", f)
			}
			return val
		}
		switch t := x.(type) {
		case map[string]any:
			for k, e := range t {
				t[k] = walk(e)
			}
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
		}
		return x
	}
	walk(v)
	return missing
}

// Values are a request's secret inputs as texts, each leaf of a map or a
// list on its own: what a scrubber removes wherever one may be repeated.
func Values(box *secrets.Box, requestID string, sealed []store.SealedInput) ([]string, error) {
	if len(sealed) == 0 {
		return nil, nil
	}
	vars := map[string]any{}
	for _, s := range sealed {
		vars[s.Field] = Marker(s.Field)
	}
	if err := Open(box, requestID, sealed, vars); err != nil {
		return nil, err
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case nil:
		case string:
			out = append(out, x)
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		default:
			if b, err := json.Marshal(x); err == nil {
				out = append(out, string(b))
			}
		}
	}
	walk(vars)
	return out, nil
}

// Mask replaces each marker in v, at any depth, with mask: what a reader
// sees of a secret input. v itself is left as it is.
func Mask(v any, mask string) any {
	if IsMarker(v) {
		return mask
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = Mask(e, mask)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = Mask(e, mask)
		}
		return out
	}
	return v
}
