package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/restcall"
	"github.com/damhau/rvav/internal/store"
)

// CatalogIcons are the icons an item may have; the web app draws the same
// set (components/common/misc.tsx).
var CatalogIcons = []string{"server", "monitor", "database", "key-round", "shield", "globe", "lock", "users", "terminal", "workflow",
	"box", "container", "hard-drive", "network", "cloud", "cpu", "package", "wrench", "mail", "file-text", "archive",
	"git-branch", "layers", "activity"}

// checkCustomSource checks a form's `rest` or `cel` value source's parameters.
func checkCustomSource(src api.ValueSource, ptr string) []api.FieldError {
	return checkSourceParams(src, ptr, "inputs")
}

// checkSourceParams checks a `rest` or `cel` source's parameters, whose
// templates read ns: a form's inputs, or a library entry's params.
func checkSourceParams(src api.ValueSource, ptr, ns string) []api.FieldError {
	params := map[string]any{}
	if src.Params != nil {
		params = *src.Params
	}
	str := func(k string) string { s, _ := params[k].(string); return s }
	var errs []api.FieldError
	switch src.Kind {
	case api.Rest:
		errs = append(errs, checkRESTParams(params, ptr+"/params", ns, "The URL the options come from")...)
		for _, k := range []string{"items", "value", "label", "meta", "parent"} {
			errs = append(errs, checkJSONPath(str(k), ptr+"/params/"+k)...)
		}
	case api.Cel:
		options, value := str("options"), str("value")
		if options == "" && value == "" {
			errs = append(errs, fieldErr(ptr+"/params", "required", "Give an `options` or a `value` expression."))
		}
		for k, expr := range map[string]string{"options": options, "value": value} {
			if expr == "" {
				continue
			}
			if err := checkCELOf(ns)(expr); err != nil {
				errs = append(errs, fieldErr(ptr+"/params/"+k, "invalid", fmt.Sprintf("This expression is not valid CEL: %v", err)))
			}
		}
	}
	if src.Search != nil && *src.Search {
		if reads := strings.Contains(str("url")+str("body"), "${search}"); src.Kind == api.Rest && !reads {
			errs = append(errs, fieldErr(ptr+"/search", "invalid", "The source searches by the typed text: put ${search} in its URL or body."))
		}
		if reads := strings.Contains(str("options"), "search"); src.Kind == api.Cel && !reads {
			errs = append(errs, fieldErr(ptr+"/search", "invalid", "The source searches by the typed text: read search in its options expression."))
		}
	}
	return errs
}

// checkRESTParams checks a REST call's url, method and body, whose
// templates read ns only; ask is what the URL is for, when it's missing.
func checkRESTParams(params map[string]any, ptr, ns, ask string) []api.FieldError {
	str := func(k string) string { s, _ := params[k].(string); return s }
	var errs []api.FieldError
	if str("url") == "" {
		errs = append(errs, fieldErr(ptr+"/url", "required", "Give "+strings.ToLower(ask[:1])+ask[1:]+"."))
	} else if u, err := url.Parse(fill(str("url"), nil, "", restcall.Escape)); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		errs = append(errs, fieldErr(ptr+"/url", "invalid", "Give an http or https URL."))
	} else if !restcall.FixedOrigin(str("url")) {
		errs = append(errs, fieldErr(ptr+"/url", "invalid", "Inputs may fill the URL's path and query, not its scheme, host or port."))
	}
	if m := strings.ToUpper(str("method")); m != "" && m != http.MethodGet && m != http.MethodPost {
		errs = append(errs, fieldErr(ptr+"/method", "invalid", "GET or POST."))
	}
	other := map[string]string{"inputs": "params", "params": "inputs"}[ns]
	for _, k := range []string{"url", "body"} {
		if strings.Contains(str(k), "${"+other+".") {
			msg := "Only a form library entry has parameters: read the form's inputs as ${inputs.x}."
			if ns == "params" {
				msg = "An entry reads what forms give it, its parameters, as ${params.x}: not a form's inputs."
			}
			errs = append(errs, fieldErr(ptr+"/"+k, "invalid", msg))
		}
	}
	return errs
}

