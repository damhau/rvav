package vra

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/observe"
)

// Options say where vRA is and how rvav signs in: with a refresh token (an
// API token), or a user's name and password in a domain.
type Options struct {
	URL      string
	Token    string // a refresh token
	Username string
	Password string
	Domain   string
	Insecure bool
}

// Client reads vRA. It signs in on first use and again when its access
// token is refused.
type Client struct {
	opts Options
	base *url.URL
	http *http.Client

	mu     sync.Mutex
	bearer string
}

// APIError is an answer vRA gave with an error status.
type APIError struct {
	Method, Path string
	Status       int
	Message      string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("vRA %s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
}

// ErrNotFound is a 404.
var ErrNotFound = errors.New("vRA: not found")

func New(o Options) (*Client, error) {
	u, err := url.Parse(strings.TrimSuffix(o.URL, "/"))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("vra url %q is not valid", o.URL)
	}
	if o.Token == "" && (o.Username == "" || o.Password == "") {
		return nil, errors.New("vra: a refresh token, or a user name and password, is needed")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if o.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for lab vRAs
	}
	return &Client{opts: o, base: u, http: &http.Client{Transport: observe.Transport("vra", tr), Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// login gets an access token: from the refresh token, or from a refresh
// token the user's name and password get first.
func (c *Client) login(ctx context.Context) (string, error) {
	refresh := c.opts.Token
	if refresh == "" {
		var out struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := c.send(ctx, http.MethodPost, "/csp/gateway/am/api/login", url.Values{"access_token": {""}},
			map[string]string{"username": c.opts.Username, "password": c.opts.Password, "domain": c.opts.Domain}, "", &out); err != nil {
			return "", fmt.Errorf("vRA sign-in: %w", err)
		}
		refresh = out.RefreshToken
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.send(ctx, http.MethodPost, "/iaas/api/login", nil, map[string]string{"refreshToken": refresh}, "", &out); err != nil {
		return "", fmt.Errorf("vRA sign-in: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("vRA sign-in: no access token in the answer")
	}
	return out.Token, nil
}

func (c *Client) token(ctx context.Context, fresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bearer != "" && !fresh {
		return c.bearer, nil
	}
	t, err := c.login(ctx)
	if err != nil {
		return "", err
	}
	c.bearer = t
	return t, nil
}

// get reads a path as JSON, signing in again once if the token is refused.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	for attempt := 0; ; attempt++ {
		t, err := c.token(ctx, attempt > 0)
		if err != nil {
			return err
		}
		err = c.send(ctx, http.MethodGet, path, q, nil, t, out)
		var apiErr *APIError
		if attempt == 0 && errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			continue
		}
		return err
	}
}

func (c *Client) send(ctx context.Context, method, path string, q url.Values, body any, bearer string, out any) error {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = q.Encode()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("vRA %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	case resp.StatusCode >= 300:
		msg := strings.TrimSpace(string(b))
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(b, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: msg[:min(len(msg), 300)]}
	}
	// An empty answer (a 204, say) leaves out as it is.
	if out == nil || len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("vRA %s %s: %w", method, path, err)
	}
	return nil
}

const pageSize = 100

// maxPages stops a list whose server ignores paging.
const maxPages = 5000

var errPaging = errors.New("vRA keeps answering with more pages: it may ignore the paging parameters")

// listTop pages through the services that take $top and $skip (iaas,
// blueprint).
func listTop[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	var all []T
	for n, skip := 0, 0; ; n, skip = n+1, skip+pageSize {
		if n == maxPages {
			return nil, fmt.Errorf("%s: %w", path, errPaging)
		}
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("$top", strconv.Itoa(pageSize))
		qq.Set("$skip", strconv.Itoa(skip))
		var page struct {
			Content       []T `json:"content"`
			TotalElements int `json:"totalElements"`
		}
		if err := c.get(ctx, path, qq, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Content...)
		// totalElements "may not be populated": only an empty or short page
		// ends the list then.
		if len(page.Content) == 0 || len(page.Content) < pageSize || page.TotalElements > 0 && len(all) >= page.TotalElements {
			return all, nil
		}
	}
}

// listPages pages through the services that take page and size (catalog,
// policy, deployment, event broker).
func listPages[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	var all []T
	for p := 0; ; p++ {
		if p == maxPages {
			return nil, fmt.Errorf("%s: %w", path, errPaging)
		}
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("page", strconv.Itoa(p))
		qq.Set("size", strconv.Itoa(pageSize))
		var page struct {
			Content    []T   `json:"content"`
			Last       *bool `json:"last"`
			TotalPages *int  `json:"totalPages"`
		}
		if err := c.get(ctx, path, qq, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Content...)
		// The server may give smaller pages than asked: its last or
		// totalPages say when it ends; a short page only without them.
		switch {
		case len(page.Content) == 0:
			return all, nil
		case page.Last != nil:
			if *page.Last {
				return all, nil
			}
		case page.TotalPages != nil:
			if p+1 >= *page.TotalPages {
				return all, nil
			}
		case len(page.Content) < pageSize:
			return all, nil
		}
	}
}

func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	return listTop[Project](ctx, c, "/iaas/api/projects", url.Values{"apiVersion": {"2021-07-15"}})
}

