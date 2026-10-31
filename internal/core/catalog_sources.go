package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/hosts"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// CatalogIcons are the icons an item may have; the web app draws the same
// set (components/common/misc.tsx).
var CatalogIcons = []string{"server", "monitor", "database", "key-round", "shield", "globe", "lock", "users", "terminal", "workflow",
	"box", "container", "hard-drive", "network", "cloud", "cpu", "package", "wrench", "mail", "file-text", "archive",
	"git-branch", "layers", "activity"}

// checkCustomSource checks a `rest` or `cel` value source's parameters.
func checkCustomSource(src api.ValueSource, ptr string) []api.FieldError {
	params := map[string]any{}
	if src.Params != nil {
		params = *src.Params
	}
	str := func(k string) string { s, _ := params[k].(string); return s }
	var errs []api.FieldError
	switch src.Kind {
	case api.Rest:
		if str("url") == "" {
			errs = append(errs, fieldErr(ptr+"/params/url", "required", "Give the URL the options come from."))
		} else if u, err := url.Parse(fill(str("url"), nil, urlEscape)); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			errs = append(errs, fieldErr(ptr+"/params/url", "invalid", "Give an http or https URL."))
		} else if !fixedOrigin(str("url")) {
			errs = append(errs, fieldErr(ptr+"/params/url", "invalid", "Inputs may fill the URL's path and query, not its scheme, host or port."))
		}
		if m := strings.ToUpper(str("method")); m != "" && m != http.MethodGet && m != http.MethodPost {
			errs = append(errs, fieldErr(ptr+"/params/method", "invalid", "GET or POST."))
		}
		for _, k := range []string{"items", "value", "label", "meta"} {
			if path := str(k); path != "" {
				if _, err := jp.ParseString(path); err != nil {
					errs = append(errs, fieldErr(ptr+"/params/"+k, "invalid", fmt.Sprintf("This is not valid JSONPath: %v", err)))
				}
			}
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
			if err := policy.Check(expr); err != nil {
				errs = append(errs, fieldErr(ptr+"/params/"+k, "invalid", fmt.Sprintf("This expression is not valid CEL: %v", err)))
			}
		}
	}
	return errs
}

// formVars are the CEL variables of form rules and `cel` value sources.
func formVars(inputs map[string]any, proj store.Project, environment string, p *auth.Principal) policy.Vars {
	return policy.Vars{
		"inputs":      inputs,
		"environment": environment,
		"project":     map[string]any{"id": proj.ID, "code": proj.Code, "name": proj.Name},
		"user":        map[string]any{"id": p.User.ID, "roles": nonNilStrings(p.GlobalRoles), "groups": nonNilStrings(p.Groups)},
	}
}

