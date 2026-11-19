package vraimport

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// formDefault maps a custom form field's default. A constant is the
// field's initial value. A bind, to another field or to an operation on
// fields and constants, is its default rule (CEL), which the requester can
// change, or its value rule on a field read-only in vRA. It reports what it
// can't map: an action's default, a conditional one. It says whether the
// field's value is now worked out.
func formDefault(id string, d any, readOnly bool, props, field, initial map[string]any, where string,
	lib map[string]LibraryImport, note func(level, where, text string)) bool {
	m, _ := d.(map[string]any)
	switch {
	case m != nil && m["bind"] != nil:
		expr, why := bindCEL(m["bind"], props, propType(props, id), readOnly)
		if why != "" {
			note(Unmapped, where, "Its default is worked out in vRA ("+why+"): write an rvav default rule (CEL) for it.")
			return false
		}
		delete(initial, id)
		rules, _ := field["rules"].(map[string]any)
		if rules == nil {
			rules = map[string]any{}
			field["rules"] = rules
		}
		if readOnly {
			rules["value"] = expr
			note(Info, where, "Read-only in vRA's form, its value worked out from other fields: its value rule is "+expr+".")
			return true
		}
		rules["default"] = expr
		note(Info, where, "Its default is worked out from other fields in vRA: its default rule is "+expr+", which the requester can change.")
		return false
	case m != nil && m["type"] == "scriptAction":
		mapped, why := takeLibrary(lib, m, true, props, field, where, note)
		if mapped {
			delete(initial, id)
			return false
		}
		note(Unmapped, where, fmt.Sprintf("Its default comes from action %v in vRA: give the field an rvav REST value source, "+
			"which gives it its default.", m["id"])+why)
		return false
	case conditional(d):
		note(Unmapped, where, "Its default depends on other fields in vRA (a condition): write an rvav default rule (CEL) for it.")
		return false
	}
	initial[id] = d
	return false
}

// conditional tells vRA's conditional value, a list of values each with
// its condition, from a list constant.
func conditional(d any) bool {
	list, ok := d.([]any)
	if !ok || len(list) == 0 {
		return false
	}
	for _, c := range list {
		m, ok := c.(map[string]any)
		if _, has := m["value"]; !ok || !has {
			return false
		}
	}
	return true
}

var celIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// celReserved are the words CEL keeps, which can't follow inputs.
var celReserved = []string{"as", "break", "const", "continue", "else", "false", "for", "function", "if", "import", "in", "let",
	"loop", "package", "namespace", "null", "return", "true", "var", "void", "while"}

func propType(props map[string]any, name string) string {
	p, _ := props[name].(map[string]any)
	t, _ := p["type"].(string)
	return t
}

// bindCEL is a bind's CEL: a field's name, or values (fields, and
// constants between backquotes) and an operator, giving a value of the
// field's type, target. On a computed field, whose rule that fails refuses
// the request, an input with no value counts as empty text, or 0 in a sum,
// as in vRA; another field's default waits for the inputs it reads. why
// says what it can't map.
func bindCEL(bind any, props map[string]any, target string, computed bool) (expr, why string) {
	var values []any
	op := "bind"
	switch b := bind.(type) {
	case string:
		values = []any{b}
	case map[string]any:
		if f, ok := b["field"].(string); ok {
			values = []any{f}
			break
		}
		values, _ = b["values"].([]any)
		op, _ = b["operator"].(string)
	}
	arith := map[string]string{"add": " + ", "subtract": " - ", "multiply": " * ", "divide": " / "}
	sep, isArith := arith[op]
	concat := op == "concatenate" || op == "concat"
	switch {
	case len(values) == 0:
		return "", "a bind without values"
	case (op == "bind" || op == "") && len(values) == 1:
		return bindOne(values[0], props, target, computed)
	case op == "":
		return "", "values without an operator"
	case !isArith && !concat:
		return "", "operator " + strconv.Quote(op)
	}
	guarded := func(ref, val, zero string) string {
		if !computed {
			return val
		}
		return guardedRef(ref, val, zero)
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		s, literal := bindValue(v)
		var part string
		switch {
		case literal && isArith:
			n, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return "", "the constant " + strconv.Quote(s) + " in a sum"
			}
			part = strconv.FormatFloat(n, 'f', -1, 64)
			if !strings.ContainsAny(part, ".eE") {
				part += ".0"
			}
		case literal:
			part = strconv.Quote(s)
		default:
			ref, typ, ok := fieldRef(s, props)
			if !ok {
				return "", "it reads " + s + ", which is no input of the template"
			}
			switch {
			case isArith:
				part = guarded(ref, "double("+ref+")", "0.0")
			case typ != "string":
				part = guarded(ref, "string("+ref+")", `""`)
			default:
				part = guarded(ref, ref, `""`)
			}
		}
		parts = append(parts, part)
	}
	if concat {
		return typed(strings.Join(parts, " + "), "string", target)
	}
	return typed(strings.Join(parts, sep), "number", target)
}