func (c *Client) ImageProfiles(ctx context.Context) ([]ImageProfile, error) {
	return listTop[ImageProfile](ctx, c, "/iaas/api/image-profiles", url.Values{"apiVersion": {"2021-07-15"}})
}

func (c *Client) FlavorProfiles(ctx context.Context) ([]FlavorProfile, error) {
	return listTop[FlavorProfile](ctx, c, "/iaas/api/flavor-profiles", url.Values{"apiVersion": {"2021-07-15"}})
}

// Blueprints lists the cloud templates with their content and versions.
func (c *Client) Blueprints(ctx context.Context) ([]Blueprint, error) {
	list, err := listTop[Blueprint](ctx, c, "/blueprint/api/blueprints", nil)
	if err != nil {
		return nil, err
	}
	for i, b := range list {
		// The list leaves out the content.
		if err := c.get(ctx, "/blueprint/api/blueprints/"+url.PathEscape(b.ID), nil, &list[i]); err != nil {
			return nil, err
		}
		versions, err := listTop[BlueprintVersion](ctx, c, "/blueprint/api/blueprints/"+url.PathEscape(b.ID)+"/versions", nil)
		if err != nil {
			return nil, err
		}
		for j, v := range versions {
			if err := c.get(ctx, "/blueprint/api/blueprints/"+url.PathEscape(b.ID)+"/versions/"+url.PathEscape(v.Version), nil,
				&versions[j]); err != nil {
				return nil, err
			}
		}
		list[i].Versions = versions
	}
	return list, nil
}

