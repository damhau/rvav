// Package servicenow calls a ServiceNow instance's REST APIs (ADR-0065): the
// Table API, to read and update records (the requested items rvav reports
// on, the CIs it retires), and the Identification and Reconciliation API,
// which matches a VM to its CI by ServiceNow's own identification rules.
package servicenow

import (
	"bytes"
	"context"
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
	"github.com/damhau/rvav/internal/trust"
)

// How rvav signs in.
const (
	AuthBasic = "basic"
	// AuthOAuth is OAuth's client credentials grant, which ServiceNow offers
	// from its Washington DC release on.
	AuthOAuth = "oauth"
)

// Tables rvav reads and writes besides the CMDB class.
const (
	TableRequestedItem = "sc_req_item"
	TableUser          = "sys_user"
)

// DefaultClass is the CMDB class VMs go in: vCenter discovery's, which the
// identification rules of ServiceNow's VMware model match on.
const DefaultClass = "cmdb_ci_vmware_instance"

// Install and operational statuses a CI is given (cmdb_ci's choices).
const (
	InstallInstalled   = "1"
	InstallRetired     = "7"
	OperationalOn      = "1"
	OperationalOff     = "2"
	OperationalRetired = "6"
)

// Requested item states (sc_req_item.state's choices).
const (
	ItemWorkInProgress   = "2"
	ItemClosedComplete   = "3"
	ItemClosedIncomplete = "4"
)

// Config is how to reach the instance and sign in.
type Config struct {
	URL      string
	Auth     string
	Username string
	ClientID string
	// Secret is the user's password, or the OAuth client's secret.
	Secret string
	TLS    trust.Options
}

// Client calls one instance. It gets an OAuth access token on first use,
// and again when it expires or is refused.
type Client struct {
	cfg  Config
	base *url.URL
	http *http.Client

	mu      sync.Mutex
	bearer  string
	expires time.Time
}

// APIError is an answer ServiceNow gave with an error status.
type APIError struct {
	Method, Path string
	Status       int
	Message      string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ServiceNow %s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
}

// Temporary says whether trying again later may work.
func (e *APIError) Temporary() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// IsRefused says whether ServiceNow refused this call itself, which
// trying again likely won't change: a 4xx answer, such as a data policy's
// or a business rule's 403, but for 408 and 429, which pass later, 401,
// which is rvav's sign-in and refuses every call alike, and 404.
func IsRefused(err error) bool {
	var e *APIError
	if !errors.As(err, &e) || e.Status < 400 || e.Status > 499 {
		return false
	}
	switch e.Status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusNotFound:
		return false
	}
	return true
}

// IsNotFound says whether the record isn't there.
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// New is a client of the instance; it calls nothing yet.
func New(c Config) (*Client, error) {
	u, err := url.Parse(strings.TrimSuffix(c.URL, "/"))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("ServiceNow's URL %q is not an https URL", c.URL)
	}
	switch {
	case c.Auth == AuthOAuth && (c.ClientID == "" || c.Secret == ""):
		return nil, errors.New("ServiceNow: OAuth needs the client ID and its secret")
	case c.Auth != AuthOAuth && (c.Username == "" || c.Secret == ""):
		return nil, errors.New("ServiceNow: basic authentication needs a user and its password")
	}
	o := c.TLS
	o.Host = u.Hostname()
	return &Client{cfg: c, base: u, http: &http.Client{Transport: observe.Transport("servicenow", trust.Transport(o)),
		Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// URL is the instance's address.
func (c *Client) URL() string { return c.base.String() }

// RecordURL is a record's form in the instance's UI.
func RecordURL(instance, table, sysID string) string {
	return strings.TrimSuffix(instance, "/") + "/" + url.PathEscape(table) + ".do?sys_id=" + url.QueryEscape(sysID)
}

// token is an OAuth access token, a new one when fresh.
func (c *Client) token(ctx context.Context, fresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bearer != "" && !fresh && time.Now().Before(c.expires) {
		return c.bearer, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.cfg.ClientID}, "client_secret": {c.cfg.Secret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String()+"/oauth_token.do", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ServiceNow sign-in: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(b, &out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		msg := out.Description
		if msg == "" {
			msg = out.Error
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return "", fmt.Errorf("ServiceNow sign-in: the OAuth client was refused (%d): %s", resp.StatusCode, msg)
	}
	life := time.Duration(out.ExpiresIn) * time.Second
	if life <= 0 {
		life = 30 * time.Minute
	}
	c.bearer, c.expires = out.AccessToken, time.Now().Add(life-life/10)
	return c.bearer, nil
}

// call sends a JSON request and decodes the answer's JSON into out; with
// OAuth, a refused token is renewed once.
func (c *Client) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		u := *c.base
		u.Path = strings.TrimSuffix(u.Path, "/") + path
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(b))
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.cfg.Auth == AuthOAuth {
			t, err := c.token(ctx, attempt > 0)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+t)
		} else {
			req.SetBasicAuth(c.cfg.Username, c.cfg.Secret)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("ServiceNow %s %s: %w", method, path, err)
		}
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if rerr != nil {
			return fmt.Errorf("ServiceNow %s %s: %w", method, path, rerr)
		}
		if resp.StatusCode == http.StatusUnauthorized && c.cfg.Auth == AuthOAuth && attempt == 0 {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: errorMessage(data, resp.StatusCode)}
		}
		if out == nil || len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("ServiceNow %s %s: the answer isn't JSON: %w", method, path, err)
		}
		return nil
	}
}

