package vraimport

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/restcall"
	"github.com/damhau/rvav/internal/vra"
)

// LibraryImport is what the import makes of a vRO action a custom form
// takes its choices or a default from (ADR-0043): an entry of rvav's form
// library, a REST value source, when the action's script makes one GET
// the import can read.
type LibraryImport struct {
	// VROAction is the action as forms name it: module/name.
	VROAction string `json:"vro_action"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	// Action is create, unchanged, differs (rvav has another entry of the
	// key, which the import leaves as it is) or skip (no entry: Notes say
	// why).
	Action string `json:"action"`
	// Version is the one the items name: the one the import makes, or
	// rvav's latest.
	Version int `json:"version"`
	// URL is the entry's, its parameters as ${params.x}.
	URL        string   `json:"url,omitempty"`
	Parameters []string `json:"parameters"`
	Notes      []Note   `json:"notes"`
	// Entry is the entry the import makes, as rvav's API writes it.
	Entry map[string]any `json:"-"`
	// single: it gives one value, a field's default, rather than choices.
	single bool
	// inputs are the action's inputs, in its order.
	inputs []string
}

// RvavEntry is the latest version of an entry of rvav's form library.
type RvavEntry struct {
	Version    int
	Use, Kind  string
	Params     map[string]any
	Parameters []string
	// Description says, of an entry an import made, the vRO action it was
	// made of.
	Description string
}

// madeOf is the vRO action an import made the entry of, as its
// description says.
func (r RvavEntry) madeOf() (fqn string, ok bool) {
	rest, ok := strings.CutPrefix(r.Description, "vRO's action ")
	if !ok {
		return "", false
	}
	fqn, _, ok = strings.Cut(rest, ",")
	return fqn, ok && fqn != ""
}

var (
	urlLiteral     = regexp.MustCompile(`["'](https?://[^"'\s]*)["']`)
	restHostByName = regexp.MustCompile(`RESTHostManager\.getHost\(\s*["']([^"']+)["']\s*\)`)
	createRequest  = regexp.MustCompile(`createRequest\(\s*["']([A-Za-z]+)["']\s*,\s*`)
	restCall       = regexp.MustCompile(`\.execute\(|executeRequest\(|createRequest\(`)
	textLiteral    = regexp.MustCompile(`^(?:"([^"]*)"|'([^']*)')`)
	concatPart     = regexp.MustCompile(`^\s*\+\s*(?:"([^"]*)"|'([^']*)'|([A-Za-z_$][A-Za-z0-9_$]*))`)
	authHeader     = regexp.MustCompile(`(?i)setHeader\(\s*["']authorization["']`)
)

// concatenation reads a string expression of literals and the action's
// inputs, which become ${params.x}; why says what it can't read. It must
// be a call's argument: an expression kept in a variable may be added to.
func concatenation(s string, inputs []string) (tpl string, used []string, why string) {
	m := textLiteral.FindStringSubmatch(s)
	if m == nil {
		return "", nil, "its URL starts with no text the import can read"
	}
	tpl, s = m[1]+m[2], s[len(m[0]):]
	for {
		m := concatPart.FindStringSubmatch(s)
		if m == nil {
			if rest := strings.TrimSpace(s); rest == "" || rest[0] != ')' && rest[0] != ',' {
				return "", nil, "its URL is built in steps the import can't follow"
			}
			return tpl, used, ""
		}
		s = s[len(m[0]):]
		if m[3] == "" {
			tpl += m[1] + m[2]
			continue
		}
		if !slices.Contains(inputs, m[3]) {
			return "", nil, "its URL reads " + m[3] + ", which is no input of the action"
		}
		if !groupPropName.MatchString(m[3]) {
			return "", nil, "its URL reads input " + m[3] + ", whose name can't be a parameter's (letters, digits and underscores)"
		}
		tpl += "${params." + m[3] + "}"
		if !slices.Contains(used, m[3]) {
			used = append(used, m[3])
		}
	}
}

// mapAction makes a form library entry of a vRO action, or says why not.
func mapAction(a vra.VROAction) LibraryImport {
	li := LibraryImport{VROAction: a.FQN(), Name: clip(humanize(a.Name), 100), Parameters: []string{}, Notes: []Note{}}
	for _, in := range a.Inputs {
		li.inputs = append(li.inputs, in.Name)
	}
	skip := func(text string) LibraryImport {
		li.Action = ActionSkip
		li.Notes = append(li.Notes, Note{Unmapped, "script", text})
		return li
	}
	params := map[string]any{}
	switch a.OutputType {
	case "Array/string", "Array/number":
		params["items"], params["value"] = "$", "$"
	case "Array/Properties":
		params["items"], params["value"], params["label"] = "$", "$.value", "$.label"
	case "string", "number", "boolean":
		params["value"], li.single = "$", true
	default:
		return skip(fmt.Sprintf("It returns %s: an rvav source gives text, numbers or true or false, or a list of them.", a.OutputType))
	}
	if !restCall.MatchString(a.Script) {
		return skip("It works its values out in JavaScript, which rvav doesn't run: write a CEL source, or have a REST service give them.")
	}
	if len(createRequest.FindAllString(a.Script, -1)) > 1 {
		return skip("It makes several REST calls: rvav's REST source makes one.")
	}
	method, path, pathWhy := "GET", "", ""
	var used []string
	if m := createRequest.FindStringSubmatchIndex(a.Script); m != nil {
		method = strings.ToUpper(a.Script[m[2]:m[3]])
		path, used, pathWhy = concatenation(a.Script[m[1]:], li.inputs)
		if h := restHostByName.FindStringSubmatch(a.Script); h != nil {
			if pathWhy != "" {
				path = "of its request (" + pathWhy + ")"
			}
			return skip(fmt.Sprintf("It calls vRO's REST host %s, whose URL the import doesn't know: add an entry under "+
				"Administration › Form library with that host's URL and the path %s.", h[1], path))
		}
	}
	if method != "GET" {
		return skip(fmt.Sprintf("It sends a %s request: write the entry by hand, its body as JSON reading ${params.x}.", method))
	}
	if pathWhy != "" {
		return skip("It calls a REST service, but " + pathWhy + ": add the entry by hand.")
	}
	tpl := path
	if !urlLiteral.MatchString(`"` + path + `"`) {
		found := urlLiteral.FindAllStringSubmatch(a.Script, -1)
		switch {
		case len(found) == 0:
			return skip("It calls a REST service whose URL the import can't read: add the entry by hand.")
		case len(found) > 1:
			return skip("It names several URLs: rvav's REST source makes one call, to a URL the import can't choose.")
		case path != "":
			// A host's URL, then the request's path.
			tpl = strings.TrimRight(found[0][1], "/") + "/" + strings.TrimLeft(path, "/")
		default:
			var why string
			if tpl, used, why = concatenation(a.Script[strings.Index(a.Script, found[0][0]):], li.inputs); why != "" {
				return skip("It calls a REST service, but " + why + ": add the entry by hand.")
			}
		}
	}
	if !restcall.FixedOrigin(tpl) {
		return skip("Its URL's host comes from an input: rvav fills only a URL's path and query.")
	}
	params["url"] = tpl
	li.URL = tpl
	parameters := []map[string]any{}
	for _, name := range used {
		li.Parameters = append(li.Parameters, name)
		parameters = append(parameters, map[string]any{"name": name, "label": clip(humanize(name), 100), "required": true})
	}
	if unused := slices.DeleteFunc(slices.Clone(li.inputs), func(n string) bool { return slices.Contains(used, n) }); len(unused) > 0 {
		li.Notes = append(li.Notes, Note{Info, "inputs", "Its URL doesn't read " + strings.Join(unused, ", ") + ": the entry has no such parameter."})
	}
	if authHeader.MatchString(a.Script) {
		li.Notes = append(li.Notes, Note{Warning, "script", "It sends an Authorization header: store the credential as a secret in rvav and name it in the entry."})
	}
	li.Notes = append(li.Notes, Note{Warning, "script", "Made from its script: check the entry's JSONPaths against what the service answers, " +
		"with Test under Administration › Form library."})
	version := a.Version
	if version != "" {
		version = ", version " + version
	}
	li.Entry = map[string]any{"name": li.Name, "description": clip("vRO's action "+a.FQN()+version+", imported from vRA.", 500),
		"use": "source", "kind": "rest", "params": params, "parameters": parameters, "search": false}
	return li
}

// formActions are the vRO actions custom forms take choices or defaults
// from, by module/name.
func formActions(e *vra.Estate) []string {
	seen := map[string]bool{}
	for _, f := range e.Forms {
		var form struct {
			Schema map[string]struct {
				ValueList json.RawMessage `json:"valueList"`
				Default   json.RawMessage `json:"default"`
			} `json:"schema"`
		}
		if json.Unmarshal([]byte(f.Form), &form) != nil {
			continue
		}
		for _, field := range form.Schema {
			for _, raw := range []json.RawMessage{field.ValueList, field.Default} {
				var src struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				}
				if json.Unmarshal(raw, &src) == nil && src.Type == "scriptAction" && src.ID != "" {
					seen[src.ID] = true
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// LibraryKeys are the entry keys of the actions forms use, by module/name:
// that of the entry an earlier import made of the action, else the
// action's name, numbered when another action has it. So a new action of
// the same name in vRA takes no key an earlier import gave.
func LibraryKeys(e *vra.Estate, r Rvav) map[string]string {
	out, taken := map[string]string{}, map[string]bool{}
	actions := formActions(e)
	for _, key := range slices.Sorted(maps.Keys(r.FormLibrary)) {
		fqn, ok := r.FormLibrary[key].madeOf()
		if !ok {
			continue
		}
		taken[key] = true
		if _, has := out[fqn]; !has && slices.Contains(actions, fqn) {
			out[fqn] = key
		}
	}
	for _, fqn := range actions {
		if _, has := out[fqn]; has {
			continue
		}
		base := GroupKey(fqn[strings.LastIndex(fqn, "/")+1:])
		key := base
		for n := 2; taken[key]; n++ {
			suffix := fmt.Sprintf("_%d", n)
			key = base[:min(len(base), 63-len(suffix))] + suffix
		}
		taken[key], out[fqn] = true, key
	}
	return out
}

// PlanFormLibrary is what the import makes of the vRO actions forms use: an
// entry is made when rvav has none of its key, and never changed.
func PlanFormLibrary(e *vra.Estate, r Rvav) []LibraryImport {
	keys := LibraryKeys(e, r)
	out := []LibraryImport{}
	for _, fqn := range formActions(e) {
		i := slices.IndexFunc(e.Actions, func(a vra.VROAction) bool { return a.FQN() == fqn })
		var li LibraryImport
		if i < 0 {
			li = LibraryImport{VROAction: fqn, Name: fqn, Action: ActionSkip, Parameters: []string{},
				Notes: []Note{{Unmapped, "", "vRO has no such action in what the import read."}}}
		} else {
			li = mapAction(e.Actions[i])
		}
		li.Key = keys[fqn]
		if li.Action != ActionSkip {
			planEntry(&li, r)
		}
		out = append(out, li)
	}
	slices.SortFunc(out, func(a, b LibraryImport) int { return strings.Compare(a.Key, b.Key) })
	return out
}

func planEntry(li *LibraryImport, r Rvav) {
	if r.RESTAllowed != nil {
		target := regexp.MustCompile(`\$\{params\.[^}]*\}`).ReplaceAllString(li.URL, "x")
		if u, err := url.Parse(target); err != nil || !r.RESTAllowed(target) {
			host := target
			if err == nil {
				host = u.Host
			}
			li.Action = ActionSkip
			li.Notes = append(li.Notes, Note{Unmapped, "script", fmt.Sprintf("rvav may not call %s: add it under Administration › "+
				"Integrations › Allowed hosts, then plan the import again.", host)})
			return
		}
	}
	have, exists := r.FormLibrary[li.Key]
	switch {
	case !exists:
		li.Action, li.Version = ActionCreate, 1
	case sameEntry(have, li.Entry):
		li.Action, li.Version = ActionUnchanged, have.Version
	default:
		li.Action, li.Version = ActionDiffers, have.Version
		left := "the import leaves it as it is, and gives no field a value source of it: check the entry, then name it " +
			"in the value source of the fields that take their choices from the action."
		ours, theirs := slices.Sorted(slices.Values(li.Parameters)), slices.Sorted(slices.Values(have.Parameters))
		fqn, _ := have.madeOf()
		switch {
		case fqn != li.VROAction:
			li.Notes = append(li.Notes, Note{Unmapped, "", fmt.Sprintf("rvav has a form library entry %s, not made of this action, "+
				"that isn't what it gives: %s", li.Key, left)})
		case have.Use != "source":
			li.Notes = append(li.Notes, Note{Unmapped, "", fmt.Sprintf("rvav's form library entry %s, made of this action, is now a %s: %s",
				li.Key, have.Use, left)})
		case !slices.Equal(ours, theirs):
			li.Notes = append(li.Notes, Note{Unmapped, "", fmt.Sprintf("rvav's form library entry %s, made of this action, takes "+
				"parameters %s, not the action's %s: %s", li.Key, orNone(theirs), orNone(ours), left)})
		default:
			li.Notes = append(li.Notes, Note{Warning, "", fmt.Sprintf("rvav has a form library entry %s, made of this action, that "+
				"isn't what the action gives now: the import leaves it as it is, and items name its version %d.", li.Key, have.Version)})
		}
	}
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// sameEntry compares an entry with rvav's as rvav stores it, JSON.
func sameEntry(have RvavEntry, w map[string]any) bool {
	var params map[string]any
	b, _ := json.Marshal(w["params"])
	_ = json.Unmarshal(b, &params)
	var names []string
	for _, p := range w["parameters"].([]map[string]any) {
		names = append(names, p["name"].(string))
	}
	return have.Use == "source" && have.Kind == "rest" && reflect.DeepEqual(have.Params, params) &&
		slices.Equal(have.Parameters, names)
}

// librarySource is a field's source naming an entry, its parameters bound
// to what vRA's form gives the action: fields, or constants. It says which
// fields it reads, and the parameters it couldn't bind.
func librarySource(li LibraryImport, raw any, props map[string]any) (src map[string]any, reads, unbound []string) {
	params := map[string]any{}
	list, _ := raw.([]any)
	for i, p := range list {
		m, _ := p.(map[string]any)
		name, _ := m["name"].(string)
		value, kind := m["value"], m["$type"]
		if name == "" && len(m) == 1 {
			for k, v := range m {
				name, value, kind = strings.TrimPrefix(k, "$"), v, "field"
			}
		}
		if name == "" && i < len(li.inputs) {
			name = li.inputs[i]
		}
		if !slices.Contains(li.Parameters, name) {
			continue
		}
		s, isText := value.(string)
		switch {
		case kind == "constant" && isText:
			params[name] = strconv.Quote(s)
		case kind == "constant":
			switch value.(type) {
			case nil, bool, float64, int, json.Number:
				b, _ := json.Marshal(value)
				params[name] = string(b)
			}
		case isText:
			ref, _, ok := fieldRef(s, props)
			if !ok {
				continue
			}
			params[name] = ref
			if ref == "environment" {
				reads = append(reads, "$environment")
			} else {
				reads = append(reads, s)
			}
		}
	}
	for _, name := range li.Parameters {
		if _, ok := params[name]; !ok {
			unbound = append(unbound, name)
		}
	}
	return map[string]any{"kind": "library", "name": li.Key, "version": li.Version, "params": params}, reads, unbound
}

// oneValue are the widgets a source gives a default rather than choices.
var oneValue = []string{"text", "textarea", "number", "slider", "date", "datetime", "toggle"}

// takeLibrary gives a field the entry made of the action vRA's form takes
// its choices (or, single, its default) from. When it can't, why is what
// the field's note adds: why the import made no entry, or can't use it.
func takeLibrary(lib map[string]LibraryImport, src map[string]any, single bool, props, field map[string]any, where string,
	note func(level, where, text string)) (ok bool, why string) {
	id, _ := src["id"].(string)
	li, found := lib[id]
	if !found || src["type"] != "scriptAction" {
		return false, ""
	}
	for _, n := range li.Notes {
		if n.Level == Unmapped {
			return false, " The import makes no form library entry of it: " + strings.ToLower(n.Text[:1]) + n.Text[1:]
		}
	}
	if li.Action == ActionSkip {
		return false, ""
	}
	widget, _ := field["widget"].(string)
	takesOne := slices.Contains(oneValue, widget)
	switch {
	case li.single != single:
		kind := map[bool]string{true: "one value", false: "a list"}[li.single]
		return false, fmt.Sprintf(" Form library entry %s, made of it, gives %s.", li.Key, kind)
	case single && !takesOne:
		return false, fmt.Sprintf(" Form library entry %s, made of it, can't give a %s field its default.", li.Key, widget)
	case !single && takesOne && widget != "text":
		return false, fmt.Sprintf(" Form library entry %s, made of it, can't give a %s field its choices.", li.Key, widget)
	}
	vs, reads, unbound := librarySource(li, src["parameters"], props)
	if len(unbound) > 0 {
		return false, fmt.Sprintf(" The import made form library entry %s of it, but vRA's form gives the action nothing for %s: "+
			"name the entry in the field's value source, with an expression for each parameter.", li.Key, strings.Join(unbound, ", "))
	}
	if !single && widget == "text" {
		field["widget"] = "select"
	}
	field["value_source"] = vs
	if len(reads) > 0 {
		field["depends_on"] = reads
	}
	what := "Its choices come"
	if single {
		what = "Its default, which the requester can change, comes"
	}
	note(Info, where, fmt.Sprintf("%s from action %s in vRA: from form library entry %s (version %d), made of it.",
		what, id, li.Key, li.Version))
	return true, ""
}

// libraryByAction is the plan's entries, by the action they are made of.
func libraryByAction(list []LibraryImport) map[string]LibraryImport {
	out := map[string]LibraryImport{}
	for _, li := range list {
		out[li.VROAction] = li
	}
	return out
}