// RequestForm is the custom form of a template or workflow, if it has one.
func (c *Client) RequestForm(ctx context.Context, sourceType, sourceID string) (*Form, error) {
	var f Form
	err := c.get(ctx, "/form-service/api/forms/search", url.Values{"sourceType": {sourceType}, "sourceId": {sourceID},
		"formType": {"requestForm"}}, &f)
	// No form: a 404, or an empty answer.
	if errors.Is(err, ErrNotFound) || err == nil && f.ID == "" && f.Form == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (c *Client) CatalogItems(ctx context.Context) ([]CatalogItem, error) {
	return listPages[CatalogItem](ctx, c, "/catalog/api/admin/items", nil)
}

// Entitlements lists what is shared with a project.
func (c *Client) Entitlements(ctx context.Context, projectID string) ([]Entitlement, error) {
	var out []Entitlement
	err := c.get(ctx, "/catalog/api/admin/entitlements", url.Values{"projectId": {projectID}}, &out)
	return out, err
}

func (c *Client) Policies(ctx context.Context) ([]Policy, error) {
	return listPages[Policy](ctx, c, "/policy/api/policies", url.Values{"expandDefinition": {"true"}})
}

func (c *Client) Deployments(ctx context.Context) ([]Deployment, error) {
	return listPages[Deployment](ctx, c, "/deployment/api/deployments", url.Values{"expand": {"resources"}})
}

func (c *Client) Subscriptions(ctx context.Context) ([]Subscription, error) {
	return listPages[Subscription](ctx, c, "/event-broker/api/subscriptions", nil)
}

func (c *Client) ResourceActions(ctx context.Context) ([]ResourceAction, error) {
	return listPages[ResourceAction](ctx, c, "/form-service/api/custom/resource-actions", nil)
}

// vroLink is how vRO lists things: links, each with its attributes.
type vroLink struct {
	Href       string `json:"href"`
	Attributes []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"attributes"`
}

func (l vroLink) attr(name string) string {
	for _, a := range l.Attributes {
		if a.Name == name {
			return a.Value
		}
	}
	return ""
}

// vroList pages through a vRO list (maxResult and startIndex). Its links are
// at the top, or under relations for executions.
func vroList(ctx context.Context, c *Client, path string) ([]vroLink, error) {
	var all []vroLink
	for start := 0; ; start += pageSize {
		var page struct {
			Link      []vroLink `json:"link"`
			Total     int       `json:"total"`
			Relations struct {
				Link  []vroLink `json:"link"`
				Total int       `json:"total"`
			} `json:"relations"`
		}
		q := url.Values{"maxResult": {strconv.Itoa(pageSize)}, "startIndex": {strconv.Itoa(start)}, "queryCount": {"true"}}
		if err := c.get(ctx, path, q, &page); err != nil {
			return nil, err
		}
		links, total := page.Link, page.Total
		if len(links) == 0 {
			links, total = page.Relations.Link, page.Relations.Total
		}
		all = append(all, links...)
		if len(links) < pageSize || (total > 0 && len(all) >= total) {
			return all, nil
		}
	}
}

// vroParams reads vRO's input-parameters.
type vroParams []struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

func (ps vroParams) params() []VROParam {
	var out []VROParam
	for _, p := range ps {
		out = append(out, VROParam{Name: p.Name, Type: p.Type, Description: p.Description})
	}
	return out
}

// vroTime reads vRO's dates: ISO 8601, or milliseconds since 1970.
func vroTime(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000-0700", s); err == nil {
		return t.UTC(), true
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), true
	}
	return time.Time{}, false
}

// Workflows lists vRO's workflows with their inputs, content, and how often
// and when they last ran.
func (c *Client) Workflows(ctx context.Context) ([]VROWorkflow, error) {
	links, err := vroList(ctx, c, "/vco/api/workflows")
	if err != nil {
		return nil, err
	}
	// vRO lists a workflow's folder by its own name only: its path (Library/
	// vCenter/...) is the category's, read once per category.
	paths := map[string]string{}
	categoryPath := func(l vroLink) (string, error) {
		id := l.attr("categoryId")
		if id == "" {
			if href := strings.TrimSuffix(l.attr("categoryHref"), "/"); href != "" {
				id = href[strings.LastIndex(href, "/")+1:]
			}
		}
		if id == "" {
			return l.attr("categoryName"), nil
		}
		if p, ok := paths[id]; ok {
			return p, nil
		}
		var cat struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		if err := c.get(ctx, "/vco/api/categories/"+url.PathEscape(id)+"/", nil, &cat); err != nil && !errors.Is(err, ErrNotFound) {
			return "", err
		}
		p := cmp.Or(cat.Path, cat.Name, l.attr("categoryName"))
		paths[id] = p
		return p, nil
	}
	var out []VROWorkflow
	for _, l := range links {
		id := l.attr("id")
		category, err := categoryPath(l)
		if err != nil {
			return nil, err
		}
		w := VROWorkflow{ID: id, Name: l.attr("name"), Category: category, Description: l.attr("description"),
			Version: l.attr("version")}
		if IsVROLibrary(category) {
			// vRO's own: the triage leaves them out, so their content and
			// runs (some thousand workflows) aren't read.
			out = append(out, w)
			continue
		}
		base := "/vco/api/workflows/" + url.PathEscape(id)
		var detail struct {
			Inputs vroParams `json:"input-parameters"`
		}
		if err := c.get(ctx, base+"/", nil, &detail); err != nil {
			return nil, err
		}
		w.Inputs = detail.Inputs.params()
		var content json.RawMessage
		if err := c.get(ctx, base+"/content/", nil, &content); err != nil {
			return nil, err
		}
		w.Content = string(content)
		runs, err := vroList(ctx, c, base+"/executions/")
		if err != nil {
			return nil, err
		}
		w.Runs = len(runs)
		for _, r := range runs {
			if t, ok := vroTime(r.attr("startDate")); ok && (w.LastRun == nil || t.After(*w.LastRun)) {
				w.LastRun = &t
			}
		}
		out = append(out, w)
	}
	return out, nil
}

// Actions lists vRO's actions with their inputs and scripts.
func (c *Client) Actions(ctx context.Context) ([]VROAction, error) {
	links, err := vroList(ctx, c, "/vco/api/actions")
	if err != nil {
		return nil, err
	}
	var out []VROAction
	for _, l := range links {
		if IsVROLibrary(l.attr("module")) {
			// vRO's own: left out of the triage, so not read one by one.
			out = append(out, VROAction{ID: l.attr("id"), Name: l.attr("name"), Module: l.attr("module"), Version: l.attr("version")})
			continue
		}
		var a struct {
			ID         string    `json:"id"`
			Name       string    `json:"name"`
			Module     string    `json:"module"`
			Version    string    `json:"version"`
			OutputType string    `json:"output-type"`
			Inputs     vroParams `json:"input-parameters"`
			Script     string    `json:"script"`
		}
		if err := c.get(ctx, "/vco/api/actions/"+url.PathEscape(l.attr("id"))+"/", nil, &a); err != nil {
			return nil, err
		}
		out = append(out, VROAction{ID: a.ID, Name: a.Name, Module: a.Module, Version: a.Version, OutputType: a.OutputType,
			Inputs: a.Inputs.params(), Script: a.Script})
	}
	return out, nil
}

// Read reads everything the importer uses.
func (c *Client) Read(ctx context.Context) (*Estate, error) {
	e := &Estate{ReadAt: time.Now().UTC(), URL: c.base.String()}
	var err error
	if e.Projects, err = c.Projects(ctx); err != nil {
		return nil, err
	}
	if e.Images, err = c.ImageProfiles(ctx); err != nil {
		return nil, err
	}
	if e.Flavors, err = c.FlavorProfiles(ctx); err != nil {
		return nil, err
	}
	if e.Blueprints, err = c.Blueprints(ctx); err != nil {
		return nil, err
	}
	for _, b := range e.Blueprints {
		f, err := c.RequestForm(ctx, "com.vmw.blueprint", b.ID)
		if err != nil {
			return nil, err
		}
		if f != nil {
			e.Forms = append(e.Forms, *f)
		}
	}
	if e.CatalogItems, err = c.CatalogItems(ctx); err != nil {
		return nil, err
	}
	for _, p := range e.Projects {
		ents, err := c.Entitlements(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		e.Entitlements = append(e.Entitlements, ents...)
	}
	if e.Policies, err = c.Policies(ctx); err != nil {
		return nil, err
	}
	if e.Deployments, err = c.Deployments(ctx); err != nil {
		return nil, err
	}
	// vRO workflows' own forms, when they are catalog items.
	for _, ci := range e.CatalogItems {
		if ci.Type.ID != "com.vmw.vro.workflow" {
			continue
		}
		// The workflow's ID is the item's (vRA 8).
		f, err := c.RequestForm(ctx, ci.Type.ID, ci.ID)
		if err != nil {
			return nil, err
		}
		if f != nil {
			e.Forms = append(e.Forms, *f)
		}
	}
	if e.Subscriptions, err = c.Subscriptions(ctx); err != nil {
		return nil, err
	}
	if e.ResourceActions, err = c.ResourceActions(ctx); err != nil {
		return nil, err
	}
	if e.Workflows, err = c.Workflows(ctx); err != nil {
		return nil, err
	}
	if e.Actions, err = c.Actions(ctx); err != nil {
		return nil, err
	}
	return e, nil
}

// IsVROLibrary: vRO ships it (a workflow's category path, or an action's
// module).
func IsVROLibrary(category string) bool {
	return category == "Library" || strings.HasPrefix(category, "Library/") || strings.HasPrefix(category, "com.vmware.")
}
