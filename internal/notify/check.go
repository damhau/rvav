package notify

import (
	"fmt"
	"strings"
	"text/template/parse"
)

// A template's fields are checked before it is saved (ADR-0059): every
// field it names, in every branch, is one of its kind's variables.

// node is what dot or a variable holds while a template is checked: a
// variable's path, or anything when it came from a function. A function's
// result has no field the check could find: or and and give back one of
// their arguments, so {{with or $ 0}} would hide every field after it.
type node struct {
	path string
	any  bool
}

type checker struct {
	tree  *parse.Tree
	vars  map[string]bool
	lists map[string]bool
	scope []map[string]node
	// ranges is how many ranges the node walked is in.
	ranges int
	err    error
}

func newChecker(kind string, tree *parse.Tree) *checker {
	lists := map[string]bool{}
	vs := common
	if k, ok := KindOf(kind); ok {
		vs = k.Variables
	}
	for _, v := range vs {
		if strings.HasPrefix(v.Type, "list") {
			lists[v.Name] = true
		}
	}
	return &checker{tree: tree, vars: variables(kind), lists: lists, scope: []map[string]node{{"$": {}}}}
}

func (c *checker) fail(n parse.Node, format string, a ...any) {
	if c.err == nil {
		loc, _ := c.tree.ErrorContext(n)
		if _, line, ok := strings.Cut(loc, ":"); ok {
			if l, _, ok := strings.Cut(line, ":"); ok {
				format = "line " + l + ": " + format
			}
		}
		c.err = fmt.Errorf(format, a...)
	}
}

// field is what a field of n holds; the root's path is "".
func (c *checker) field(at parse.Node, n node, names []string) node {
	for _, name := range names {
		if n.any {
			c.fail(at, "%s is a field of what a function gave, which can't be checked: name the variable itself, such as .Request.Number", name)
			return n
		}
		p := n.path + "." + name
		if !c.vars[p] {
			switch list, isItem := strings.CutSuffix(n.path, "[]"); {
			case isItem:
				c.fail(at, "the items of %s are text: they have no field %s", list, name)
			case n.path == "":
				c.fail(at, "%s is not a variable of this kind", p)
			default:
				c.fail(at, "%s has no field %s: %s is not a variable of this kind", n.path, name, p)
			}
			return node{any: true}
		}
		n = node{path: p}
	}
	return n
}

func (c *checker) lookup(name string) (node, bool) {
	for i := len(c.scope) - 1; i >= 0; i-- {
		if n, ok := c.scope[i][name]; ok {
			return n, true
		}
	}
	return node{}, false
}

// pipe checks a pipeline and says what it gives: a variable when it is one
// alone, anything when a function made it.
func (c *checker) pipe(dot node, p *parse.PipeNode) node {
	if p == nil {
		return dot
	}
	out := node{any: true}
	for i, cmd := range p.Cmds {
		for _, arg := range cmd.Args {
			got := c.arg(dot, arg)
			if i == len(p.Cmds)-1 && len(p.Cmds) == 1 && len(cmd.Args) == 1 {
				out = got
			}
		}
	}
	for _, v := range p.Decl {
		c.scope[len(c.scope)-1][v.Ident[0]] = out
	}
	return out
}

func (c *checker) arg(dot node, a parse.Node) node {
	switch a := a.(type) {
	case *parse.FieldNode:
		return c.field(a, dot, a.Ident)
	case *parse.DotNode:
		return dot
	case *parse.VariableNode:
		v, ok := c.lookup(a.Ident[0])
		if !ok {
			return node{any: true}
		}
		return c.field(a, v, a.Ident[1:])
	case *parse.ChainNode:
		inner := node{any: true}
		if p, ok := a.Node.(*parse.PipeNode); ok {
			inner = c.pipe(dot, p)
		} else {
			inner = c.arg(dot, a.Node)
		}
		return c.field(a, inner, a.Field)
	case *parse.PipeNode:
		c.scope = append(c.scope, map[string]node{})
		out := c.pipe(dot, a)
		c.scope = c.scope[:len(c.scope)-1]
		return out
	}
	return node{any: true}
}

func (c *checker) list(dot node, l *parse.ListNode) {
	if l == nil {
		return
	}
	c.scope = append(c.scope, map[string]node{})
	for _, n := range l.Nodes {
		c.walk(dot, n)
	}
	c.scope = c.scope[:len(c.scope)-1]
}

func (c *checker) walk(dot node, n parse.Node) {
	switch n := n.(type) {
	case *parse.ActionNode:
		c.pipe(dot, n.Pipe)
	case *parse.IfNode:
		c.pipe(dot, n.Pipe)
		c.list(dot, n.List)
		c.list(dot, n.ElseList)
	case *parse.WithNode:
		c.scope = append(c.scope, map[string]node{})
		inner := c.pipe(dot, n.Pipe)
		c.list(inner, n.List)
		c.scope = c.scope[:len(c.scope)-1]
		c.list(dot, n.ElseList)
	case *parse.RangeNode:
		c.scope = append(c.scope, map[string]node{})
		over := c.pipe(dot, n.Pipe)
		// Only over a list variable: a range over a number could write
		// without end.
		if over.any || !c.lists[over.path] {
			c.fail(n, "range goes over a list variable of this kind, such as .Approval.Reasons of approvals")
		}
		// A list in a list writes its length squared.
		if c.ranges > 0 {
			c.fail(n, "a range can't be inside another")
		}
		elem := node{path: over.path + "[]"}
		if len(n.Pipe.Decl) > 0 {
			last := n.Pipe.Decl[len(n.Pipe.Decl)-1].Ident[0]
			c.scope[len(c.scope)-1][last] = elem
			if len(n.Pipe.Decl) == 2 {
				c.scope[len(c.scope)-1][n.Pipe.Decl[0].Ident[0]] = node{any: true}
			}
		}
		c.ranges++
		c.list(elem, n.List)
		c.ranges--
		c.scope = c.scope[:len(c.scope)-1]
		c.list(dot, n.ElseList)
	case *parse.TemplateNode:
		c.fail(n, "a template can't call another ({{template %q}})", n.Name)
	case *parse.ListNode:
		c.list(dot, n)
	}
}

// checkFields says the first field a template names that its kind doesn't
// have, in any branch.
func checkFields(kind string, tree *parse.Tree) error {
	c := newChecker(kind, tree)
	c.list(node{}, tree.Root)
	return c.err
}
