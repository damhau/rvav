package vraimport

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// udExpr is one ${...} of a cloud-init, and what it reads when rvav
// translates it.
type udExpr struct {
	text string
	r    ref
	ok   bool
}

func (x udExpr) input() bool { return x.ok && x.r.what == "input" && !environmentInput(x.r.name) }

// userData translates cloud-init's vRA expressions into the Go template
// rvav renders, and lists those it can't.
//
// rvav prints an input only through json (blueprint.CheckUserData), which
// quotes it: /etc/${input.app}.conf as /etc/{{ .inputs.app | json }}.conf
// would be /etc/"shop".conf, and in a block scalar the quotes would be
// the text's. So in a #cloud-config document, a YAML scalar holding an
// input becomes one action printing the whole scalar through json:
// {{ printf "/etc/%v.conf" .inputs.app | json }}.
func (s scope) userData(src string) (string, []string) {
	locs := refPattern.FindAllStringIndex(src, -1)
	if len(locs) == 0 {
		return src, nil
	}
	var problems []string
	exprs := make([]udExpr, len(locs))
	for i, l := range locs {
		x := udExpr{text: src[l[0]:l[1]]}
		inner := strings.TrimSpace(x.text[2 : len(x.text)-1])
		toks, err := tokenize(inner)
		switch {
		case err == nil && len(toks) == 1 && toks[0].kind == tokIdent || secretRef.MatchString(inner):
			if x.r, err = s.ref(inner); err != nil {
				problems = append(problems, fmt.Sprintf("cloud-init uses %s: %v. It is left as it was.", x.text, err))
				break
			}
			x.ok = true
			if x.r.what == "propgroup" {
				s.groups.inlined = append(s.groups.inlined, inner)
			}
		default:
			problems = append(problems, "cloud-init uses "+x.text+": rvav's user data prints values, without functions or operators. "+
				"It is left as it was: compute the value in the form, or in the tags.")
		}
		exprs[i] = x
	}

	// Each expression becomes a mark YAML reads as plain text anywhere,
	// even in flow collections, where ${...}'s braces wouldn't be.
	mark := "rvavx"
	for strings.Contains(src, mark) {
		mark += "x"
	}
	var b strings.Builder
	last := 0
	for i, l := range locs {
		fmt.Fprintf(&b, "%s%s%dz", src[last:l[0]], mark, i)
		last = l[1]
	}
	b.WriteString(src[last:])
	text, marks := b.String(), regexp.MustCompile(mark+`(\d+)z`)
	if strings.HasPrefix(strings.TrimLeft(src, " \t\r\n"), "#cloud-config") && slices.ContainsFunc(exprs, udExpr.input) {
		var more []string
		text, more = s.quoteScalars(text, marks, exprs)
		problems = append(problems, more...)
	}
	return marks.ReplaceAllStringFunc(text, func(m string) string {
		x := exprs[markIndex(marks, m)]
		if x.ok {
			return x.r.action()
		}
		return x.text
	}), problems
}

func markIndex(marks *regexp.Regexp, m string) int {
	i, _ := strconv.Atoi(marks.FindStringSubmatch(m)[1])
	return i
}

// quoteScalars makes each YAML scalar of text that holds an input one
// action printing it through json.
func (s scope) quoteScalars(text string, marks *regexp.Regexp, exprs []udExpr) (string, []string) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		return text, []string{fmt.Sprintf("cloud-init isn't YAML rvav can read (%v): its inputs are printed through json, "+
			"quotes included in the middle of a text. Check it.", err)}
	}
	starts := []int{0}
	for i, c := range text {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	type edit struct {
		from, to int
		action   string
	}
	var edits []edit
	var problems []string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		for _, c := range n.Content {
			walk(c)
		}
		var inputs []string
		for _, m := range marks.FindAllString(n.Value, -1) {
			if x := exprs[markIndex(marks, m)]; x.input() {
				inputs = append(inputs, x.text)
			}
		}
		if n.Kind != yaml.ScalarNode || len(inputs) == 0 {
			return
		}
		from := starts[n.Line-1]
		for col := n.Column; col > 1 && from < len(text); col-- {
			_, size := utf8.DecodeRuneInString(text[from:])
			from += size
		}
		to, ok := scalarEnd(text, from, n)
		if !ok {
			problems = append(problems, fmt.Sprintf("cloud-init uses %s in a YAML value rvav can't print as one: "+
				"printed through json, an input keeps its quotes in the middle of a text. "+
				`Write the value as one action, as in {{ printf "/etc/%%v.conf" .inputs.app | json }}.`, strings.Join(inputs, ", ")))
			return
		}
		edits = append(edits, edit{from, to, s.printScalar(n, marks, exprs)})
	}
	walk(&root)
	for _, e := range slices.Backward(edits) {
		text = text[:e.from] + e.action + text[e.to:]
	}
	return text, problems
}

