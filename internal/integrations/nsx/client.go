package nsx

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
	"time"

	"github.com/damhau/rvav/internal/observe"
)

// Options say where the NSX Manager is and how rvav signs in: a user and
// password (basic authentication), or a client certificate (a principal
// identity, as NSX recommends for automation).
type Options struct {
	URL                string
	Username, Password string
	// CertFile and KeyFile: the principal identity's certificate and key,
	// PEM files.
	CertFile, KeyFile string
	Insecure          bool
}

// Client calls the NSX Manager.
type Client struct {
	opts Options
	base *url.URL
	http *http.Client
}

// APIError is an error NSX answered, with its error code.
type APIError struct {
	Method, Path string
	Status       int
	Code         int
	Message      string
}

func (e *APIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("NSX %s %s: %d (error %d): %s", e.Method, e.Path, e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("NSX %s %s: %d: %s", e.Method, e.Path, e.Status, e.Message)
}

// ErrNotFound is a 404.
var ErrNotFound = errors.New("not found in NSX")

const policy = "/policy/api/v1"

// pageSize is what a list asks for; NSX gives at most 1000.
const pageSize = 1000

// maxPages stops a list whose cursor never ends.
const maxPages = 1000

func New(o Options) (*Client, error) {
	u, err := url.Parse(strings.TrimSuffix(o.URL, "/"))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("nsx url %q is not valid", o.URL)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if o.Insecure {
		tc.InsecureSkipVerify = true //nolint:gosec // opt-in for lab NSX Managers
	}
	switch {
	case o.CertFile != "" || o.KeyFile != "":
		cert, err := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("nsx client certificate: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	case o.Username == "" || o.Password == "":
		return nil, errors.New("nsx: a user and password, or a client certificate, is needed")
	}
	tr.TLSClientConfig = tc
	return &Client{opts: o, base: u, http: &http.Client{Transport: observe.Transport("nsx", tr), Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
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
	if c.opts.CertFile == "" {
		req.SetBasicAuth(c.opts.Username, c.opts.Password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("NSX %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if resp.StatusCode >= 300 {
		e := &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: strings.TrimSpace(string(b))}
		var nsxErr struct {
			Code    int    `json:"error_code"`
			Message string `json:"error_message"`
		}
		if json.Unmarshal(b, &nsxErr) == nil && nsxErr.Message != "" {
			e.Code, e.Message = nsxErr.Code, nsxErr.Message
		}
		e.Message = e.Message[:min(len(e.Message), 300)]
		return e
	}
	if out == nil || len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("NSX %s %s: %w", method, path, err)
	}
	return nil
}

// list reads every page of a Policy API list, by its cursor.
func list[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	all := []T{}
	cursor := ""
	for range maxPages {
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("page_size", strconv.Itoa(pageSize))
		if cursor != "" {
			qq.Set("cursor", cursor)
		}
		var page struct {
			Results []T    `json:"results"`
			Cursor  string `json:"cursor"`
		}
		if err := c.do(ctx, http.MethodGet, path, qq, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || page.Cursor == cursor || len(page.Results) == 0 {
			return all, nil
		}
		cursor = page.Cursor
	}
	return nil, fmt.Errorf("NSX %s: the list's cursor never ends", path)
}

// del deletes; something already gone is no error.
func (c *Client) del(ctx context.Context, path string) error {
	err := c.do(ctx, http.MethodDelete, path, nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Version is the NSX Manager's product version.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		ProductVersion string `json:"product_version"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/node/version", nil, nil, &v)
	return v.ProductVersion, err
}

func (c *Client) Tier1s(ctx context.Context) ([]Tier1, error) {
	return list[Tier1](ctx, c, policy+"/infra/tier-1s", nil)
}

func (c *Client) TransportZones(ctx context.Context) ([]TransportZone, error) {
	return list[TransportZone](ctx, c, policy+"/infra/sites/default/enforcement-points/default/transport-zones", nil)
}

func (c *Client) Services(ctx context.Context) ([]Service, error) {
	return list[Service](ctx, c, policy+"/infra/services", nil)
}

func (c *Client) Segments(ctx context.Context) ([]Segment, error) {
	return list[Segment](ctx, c, policy+"/infra/segments", nil)
}

func (c *Client) Segment(ctx context.Context, id string) (Segment, error) {
	var s Segment
	err := c.do(ctx, http.MethodGet, policy+SegmentPath+url.PathEscape(id), nil, nil, &s)
	return s, err
}

// PutSegment creates the segment of its ID, or updates it.
func (c *Client) PutSegment(ctx context.Context, s Segment) error {
	return c.do(ctx, http.MethodPatch, policy+SegmentPath+url.PathEscape(s.ID), nil, s, nil)
}

func (c *Client) DeleteSegment(ctx context.Context, id string) error {
	return c.del(ctx, policy+SegmentPath+url.PathEscape(id))
}

// SegmentState is how far NSX has realised a segment: success once it is
// a port group in vCenter; pending, in_progress, failed or error before.
func (c *Client) SegmentState(ctx context.Context, id string) (string, error) {
	var s struct {
		State string `json:"state"`
	}
	err := c.do(ctx, http.MethodGet, policy+SegmentPath+url.PathEscape(id)+"/state", nil, nil, &s)
	return strings.ToLower(s.State), err
}

func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	return list[Group](ctx, c, policy+"/infra/domains/default/groups", nil)
}

func (c *Client) Group(ctx context.Context, id string) (Group, error) {
	var g Group
	err := c.do(ctx, http.MethodGet, policy+GroupPath+url.PathEscape(id), nil, nil, &g)
	return g, err
}

func (c *Client) PutGroup(ctx context.Context, g Group) error {
	return c.do(ctx, http.MethodPatch, policy+GroupPath+url.PathEscape(g.ID), nil, g, nil)
}

func (c *Client) DeleteGroup(ctx context.Context, id string) error {
	return c.del(ctx, policy+GroupPath+url.PathEscape(id))
}

func (c *Client) SecurityPolicies(ctx context.Context) ([]SecurityPolicy, error) {
	return list[SecurityPolicy](ctx, c, policy+"/infra/domains/default/security-policies", nil)
}

func (c *Client) SecurityPolicy(ctx context.Context, id string) (SecurityPolicy, error) {
	var p SecurityPolicy
	err := c.do(ctx, http.MethodGet, policy+PolicyPath+url.PathEscape(id), nil, nil, &p)
	return p, err
}

// PutSecurityPolicy creates the policy of its ID with its rules, or
// updates them (rules not given are left as they are).
func (c *Client) PutSecurityPolicy(ctx context.Context, p SecurityPolicy) error {
	return c.do(ctx, http.MethodPatch, policy+PolicyPath+url.PathEscape(p.ID), nil, p, nil)
}

// DeleteSecurityPolicy deletes the policy and its rules.
func (c *Client) DeleteSecurityPolicy(ctx context.Context, id string) error {
	return c.del(ctx, policy+PolicyPath+url.PathEscape(id))
}

const vms = policy + "/infra/realized-state/enforcement-points/default/virtual-machines"

// VMs are the VMs NSX knows, with their tags.
func (c *Client) VMs(ctx context.Context) ([]VM, error) {
	return list[VM](ctx, c, vms, nil)
}

// VM is the VM of a vCenter instance UUID, once NSX has seen it:
// ErrNotFound before.
func (c *Client) VM(ctx context.Context, externalID string) (VM, error) {
	found, err := list[VM](ctx, c, vms, url.Values{"query": {"external_id:" + externalID}})
	if err != nil {
		return VM{}, err
	}
	for _, v := range found {
		if v.ExternalID == externalID {
			return v, nil
		}
	}
	return VM{}, ErrNotFound
}

// SetVMTags sets a VM's tags, all of them: those not given are removed.
func (c *Client) SetVMTags(ctx context.Context, externalID string, tags []Tag) error {
	if tags == nil {
		tags = []Tag{}
	}
	return c.do(ctx, http.MethodPost, vms, url.Values{"action": {"update_tags"}},
		map[string]any{"virtual_machine_id": externalID, "tags": tags}, nil)
}
