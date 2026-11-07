package restcall

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Removed is what is left where a secret was.
const Removed = "[removed]"

// Scrubber takes values out of what a call leaves behind (an API that echoes
// its request, a header in its answer's JSON, a proxy's error), in the forms
// they travel in: as they are, base64, URL-escaped and JSON-escaped.
type Scrubber struct{ r *strings.Replacer }

// NewScrubber scrubs these values.
func NewScrubber(values ...string) *Scrubber {
	var forms []string
	for _, v := range values {
		if v == "" {
			continue
		}
		b := []byte(v)
		js, _ := json.Marshal(v)
		forms = append(forms, v, string(js[1:len(js)-1]), url.QueryEscape(v), url.PathEscape(v), Escape(v),
			base64.StdEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b),
			base64.URLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b))
	}
	// The longest first: a value inside another form of it, or of another.
	slices.SortFunc(forms, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	forms = slices.Compact(forms)
	pairs := make([]string, 0, 2*len(forms))
	for _, f := range forms {
		pairs = append(pairs, f, Removed)
	}
	return &Scrubber{r: strings.NewReplacer(pairs...)}
}

// Text scrubs a string.
func (s *Scrubber) Text(t string) string { return s.r.Replace(t) }

// Value scrubs a JSON value at any depth, its keys too. A number or a
// boolean that holds a value becomes the mark that says so.
func (s *Scrubber) Value(v any) any {
	switch x := v.(type) {
	case string:
		return s.Text(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = s.Value(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[s.Text(k)] = s.Value(e)
		}
		return out
	case nil:
		return nil
	}
	if t := s.number(v); t != "" {
		return t
	}
	return v
}

func (s *Scrubber) number(v any) string {
	t := fmt.Sprint(v)
	if f, ok := v.(float64); ok {
		b, _ := json.Marshal(f)
		t = string(b)
	}
	if s.Text(t) != t {
		return Removed
	}
	return ""
}