// scalarEnd is where scalar n, which starts at from, ends in text; false
// when rvav can't tell, or what is there doesn't read as n's value. An
// anchor would go with the scalar, and its aliases with it.
func scalarEnd(text string, from int, n *yaml.Node) (int, bool) {
	end := -1
	switch {
	case from >= len(text) || n.Anchor != "":
	case n.Style == 0:
		end = from + len(n.Value)
	case n.Style == yaml.DoubleQuotedStyle && text[from] == '"', n.Style == yaml.SingleQuotedStyle && text[from] == '\'':
		q := text[from]
		for i := from + 1; i < len(text) && end < 0; i++ {
			switch {
			case q == '"' && text[i] == '\\':
				i++
			case q == '\'' && text[i] == '\'' && i+1 < len(text) && text[i+1] == '\'':
				i++
			case text[i] == q:
				end = i + 1
			}
		}
	case n.Style == yaml.LiteralStyle, n.Style == yaml.FoldedStyle:
		end = blockEnd(text, from)
	}
	if end < 0 || end > len(text) {
		return 0, false
	}
	read := text[from:end]
	if (n.Style == yaml.LiteralStyle || n.Style == yaml.FoldedStyle) && end < len(text) {
		// Its last line break is the block's, kept where the scalar was.
		read += "\n"
	}
	var v string
	if err := yaml.Unmarshal([]byte(read), &v); err != nil || v != n.Value {
		return 0, false
	}
	return end, true
}

// blockEnd is where the block scalar whose indicator is at from ends, at
// its last line that isn't blank; -1 with an indentation or a keep
// indicator, which rvav doesn't rewrite.
func blockEnd(text string, from int) int {
	eol := strings.IndexByte(text[from:], '\n')
	if eol < 0 || strings.ContainsAny(strings.Fields(text[from : from+eol])[0], "+0123456789") {
		return -1
	}
	indent, end := -1, -1
	for i := from + eol + 1; i < len(text); {
		j := strings.IndexByte(text[i:], '\n')
		if j < 0 {
			j = len(text) - i
		}
		line := text[i : i+j]
		if body := strings.TrimLeft(line, " "); body != "" {
			n := len(line) - len(body)
			if indent < 0 {
				indent = n
			}
			if n < indent {
				break
			}
			end = i + j
		}
		i += j + 1
	}
	return end
}

// printScalar is the action printing scalar n through json, its
// expressions worked out within it. An input alone in a plain scalar is
// printed as it is, a number as a number.
func (s scope) printScalar(n *yaml.Node, marks *regexp.Regexp, exprs []udExpr) string {
	locs := marks.FindAllStringIndex(n.Value, -1)
	if n.Style == 0 && len(locs) == 1 && locs[0][0] == 0 && locs[0][1] == len(n.Value) {
		return exprs[markIndex(marks, n.Value)].r.action()
	}
	var format strings.Builder
	literal := func(s string) { format.WriteString(strings.ReplaceAll(s, "%", "%%")) }
	var args []string
	last := 0
	for _, l := range locs {
		literal(n.Value[last:l[0]])
		switch x := exprs[markIndex(marks, n.Value[l[0]:l[1]])]; {
		case !x.ok:
			literal(x.text)
		case x.r.what == "propgroup":
			literal(fmt.Sprint(x.r.value))
		default:
			format.WriteString("%v")
			args = append(args, s.operand(x.r))
		}
		last = l[1]
	}
	literal(n.Value[last:])
	return "{{ printf " + strconv.Quote(format.String()) + " " + strings.Join(args, " ") + " | json }}"
}

// operand is a ref as an argument of printf. An input of numbers goes
// through json, which prints 1000000 where %v would print 1e+06.
func (s scope) operand(r ref) string {
	p := r.pipeline()
	if r.what == "input" && s.numbers[r.name] {
		p += " | json"
	}
	if strings.Contains(p, " ") && !strings.HasPrefix(p, "(") {
		return "(" + p + ")"
	}
	return p
}
