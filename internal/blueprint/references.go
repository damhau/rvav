package blueprint

import (
	"fmt"
	"slices"
	"text/template/parse"

	"github.com/damhau/rvav/internal/policy"
)

// References between components (ADR-0024): a component's tags binding and
// its cloud-init user data may read what other components built, their
// names, FQDNs, addresses and count, as components.<key>. A component is
// built after those it reads and those its depends_on names.

// Refs are the components a component reads.
type Refs struct {
	// Tags are those its tags binding reads, UserData those its cloud-init
	// user data reads (itself included: its own VMs all have their
	// addresses before any is customized).
	Tags, UserData []string
	// BuildTags says its tags binding reads self or components: it is
	// worked out as each VM is cloned, not when the request is submitted.
	BuildTags bool
}

// Refs says what the component (from ComponentList) reads. An expression or
// a template that doesn't parse reads nothing: saving it is refused.
func (c Component) Refs() Refs {
	var r Refs
	if expr, ok := c.Bindings["tags"]; ok {
		if rd, err := policy.ReadsOf(expr); err == nil {
			r.Tags, r.BuildTags = rd.Components, BuildTime(rd)
		}
	}
	if c.Guest != nil && c.Guest.CloudInit != nil {
		r.UserData, _ = UserDataComponents(c.Guest.CloudInit.UserData)
	}
	return r
}

// BuildTime says a binding reads what only exists as each VM is built:
// the VM itself (self) or other components.
func BuildTime(rd policy.Reads) bool { return rd.Reading("self") || rd.Reading("components") }

// Reads are the components whose outputs the component's VMs are given:
// those it reads that the blueprint has.
func (b Blueprint) Reads(c Component) []string {
	r := c.Refs()
	var out []string
	for _, o := range b.Components {
		if slices.Contains(r.Tags, o.Key) || slices.Contains(r.UserData, o.Key) {
			out = append(out, o.Key)
		}
	}
	return out
}

// DependsOn are the components c (from ComponentList) is built after: its
// depends_on and the others it reads, in the blueprint's order.
func (b Blueprint) DependsOn(c Component) []string {
	r := c.Refs()
	var out []string
	for _, o := range b.Components {
		if o.Key != c.Key && (slices.Contains(c.DependsOn, o.Key) || slices.Contains(r.Tags, o.Key) || slices.Contains(r.UserData, o.Key)) {
			out = append(out, o.Key)
		}
	}
	return out
}

// Cycle is a chain of components each built after the next that comes
// back to its first (web, db, web), or nil when there is none.
func (b Blueprint) Cycle() []string {
	deps := map[string][]string{}
	for _, c := range b.ComponentList() {
		deps[c.Key] = b.DependsOn(c)
	}
	const (
		unseen = iota
		open
		closed
	)
	state := map[string]int{}
	var path []string
	var visit func(k string) []string
	visit = func(k string) []string {
		state[k] = open
		path = append(path, k)
		for _, d := range deps[k] {
			switch state[d] {
			case open:
				i := slices.Index(path, d)
				return append(slices.Clone(path[i:]), d)
			case unseen:
				if c := visit(d); c != nil {
					return c
				}
			}
		}
		state[k] = closed
		path = path[:len(path)-1]
		return nil
	}
	for _, c := range b.Components {
		if state[c.Key] == unseen {
			if cycle := visit(c.Key); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}

// UserDataComponents are the components user data reads, as
// .components.<key>, in the order it names them. Reading .components some
// other way (ranging over it, index .components "db") is an error: which
// ones it reads couldn't be told before it runs.
func UserDataComponents(text string) ([]string, error) {
	t, err := ParseUserData(text)
	if err != nil {
		return nil, err
	}
	var keys []string
	loose := false
	field := func(ident []string) {
		if ident[0] != "components" {
			return
		}
		if len(ident) < 2 {
			loose = true
		} else if !slices.Contains(keys, ident[1]) {
			keys = append(keys, ident[1])
		}
	}
	var visit func(n parse.Node)
	branch := func(b *parse.BranchNode) {
		visit(b.Pipe)
		visit(b.List)
		visit(b.ElseList)
	}
	visit = func(n parse.Node) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n != nil {
				for _, c := range n.Nodes {
					visit(c)
				}
			}
		case *parse.ActionNode:
			visit(n.Pipe)
		case *parse.PipeNode:
			if n != nil {
				for _, c := range n.Cmds {
					visit(c)
				}
			}
		case *parse.CommandNode:
			for _, a := range n.Args {
				visit(a)
			}
		case *parse.FieldNode:
			field(n.Ident)
		case *parse.VariableNode:
			// Not only $: {{ $r := . }} makes $r.components read them too.
			if len(n.Ident) > 1 {
				field(n.Ident[1:])
			}
		case *parse.ChainNode:
			visit(n.Node)
		case *parse.IfNode:
			branch(&n.BranchNode)
		case *parse.RangeNode:
			branch(&n.BranchNode)
		case *parse.WithNode:
			branch(&n.BranchNode)
		case *parse.TemplateNode:
			visit(n.Pipe)
		}
	}
	for _, tt := range t.Templates() {
		if tt.Tree != nil {
			visit(tt.Root)
		}
	}
	if loose {
		return keys, fmt.Errorf("name the component you read, as in {{ .components.db.addresses }}, not .components alone")
	}
	return keys, nil
}