func checkJSONPath(path, ptr string) []api.FieldError {
	if path == "" {
		return nil
	}
	if _, err := jp.ParseString(path); err != nil {
		return []api.FieldError{fieldErr(ptr, "invalid", fmt.Sprintf("This is not valid JSONPath: %v", err))}
	}
	return nil
}

// checkCELOf is how a source's expressions are checked: a form's read
// what form rules read, a library entry's its parameters.
func checkCELOf(ns string) func(string) error {
	if ns == "params" {
		return policy.CheckLibrary
	}
	return policy.CheckSource
}

// formVars are the CEL variables of form rules and `cel` value sources.
func formVars(inputs map[string]any, proj store.Project, environment string, p *auth.Principal, v store.ItemVersion) policy.Vars {
	user := map[string]any{"id": p.User.ID, "roles": nonNilStrings(p.Roles), "groups": nonNilStrings(p.Groups),
		"permissions": p.PermissionsIn(proj.ID)}
	vars := policy.Vars{
		"inputs":      inputs,
		"environment": environment,
		"project":     projectVars(proj),
		"user":        user,
		"propgroup":   groupVars(v),
	}
	maps.Copy(vars, v.Target)
	return vars
}

// celValues resolves a `cel` value source: options from a list of strings
// or of {value, label, meta}, or a computed value; eval is how its
// expressions run, a form's or a library entry's.
func celValues(params map[string]any, vars policy.Vars, eval func(string, policy.Vars) (any, error)) (opts *[]api.Option, value any, err error) {
	if expr, _ := params["value"].(string); expr != "" {
		if value, err = eval(expr, vars); err != nil {
			return nil, nil, &BadRequestError{Msg: "The value expression failed: " + err.Error()}
		}
	}
	expr, _ := params["options"].(string)
	if expr == "" {
		return nil, value, nil
	}
	v, err := eval(expr, vars)
	if err != nil {
		return nil, nil, &BadRequestError{Msg: "The options expression failed: " + err.Error()}
	}
	list, ok := v.([]any)
	if !ok {
		return nil, nil, &BadRequestError{Msg: "The options expression must give a list."}
	}
	out := []api.Option{}
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			o := option(fmt.Sprint(m["value"]), fmt.Sprint(cmpOr(m["label"], m["value"])), str(m["meta"]))
			if parent := str(m["parent"]); parent != "" {
				o.Parent = &parent
			}
			out = append(out, o)
		} else {
			out = append(out, option(fmt.Sprint(item), fmt.Sprint(item), ""))
		}
	}
	return &out, value, nil
}

func cmpOr(a, b any) any {
	if a == nil || a == "" {
		return b
	}
	return a
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// RESTSources calls the URLs of `rest` value sources (ADR-0019), as
// restcall does: only allowed hosts, no redirects, 5 seconds and 1 MB at
// most; answers cached.
type RESTSources struct {
	restcall.Caller

	mu    sync.Mutex
	cache map[string]restEntry
}

type restEntry struct {
	opts  []api.Option
	value any
	check []checkMessage
	until time.Time
}

// Forget empties the cache, for example when a secret changes.
func (r *RESTSources) Forget() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.cache)
}

func NewRESTSources(hosts []string) *RESTSources {
	return &RESTSources{Caller: restcall.Caller{Hosts: hosts, Client: restcall.NewClient(5 * time.Second)}}
}

// Allowed says whether a URL's host may be called (package hosts).
func (r *RESTSources) Allowed(ctx context.Context, u *url.URL) bool {
	return r != nil && r.Caller.Allowed(ctx, u)
}

// sourceRef is ${inputs.x}, ${params.x} (a form library entry's
// parameter), or ${search}: the text typed in a search field.
var sourceRef = regexp.MustCompile(`\$\{(?:(inputs|params)\.([a-zA-Z0-9_]+)|search)\}`)