// celValues resolves a `cel` value source: options from a list of strings
// or of {value, label, meta}, or a computed value.
func celValues(params map[string]any, vars policy.Vars) (opts *[]api.Option, value any, err error) {
	if expr, _ := params["value"].(string); expr != "" {
		if value, err = policy.Eval(expr, vars); err != nil {
			return nil, nil, &BadRequestError{Msg: "The value expression failed: " + err.Error()}
		}
	}
	expr, _ := params["options"].(string)
	if expr == "" {
		return nil, value, nil
	}
	v, err := policy.Eval(expr, vars)
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
			out = append(out, option(fmt.Sprint(m["value"]), fmt.Sprint(cmpOr(m["label"], m["value"])), str(m["meta"])))
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

// RESTSources calls the URLs of `rest` value sources (ADR-0019): only
// allowed hosts, no redirects, 5 seconds and 1 MB at most, answers cached.
type RESTSources struct {
	// Hosts are the allowed hosts: "cmdb.corp.example", "*.corp.example"
	// (subdomains), "host:8443".
	Hosts []string
	// HostsNow, when set, gives them as saved now (ADR-0030).
	HostsNow func(context.Context) []string
	Client   *http.Client

	mu    sync.Mutex
	cache map[string]restEntry
}

type restEntry struct {
	opts  []api.Option
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
	return &RESTSources{Hosts: hosts, Client: &http.Client{Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Allowed says whether a URL's host may be called (package hosts).
func (r *RESTSources) Allowed(ctx context.Context, u *url.URL) bool {
	if r == nil {
		return false
	}
	if r.HostsNow != nil {
		return hosts.Allowed(r.HostsNow(ctx), u)
	}
	return hosts.Allowed(r.Hosts, u)
}

var inputRef = regexp.MustCompile(`\$\{inputs\.([a-zA-Z0-9_]+)\}`)

// fixedOrigin says whether inputs leave a URL template's scheme, host and
// port alone: they may fill its path and query only, so a form can't send
// a request, or a secret, anywhere else.
func fixedOrigin(tpl string) bool {
	ref := strings.Index(tpl, "${")
	if ref < 0 {
		return true
	}
	scheme := strings.Index(tpl, "://")
	if scheme < 0 || ref < scheme+3 {
		return false
	}
	end := strings.IndexAny(tpl[scheme+3:], "/?#")
	return end >= 0 && ref >= scheme+3+end
}

// fill puts inputs into a template, each escaped by esc (empty when absent).
func fill(tpl string, inputs map[string]any, esc func(string) string) string {
	return inputRef.ReplaceAllStringFunc(tpl, func(m string) string {
		v, ok := inputs[inputRef.FindStringSubmatch(m)[1]]
		if !ok || v == nil {
			return ""
		}
		return esc(fmt.Sprint(v))
	})
}

func urlEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// Options calls the source and maps its answer to options.
func (r *RESTSources) Options(ctx context.Context, params map[string]any, inputs map[string]any,
	secret func(name string) (store.Secret, string, error)) ([]api.Option, int, error) {
	p := func(k string) string { s, _ := params[k].(string); return s }
	if !fixedOrigin(p("url")) {
		return nil, 0, &BadRequestError{Msg: "The source's URL lets inputs choose its host: inputs may fill its path and query only."}
	}
	for _, m := range inputRef.FindAllStringSubmatch(p("url"), -1) {
		if v := fmt.Sprint(inputs[m[1]]); v == "." || v == ".." {
			return nil, 0, &BadRequestError{Msg: fmt.Sprintf("%s can't be %q in the source's URL.", m[1], v)}
		}
	}
	target := fill(p("url"), inputs, urlEscape)
	u, err := url.Parse(target)
	if err != nil || !r.Allowed(ctx, u) {
		return nil, 0, &BadRequestError{Msg: fmt.Sprintf("The source's host %q is not in RVAV_REST_SOURCE_HOSTS.", hostOf(target))}
	}
	method := strings.ToUpper(cmp.Or(p("method"), http.MethodGet))
	body := fill(p("body"), inputs, jsonEscape)
	cacheSeconds := 60
	if n, ok := params["cache_seconds"].(float64); ok {
		cacheSeconds = min(max(int(n), 0), 3600)
	}
	// The mapping is in the key too: two fields may read one answer differently.
	key := strings.Join([]string{method + " " + target, body, p("secret"), p("items"), p("value"), p("label"), p("meta")}, "\n")
	r.mu.Lock()
	if e, ok := r.cache[key]; ok && time.Now().Before(e.until) {
		r.mu.Unlock()
		return e.opts, cacheSeconds, nil
	}
	r.mu.Unlock()

	var rd io.Reader
	if method == http.MethodPost {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if name := p("secret"); name != "" {
		sec, value, err := secret(name)
		if err != nil {
			return nil, 0, fmt.Errorf("secret %s: %w", name, err)
		}
		switch sec.Kind {
		case "token":
			req.Header.Set("Authorization", "Bearer "+value)
		case "basic":
			req.SetBasicAuth(sec.Username, value)
		case "header":
			req.Header.Set(sec.Header, value)
		}
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, 0, &UpstreamError{System: "REST source " + u.Host, Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	switch {
	case err != nil:
		return nil, 0, &UpstreamError{System: "REST source " + u.Host, Err: err}
	case resp.StatusCode >= 300:
		return nil, 0, &UpstreamError{System: "REST source " + u.Host, Err: fmt.Errorf("it answered %s", resp.Status)}
	case len(raw) > 1<<20:
		return nil, 0, &UpstreamError{System: "REST source " + u.Host, Err: errors.New("its answer is over 1 MB")}
	}
	data, err := oj.Parse(raw)
	if err != nil {
		return nil, 0, &UpstreamError{System: "REST source " + u.Host, Err: fmt.Errorf("its answer is not JSON: %w", err)}
	}
	opts, err := mapOptions(data, cmp.Or(p("items"), "$"), cmp.Or(p("value"), "$"), p("label"), p("meta"))
	if err != nil {
		return nil, 0, &BadRequestError{Msg: err.Error()}
	}
	if cacheSeconds > 0 {
		r.mu.Lock()
		if r.cache == nil || len(r.cache) > 1000 {
			r.cache = map[string]restEntry{}
		}
		r.cache[key] = restEntry{opts: opts, until: time.Now().Add(time.Duration(cacheSeconds) * time.Second)}
		r.mu.Unlock()
	}
	return opts, cacheSeconds, nil
}

func hostOf(target string) string {
	if u, err := url.Parse(target); err == nil {
		return u.Host
	}
	return target
}

// mapOptions picks the items with a JSONPath, then each one's value, label
// and meta.
func mapOptions(data any, itemsPath, valuePath, labelPath, metaPath string) ([]api.Option, error) {
	paths := map[string]jp.Expr{}
	for name, p := range map[string]string{"items": itemsPath, "value": valuePath, "label": labelPath, "meta": metaPath} {
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
		out = append(out, option(value, cmp.Or(first("label", item), value), first("meta", item)))
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
		if u, err := url.Parse(fill(target, nil, urlEscape)); err == nil && !s.REST.Allowed(ctx, u) {
			errs = append(errs, fieldErr(ptr+"/url", "not_allowed", fmt.Sprintf("rvav may not call %s: it is not in RVAV_REST_SOURCE_HOSTS.", u.Host)))
		}
		if secret, _ := params["secret"].(string); secret != "" {
			if _, err := s.Store.GetSecret(ctx, secret); errors.Is(err, store.ErrNotFound) {
				errs = append(errs, fieldErr(ptr+"/secret", "unknown", fmt.Sprintf("There is no secret %q.", secret)))
			} else if err != nil {
				return nil, err
			}
		}
	}
	// Secrets a VM blueprint names (ADR-0024).
	raw, _ := json.Marshal(d.Blueprint)
	if b, err := blueprint.Parse(raw); err == nil && b.Kind == blueprint.KindVM {
		refs := blueprintSecrets(b, prefix+"/blueprint")
		for _, at := range sortedKeys(refs) {
			name, kind := refs[at][0], refs[at][1]
			v, err := s.Store.GetSecret(ctx, name)
			switch {
			case errors.Is(err, store.ErrNotFound):
				errs = append(errs, fieldErr(at, "unknown", fmt.Sprintf("There is no secret %q.", name)))
			case err != nil:
				return nil, err
			case kind != "" && v.Kind != kind:
				errs = append(errs, fieldErr(at, "invalid", fmt.Sprintf("Secret %q must be a %s secret: a user name and its password.", name, kind)))
			}
		}
		nsxErrs, err := s.checkNSXRefs(ctx, b, prefix+"/blueprint")
		if err != nil {
			return nil, err
		}
		errs = append(errs, nsxErrs...)
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
