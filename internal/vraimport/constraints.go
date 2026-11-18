package vraimport

import (
	"errors"
	"fmt"
	"strings"
)

// vRA's constraint tags, key:value[:hard|:soft] with ! to rule out, as
// rvav's constraints, [!]category/tag[:soft] (ADR-0035).

// constraintsOf maps a resource's constraints property: its entries rvav
// can write, and a note for each it can't.
func (s scope) constraintsOf(raw any, where string, note func(level, where, text string)) []any {
	list, _ := raw.([]any)
	var out []any
	for i, x := range list {
		m, _ := x.(map[string]any)
		tag, _ := m["tag"].(string)
		at := fmt.Sprintf("%s[%d]", where, i)
		entry, err := s.constraint(tag)
		if err != nil {
			note(Unmapped, at, fmt.Sprintf("Constraint tag %q: %v. Left out.", tag, err))
			continue
		}
		out = append(out, entry)
	}
	return out
}

var errKeyOnly = errors.New("a tag without a value has no rvav form: a vSphere tag has a category and a name")

// constraint maps one vRA constraint tag.
func (s scope) constraint(tag string) (string, error) {
	text := strings.TrimSpace(tag)
	negated := strings.HasPrefix(text, "!")
	text = strings.TrimSpace(strings.TrimPrefix(text, "!"))
	if text == "" {
		return "", errors.New("it is empty")
	}
	// Expressions may hold colons: they are set aside while the tag is split.
	var exprs []string
	text = refPattern.ReplaceAllStringFunc(text, func(m string) string {
		exprs = append(exprs, m[2:len(m)-1])
		return fmt.Sprintf("\x00%d\x00", len(exprs)-1)
	})
	parts := strings.Split(text, ":")
	soft := false
	if n := len(parts); n > 1 {
		switch strings.ToLower(strings.TrimSpace(parts[n-1])) {
		case "soft":
			soft, parts = true, parts[:n-1]
		case "hard":
			parts = parts[:n-1]
		}
	}
	if len(parts) == 1 {
		if len(exprs) == 1 && parts[0] == "\x000\x00" {
			return "", errors.New("the whole tag comes from an expression, as vRA's key:value: write rvav's category/tag in the blueprint")
		}
		return "", errKeyOnly
	}
	key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(strings.Join(parts[1:], ":"))
	if key == "" || value == "" {
		return "", errKeyOnly
	}
	out := key + "/" + value
	for i, src := range exprs {
		t, err := s.cel(src)
		if err != nil {
			return "", err
		}
		if t.self || len(t.components) > 0 {
			return "", errors.New("it reads a machine, and rvav places VMs before it builds any")
		}
		out = strings.Replace(out, fmt.Sprintf("\x00%d\x00", i), "${"+t.cel+"}", 1)
	}
	if negated {
		out = "!" + out
	}
	if soft {
		out += ":soft"
	}
	return out, nil
}