// Refs are what a template's references read, by namespace: a form's
// inputs, or a library entry's parameters (ADR-0043).
type Refs map[string]map[string]any

// inputRefs are the references of a form's own template: its inputs.
func inputRefs(inputs map[string]any) Refs { return Refs{"inputs": inputs} }

// fill puts the references and the searched text into a template, each
// escaped by esc (empty when absent), in one pass: a value can't add a
// reference.
func fill(tpl string, refs Refs, search string, esc func(string) string) string {
	return sourceRef.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := sourceRef.FindStringSubmatch(m)
		v, ok := refValue(sub[1], sub[2], refs, search)
		if !ok {
			return ""
		}
		return esc(v)
	})
}

// fillBody fills a JSON body's template. A reference inside a string is
// text, escaped; one outside is a JSON value, null when absent, so that
// the body stays JSON whatever the input holds.
func fillBody(tpl string, refs Refs, search string) string {
	var b strings.Builder
	inString, escaped, last := false, false, 0
	for _, loc := range sourceRef.FindAllStringSubmatchIndex(tpl, -1) {
		for _, c := range tpl[last:loc[0]] {
			switch {
			case escaped:
				escaped = false
			case c == '\\' && inString:
				escaped = true
			case c == '"':
				inString = !inString
			}
		}
		b.WriteString(tpl[last:loc[0]])
		var ns, name string
		if loc[2] >= 0 {
			ns, name = tpl[loc[2]:loc[3]], tpl[loc[4]:loc[5]]
		}
		v, ok := refRaw(ns, name, refs, search)
		switch {
		case inString && ok:
			b.WriteString(jsonEscape(refText(v)))
		case inString:
		case !ok:
			b.WriteString("null")
		default:
			j, err := json.Marshal(v)
			if err != nil {
				j = []byte("null")
			}
			b.Write(j)
		}
		last = loc[1]
	}
	b.WriteString(tpl[last:])
	return b.String()
}

// refValue is the text a reference of a template stands for: an input or
// a parameter, or with no namespace, the searched text.
func refValue(ns, name string, refs Refs, search string) (string, bool) {
	v, ok := refRaw(ns, name, refs, search)
	if !ok {
		return "", false
	}
	return refText(v), true
}

func refRaw(ns, name string, refs Refs, search string) (any, bool) {
	if ns == "" {
		return search, true
	}
	v, ok := refs[ns][name]
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

// refText writes a value as a template's text: a list's items joined with
// commas, as a multi-select's in a query, and a map as JSON.
func refText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = refText(item)
		}
		return strings.Join(parts, ",")
	case map[string]any:
		b, _ := json.Marshal(x)
		return string(b)
	}
	return fmt.Sprint(v)
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// Options calls the source and maps its answer to options.
func (r *RESTSources) Options(ctx context.Context, params map[string]any, refs Refs, search string,
	secret func(name string) (store.Secret, string, error)) ([]api.Option, int, error) {
	p := func(k string) string { s, _ := params[k].(string); return s }
	e, cacheSeconds, err := r.answer(ctx, params, refs, search, secret, "options", func(data any) (restEntry, error) {
		opts, err := mapOptions(data, cmp.Or(p("items"), "$"), cmp.Or(p("value"), "$"), p("label"), p("meta"), p("parent"))
		return restEntry{opts: opts}, err
	})
	return e.opts, cacheSeconds, err
}

// Value calls the source for one value: what the value path finds in the
// first item the items path finds, or in the answer itself.
func (r *RESTSources) Value(ctx context.Context, params map[string]any, refs Refs,
	secret func(name string) (store.Secret, string, error)) (any, int, error) {
	p := func(k string) string { s, _ := params[k].(string); return s }
	e, cacheSeconds, err := r.answer(ctx, params, refs, "", secret, "value", func(data any) (restEntry, error) {
		v, err := mapValue(data, cmp.Or(p("items"), "$"), cmp.Or(p("value"), "$"))
		return restEntry{value: v}, err
	})
	return e.value, cacheSeconds, err
}

