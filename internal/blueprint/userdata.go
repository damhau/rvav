package blueprint

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"text/template/parse"
)

// User data for cloud-init (ADR-0024) is a Go template. What a requester
// typed must not become YAML of its own: an input is printed only through
// json, which quotes it as a YAML scalar.

// UserDataFuncs are the functions user data may call.
var UserDataFuncs = template.FuncMap{
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	},
}

// ParseUserData parses user data, unknown values an error when run.
func ParseUserData(text string) (*template.Template, error) {
	return template.New("user_data").Funcs(UserDataFuncs).Option("missingkey=error").Parse(text)
}

// CheckUserData parses user data and refuses an input printed without
// json: {{ .inputs.motd | json }}, not {{ .inputs.motd }}. Within range or
// with over an input, and through variables, anything printed counts as
// an input.
func CheckUserData(text string) error {
	t, err := ParseUserData(text)
	if err != nil {
		return err
	}
	var bad []string
	var walk func(n parse.Node, fromInputs bool)
	walk = func(n parse.Node, fromInputs bool) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c, fromInputs)
			}
		case *parse.ActionNode:
			if len(n.Pipe.Decl) == 0 && (fromInputs || usesInputs(n.Pipe)) && !endsWithJSON(n.Pipe) {
				bad = append(bad, n.String())
			}
		case *parse.IfNode:
			walk(n.List, fromInputs)
			walk(n.ElseList, fromInputs)
		case *parse.RangeNode:
			walk(n.List, fromInputs || usesInputs(n.Pipe))
			walk(n.ElseList, fromInputs)
		case *parse.WithNode:
			walk(n.List, fromInputs || usesInputs(n.Pipe))
			walk(n.ElseList, fromInputs)
		}
	}
	walk(t.Root, false)
	if len(bad) > 0 {
		return fmt.Errorf("print inputs through json, as in {{ .inputs.name | json }}: %s", strings.Join(bad, ", "))
	}
	return nil
}

// usesInputs says whether a pipeline reads an input, or a variable (which
// may hold one).
func usesInputs(p *parse.PipeNode) bool {
	if p == nil {
		return false
	}
	for _, c := range p.Cmds {
		for _, a := range c.Args {
			switch a := a.(type) {
			case *parse.FieldNode:
				if a.Ident[0] == "inputs" {
					return true
				}
			case *parse.VariableNode:
				if a.Ident[0] != "$" || len(a.Ident) > 1 && a.Ident[1] == "inputs" {
					return true
				}
			case *parse.PipeNode:
				if usesInputs(a) {
					return true
				}
			case *parse.ChainNode:
				if pn, ok := a.Node.(*parse.PipeNode); ok && usesInputs(pn) {
					return true
				}
			}
		}
	}
	return false
}

func endsWithJSON(p *parse.PipeNode) bool {
	last := p.Cmds[len(p.Cmds)-1]
	id, ok := last.Args[0].(*parse.IdentifierNode)
	return ok && id.Ident == "json"
}
