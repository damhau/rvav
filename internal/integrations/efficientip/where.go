package efficientip

import (
	"fmt"
	"regexp"
	"strings"
)

// The fake server's reading of SOLIDserver's WHERE parameter: comparisons
// (=, !=, like with %) of a field with a quoted or bare value, joined by
// and, or and parentheses. Enough for the clauses rvav sends. Comparisons
// are case-sensitive, so a mis-cased name in rvav's settings fails here as
// it would on SOLIDserver.

type cond func(row) bool

type whereParser struct {
	s string
	i int
}

func parseWhere(s string) (cond, error) {
	if strings.TrimSpace(s) == "" {
		return func(row) bool { return true }, nil
	}
	p := &whereParser{s: s}
	c, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.skip(); p.i < len(p.s) {
		return nil, fmt.Errorf("WHERE: unexpected %q", p.s[p.i:])
	}
	return c, nil
}

func (p *whereParser) skip() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

func (p *whereParser) keyword(k string) bool {
	p.skip()
	end := p.i + len(k)
	if end <= len(p.s) && strings.EqualFold(p.s[p.i:end], k) && (end == len(p.s) || strings.ContainsRune(" (", rune(p.s[end]))) {
		p.i = end
		return true
	}
	return false
}

func (p *whereParser) or() (cond, error) {
	left, err := p.and()
	for err == nil && p.keyword("or") {
		var right cond
		if right, err = p.and(); err == nil {
			l := left
			left = func(r row) bool { return l(r) || right(r) }
		}
	}
	return left, err
}

func (p *whereParser) and() (cond, error) {
	left, err := p.atom()
	for err == nil && p.keyword("and") {
		var right cond
		if right, err = p.atom(); err == nil {
			l := left
			left = func(r row) bool { return l(r) && right(r) }
		}
	}
	return left, err
}

func (p *whereParser) atom() (cond, error) {
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == '(' {
		p.i++
		c, err := p.or()
		if err != nil {
			return nil, err
		}
		if p.skip(); p.i >= len(p.s) || p.s[p.i] != ')' {
			return nil, fmt.Errorf("WHERE: missing )")
		}
		p.i++
		return c, nil
	}
	start := p.i
	for p.i < len(p.s) && (isWord(p.s[p.i])) {
		p.i++
	}
	field := p.s[start:p.i]
	if field == "" {
		return nil, fmt.Errorf("WHERE: expected a field at %q", p.s[start:])
	}
	p.skip()
	var op string
	switch {
	case strings.HasPrefix(p.s[p.i:], "!="):
		op, p.i = "!=", p.i+2
	case strings.HasPrefix(p.s[p.i:], "="):
		op, p.i = "=", p.i+1
	case p.keyword("like"):
		op = "like"
	default:
		return nil, fmt.Errorf("WHERE: expected =, != or like after %s", field)
	}
	value, err := p.value()
	if err != nil {
		return nil, err
	}
	switch op {
	case "=":
		return func(r row) bool { return r[field] == value }, nil
	case "!=":
		return func(r row) bool { return r[field] != value }, nil
	}
	re, err := regexp.Compile("(?s)^" + strings.ReplaceAll(regexp.QuoteMeta(value), "%", ".*") + "$")
	if err != nil {
		return nil, err
	}
	return func(r row) bool { return re.MatchString(r[field]) }, nil
}

func (p *whereParser) value() (string, error) {
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == '\'' {
		var b strings.Builder
		for p.i++; p.i < len(p.s); p.i++ {
			if p.s[p.i] == '\'' {
				if p.i+1 < len(p.s) && p.s[p.i+1] == '\'' {
					b.WriteByte('\'')
					p.i++
					continue
				}
				p.i++
				return b.String(), nil
			}
			b.WriteByte(p.s[p.i])
		}
		return "", fmt.Errorf("WHERE: unterminated string")
	}
	start := p.i
	for p.i < len(p.s) && (isWord(p.s[p.i]) || p.s[p.i] == '.') {
		p.i++
	}
	if start == p.i {
		return "", fmt.Errorf("WHERE: expected a value at %q", p.s[start:])
	}
	return p.s[start:p.i], nil
}

func isWord(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
