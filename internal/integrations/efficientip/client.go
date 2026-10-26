package efficientip

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient talks to the SOLIDserver REST API.
//
// TODO(efficientip): the endpoint paths and parameter names below follow the
// SOLIDserver REST service names (ip_find_free_address, ip_address_add,
// ip_address_delete, dns_rr_add, dns_rr_delete, dns_rr_list). They have not
// been checked against a live SOLIDserver or the API reference of the
// release rvav targets. Confirm them, the authentication headers and the
// response shapes before using this outside the fake.
type HTTPClient struct {
	base     *url.URL
	username string
	password string
	http     *http.Client
	// SiteID is the IPAM space the subnets live in (SOLIDserver "site").
	SiteID string
	// DNSServer is the SOLIDserver DNS server name records are created on.
	DNSServer string
}

// TODO(efficientip): verify each path against the SOLIDserver API reference.
const (
	pathFindFree   = "/rest/ip_find_free_address"
	pathAddressAdd = "/rest/ip_address_add"
	pathAddressDel = "/rest/ip_address_delete"
	pathRRAdd      = "/rest/dns_rr_add"
	pathRRDelete   = "/rest/dns_rr_delete"
	pathRRList     = "/rest/dns_rr_list"
)

func NewHTTPClient(rawURL, username, password string, insecure bool) (*HTTPClient, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("efficientip url %q is not valid", rawURL)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for lab SOLIDservers
	}
	return &HTTPClient{base: u, username: username, password: password,
		http: &http.Client{Transport: tr, Timeout: 30 * time.Second}}, nil
}

func (c *HTTPClient) do(ctx context.Context, method, path string, params url.Values) ([]map[string]any, error) {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// SOLIDserver accepts base64 credentials in these headers.
	req.Header.Set("X-IPM-Username", base64.StdEncoding.EncodeToString([]byte(c.username)))
	req.Header.Set("X-IPM-Password", base64.StdEncoding.EncodeToString([]byte(c.password)))
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("efficientip %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("efficientip %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	var out []map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("efficientip %s: decode response: %w", path, err)
		}
	}
	return out, nil
}

func str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	}
	return ""
}

func (c *HTTPClient) NextFree(ctx context.Context, s Subnet) (string, error) {
	res, err := c.do(ctx, http.MethodGet, pathFindFree, url.Values{"subnet_id": {s.ID}, "max_find": {"1"}})
	if err != nil {
		return "", err
	}
	if len(res) == 0 {
		return "", ErrSubnetFull
	}
	return str(res[0], "hostaddr"), nil
}

func (c *HTTPClient) Reserve(ctx context.Context, s Subnet, hostname string) (Address, error) {
	ip, err := c.NextFree(ctx, s)
	if err != nil {
		return Address{}, err
	}
	res, err := c.do(ctx, http.MethodPost, pathAddressAdd, url.Values{
		"site_id": {c.SiteID}, "hostaddr": {ip}, "name": {hostname},
		"ip_class_parameters": {"rvav_managed=1"},
	})
	if err != nil {
		return Address{}, err
	}
	a := Address{IP: ip, Subnet: s.CIDR, Hostname: hostname}
	if len(res) > 0 {
		a.ID = str(res[0], "ret_oid")
	}
	return a, nil
}

func (c *HTTPClient) Release(ctx context.Context, a Address) error {
	_, err := c.do(ctx, http.MethodDelete, pathAddressDel, url.Values{"ip_id": {a.ID}})
	return err
}

func (c *HTTPClient) CreateRecord(ctx context.Context, r Record) (Record, error) {
	res, err := c.do(ctx, http.MethodPost, pathRRAdd, url.Values{
		"dns_name": {c.DNSServer}, "rr_name": {r.FQDN}, "rr_type": {r.Type}, "value1": {r.Value},
	})
	if err != nil {
		return Record{}, err
	}
	if len(res) > 0 {
		r.ID = str(res[0], "ret_oid")
	}
	return r, nil
}

func (c *HTTPClient) DeleteRecord(ctx context.Context, r Record) error {
	_, err := c.do(ctx, http.MethodDelete, pathRRDelete, url.Values{"rr_id": {r.ID}})
	return err
}

func (c *HTTPClient) NameExists(ctx context.Context, fqdn string) (bool, error) {
	res, err := c.do(ctx, http.MethodGet, pathRRList, url.Values{"WHERE": {fmt.Sprintf("rr_full_name='%s'", fqdn)}})
	if err != nil {
		return false, err
	}
	return len(res) > 0, nil
}
