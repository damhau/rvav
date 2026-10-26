// Package naming renders VM names from naming policy templates such as
// "{project}-{environment}-{inputs.role}{seq:03}".
package naming

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Template is a parsed naming template. The sequence placeholder must come
// last so names sort and the counter prefix is everything before it.
type Template struct {
	raw      string
	parts    []part
	seqWidth int
}

type part struct {
	literal string
	field   string
}

var placeholder = regexp.MustCompile(`\{([a-z_.]+)(?::(\d+))?\}`)
var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func Parse(s string) (Template, error) {
	t := Template{raw: s}
	last := 0
	for _, m := range placeholder.FindAllStringSubmatchIndex(s, -1) {
		if m[0] > last {
			t.parts = append(t.parts, part{literal: s[last:m[0]]})
		}
		field := s[m[2]:m[3]]
		if field == "seq" {
			if m[1] != len(s) {
				return t, fmt.Errorf("naming %q: {seq} must be last", s)
			}
			t.seqWidth = 3
			if m[4] >= 0 {
				t.seqWidth, _ = strconv.Atoi(s[m[4]:m[5]])
			}
		} else {
			t.parts = append(t.parts, part{field: field})
		}
		last = m[1]
	}
	if last < len(s) {
		t.parts = append(t.parts, part{literal: s[last:]})
	}
	if t.seqWidth == 0 {
		return t, fmt.Errorf("naming %q: needs a {seq} placeholder", s)
	}
	return t, nil
}

// Prefix renders everything before the sequence number. vals holds
// "project", "environment" and "inputs.<name>" values.
func (t Template) Prefix(vals map[string]string) (string, error) {
	var b strings.Builder
	for _, p := range t.parts {
		if p.field == "" {
			b.WriteString(p.literal)
			continue
		}
		v := strings.ToLower(strings.TrimSpace(vals[p.field]))
		if v == "" {
			return "", fmt.Errorf("naming: %s is empty", p.field)
		}
		b.WriteString(v)
	}
	return b.String(), nil
}

// Name renders prefix plus the zero-padded sequence number.
func (t Template) Name(prefix string, seq int) string {
	return fmt.Sprintf("%s%0*d", prefix, t.seqWidth, seq)
}

// Valid reports whether name is usable as a VM and DNS host name.
func Valid(name string) bool { return validName.MatchString(name) }

// Values builds the placeholder values from project code, environment and inputs.
func Values(projectCode, environment string, inputs map[string]any) map[string]string {
	vals := map[string]string{"project": projectCode, "environment": environment}
	for k, v := range inputs {
		if s, ok := v.(string); ok {
			vals["inputs."+k] = s
		}
	}
	return vals
}
