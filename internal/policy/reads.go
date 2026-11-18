package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/google/cel-go/common/ast"
)

// Reads is what a CEL expression reads: the identifiers it names, and the
// components it names as components.<key>.
type Reads struct {
	Idents     []string
	Components []string
	// Loose is set when components is read other than as components.<key>
	// (components['db'], or components alone): which ones it reads can't
	// be told before it runs.
	Loose bool
}

// Reading says whether the expression names the identifier.
func (r Reads) Reading(ident string) bool { return slices.Contains(r.Idents, ident) }

// ReadsOf parses expr, without checking it, and says what it reads.
func ReadsOf(expr string) (Reads, error) {
	var r Reads
	env, err := build.celEnv()
	if err != nil {
		return r, err
	}
	parsed, iss := env.Parse(expr)
	if iss.Err() != nil {
		return r, fmt.Errorf("parse %q: %w", expr, iss.Err())
	}
	selected := map[int64]bool{}
	ast.PreOrderVisit(parsed.NativeRep().Expr(), ast.NewExprVisitor(func(e ast.Expr) {
		switch e.Kind() {
		case ast.SelectKind:
			sel := e.AsSelect()
			if op := sel.Operand(); op.Kind() == ast.IdentKind && op.AsIdent() == "components" {
				selected[op.ID()] = true
				if !slices.Contains(r.Components, sel.FieldName()) {
					r.Components = append(r.Components, sel.FieldName())
				}
			}
		case ast.IdentKind:
			name := e.AsIdent()
			// @result and the like: a macro's own variables.
			if !strings.HasPrefix(name, "@") && !slices.Contains(r.Idents, name) {
				r.Idents = append(r.Idents, name)
			}
			if name == "components" && !selected[e.ID()] {
				r.Loose = true
			}
		}
	}))
	return r, nil
}
