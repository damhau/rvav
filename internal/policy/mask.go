package policy

import (
	"slices"
	"strings"
	"sync"

	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/parser"
)

// Masked is what a string literal reads as once masked, and what a whole
// expression that doesn't parse reads as.
const Masked = "…"

var maskParser = sync.OnceValues(func() (*parser.Parser, error) {
	return parser.NewParser(parser.EnableOptionalSyntax(true))
})

// MaskLiterals is a CEL expression as a requester may read it (ADR-0032):
// each string and bytes literal's text replaced by "…" in its own quotes,
// and its comments removed. Identifiers, operators, numbers and booleans
// stay as written, so the expression still says what it reads (inputs.env,
// steps.nodes.fqdn) and how, not the values it compares or maps to: a
// token, another project's code, a network a map picks. An empty literal
// stays: it says nothing. With lone, an expression that is only a string
// literal, or a list of string literals, keeps them: it is the value the
// item uses (a network, a template, post-configuration keys), which the
// canvas draws by name. An expression that doesn't parse is "…".
//
// The literals are found by cel-go's parser, then masked where they are
// written, so the rest of the text is the author's; the mocks' topologyOf
// does the same with a scanner.
func MaskLiterals(expr string, lone bool) string {
	p, err := maskParser()
	if err != nil {
		return Masked
	}
	parsed, errs := p.Parse(common.NewTextSource(expr))
	if parsed == nil || errs != nil && len(errs.GetErrors()) > 0 {
		return Masked
	}
	info := parsed.SourceInfo()
	var starts []int
	ast.PreOrderVisit(parsed.Expr(), ast.NewExprVisitor(func(e ast.Expr) {
		if isText(e) {
			if r, ok := info.GetOffsetRange(e.ID()); ok {
				starts = append(starts, int(r.Start))
			}
		}
	}))
	slices.Sort(starts)
	mask := !(lone && onlyText(parsed.Expr()))

	src := []rune(expr)
	var out strings.Builder
	next := 0 // the next literal in starts
	for i := 0; i < len(src); {
		if next < len(starts) && starts[next] == i {
			next++
			open, end, stop, ok := literalAt(src, i)
			if !ok {
				return Masked
			}
			if mask && end > open {
				out.WriteString(string(src[i:open]) + Masked + string(src[end:stop]))
			} else {
				out.WriteString(string(src[i:stop]))
			}
			i = stop
			continue
		}
		if src[i] == '/' && i+1 < len(src) && src[i+1] == '/' {
			// A comment, to the end of its line, and the blanks before it.
			trimmed := strings.TrimRight(out.String(), " \t")
			out.Reset()
			out.WriteString(trimmed)
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		out.WriteRune(src[i])
		i++
	}
	return out.String()
}

// isText is a string or bytes literal.
func isText(e ast.Expr) bool {
	if e.Kind() != ast.LiteralKind {
		return false
	}
	switch e.AsLiteral().(type) {
	case types.String, types.Bytes:
		return true
	}
	return false
}

// onlyText is a string literal, or a list of nothing else.
func onlyText(e ast.Expr) bool {
	if e.Kind() == ast.ListKind {
		l := e.AsList()
		if len(l.OptionalIndices()) > 0 {
			return false
		}
		for _, x := range l.Elements() {
			if !isText(x) {
				return false
			}
		}
		return true
	}
	_, ok := e.AsLiteral().(types.String)
	return e.Kind() == ast.LiteralKind && ok
}

// literalAt reads the string or bytes literal written at src[i]: its
// prefix (r, b), its quotes (one or three, ' or "), its text from open to
// end, and where it stops. A raw literal has no escapes.
func literalAt(src []rune, i int) (open, end, stop int, ok bool) {
	j, raw := i, false
	for j < len(src) && j-i < 2 && strings.ContainsRune("rRbB", src[j]) {
		raw = raw || src[j] == 'r' || src[j] == 'R'
		j++
	}
	if j >= len(src) || src[j] != '\'' && src[j] != '"' {
		return 0, 0, 0, false
	}
	q := string(src[j])
	if j+2 < len(src) && string(src[j:j+3]) == q+q+q {
		q += q + q
	}
	open = j + len(q)
	for k := open; k < len(src); k++ {
		if !raw && src[k] == '\\' {
			k++
			continue
		}
		if k+len(q) <= len(src) && string(src[k:k+len(q)]) == q {
			return open, k, k + len(q), true
		}
	}
	return 0, 0, 0, false
}
