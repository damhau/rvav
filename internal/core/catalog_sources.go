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
	"strings"
	"sync"
	"time"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
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
	Hosts  []string
	Client *http.Client

	mu    sync.Mutex
	cache map[string]restEntry
}

type restEntry struct {
	opts  []api.Option
	until time.Time
}

func NewRESTSources(hosts []string) *RESTSources {
	return &RESTSources{Hosts: hosts, Client: &http.Client{Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Allowed says whether a URL's host may be called.
func (r *RESTSources) Allowed(u *url.URL) bool {
	if r == nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	host, hostPort := strings.ToLower(u.Hostname()), strings.ToLower(u.Host)
	for _, h := range r.Hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		switch {
		case h == "":
		case strings.HasPrefix(h, "*."):
			if strings.HasSuffix(host, h[1:]) {
				return true
			}
		case h == host || h == hostPort:
			return true
		}
	}
	return false
}

var inputRef = regexp.MustCompile(`\$\{inputs\.([a-zA-Z0-9_]+)\}`)

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
	target := fill(p("url"), inputs, urlEscape)
	u, err := url.Parse(target)
	if err != nil || !r.Allowed(u) {
		return nil, 0, &BadRequestError{Msg: fmt.Sprintf("The source's host %q is not in RVAV_REST_SOURCE_HOSTS.", hostOf(target))}
	}
	method := strings.ToUpper(cmp.Or(p("method"), http.MethodGet))
	body := fill(p("body"), inputs, jsonEscape)
	cacheSeconds := 60
	if n, ok := params["cache_seconds"].(float64); ok {
		cacheSeconds = min(max(int(n), 0), 3600)
	}
	key := method + " " + target + "\n" + body + "\n" + p("secret")
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
		if u, err := url.Parse(fill(target, nil, urlEscape)); err == nil && !s.REST.Allowed(u) {
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
	return errs, nil
}
