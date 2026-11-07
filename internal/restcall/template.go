package restcall

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Part is a piece of a URL template: text as written, or a CEL expression
// (`${...}`) whose value goes there, escaped.
type Part struct {
	Text string
	Expr string
}

// ParseURL splits a URL template. An expression ends at the `}` that closes
// its `${`: braces inside it (a map) and in its strings don't.
func ParseURL(tpl string) ([]Part, error) {
	var parts []Part
	for {
		i := strings.Index(tpl, "${")
		if i < 0 {
			if tpl != "" {
				parts = append(parts, Part{Text: tpl})
			}
			return parts, nil
		}
		if i > 0 {
			parts = append(parts, Part{Text: tpl[:i]})
		}
		end := exprEnd(tpl[i+2:])
		if end < 0 {
			return nil, errors.New("a ${ isn't closed")
		}
		expr := strings.TrimSpace(tpl[i+2 : i+2+end])
		if expr == "" {
			return nil, errors.New("a ${} is empty")
		}
		parts = append(parts, Part{Expr: expr})
		tpl = tpl[i+2+end+1:]
	}
}

// exprEnd is where the expression opening s ends: the index of its `}`.
func exprEnd(s string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '{':
			depth++
		case c == '}':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// Expressions are a URL template's expressions; none when it doesn't parse.
func Expressions(tpl string) []string {
	parts, err := ParseURL(tpl)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range parts {
		if p.Expr != "" {
			out = append(out, p.Expr)
		}
	}
	return out
}

// Skeleton is a template with each expression's place filled by a word: what
// its scheme and host are, whatever the expressions give. It's the template
// as written when it doesn't parse.
func Skeleton(tpl string) string {
	parts, err := ParseURL(tpl)
	if err != nil {
		return tpl
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Expr != "" {
			b.WriteString("x")
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

// Fill gives a template's URL: each expression evaluated, its value escaped.
// A value that would change what the URL names is refused: empty (DELETE
// /ci/ isn't DELETE /ci/42), `.` or `..`, or not a single value.
func Fill(parts []Part, eval func(expr string) (any, error)) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		if p.Expr == "" {
			b.WriteString(p.Text)
			continue
		}
		v, err := eval(p.Expr)
		if err != nil {
			return "", &FillError{Expr: p.Expr, Err: err}
		}
		s, err := urlValue(v)
		if err != nil {
			return "", &FillError{Expr: p.Expr, Err: err}
		}
		b.WriteString(Escape(s))
	}
	return b.String(), nil
}

// FillError says which expression of a template failed.
type FillError struct {
	Expr string
	Err  error
}

func (e *FillError) Error() string { return e.Expr + ": " + e.Err.Error() }
func (e *FillError) Unwrap() error { return e.Err }

func urlValue(v any) (string, error) {
	var s string
	switch x := v.(type) {
	case nil:
		return "", errors.New("it is empty")
	case string:
		s = x
	case bool:
		s = strconv.FormatBool(x)
	case int64:
		s = strconv.FormatInt(x, 10)
	case uint64:
		s = strconv.FormatUint(x, 10)
	case float64:
		// A number comes back from JSON as a float: 42, not 4.2e+01.
		s = strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return "", errors.New("it isn't a single value")
	}
	switch s {
	case "":
		return "", errors.New("it is empty")
	case ".", "..":
		return "", fmt.Errorf("it can't be %q", s)
	}
	return s, nil
}
