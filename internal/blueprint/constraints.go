package blueprint

import (
	"errors"
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/restcall"
)

// Constraints are the vSphere tags the clusters, datastores and networks
// a component's VMs go to must have, or must not (ADR-0035). Each entry is
// [!]category/tag[:hard|:soft], and may hold ${CEL}. A component inherits
// each kind the blueprint lists and it doesn't; [] lists none.
type Constraints struct {
	Compute []string `json:"compute,omitempty"`
	Storage []string `json:"storage,omitempty"`
	Network []string `json:"network,omitempty"`
}

// Constraint kinds: what a constraint chooses.
const (
	Compute = "compute"
	Storage = "storage"
	Network = "network"
)

// under is c with what it doesn't list taken from the blueprint's.
func (c *Constraints) under(b *Constraints) *Constraints {
	if c == nil {
		return b
	}
	if b == nil {
		return c
	}
	out := *c
	if out.Compute == nil {
		out.Compute = b.Compute
	}
	if out.Storage == nil {
		out.Storage = b.Storage
	}
	if out.Network == nil {
		out.Network = b.Network
	}
	return &out
}

// Entries lists the constraints' entries by kind, with their place under
// the component (constraints/compute/0).
func (c *Constraints) Entries() []Entry {
	if c == nil {
		return nil
	}
	var out []Entry
	for _, k := range []struct {
		kind string
		list []string
	}{{Compute, c.Compute}, {Storage, c.Storage}, {Network, c.Network}} {
		for i, e := range k.list {
			out = append(out, Entry{Kind: k.kind, Text: e, At: fmt.Sprintf("constraints/%s/%d", k.kind, i)})
		}
	}
	return out
}

// Entry is one constraint as the blueprint writes it.
type Entry struct {
	Kind string
	Text string
	// At is its place under the component, as a JSON pointer without the
	// leading slash.
	At string
	// Disk is 1 + the index of the added disk it is the own of; 0 for the
	// VMs'.
	Disk int
}

// Constraint is one constraint, worked out.
type Constraint struct {
	Kind string
	Tag  string // category/tag
	Not  bool
	Soft bool
	Disk int
}

func (c Constraint) String() string {
	s := c.Tag
	if c.Not {
		s = "!" + s
	}
	if c.Soft {
		s += ":soft"
	}
	return s
}

// Meets says whether an object with these tags meets the constraint.
func (c Constraint) Meets(tags []string) bool {
	for _, t := range tags {
		if t == c.Tag {
			return !c.Not
		}
	}
	return c.Not
}

// ParseConstraint reads [!]category/tag[:hard|:soft].
func ParseConstraint(s string) (Constraint, error) {
	var c Constraint
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "!"); ok {
		c.Not, s = true, strings.TrimSpace(rest)
	}
	if rest, ok := strings.CutSuffix(s, ":soft"); ok {
		c.Soft, s = true, rest
	} else {
		s, _ = strings.CutSuffix(s, ":hard")
	}
	cat, tag, ok := strings.Cut(s, "/")
	if !ok || strings.TrimSpace(cat) == "" || strings.TrimSpace(tag) == "" {
		if k, v, colon := strings.Cut(s, ":"); colon && k != "" && v != "" {
			return c, fmt.Errorf("%q is vRA's way: rvav writes category/tag, as %s/%s", s, k, v)
		}
		return c, fmt.Errorf("%q is not category/tag, as rvav-tier/gold", s)
	}
	c.Tag = s
	return c, nil
}

// HasExpr says whether an entry holds ${CEL}, worked out per request.
func HasExpr(entry string) bool { return strings.Contains(entry, "${") }

// ExprsOf returns the CEL an entry holds.
func ExprsOf(entry string) ([]string, error) {
	parts, err := restcall.ParseURL(entry)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range parts {
		if p.Expr != "" {
			out = append(out, p.Expr)
		}
	}
	return out, nil
}

// ConstraintEntries lists a component's (from ComponentList) entries: its
// VMs', then each added disk's own.
func (c Component) ConstraintEntries() []Entry {
	out := c.Constraints.Entries()
	for i, d := range c.Disks {
		for j, e := range d.Constraints {
			out = append(out, Entry{Kind: Storage, Text: e, At: fmt.Sprintf("disks/%d/constraints/%d", i, j), Disk: i + 1})
		}
	}
	return out
}

// ConstraintProblem is an entry that gives no constraint.
type ConstraintProblem struct {
	Entry
	// Missing: its CEL could not be worked out (inputs not given yet).
	Missing bool
	Msg     string
}

// EvaluateConstraints works out a component's constraints over vars. An
// entry that is only ${...} may give a list; one whose CEL gives no text
// adds none.
func (c Component) EvaluateConstraints(vars policy.Vars) ([]Constraint, []ConstraintProblem) {
	var out []Constraint
	var problems []ConstraintProblem
	for _, e := range c.ConstraintEntries() {
		texts, err := expand(e.Text, vars)
		if err != nil {
			problems = append(problems, ConstraintProblem{Entry: e, Missing: true, Msg: err.Error()})
			continue
		}
		for _, t := range texts {
			k, err := ParseConstraint(t)
			if err != nil {
				problems = append(problems, ConstraintProblem{Entry: e, Msg: err.Error()})
				continue
			}
			k.Kind, k.Disk = e.Kind, e.Disk
			out = append(out, k)
		}
	}
	return out, problems
}

var errNotText = errors.New("gives no text")

// expand works out an entry's ${CEL}.
func expand(entry string, vars policy.Vars) ([]string, error) {
	if !HasExpr(entry) {
		return []string{entry}, nil
	}
	parts, err := restcall.ParseURL(entry)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 && parts[0].Expr != "" {
		v, err := policy.Eval(parts[0].Expr, vars)
		if err != nil {
			return nil, err
		}
		var out []string
		switch x := v.(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				out = append(out, x)
			}
		case []any:
			for _, e := range x {
				s, ok := e.(string)
				if !ok {
					return nil, errNotText
				}
				if strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
			}
		default:
			return nil, errNotText
		}
		return out, nil
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Expr == "" {
			b.WriteString(p.Text)
			continue
		}
		v, err := policy.Eval(p.Expr, vars)
		if err != nil {
			return nil, err
		}
		switch v.(type) {
		case string, int64, float64, bool:
		default:
			return nil, errNotText
		}
		s := fmt.Sprint(v)
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		b.WriteString(s)
	}
	return []string{b.String()}, nil
}
