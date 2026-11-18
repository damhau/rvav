package blueprint

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"text/template/parse"
)

// Stored secrets in templates (ADR-0038): cloud-init user data reads one
// as {{ secret "name" }}, and an AWX step's binding is ${secret.name}. Only
// names travel in the plan and Temporal's history; the worker opens the
// value when it writes the user data or launches the job.

// SecretRef is a stored secret a blueprint names, and where: a JSON pointer
// into the blueprint, and the line within user data.
type SecretRef struct {
	Name    string
	Pointer string
	Line    int
}

var bindingSecret = regexp.MustCompile(`^\$\{\s*secret\.([a-z][a-z0-9-]{1,62})\s*\}$`)

// BindingSecret gives the secret a binding names when the whole binding is
// ${secret.name}.
func BindingSecret(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	m := bindingSecret.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// UserDataSecrets lists the secrets user data reads, each by its line. A
// secret named otherwise than in quotes is an error: the requester's inputs
// must never choose which secret is opened.
func UserDataSecrets(text string) ([]SecretRef, error) {
	t, err := ParseUserData(text)
	if err != nil {
		return nil, err
	}
	var refs []SecretRef
	var bad []string
	line := func(n parse.Node) int { return strings.Count(text[:min(int(n.Position()), len(text))], "\n") + 1 }
	var walk func(n parse.Node)
	walkPipe := func(p *parse.PipeNode) {
		if p == nil {
			return
		}
		for i, c := range p.Cmds {
			for j, a := range c.Args {
				if id, ok := a.(*parse.IdentifierNode); ok && id.Ident == "secret" {
					name, literal := "", false
					if j == 0 && i == 0 && len(c.Args) == 2 {
						if s, ok := c.Args[1].(*parse.StringNode); ok {
							name, literal = s.Text, true
						}
					}
					if !literal {
						bad = append(bad, fmt.Sprintf("line %d", line(c)))
						continue
					}
					refs = append(refs, SecretRef{Name: name, Line: line(c)})
				}
				walk(a)
			}
		}
	}
	walk = func(n parse.Node) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			walkPipe(n.Pipe)
		case *parse.PipeNode:
			walkPipe(n)
		case *parse.ChainNode:
			walk(n.Node)
		case *parse.IfNode:
			walkPipe(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.RangeNode:
			walkPipe(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.WithNode:
			walkPipe(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.TemplateNode:
			walkPipe(n.Pipe)
		}
	}
	for _, tt := range t.Templates() {
		walk(tt.Root)
	}
	if len(bad) > 0 {
		return refs, fmt.Errorf("name the secret in quotes, as in {{ secret \"name\" }}: %s", strings.Join(bad, ", "))
	}
	return refs, nil
}

// Secrets lists the stored secrets a blueprint names: a Windows guest's
// password and domain join, user data's {{ secret "name" }}, and the
// ${secret.name} bindings of AWX steps and their undo.
func (b Blueprint) Secrets() []SecretRef {
	var refs []SecretRef
	for i, c := range b.ComponentList() {
		at := "/guest"
		if b.Multi() {
			at = fmt.Sprintf("/components/%d/guest", i)
		}
		if c.Guest != nil {
			refs = append(refs, guestSecrets(*c.Guest, at)...)
		}
	}
	var steps func(list []CompositeStep, at string)
	steps = func(list []CompositeStep, at string) {
		for i, s := range list {
			ptr := fmt.Sprintf("%s/%d", at, i)
			if s.AWX != nil {
				refs = append(refs, bindingSecrets(s.With, ptr+"/with")...)
			}
			if s.Undo != nil && s.Undo.JobTemplate != "" {
				refs = append(refs, bindingSecrets(s.Undo.With, ptr+"/undo/with")...)
			}
			if s.Branch != nil {
				for j, c := range s.Branch.Cases {
					steps(c.Steps, fmt.Sprintf("%s/branch/cases/%d/steps", ptr, j))
				}
				steps(s.Branch.Else, ptr+"/branch/else")
			}
			steps(s.Parallel, ptr+"/parallel")
		}
	}
	steps(b.Steps, "/steps")
	return dedupeRefs(refs)
}

func guestSecrets(g Guest, at string) []SecretRef {
	var refs []SecretRef
	if g.AdminPassword != "" {
		refs = append(refs, SecretRef{Name: g.AdminPassword, Pointer: at + "/admin_password"})
	}
	if j := g.DomainJoin; j != nil && j.Credential != "" {
		refs = append(refs, SecretRef{Name: j.Credential, Pointer: at + "/domain_join/credential"})
	}
	if ci := g.CloudInit; ci != nil {
		names, _ := UserDataSecrets(ci.UserData)
		for _, r := range names {
			r.Pointer = at + "/cloud_init/user_data"
			refs = append(refs, r)
		}
	}
	return refs
}

func bindingSecrets(with map[string]any, at string) []SecretRef {
	var refs []SecretRef
	for _, k := range sortedKeys(with) {
		if name, ok := BindingSecret(with[k]); ok {
			refs = append(refs, SecretRef{Name: name, Pointer: at + "/" + k})
		}
	}
	return refs
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func dedupeRefs(refs []SecretRef) []SecretRef {
	seen := map[SecretRef]bool{}
	out := refs[:0]
	for _, r := range refs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// MisplacedSecrets are the places of a blueprint, as JSON pointers, that
// hold ${secret.name} where no secret is opened: anywhere but an AWX step's
// binding. Its value would otherwise be worked out with the request, and
// kept in the plan.
func MisplacedSecrets(raw json.RawMessage) []string {
	var doc any
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	allowed := map[string]bool{}
	var b Blueprint
	if json.Unmarshal(raw, &b) == nil {
		for _, r := range b.Secrets() {
			if strings.Contains(r.Pointer, "/with/") {
				allowed[r.Pointer] = true
			}
		}
	}
	var out []string
	var walk func(v any, ptr string)
	walk = func(v any, ptr string) {
		switch x := v.(type) {
		case string:
			if strings.Contains(x, "${secret.") && !allowed[ptr] {
				out = append(out, ptr)
			}
		case map[string]any:
			for _, k := range sortedKeys(x) {
				walk(x[k], ptr+"/"+k)
			}
		case []any:
			for i, e := range x {
				walk(e, fmt.Sprintf("%s/%d", ptr, i))
			}
		}
	}
	walk(doc, "")
	return out
}
