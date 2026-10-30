package vra

import (
	"bytes"
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
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("vRA %s %s: %w", method, path, err)
	}
	return nil
}

const pageSize = 100

// listTop pages through the services that take $top and $skip (iaas,
// blueprint).
func listTop[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	var all []T
	for skip := 0; ; skip += pageSize {
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
		if len(page.Content) < pageSize || len(all) >= page.TotalElements {
			return all, nil
		}
	}
}

// listPages pages through the services that take page and size (catalog,
// policy, deployment, event broker).
func listPages[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	var all []T
	for p := 0; ; p++ {
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("page", strconv.Itoa(p))
		qq.Set("size", strconv.Itoa(pageSize))
		var page struct {
			Content []T  `json:"content"`
			Last    bool `json:"last"`
		}
		if err := c.get(ctx, path, qq, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Content...)
		if page.Last || len(page.Content) < pageSize {
			return all, nil
		}
	}
}

func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	return listTop[Project](ctx, c, "/iaas/api/projects", url.Values{"apiVersion": {"2021-07-15"}})
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
	if errors.Is(err, ErrNotFound) {
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

// Read reads everything the importer uses.
func (c *Client) Read(ctx context.Context) (*Estate, error) {
	e := &Estate{ReadAt: time.Now().UTC(), URL: c.base.String()}
	var err error
	if e.Projects, err = c.Projects(ctx); err != nil {
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
	return e, nil
}
