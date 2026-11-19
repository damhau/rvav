package vraimport

import (
	"fmt"
	"regexp"
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
	note func(level, where, text string)) bool {
	m, _ := d.(map[string]any)
	switch {
	case m != nil && m["bind"] != nil:
		expr, why := bindCEL(m["bind"], props)
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
		note(Unmapped, where, fmt.Sprintf("Its default comes from action %v in vRA: give the field an rvav REST value source, "+
			"which gives it its default.", m["id"]))
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

// bindCEL is a bind's CEL: a field's name, or values (fields, and
// constants between backquotes) and an operator. why says what it can't
// map.
func bindCEL(bind any, props map[string]any) (expr, why string) {
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
	case op == "":
		return "", "values without an operator"
	case !isArith && !concat:
		return "", "operator " + strconv.Quote(op)
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		s, isText := v.(string)
		literal := !isText || strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") && len(s) >= 2
		if isText && literal {
			s = s[1 : len(s)-1]
		} else if !isText {
			s = fmt.Sprint(v)
		}
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
				ref = "double(" + ref + ")"
			case concat && typ != "string":
				ref = "string(" + ref + ")"
			}
			part = ref
		}
		parts = append(parts, part)
	}
	if concat {
		return strings.Join(parts, " + "), ""
	}
	return strings.Join(parts, sep), ""
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
	if celIdent.MatchString(name) {
		return "inputs." + name, typ, true
	}
	return "inputs[" + strconv.Quote(name) + "]", typ, true
}
