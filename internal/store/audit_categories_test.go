package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var auditAction = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// auditedActions are the actions rvav's code audits, as string literals:
// those given to a function whose name says audit, an AuditEvent's
// Action, or a variable named for an action.
func auditedActions(t *testing.T, root string) map[string]string {
	t.Helper()
	found := map[string]string{}
	fset := token.NewFileSet()
	literals := func(n ast.Node, where token.Pos) {
		ast.Inspect(n, func(n ast.Node) bool {
			if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING {
				if v, err := strconv.Unquote(l.Value); err == nil && auditAction.MatchString(v) {
					found[v] = fset.Position(where).String()
				}
			}
			return true
		})
	}
	// action, auditAction: not Actions, vRO's action modules.
	actionVar := func(name string) bool {
		return strings.HasSuffix(name, "action") || strings.HasSuffix(name, "Action")
	}
	named := func(e ast.Expr) string {
		switch e := e.(type) {
		case *ast.Ident:
			return e.Name
		case *ast.SelectorExpr:
			return e.Sel.Name
		}
		return ""
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasSuffix(path, ".gen.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if strings.Contains(strings.ToLower(named(n.Fun)), "audit") {
					for _, a := range n.Args {
						literals(a, n.Pos())
					}
				}
			case *ast.CompositeLit:
				if named(n.Type) == "AuditEvent" {
					for _, el := range n.Elts {
						if kv, ok := el.(*ast.KeyValueExpr); ok && named(kv.Key) == "Action" {
							literals(kv.Value, kv.Pos())
						}
					}
				}
			case *ast.AssignStmt:
				for i, l := range n.Lhs {
					if actionVar(named(l)) && i < len(n.Rhs) {
						literals(n.Rhs[i], n.Pos())
					}
				}
			case *ast.ValueSpec:
				for i, name := range n.Names {
					if actionVar(name.Name) && i < len(n.Values) {
						literals(n.Values[i], n.Pos())
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// Every action the code audits is forwarded under a category of its own:
// a destination that takes only some categories misses none of them as
// "other" (ADR-0058).
func TestEveryAuditedActionHasACategory(t *testing.T) {
	found := auditedActions(t, "..")
	for _, want := range []string{"auth.sign_in", "ip_range.create", "schedule.create", "variable.create", "ad.placement.create",
		"settings.audit_destination.create", "catalog.version.publish"} {
		if _, ok := found[want]; !ok {
			t.Errorf("%s isn't found among the audited actions: the scan misses some", want)
		}
	}
	var other []string
	for action, where := range found {
		if AuditCategory(action) == AuditOther {
			other = append(other, action+" ("+where+")")
		}
	}
	slices.Sort(other)
	if len(other) > 0 {
		t.Errorf("audited under \"other\":\n%s", strings.Join(other, "\n"))
	}
}