// errorMessage is what ServiceNow's error answer says:
// {"error": {"message": …, "detail": …}, "status": "failure"}.
func errorMessage(b []byte, status int) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Detail  string `json:"detail"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		if e.Error.Detail != "" {
			return e.Error.Message + ": " + e.Error.Detail
		}
		return e.Error.Message
	}
	return http.StatusText(status)
}

// Record is a row as the Table API gives it: every value a string.
type Record map[string]string

// Query is what a Table API read asks for.
type Query struct {
	// Encoded is ServiceNow's encoded query, for example
	// discovery_source=rvav^install_status!=7.
	Encoded string
	Fields  []string
	Limit   int
	Offset  int
}

// Records reads a page of a table's rows.
func (c *Client) Records(ctx context.Context, table string, q Query) ([]Record, error) {
	v := url.Values{"sysparm_exclude_reference_link": {"true"}}
	if q.Encoded != "" {
		v.Set("sysparm_query", q.Encoded)
	}
	if len(q.Fields) > 0 {
		v.Set("sysparm_fields", strings.Join(q.Fields, ","))
	}
	if q.Limit > 0 {
		v.Set("sysparm_limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		v.Set("sysparm_offset", strconv.Itoa(q.Offset))
	}
	var out struct {
		Result []map[string]any `json:"result"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/now/table/"+url.PathEscape(table), v, nil, &out); err != nil {
		return nil, err
	}
	recs := make([]Record, len(out.Result))
	for i, r := range out.Result {
		recs[i] = Record{}
		for k, v := range r {
			recs[i][k] = text(v)
		}
	}
	return recs, nil
}

// All reads every row a query matches, a page of pageSize at a time.
func (c *Client) All(ctx context.Context, table string, q Query, pageSize int) ([]Record, error) {
	var out []Record
	q.Limit = pageSize
	for q.Offset = 0; ; q.Offset += pageSize {
		page, err := c.Records(ctx, table, q)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < pageSize {
			return out, nil
		}
	}
}

// text is a value as a string; a display value pair keeps its value.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case map[string]any:
		return text(x["value"])
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Update changes a record's fields. A work_notes value adds a work note.
func (c *Client) Update(ctx context.Context, table, sysID string, values map[string]string) error {
	return c.call(ctx, http.MethodPatch, "/api/now/table/"+url.PathEscape(table)+"/"+url.PathEscape(sysID), nil, values, nil)
}

// CI is a configuration item as the IRE takes it.
type CI struct {
	Class  string
	Values map[string]string
	// NativeKey is the source's own key of the CI: the VM's instance UUID.
	NativeKey string
}

// Identified is what the IRE did with a CI.
type Identified struct {
	SysID string
	// Operation is INSERT, UPDATE, NO_CHANGE or DELETE.
	Operation string
}

// Identify sends a CI to the Identification and Reconciliation API: its
// identification rules find the CI it is, if any, which it updates;
// otherwise it inserts one, with dataSource as its discovery source.
func (c *Client) Identify(ctx context.Context, dataSource string, ci CI) (Identified, error) {
	item := map[string]any{"className": ci.Class, "values": ci.Values}
	if ci.NativeKey != "" {
		item["sys_object_source_info"] = map[string]string{"source_name": dataSource, "source_native_key": ci.NativeKey}
	}
	body := map[string]any{"items": []any{item}, "relations": []any{}}
	var out struct {
		Result struct {
			Items []struct {
				SysID     string `json:"sysId"`
				Operation string `json:"operation"`
				Errors    []struct {
					Error   string `json:"error"`
					Message string `json:"message"`
				} `json:"errors"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/now/identifyreconcile", url.Values{"sysparm_data_source": {dataSource}}, body,
		&out); err != nil {
		return Identified{}, err
	}
	if len(out.Result.Items) != 1 {
		return Identified{}, fmt.Errorf("ServiceNow's IRE gave %d items for one", len(out.Result.Items))
	}
	it := out.Result.Items[0]
	if len(it.Errors) > 0 {
		e := it.Errors[0]
		return Identified{}, fmt.Errorf("ServiceNow's IRE: %s: %s", e.Error, e.Message)
	}
	if it.SysID == "" {
		return Identified{}, errors.New("ServiceNow's IRE gave no sys_id")
	}
	return Identified{SysID: it.SysID, Operation: it.Operation}, nil
}

// UserSysID is the sys_id of the active user with this email, or "".
func (c *Client) UserSysID(ctx context.Context, email string) (string, error) {
	if email == "" || strings.ContainsAny(email, "^=") {
		return "", nil
	}
	recs, err := c.Records(ctx, TableUser, Query{Encoded: "active=true^email=" + email, Fields: []string{"sys_id"}, Limit: 2})
	if err != nil || len(recs) != 1 {
		return "", err
	}
	return recs[0]["sys_id"], nil
}