// Check calls a REST validation: what it says of the form, nothing when it
// passes. Its answer isn't cached unless cache_seconds says so.
func (r *RESTSources) Check(ctx context.Context, params map[string]any, refs Refs,
	secret func(name string) (store.Secret, string, error)) ([]checkMessage, error) {
	if _, ok := params["cache_seconds"]; !ok {
		params = maps.Clone(params)
		params["cache_seconds"] = 0.0
	}
	p := func(k string) string { s, _ := params[k].(string); return s }
	e, _, err := r.answer(ctx, params, refs, "", secret, "check", func(data any) (restEntry, error) {
		msgs, err := readCheck(data, p("valid"), cmp.Or(p("message"), "$.message"), cmp.Or(p("errors"), "$.errors"))
		return restEntry{check: msgs}, err
	})
	return e.check, err
}

// answer calls the source, or takes what the cache keeps of its answer for
// the same call read the same way (mode), and reads it with read.
func (r *RESTSources) answer(ctx context.Context, params map[string]any, refs Refs, search string,
	secret func(name string) (store.Secret, string, error), mode string, read func(data any) (restEntry, error)) (restEntry, int, error) {
	p := func(k string) string { s, _ := params[k].(string); return s }
	if !restcall.FixedOrigin(p("url")) {
		return restEntry{}, 0, &BadRequestError{Msg: "The source's URL lets inputs choose its host: inputs may fill its path and query only."}
	}
	for _, m := range sourceRef.FindAllStringSubmatch(p("url"), -1) {
		if v, _ := refValue(m[1], m[2], refs, search); v == "." || v == ".." {
			return restEntry{}, 0, &BadRequestError{Msg: fmt.Sprintf("%s can't be %q in the source's URL.", cmp.Or(m[2], "The search"), v)}
		}
	}
	target := fill(p("url"), refs, search, restcall.Escape)
	u, err := url.Parse(target)
	if err != nil || !r.Allowed(ctx, u) {
		return restEntry{}, 0, &BadRequestError{Msg: fmt.Sprintf("The source's host %q is not one of the hosts REST value sources may call (Administration › Integrations › Allowed hosts).", hostOf(target))}
	}
	method := strings.ToUpper(cmp.Or(p("method"), http.MethodGet))
	body := fillBody(p("body"), refs, search)
	cacheSeconds := 60
	if n, ok := params["cache_seconds"].(float64); ok {
		cacheSeconds = min(max(int(n), 0), 3600)
	}
	call := restcall.Request{Method: method, URL: target}
	if method == http.MethodPost {
		call.Body = []byte(body)
	}
	// The secret opened, a project's or the global one, is in the key: an
	// answer one project's credentials fetched isn't another's.
	sealedAs := ""
	if name := p("secret"); name != "" {
		sec, value, err := secret(name)
		if err != nil {
			return restEntry{}, 0, fmt.Errorf("secret %s: %w", name, err)
		}
		call.Credential = &restcall.Credential{Kind: sec.Kind, Username: sec.Username, Header: sec.Header, Value: value}
		sealedAs = sec.SealName()
	}
	// The mapping is in the key too: two fields may read one answer differently.
	key := strings.Join([]string{mode, method + " " + target, body, sealedAs, p("items"), p("value"), p("label"), p("meta"), p("parent"),
		p("valid"), p("message"), p("errors")}, "\n")
	r.mu.Lock()
	if e, ok := r.cache[key]; ok && time.Now().Before(e.until) {
		r.mu.Unlock()
		return e, cacheSeconds, nil
	}
	r.mu.Unlock()

	system := "REST source " + u.Host
	if mode == "check" {
		system = "REST validation " + u.Host
	}
	answer, err := r.Do(ctx, call)
	switch {
	case err != nil:
		return restEntry{}, 0, &UpstreamError{System: system, Err: err}
	case answer.Status >= 300:
		return restEntry{}, 0, &UpstreamError{System: system, Err: fmt.Errorf("it answered %s", restcall.StatusText(answer.Status))}
	}
	data, err := oj.Parse(answer.Body)
	if err != nil {
		return restEntry{}, 0, &UpstreamError{System: system, Err: fmt.Errorf("its answer is not JSON: %w", err)}
	}
	e, err := read(data)
	if err != nil {
		return restEntry{}, 0, &BadRequestError{Msg: err.Error()}
	}
	if cacheSeconds > 0 {
		r.mu.Lock()
		if r.cache == nil || len(r.cache) > 1000 {
			r.cache = map[string]restEntry{}
		}
		e.until = time.Now().Add(time.Duration(cacheSeconds) * time.Second)
		r.cache[key] = e
		r.mu.Unlock()
	}
	return e, cacheSeconds, nil
}

