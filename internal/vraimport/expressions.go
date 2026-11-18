package vraimport

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// vRA's expressions, ${...}, as rvav's (ADR-0024): CEL in bindings (the
// tags, the count), a Go template action in cloud-init. What another
// machine built, ${resource.db.address}, is components.db; count.index is
// self.index; vRA's string functions are CEL's.

// machineRef is a template's machine as a component: its key, and whether
// it has a count (vRA then gives a list for ${resource.x.address}).
type machineRef struct {
	key  string
	many bool
}

// scope is where an expression is translated: the template's machines by
// resource name, and the machine whose property holds it. environment says
// rvav's environment can be read there, as constraints can (ADR-0035).
type scope struct {
	machines    map[string]machineRef
	self        string
	environment bool
}

// What an expression gives, when rvav can tell.
type valueKind int

const (
	kindOther valueKind = iota
	kindString
	kindList
)

// celFuncs are vRA's string functions as CEL's: the first argument is the
// receiver, to_lower(input.x) is inputs.x.lowerAscii().
var celFuncs = map[string]string{"to_lower": "lowerAscii", "to_upper": "upperAscii", "replace": "replace",
	"join": "join", "split": "split", "substring": "substring"}

// A machine's property another reads: vRA's name for it, rvav's for the
// list and for one VM.
var props = map[string][2]string{"address": {"addresses", "address"}, "networks[0].address": {"addresses", "address"},
	"resourceName": {"names", "name"}, "name": {"names", "name"}}

var refPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

