package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
)

// Reads is what a CEL expression reads: the identifiers it names, the
// components it names as components.<key> and the inputs as inputs.<name>
// or inputs["<name>"].
type Reads struct {
	Idents     []string
	Components []string
	Inputs     []string
	// Loose is set when components is read other than as components.<key>
	// (components['db'], or components alone): which ones it reads can't
	// be told before it runs. AllInputs is the same of inputs, read other
	// than by name.
	Loose     bool
	AllInputs bool
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
	fields := map[string]*[]string{"components": &r.Components, "inputs": &r.Inputs}
	whole := map[string]*bool{"components": &r.Loose, "inputs": &r.AllInputs}
	selected := map[int64]bool{}
	ast.PreOrderVisit(parsed.NativeRep().Expr(), ast.NewExprVisitor(func(e ast.Expr) {
		switch e.Kind() {
		case ast.CallKind:
			call := e.AsCall()
			if call.FunctionName() != operators.Index || len(call.Args()) != 2 {
				return
			}
			op, key := call.Args()[0], call.Args()[1]
			if op.Kind() != ast.IdentKind || op.AsIdent() != "inputs" || key.Kind() != ast.LiteralKind {
				return
			}
			if name, ok := key.AsLiteral().Value().(string); ok {
				selected[op.ID()] = true
				if !slices.Contains(r.Inputs, name) {
					r.Inputs = append(r.Inputs, name)
				}
			}
		case ast.SelectKind:
			sel := e.AsSelect()
			op := sel.Operand()
			if op.Kind() != ast.IdentKind {
				return
			}
			if list, ok := fields[op.AsIdent()]; ok {
				selected[op.ID()] = true
				if !slices.Contains(*list, sel.FieldName()) {
					*list = append(*list, sel.FieldName())
				}
			}
		case ast.IdentKind:
			name := e.AsIdent()
			// @result and the like: a macro's own variables.
			if !strings.HasPrefix(name, "@") && !slices.Contains(r.Idents, name) {
				r.Idents = append(r.Idents, name)
			}
			if w, ok := whole[name]; ok && !selected[e.ID()] {
				*w = true
			}
		}
	}))
	return r, nil
}