func hostOf(target string) string {
	if u, err := url.Parse(target); err == nil {
		return u.Host
	}
	return target
}

// mapValue is what valuePath finds in the first item itemsPath finds: a
// text, a number or true or false.
func mapValue(data any, itemsPath, valuePath string) (any, error) {
	items, err := jp.ParseString(itemsPath)
	if err != nil {
		return nil, fmt.Errorf("the items path %q is not valid JSONPath: %v", itemsPath, err)
	}
	value, err := jp.ParseString(valuePath)
	if err != nil {
		return nil, fmt.Errorf("the value path %q is not valid JSONPath: %v", valuePath, err)
	}
	found := items.Get(data)
	if len(found) == 1 {
		if list, ok := found[0].([]any); ok {
			found = list
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("the answer has no item at %s", itemsPath)
	}
	got := value.Get(found[0])
	if len(got) == 0 || got[0] == nil {
		return nil, fmt.Errorf("the answer's first item has no value at %s", valuePath)
	}
	switch got[0].(type) {
	case string, bool, int64, float64:
		return got[0], nil
	}
	return nil, fmt.Errorf("the value at %s is a list or an object, not a text, a number or true or false", valuePath)
}

// mapOptions picks the items with a JSONPath, then each one's value, label,
// meta and parent.
func mapOptions(data any, itemsPath, valuePath, labelPath, metaPath, parentPath string) ([]api.Option, error) {
	paths := map[string]jp.Expr{}
	for name, p := range map[string]string{"items": itemsPath, "value": valuePath, "label": labelPath, "meta": metaPath, "parent": parentPath} {
		if p == "" {
			continue
		}
		x, err := jp.ParseString(p)
		if err != nil {
			return nil, fmt.Errorf("the %s path %q is not valid JSONPath: %v", name, p, err)
		}
		paths[name] = x
	}
	items := paths["items"].Get(data)
	if len(items) == 1 {
		if list, ok := items[0].([]any); ok {
			items = list
		}
	}
	first := func(name string, item any) string {
		x, ok := paths[name]
		if !ok {
			return ""
		}
		if got := x.Get(item); len(got) > 0 && got[0] != nil {
			return fmt.Sprint(got[0])
		}
		return ""
	}
	out := []api.Option{}
	for _, item := range items {
		value := first("value", item)
		if value == "" {
			continue
		}
		o := option(value, cmp.Or(first("label", item), value), first("meta", item))
		if parent := first("parent", item); parent != "" {
			o.Parent = &parent
		}
		out = append(out, o)
	}
	return out, nil
}

// checkSourceRefs checks what a definition's `rest` sources depend on:
// their host is allowed and their secret exists.
func (s *Service) checkSourceRefs(ctx context.Context, d api.CatalogVersionDefinition, prefix string) ([]api.FieldError, error) {
	var errs []api.FieldError
	for _, name := range sortedKeys(d.UiSchema.Fields) {
		src := d.UiSchema.Fields[name].ValueSource
		if src == nil || src.Kind != api.Rest || src.Params == nil {
			continue
		}
		params := *src.Params
		ptr := prefix + "/ui_schema/fields/" + name + "/value_source/params"
		target, _ := params["url"].(string)
		if u, err := url.Parse(fill(target, nil, "", restcall.Escape)); err == nil && !s.REST.Allowed(ctx, u) {
			errs = append(errs, fieldErr(ptr+"/url", "not_allowed", fmt.Sprintf("rvav may not call %s: it is not one of the hosts REST value sources may call (Administration › Integrations › Allowed hosts).", u.Host)))
		}
		if secret, _ := params["secret"].(string); secret != "" {
			if _, err := s.anySecret(ctx, secret); errors.Is(err, store.ErrNotFound) {
				errs = append(errs, fieldErr(ptr+"/secret", "unknown", fmt.Sprintf("There is no secret %q.", secret)))
			} else if err != nil {
				return nil, err
			}
		}
	}
	raw, _ := json.Marshal(d.Blueprint)
	// A composite's REST calls (ADR-0033).
	if b, err := blueprint.Parse(raw); err == nil && b.Kind == blueprint.KindComposite {
		calls := restCalls(b, prefix+"/blueprint")
		for _, at := range sortedKeys(calls) {
			c := calls[at]
			if u, err := url.Parse(restcall.Skeleton(c.URL)); err == nil && u.Host != "" && !s.REST.Allowed(ctx, u) {
				errs = append(errs, fieldErr(at+"/url", "not_allowed", fmt.Sprintf("rvav may not call %s: it is not one of the hosts REST calls may reach (Administration › Integrations › Allowed hosts).", u.Host)))
			}
			if c.Secret != "" {
				if _, err := s.anySecret(ctx, c.Secret); errors.Is(err, store.ErrNotFound) {
					errs = append(errs, fieldErr(at+"/secret", "unknown", fmt.Sprintf("There is no secret %q.", c.Secret)))
				} else if err != nil {
					return nil, err
				}
			}
		}
	}
	// The people a composite's waits name (ADR-0033).
	if b, err := blueprint.Parse(raw); err == nil && b.Kind == blueprint.KindComposite {
		named, err := s.checkAskUsers(ctx, b, prefix+"/blueprint")
		if err != nil {
			return nil, err
		}
		errs = append(errs, named...)
		// And its messages (ADR-0033): whom they name, what they may not show.
		told, err := s.checkNotifyRefs(ctx, b, d.UiSchema, prefix+"/blueprint")
		if err != nil {
			return nil, err
		}
		errs = append(errs, told...)
	}
	// Secrets a blueprint names (ADR-0024, ADR-0038).
	if b, err := blueprint.Parse(raw); err == nil {
		secretErrs, err := s.checkBlueprintSecrets(ctx, b, prefix+"/blueprint")
		if err != nil {
			return nil, err
		}
		errs = append(errs, secretErrs...)
	}
	if b, err := blueprint.Parse(raw); err == nil && b.Kind == blueprint.KindVM {
		nsxErrs, err := s.checkNSXRefs(ctx, b, prefix+"/blueprint")
		if err != nil {
			return nil, err
		}
		errs = append(errs, nsxErrs...)
	}
	groupErrs, err := s.checkGroupRefs(ctx, d, prefix)
	return append(errs, groupErrs...), err
}

// checkBlueprintSecrets checks the stored secrets a blueprint names: each
// exists, and a domain join's is a user name and password.
func (s *Service) checkBlueprintSecrets(ctx context.Context, b blueprint.Blueprint, ptr string) ([]api.FieldError, error) {
	var errs []api.FieldError
	for _, r := range b.Secrets() {
		v, err := s.anySecret(ctx, r.Name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			msg := fmt.Sprintf("There is no secret %q.", r.Name)
			if r.Line > 0 {
				msg = fmt.Sprintf("Line %d: there is no secret %q.", r.Line, r.Name)
			}
			errs = append(errs, fieldErr(ptr+r.Pointer, "unknown", msg))
		case err != nil:
			return nil, err
		case strings.HasSuffix(r.Pointer, "/domain_join/credential") && v.Kind != "basic":
			errs = append(errs, fieldErr(ptr+r.Pointer, "invalid", fmt.Sprintf("Secret %q must be a basic secret: a user name and its password.", r.Name)))
		}
	}
	return errs, nil
}

// checkNSXRefs checks what a VM blueprint asks of NSX against it
// (ADR-0028): an NSX Manager to ask; the existing groups it names, which
// must select VMs by tag; the services of its rules.
func (s *Service) checkNSXRefs(ctx context.Context, b blueprint.Blueprint, ptr string) ([]api.FieldError, error) {
	groups, services := map[string]string{}, map[string]string{} // pointer → ID
	for i, g := range b.Groups {
		groups[fmt.Sprintf("%s/groups/%d", ptr, i)] = g
	}
	for i, c := range b.Components {
		for j, g := range c.Groups {
			groups[fmt.Sprintf("%s/components/%d/groups/%d", ptr, i, j)] = g
		}
	}
	for i, g := range b.SecurityGroups {
		for j, r := range g.Rules {
			at := fmt.Sprintf("%s/security_groups/%d/rules/%d", ptr, i, j)
			for k, f := range r.From {
				groups[fmt.Sprintf("%s/from/%d", at, k)] = f
			}
			for k, sv := range r.Services {
				if sv != "any" {
					services[fmt.Sprintf("%s/services/%d", at, k)] = sv
				}
			}
		}
	}
	for at, g := range groups {
		if id, ok := blueprint.ExistingGroup(g); ok {
			groups[at] = id
		} else {
			delete(groups, at)
		}
	}
	_, binds := b.Bindings["groups"]
	for _, c := range b.Components {
		_, bound := c.Bindings["groups"]
		binds = binds || bound
	}
	if len(b.Segments)+len(b.SecurityGroups)+len(groups) == 0 && !binds {
		return nil, nil
	}
	if !s.Config.NSX.Configured() {
		return []api.FieldError{fieldErr(ptr, "no_nsx", "This item asks NSX for networks or security groups, and rvav has no NSX Manager. Set one in Integrations first.")}, nil
	}
	client, err := s.nsx()
	if err != nil {
		return nil, err
	}
	var errs []api.FieldError
	read := map[string]*nsx.Group{}
	for _, at := range sortedKeys(groups) {
		id := groups[at]
		if _, done := read[id]; !done {
			g, err := client.Group(ctx, id)
			switch {
			case errors.Is(err, nsx.ErrNotFound):
				read[id] = nil
			case err != nil:
				return nil, &UpstreamError{System: "NSX", Err: err}
			default:
				read[id] = &g
			}
		}
		switch g := read[id]; {
		case g == nil:
			errs = append(errs, fieldErr(at, "unknown", fmt.Sprintf("NSX has no group %s.", id)))
		case g.RvavMade():
			errs = append(errs, fieldErr(at, "rvav_group", fmt.Sprintf("NSX group %s is one rvav made for a deployment: no item may name it.", id)))
		case !strings.Contains(at, "/rules/"):
			if _, ok := g.JoinTags(); !ok {
				errs = append(errs, fieldErr(at, "not_by_tag", fmt.Sprintf("NSX group %s doesn't select VMs by tag: rvav can't put VMs in it.", id)))
			}
		}
	}
	if len(services) > 0 {
		known, err := client.Services(ctx)
		if err != nil {
			return nil, &UpstreamError{System: "NSX", Err: err}
		}
		for _, at := range sortedKeys(services) {
			if !slices.ContainsFunc(known, func(k nsx.Service) bool { return k.ID == services[at] }) {
				errs = append(errs, fieldErr(at, "unknown", fmt.Sprintf("NSX has no service %s.", services[at])))
			}
		}
	}
	return errs, nil
}
