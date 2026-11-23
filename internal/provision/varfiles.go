package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/blueprint"
)

// Files in shared variables (ADR-0057): a step's expressions read one's
// content as vars.<group>.<name>.text, loaded for the files they name only.
// A binding that is a file's content whole is given on as its marker,
// {"$file": "<group>.<name>"}, which the activity that launches the AWX job
// opens: the content stays out of Temporal's history, whose payloads are
// limited. A custom workflow is given the marker, as it is a secret input's.

const fileKey = "$file"

var variableRef = regexp.MustCompile(`\bvars\.([a-z][a-z0-9_]*)\.([a-z][a-z0-9_]*)`)

// variableKeys are the shared variables, as group.name, that reads name:
// texts, or values whose JSON is read.
func variableKeys(reads ...any) []string {
	var keys []string
	for _, r := range reads {
		text, ok := r.(string)
		if !ok {
			b, _ := json.Marshal(r)
			text = string(b)
		}
		for _, m := range variableRef.FindAllStringSubmatch(text, -1) {
			if k := m[1] + "." + m[2]; !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// withFileTexts gives the files of the variables reads names their text, in
// shared (by group and name).
func (a *Activities) withFileTexts(ctx context.Context, shared map[string]any, reads ...any) error {
	keys := variableKeys(reads...)
	if len(keys) == 0 {
		return nil
	}
	texts, err := a.Store.VariableFileTexts(ctx, keys)
	if err != nil {
		return err
	}
	for key, text := range texts {
		g, n, _ := strings.Cut(key, ".")
		group, _ := shared[g].(map[string]any)
		if rec, ok := group[n].(map[string]any); ok {
			rec = maps.Clone(rec)
			rec["text"] = text
			group[n] = rec
		}
	}
	return nil
}

// byReference replaces, in each value bound by with, the content of a file
// its expression names with the file's marker.
func byReference(values, with map[string]any, shared map[string]any) {
	for k, v := range values {
		expr, ok := with[k].(string)
		if !ok {
			continue
		}
		texts := map[string]string{}
		for _, key := range variableKeys(expr) {
			g, n, _ := strings.Cut(key, ".")
			group, _ := shared[g].(map[string]any)
			rec, _ := group[n].(map[string]any)
			if text, ok := rec["text"].(string); ok && text != "" {
				texts[text] = key
			}
		}
		if len(texts) > 0 {
			values[k] = markFiles(v, texts)
		}
	}
}

func markFiles(v any, texts map[string]string) any {
	switch x := v.(type) {
	case string:
		if key, ok := texts[x]; ok {
			return map[string]any{fileKey: key}
		}
	case map[string]any:
		for k, e := range x {
			x[k] = markFiles(e, texts)
		}
	case []any:
		for i, e := range x {
			x[i] = markFiles(e, texts)
		}
	}
	return v
}

func fileMarker(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return "", false
	}
	key, ok := m[fileKey].(string)
	return key, ok
}

// openFiles replaces the files' markers in vars, at any depth, with their
// content as it is now; vars is left as it is. A file gone since is an
// error.
func (a *Activities) openFiles(ctx context.Context, vars map[string]any) (map[string]any, error) {
	var keys []string
	var find func(any)
	find = func(v any) {
		if key, ok := fileMarker(v); ok {
			keys = append(keys, key)
			return
		}
		switch x := v.(type) {
		case map[string]any:
			for _, e := range x {
				find(e)
			}
		case []any:
			for _, e := range x {
				find(e)
			}
		}
	}
	find(vars)
	if len(keys) == 0 {
		return vars, nil
	}
	texts, err := a.Store.VariableFileTexts(ctx, keys)
	if err != nil {
		return nil, err
	}
	var gone error
	var open func(any) any
	open = func(v any) any {
		if key, ok := fileMarker(v); ok {
			text, ok := texts[key]
			if !ok {
				gone = permanent("the variable %s holds no file any more", key)
			}
			return text
		}
		switch x := v.(type) {
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, e := range x {
				out[k] = open(e)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = open(e)
			}
			return out
		}
		return v
	}
	out, _ := open(vars).(map[string]any)
	return out, gone
}

// userDataFiles reads the files user data reads, by group.name, for its
// file function.
func (a *Activities) userDataFiles(ctx context.Context, texts ...string) (func(string) (string, error), error) {
	var keys []string
	for _, t := range texts {
		refs, _ := blueprint.UserDataFiles(t)
		for _, r := range refs {
			keys = append(keys, r.Name)
		}
	}
	files, err := a.Store.VariableFileTexts(ctx, keys)
	if err != nil {
		return nil, err
	}
	return func(key string) (string, error) {
		text, ok := files[key]
		if !ok {
			return "", fmt.Errorf("there is no variable %s that holds a file", key)
		}
		return text, nil
	}, nil
}