var (
	resourcePath = regexp.MustCompile(`^resource\.([A-Za-z0-9_]+)(?:\[(\d+|\*|count\.index)\])?\.(.+)$`)
	inputName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ref is what a path such as resource.db[0].address names.
type ref struct {
	// One of input, self, index, resource.
	what string
	// The input's name, or self's or the resource's property as rvav names
	// it for one VM (name, address, count), and for the list (names,
	// addresses).
	name, plural string
	// The component read, and which of its VMs: "" for the one VM or all,
	// a number, or count.index.
	component, index string
	list             bool
	ownComponent     bool
}

func (s scope) ref(path string) (ref, error) {
	switch {
	case path == "count.index":
		return ref{what: "index"}, nil
	case path == "self.address":
		return ref{what: "self", name: "address"}, nil
	case path == "self.resourceName" || path == "self.name":
		return ref{what: "self", name: "name"}, nil
	case strings.HasPrefix(path, "input."):
		name := strings.TrimPrefix(path, "input.")
		if !inputName.MatchString(name) {
			return ref{}, fmt.Errorf("%s has no rvav equivalent", path)
		}
		return ref{what: "input", name: name}, nil
	case strings.HasPrefix(path, "secret."):
		name := SecretName(strings.TrimPrefix(path, "secret."))
		if name == "" {
			return ref{}, fmt.Errorf("%s names no secret", path)
		}
		return ref{what: "secret", name: name}, nil
	}
	m := resourcePath.FindStringSubmatch(path)
	if m == nil {
		return ref{}, fmt.Errorf("%s has no rvav equivalent", path)
	}
	mr, ok := s.machines[m[1]]
	if !ok {
		return ref{}, fmt.Errorf("%s reads %s, which isn't a machine rvav builds", path, m[1])
	}
	r := ref{what: "resource", component: mr.key, index: m[2], ownComponent: m[1] == s.self}
	if m[3] == "count" && m[2] == "" {
		r.name = "count"
		return r, nil
	}
	p, ok := props[m[3]]
	if !ok {
		return ref{}, fmt.Errorf("%s: rvav gives a machine's names, addresses and count, not its %s", path, m[3])
	}
	r.list = r.index == "*" || r.index == "" && mr.many
	if r.index == "*" {
		r.index = ""
	}
	r.name, r.plural = p[1], p[0]
	return r, nil
}

// cel is a ref in CEL.
func (r ref) cel() (string, valueKind) {
	switch r.what {
	case "index":
		return "self.index", kindOther
	case "self":
		return "self." + r.name, kindString
	case "input":
		return "inputs." + r.name, kindOther
	}
	c := "components." + r.component
	switch {
	case r.name == "count":
		return c + ".count", kindOther
	case r.list:
		return c + "." + r.plural, kindList
	case r.index == "":
		return c + "." + r.plural + "[0]", kindString
	case r.index == "count.index":
		return c + ".vms[self.index]." + r.name, kindString
	}
	return c + ".vms[" + r.index + "]." + r.name, kindString
}

// action is a ref as a Go template action, for cloud-init.
func (r ref) action() string {
	switch r.what {
	case "index":
		return "{{ .vm.index }}"
	case "self":
		if r.name == "address" {
			return "{{ .vm.ip }}"
		}
		return "{{ .vm.name }}"
	case "input":
		if environmentInput(r.name) {
			return "{{ .environment }}"
		}
		return "{{ .inputs." + r.name + " | json }}"
	case "secret":
		// Printed as it is, as vRA does.
		return `{{ secret "` + r.name + `" }}`
	}
	c := ".components." + r.component
	switch {
	case r.name == "count":
		return "{{ " + c + ".count }}"
	case r.list:
		return "{{ " + c + "." + r.plural + " | json }}"
	case r.index == "":
		return "{{ index " + c + "." + r.plural + " 0 }}"
	case r.index == "count.index":
		return "{{ (index " + c + ".vms .vm.index)." + r.name + " }}"
	}
	return "{{ (index " + c + ".vms " + r.index + ")." + r.name + " }}"
}

// translated is an expression in CEL, with what it reads.
type translated struct {
	cel        string
	kind       valueKind
	self       bool
	components []string
	own        bool
}

// cel translates the inside of ${...} to CEL.
func (s scope) cel(src string) (translated, error) {
	toks, err := tokenize(src)
	if err != nil {
		return translated{}, err
	}
	var t translated
	t.cel, t.kind, err = s.celOf(toks, &t)
	t.cel = strings.TrimSpace(t.cel)
	return t, err
}

func (s scope) celOf(toks []token, t *translated) (string, valueKind, error) {
	var b strings.Builder
	kind, parts := kindOther, 0
	for i := 0; i < len(toks); i++ {
		tk := toks[i]
		parts++
		switch {
		case tk.kind == tokIdent && i+1 < len(toks) && toks[i+1].text == "(":
			method, ok := celFuncs[tk.text]
			if !ok {
				return "", 0, fmt.Errorf("%s() has no rvav equivalent", tk.text)
			}
			end := closing(toks, i+1)
			if end < 0 {
				return "", 0, fmt.Errorf("%s( isn't closed", tk.text)
			}
			args := splitArgs(toks[i+2 : end])
			if len(args) == 0 || slices.ContainsFunc(args, func(a []token) bool { return len(a) == 0 }) {
				return "", 0, fmt.Errorf("%s() lacks an argument", tk.text)
			}
			recv, _, err := s.celOf(args[0], t)
			if err != nil {
				return "", 0, err
			}
			if recv = strings.TrimSpace(recv); !simple(args[0]) {
				recv = "(" + recv + ")"
			}
			rest := make([]string, 0, len(args)-1)
			for _, a := range args[1:] {
				x, _, err := s.celOf(a, t)
				if err != nil {
					return "", 0, err
				}
				rest = append(rest, strings.TrimSpace(x))
			}
			b.WriteString(tk.pre + recv + "." + method + "(" + strings.Join(rest, ", ") + ")")
			kind = kindString
			if method == "split" {
				kind = kindList
			}
			i = end
		case tk.kind == tokIdent && (tk.text == "true" || tk.text == "false" || tk.text == "null"):
			b.WriteString(tk.pre + tk.text)
			kind = kindOther
		case tk.kind == tokIdent:
			r, err := s.ref(tk.text)
			if err != nil {
				return "", 0, err
			}
			switch r.what {
			case "secret":
				return "", 0, fmt.Errorf("%s: rvav opens stored secrets in cloud-init, never in a value worked out with the request", tk.text)
			case "index", "self":
				t.self = true
			case "input":
				if environmentInput(r.name) && s.environment {
					b.WriteString(tk.pre + "environment")
					kind = kindString
					continue
				}
				if environmentInput(r.name) {
					return "", 0, fmt.Errorf("input.%s is the environment, which rvav's bindings don't read", r.name)
				}
			case "resource":
				t.components = append(t.components, r.component)
				t.own = t.own || r.ownComponent
				if r.index == "count.index" {
					t.self = true
				}
			}
			var x string
			x, kind = r.cel()
			b.WriteString(tk.pre + x)
		default:
			b.WriteString(tk.pre + tk.text)
			kind = kindOther
			if tk.kind == tokString {
				kind = kindString
			}
		}
	}
	if parts != 1 {
		kind = kindOther
	}
	return b.String(), kind, nil
}

// simple says the tokens are one ident, literal or call: no parentheses
// needed around them as a receiver.
func simple(toks []token) bool {
	if len(toks) == 1 {
		return true
	}
	return toks[0].kind == tokIdent && toks[1].text == "(" && closing(toks, 1) == len(toks)-1
}

// closing is the index of the parenthesis closing the one at open, or -1.
func closing(toks []token, open int) int {
	depth := 0
	for i := open; i < len(toks); i++ {
		switch toks[i].text {
		case "(", "[":
			depth++
		case ")", "]":
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitArgs splits a call's arguments at its top-level commas.
func splitArgs(toks []token) [][]token {
	if len(toks) == 0 {
		return nil
	}
	var out [][]token
	depth, start := 0, 0
	for i, tk := range toks {
		switch tk.text {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		case ",":
			if depth == 0 {
				out = append(out, toks[start:i])
				start = i + 1
			}
		}
	}
	return append(out, toks[start:])
}

const (
	tokIdent = iota
	tokString
	tokNumber
	tokPunct
)

type token struct {
	pre, text string
	kind      int
}

var twoChar = []string{"==", "!=", "<=", ">=", "&&", "||"}

// tokenize splits an expression into paths (resource.db[0].address),
// literals and operators, each with the space before it.
func tokenize(src string) ([]token, error) {
	var out []token
	rs := []rune(src)
	for i := 0; i < len(rs); {
		j := i
		for j < len(rs) && unicode.IsSpace(rs[j]) {
			j++
		}
		pre := string(rs[i:j])
		if j == len(rs) {
			break
		}
		i = j
		r := rs[i]
		switch {
		case unicode.IsLetter(r) || r == '_':
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || strings.ContainsRune("_.", rs[j]) ||
				rs[j] == '[' && pathIndex(rs[j:]) > 0) {
				if rs[j] == '[' {
					j += pathIndex(rs[j:])
				} else {
					j++
				}
			}
			out = append(out, token{pre, string(rs[i:j]), tokIdent})
		case r == '\'' || r == '"':
			j++
			for j < len(rs) && rs[j] != r {
				if rs[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(rs) {
				return nil, fmt.Errorf("a string isn't closed: %s", src)
			}
			j++
			out = append(out, token{pre, string(rs[i:j]), tokString})
		case unicode.IsDigit(r):
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.') {
				j++
			}
			out = append(out, token{pre, string(rs[i:j]), tokNumber})
		default:
			text := string(r)
			if i+1 < len(rs) {
				for _, op := range twoChar {
					if string(rs[i:i+2]) == op {
						text = op
					}
				}
			}
			j = i + len([]rune(text))
			out = append(out, token{pre, text, tokPunct})
		}
		i = j
	}
	return out, nil
}

// pathIndex is the length of an index within a path, [0], [*] or
// [count.index], or 0.
func pathIndex(rs []rune) int {
	for _, ix := range []string{"[*]", "[count.index]"} {
		if strings.HasPrefix(string(rs), ix) {
			return len(ix)
		}
	}
	j := 1
	for j < len(rs) && unicode.IsDigit(rs[j]) {
		j++
	}
	if j > 1 && j < len(rs) && rs[j] == ']' {
		return j + 1
	}
	return 0
}

// celText is a text with ${...} in it as a CEL string: its literal parts
// quoted, each expression made a string.
func (s scope) celText(text string) (translated, error) {
	var all translated
	var parts []string
	last := 0
	for _, m := range refPattern.FindAllStringSubmatchIndex(text, -1) {
		if lit := text[last:m[0]]; lit != "" {
			parts = append(parts, quote(lit))
		}
		t, err := s.cel(text[m[2]:m[3]])
		if err != nil {
			return translated{}, err
		}
		if t.kind == kindList {
			return translated{}, fmt.Errorf("%s gives a list, not a text", text[m[0]:m[1]])
		}
		x := t.cel
		if t.kind != kindString {
			x = "string(" + x + ")"
		}
		parts = append(parts, x)
		all.self = all.self || t.self
		all.own = all.own || t.own
		all.components = append(all.components, t.components...)
		last = m[1]
	}
	if lit := text[last:]; lit != "" || len(parts) == 0 {
		parts = append(parts, quote(lit))
	}
	all.cel, all.kind = strings.Join(parts, " + "), kindString
	return all, nil
}

// interp is a text with vRA's ${...} as rvav's text with ${CEL}, as
// custom properties and folders take it: worked out when the request is
// made, so from the inputs, never from the machines.
func (s scope) interp(text string) (string, error) {
	var b strings.Builder
	last := 0
	for _, m := range refPattern.FindAllStringSubmatchIndex(text, -1) {
		b.WriteString(text[last:m[0]])
		t, err := s.cel(text[m[2]:m[3]])
		switch {
		case err != nil:
			return "", err
		case t.self || len(t.components) > 0:
			return "", fmt.Errorf("%s reads the machines, which aren't built when rvav works it out", text[m[0]:m[1]])
		case t.kind == kindList:
			return "", fmt.Errorf("%s gives a list, not a text", text[m[0]:m[1]])
		}
		b.WriteString("${" + t.cel + "}")
		last = m[1]
	}
	b.WriteString(text[last:])
	return b.String(), nil
}

// secretRef is ${secret.name}, whose name may hold dashes, which the
// expression tokenizer would take for a minus.
var secretRef = regexp.MustCompile(`^secret\.[A-Za-z0-9_.-]+$`)

// secretsIn are the secrets a text opens with ${secret.name}, as rvav
// names them.
func secretsIn(text string) []string {
	var out []string
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		inner := strings.TrimSpace(m[1])
		if !secretRef.MatchString(inner) {
			continue
		}
		if r, err := (scope{}).ref(inner); err == nil && !slices.Contains(out, r.name) {
			out = append(out, r.name)
		}
	}
	return out
}

// SecretName is a vRA secret's name as rvav's secrets are named: lower
// case, a dash for anything else, starting with a letter; "" for a name
// with nothing to keep.
func SecretName(name string) string {
	s := strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, strings.ToLower(name)), "-")
	switch {
	case s == "":
		return ""
	case s[0] < 'a':
		s = "s-" + s
	case len(s) == 1:
		s += "-secret"
	}
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

func quote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// userData translates cloud-init's vRA expressions into the Go template
// rvav renders, and lists those it can't.
func (s scope) userData(src string) (string, []string) {
	var problems []string
	out := refPattern.ReplaceAllStringFunc(src, func(m string) string {
		inner := strings.TrimSpace(m[2 : len(m)-1])
		toks, err := tokenize(inner)
		if err == nil && len(toks) == 1 && toks[0].kind == tokIdent || secretRef.MatchString(inner) {
			r, err := s.ref(inner)
			if err == nil {
				return r.action()
			}
			problems = append(problems, fmt.Sprintf("cloud-init uses %s: %v. It is left as it was.", m, err))
			return m
		}
		problems = append(problems, "cloud-init uses "+m+": rvav's user data prints values, without functions or operators. "+
			"It is left as it was: compute the value in the form, or in the tags.")
		return m
	})
	return out, problems
}

// tags maps a machine's vRA tags, a key and a value, to vSphere tags as
// category/tag: fixed ones as its tags, those with expressions in its
// tags binding.
func (s scope) tags(raw any, where string, c, bindings map[string]any, note func(level, where, text string)) {
	list, _ := raw.([]any)
	var fixed, exprs []string
	for i, x := range list {
		m, _ := x.(map[string]any)
		k, _ := m["key"].(string)
		v, _ := m["value"].(string)
		at := fmt.Sprintf("%s[%d]", where, i)
		text := k + "/" + v
		switch {
		case k == "" || v == "":
			note(Unmapped, at, "A tag without a key or a value: a vSphere tag has a category and a name. Left out.")
			continue
		case !strings.Contains(text, "${"):
			fixed = append(fixed, text)
			continue
		}
		t, err := s.celText(text)
		switch {
		case err != nil:
			note(Unmapped, at, fmt.Sprintf("Tag %s: %v. Left out.", text, err))
		case t.own:
			note(Unmapped, at, "Tag "+text+" reads its own machines: rvav attaches tags as each VM is cloned, before they all have addresses. Left out.")
		default:
			exprs = append(exprs, t.cel)
		}
	}
	if len(fixed) > 0 {
		c["tags"] = fixed
	}
	if len(exprs) > 0 {
		bindings["tags"] = "[" + strings.Join(exprs, ", ") + "]"
	}
	if len(fixed)+len(exprs) > 0 {
		note(Warning, where, "vRA's tags are vSphere tags, each key a category: the categories must exist in vCenter.")
	}
}

// count is a machine's vRA count as rvav's: a number, or CEL over the
// inputs.
func (s scope) count(v any) (any, error) {
	text, ok := v.(string)
	if !ok {
		return v, nil
	}
	m := refPattern.FindStringSubmatchIndex(text)
	if m == nil || m[0] != 0 || m[1] != len(text) {
		if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
			return n, nil
		}
		return nil, fmt.Errorf("it isn't a number or one expression")
	}
	t, err := s.cel(text[m[2]:m[3]])
	if err != nil {
		return nil, err
	}
	if t.self || len(t.components) > 0 {
		return nil, fmt.Errorf("it reads a machine, and rvav counts VMs before it builds any")
	}
	return t.cel, nil
}