// bindValue is a bind's value as text, and whether it is a constant
// rather than a field's name.
func bindValue(v any) (s string, literal bool) {
	s, isText := v.(string)
	if !isText {
		return fmt.Sprint(v), true
	}
	if strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") && len(s) >= 2 {
		return s[1 : len(s)-1], true
	}
	return s, false
}

// bindOne is the CEL of a bind to one field or constant. On a computed
// text field, a field with no value gives empty text; else the rule waits
// for it: no other type has an empty value.
func bindOne(v any, props map[string]any, target string, computed bool) (expr, why string) {
	s, literal := bindValue(v)
	if literal {
		switch target {
		case "integer", "number":
			n, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return "", "the constant " + strconv.Quote(s) + " in a " + target + " field"
			}
			if target == "integer" {
				return strconv.FormatInt(int64(n), 10), ""
			}
			return typed(strconv.FormatFloat(n, 'f', -1, 64), "integer", "number")
		case "boolean":
			if s != "true" && s != "false" {
				return "", "the constant " + strconv.Quote(s) + " in a boolean field"
			}
			return s, ""
		}
		return typed(strconv.Quote(s), "string", target)
	}
	ref, typ, ok := fieldRef(s, props)
	if !ok {
		return "", "it reads " + s + ", which is no input of the template"
	}
	if computed && (typ == "string" || target == "string" && slices.Contains([]string{"integer", "number", "boolean"}, typ)) {
		val, why := typed(ref, typ, target)
		return guardedRef(ref, val, `""`), why
	}
	return typed(ref, typ, target)
}

// typed converts expr, of type from, to the target field's type. why says
// when it can't: text for a number, a list for text.
func typed(expr, from, target string) (string, string) {
	switch {
	case target == "" || from == target:
		return expr, ""
	case target == "string" && slices.Contains([]string{"integer", "number", "boolean"}, from):
		return "string(" + expr + ")", ""
	case target == "integer" && from == "number":
		return "int(" + expr + ")", ""
	case target == "number" && from == "integer":
		return "double(" + expr + ")", ""
	}
	if from == "" {
		from = "untyped"
	}
	return "", fmt.Sprintf("it gives a value of type %s to a field of type %s", from, target)
}

// guardedRef is val, or zero when the input ref reads has no value: vRA
// leaves it empty, and a rule reading an absent or null input fails.
func guardedRef(ref, val, zero string) string {
	if ref == "environment" {
		return val
	}
	has := "has(" + ref + ")"
	if key, ok := strings.CutPrefix(ref, "inputs["); ok {
		has = strings.TrimSuffix(key, "]") + " in inputs"
	}
	return "(" + has + " && " + ref + " != null ? " + val + " : " + zero + ")"
}

// fieldRef is how a form rule reads a vRA field: an input, or the
// request's environment.
func fieldRef(name string, props map[string]any) (ref, typ string, ok bool) {
	if environmentInput(name) {
		return "environment", "string", true
	}
	prop, ok := props[name].(map[string]any)
	if !ok {
		return "", "", false
	}
	typ, _ = prop["type"].(string)
	if celIdent.MatchString(name) && !slices.Contains(celReserved, name) {
		return "inputs." + name, typ, true
	}
	return "inputs[" + strconv.Quote(name) + "]", typ, true
}
