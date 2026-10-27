package efficientip

import (
	"context"
	"crypto/sha3"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HTTPClient talks to the SOLIDserver REST API. Services, methods and
// parameters follow EfficientIP's SOLIDserverRest library (2.14.0) and the
// API reference it ships (SOLIDserver 9.0.1a): services under /rest/, the
// free address search under /rpc/ with OPTIONS, parameters in the query
// string, results as a JSON array of string fields, 204 for an empty
// result. See ADR-0015.
type HTTPClient struct {
	opts Options
	base *url.URL
	http *http.Client
	now  func() time.Time

	mu      sync.Mutex
	subnets map[string]subnetRef // by subnet ID and by CIDR
}

// Auth is how rvav signs in to SOLIDserver.
type Auth string

const (
	// AuthNative sends the user name and password, base64-encoded, in the
	// X-IPM-Username and X-IPM-Password headers.
	AuthNative Auth = "native"
	AuthBasic  Auth = "basic"
	// AuthToken signs each call with an API token: the key ID, and a SHA3-256
	// of the secret, the time, the method and the URL.
	AuthToken Auth = "token"
)

type Options struct {
	URL      string
	Auth     Auth
	Username string // the token's key ID with AuthToken
	Password string // the token's secret with AuthToken
	Insecure bool
	// Space limits the lookup of a subnet by CIDR to one IPAM space.
	Space string
	// DNSServer is the DNS server, or smart architecture, records are created on.
	DNSServer string
	// DNSView is the DNS view records go in, when the server has views.
	DNSView string
}

// TagParam is the class parameter set on every address and record rvav
// creates, so the reconcile report can tell them apart.
const TagParam = "rvav_managed"

var tag = url.Values{TagParam: {"1"}}.Encode()

// APIError is an error SOLIDserver answered with.
type APIError struct {
	Service string
	Status  int
	Errno   string
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Errno != "" {
		return fmt.Sprintf("%s: %s (errno %s)", e.Service, msg, e.Errno)
	}
	return e.Service + ": " + msg
}

// refused reports whether SOLIDserver turned the call down (a parameter, a
// duplicate, an object not found), as opposed to failing.
func refused(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return false
	}
	return e.Status < 500
}

var errUnknownSubnet = errors.New("subnet is not in EfficientIP")

type row = map[string]string

func New(o Options) (*HTTPClient, error) {
	u, err := url.Parse(o.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("efficientip url %q is not valid", o.URL)
	}
	switch o.Auth {
	case "":
		o.Auth = AuthNative
	case AuthNative, AuthBasic, AuthToken:
	default:
		return nil, fmt.Errorf("efficientip authentication %q is not native, basic or token", o.Auth)
	}
	if o.DNSServer == "" {
		return nil, errors.New("efficientip: the DNS server records are created on is not set")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if o.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for lab SOLIDservers
	}
	return &HTTPClient{opts: o, base: u, http: &http.Client{Transport: tr, Timeout: 30 * time.Second},
		now: time.Now, subnets: map[string]subnetRef{}}, nil
}

// call runs one service. A 429 is retried after its Retry-After, up to
// three times.
func (c *HTTPClient) call(ctx context.Context, method, service string, params url.Values) ([]row, error) {
	u := *c.base
	prefix := "/rest/"
	if method == http.MethodOptions {
		prefix = "/rpc/"
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + prefix + service
	u.RawQuery = params.Encode()
	for attempt := 1; ; attempt++ {
		rows, wait, err := c.once(ctx, method, service, u.String())
		if wait == 0 || attempt == 3 {
			return rows, err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *HTTPClient) once(ctx context.Context, method, service, target string) ([]row, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	switch c.opts.Auth {
	case AuthNative:
		req.Header.Set("X-IPM-Username", base64.StdEncoding.EncodeToString([]byte(c.opts.Username)))
		req.Header.Set("X-IPM-Password", base64.StdEncoding.EncodeToString([]byte(c.opts.Password)))
	case AuthBasic:
		req.SetBasicAuth(c.opts.Username, c.opts.Password)
	case AuthToken:
		ts := strconv.FormatInt(c.now().Unix(), 10)
		req.Header.Set("Authorization", "SDS "+c.opts.Username+":"+tokenSignature(c.opts.Password, ts, method, target))
		req.Header.Set("X-SDS-TS", ts)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", service, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusNoContent:
		return nil, 0, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, 0, &APIError{Service: service, Status: resp.StatusCode, Message: "SOLIDserver refused rvav's credentials"}
	case resp.StatusCode == http.StatusTooManyRequests:
		wait := time.Second
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			wait = min(time.Duration(s)*time.Second, 30*time.Second)
		}
		return nil, wait, &APIError{Service: service, Status: resp.StatusCode, Message: "rate limited"}
	}
	rows, decodeErr := decodeRows(body)
	if resp.StatusCode >= 300 {
		e := &APIError{Service: service, Status: resp.StatusCode}
		if len(rows) > 0 {
			e.Errno, e.Message = rows[0]["errno"], rows[0]["errmsg"]
		} else if s := strings.TrimSpace(string(body)); s != "" {
			e.Message = s[:min(len(s), 200)]
		}
		return nil, 0, e
	}
	if decodeErr != nil {
		return nil, 0, fmt.Errorf("%s: decode response: %w", service, decodeErr)
	}
	if len(rows) > 0 && rows[0]["errno"] != "" && rows[0]["errno"] != "0" {
		return nil, 0, &APIError{Service: service, Status: resp.StatusCode, Errno: rows[0]["errno"], Message: rows[0]["errmsg"]}
	}
	return rows, 0, nil
}

func tokenSignature(secret, ts, method, target string) string {
	sum := sha3.Sum256([]byte(secret + "\n" + ts + "\n" + method + "\n" + target))
	return hex.EncodeToString(sum[:])
}

// decodeRows reads SOLIDserver's array of objects, keeping every field as a
// string (numbers included).
func decodeRows(body []byte) ([]row, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]row, len(raw))
	for i, m := range raw {
		out[i] = row{}
		for k, v := range m {
			switch v := v.(type) {
			case string:
				out[i][k] = v
			case float64:
				out[i][k] = strconv.FormatFloat(v, 'f', -1, 64)
			case nil:
			default:
				b, _ := json.Marshal(v)
				out[i][k] = string(b)
			}
		}
	}
	return out, nil
}

// quote makes a string literal for a WHERE clause.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func list(where string, limit int) url.Values {
	return url.Values{"WHERE": {where}, "limit": {strconv.Itoa(limit)}}
}

type subnetRef struct{ ID, Site string }

// subnet finds the subnet's ID and IPAM space, by ID or else by its start
// address and size among terminal subnets.
func (c *HTTPClient) subnet(ctx context.Context, s Subnet) (subnetRef, error) {
	key := s.ID
	if key == "" {
		key = s.CIDR
	}
	c.mu.Lock()
	ref, ok := c.subnets[key]
	c.mu.Unlock()
	if ok {
		return ref, nil
	}
	var where string
	var prefix netip.Prefix
	if s.ID != "" {
		where = "subnet_id=" + quote(s.ID)
	} else {
		var err error
		if prefix, err = netip.ParsePrefix(s.CIDR); err != nil {
			return subnetRef{}, fmt.Errorf("subnet %q: %w", s.CIDR, err)
		}
		where = "start_hostaddr=" + quote(prefix.Masked().Addr().String()) + " and is_terminal='1'"
		if c.opts.Space != "" {
			where += " and site_name=" + quote(c.opts.Space)
		}
	}
	rows, err := c.call(ctx, http.MethodGet, "ip_block_subnet_list", list(where, 10))
	if err != nil {
		return subnetRef{}, err
	}
	var found []subnetRef
	for _, r := range rows {
		if s.ID == "" && prefixLen(r) != prefix.Bits() {
			continue
		}
		found = append(found, subnetRef{ID: r["subnet_id"], Site: r["site_id"]})
	}
	switch {
	case len(found) == 0:
		return subnetRef{}, fmt.Errorf("%s: %w", key, errUnknownSubnet)
	case len(found) > 1:
		return subnetRef{}, fmt.Errorf("subnet %s is in several IPAM spaces; set the space rvav uses", key)
	}
	c.mu.Lock()
	c.subnets[key] = found[0]
	c.subnets[found[0].ID] = found[0]
	c.mu.Unlock()
	return found[0], nil
}

// prefixLen is the subnet's prefix length, from subnet_prefix or from its
// size in addresses.
func prefixLen(r row) int {
	if p, err := strconv.Atoi(r["subnet_prefix"]); err == nil {
		return p
	}
	size, err := strconv.ParseUint(r["subnet_size"], 10, 64)
	if err != nil || size == 0 || size&(size-1) != 0 {
		return -1
	}
	return 32 - (bits.Len64(size) - 1)
}

func (c *HTTPClient) free(ctx context.Context, ref subnetRef, n int) ([]string, error) {
	rows, err := c.call(ctx, http.MethodOptions, "ip_find_free_address",
		url.Values{"subnet_id": {ref.ID}, "max_find": {strconv.Itoa(n)}})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		if ip := r["hostaddr"]; ip != "" {
			out = append(out, ip)
		}
	}
	if len(out) == 0 {
		return nil, ErrSubnetFull
	}
	return out, nil
}

func (c *HTTPClient) NextFree(ctx context.Context, s Subnet) (string, error) {
	ref, err := c.subnet(ctx, s)
	if err != nil {
		return "", err
	}
	free, err := c.free(ctx, ref, 1)
	if err != nil {
		return "", err
	}
	return free[0], nil
}

// Reserve returns the address already named hostname in the subnet, or
// adds the first free one. An address taken between the search and the
// add is skipped.
func (c *HTTPClient) Reserve(ctx context.Context, s Subnet, hostname string) (Address, error) {
	ref, err := c.subnet(ctx, s)
	if err != nil {
		return Address{}, err
	}
	existing, err := c.call(ctx, http.MethodGet, "ip_address_list",
		list("subnet_id="+quote(ref.ID)+" and name="+quote(hostname), 1))
	if err != nil {
		return Address{}, err
	}
	if len(existing) > 0 {
		return Address{ID: existing[0]["ip_id"], IP: existing[0]["hostaddr"], Subnet: s.CIDR, Hostname: hostname}, nil
	}
	var last error
	for range 2 {
		free, err := c.free(ctx, ref, 5)
		if err != nil {
			return Address{}, err
		}
		for _, ip := range free {
			res, err := c.call(ctx, http.MethodPost, "ip_add", url.Values{"site_id": {ref.Site}, "hostaddr": {ip},
				"name": {hostname}, "ip_class_parameters": {tag}, "add_flag": {"new_only"}})
			if refused(err) {
				last = err
				continue
			}
			if err != nil {
				return Address{}, err
			}
			return Address{ID: first(res, "ret_oid"), IP: ip, Subnet: s.CIDR, Hostname: hostname}, nil
		}
	}
	return Address{}, fmt.Errorf("no address in %s could be reserved: %w", s.CIDR, last)
}

func first(rows []row, key string) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[0][key]
}

// gone reports whether the object with this ID no longer exists.
func (c *HTTPClient) gone(ctx context.Context, service, key, id string) (bool, error) {
	rows, err := c.call(ctx, http.MethodGet, service, list(key+"="+quote(id), 1))
	return len(rows) == 0, err
}

// addressID is the address's ID: its own, or found by subnet and IP ("" when
// EfficientIP doesn't have it).
func (c *HTTPClient) addressID(ctx context.Context, a Address) (string, error) {
	if a.ID != "" {
		return a.ID, nil
	}
	ref, err := c.subnet(ctx, Subnet{CIDR: a.Subnet})
	if errors.Is(err, errUnknownSubnet) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	rows, err := c.call(ctx, http.MethodGet, "ip_address_list",
		list("subnet_id="+quote(ref.ID)+" and hostaddr="+quote(a.IP), 1))
	return first(rows, "ip_id"), err
}

func (c *HTTPClient) Release(ctx context.Context, a Address) error {
	id, err := c.addressID(ctx, a)
	if err != nil || id == "" {
		return err
	}
	_, err = c.call(ctx, http.MethodDelete, "ip_delete", url.Values{"ip_id": {id}})
	if refused(err) {
		if gone, gerr := c.gone(ctx, "ip_address_list", "ip_id", id); gerr == nil && gone {
			return nil
		}
	}
	return err
}

// RenameAddress gives the address a new name.
func (c *HTTPClient) RenameAddress(ctx context.Context, a Address, hostname string) error {
	id, err := c.addressID(ctx, a)
	if err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("%s is not in EfficientIP", a.IP)
	}
	_, err = c.call(ctx, http.MethodPut, "ip_add", url.Values{"ip_id": {id}, "name": {hostname}, "add_flag": {"edit_only"}})
	return err
}

func fqdn(name string) string { return strings.ToLower(strings.TrimSuffix(name, ".")) }

func sameValue(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}

// records lists the records of a name and type on rvav's DNS server.
func (c *HTTPClient) records(ctx context.Context, name, typ string) ([]row, error) {
	where := "rr_full_name=" + quote(fqdn(name)) + " and rr_type=" + quote(typ) + " and dns_name=" + quote(c.opts.DNSServer)
	if c.opts.DNSView != "" {
		where += " and dnsview_name=" + quote(c.opts.DNSView)
	}
	return c.call(ctx, http.MethodGet, "dns_rr_list", list(where, 20))
}

// CreateRecord adopts a record with the same name, type and value (one
// SOLIDserver may have made itself from the address), and refuses one
// that points elsewhere.
func (c *HTTPClient) CreateRecord(ctx context.Context, r Record) (Record, error) {
	rows, err := c.records(ctx, r.FQDN, r.Type)
	if err != nil {
		return Record{}, err
	}
	for _, existing := range rows {
		if sameValue(existing["value1"], r.Value) {
			r.ID = existing["rr_id"]
			return r, nil
		}
	}
	if len(rows) > 0 {
		return Record{}, fmt.Errorf("%s record %s already points to %s", r.Type, r.FQDN, rows[0]["value1"])
	}
	params := url.Values{"rr_name": {fqdn(r.FQDN)}, "rr_type": {r.Type}, "value1": {r.Value},
		"dns_name": {c.opts.DNSServer}, "rr_class_parameters": {tag}, "add_flag": {"new_only"}}
	if c.opts.DNSView != "" {
		params.Set("dnsview_name", c.opts.DNSView)
	}
	if r.Zone != "" && r.Type != "PTR" {
		params.Set("dnszone_name", r.Zone)
	}
	res, err := c.call(ctx, http.MethodPost, "dns_rr_add", params)
	if err != nil {
		return Record{}, err
	}
	r.ID = first(res, "ret_oid")
	return r, nil
}

func (c *HTTPClient) DeleteRecord(ctx context.Context, r Record) error {
	id := r.ID
	if id == "" {
		rows, err := c.records(ctx, r.FQDN, r.Type)
		if err != nil {
			return err
		}
		for _, existing := range rows {
			if sameValue(existing["value1"], r.Value) {
				id = existing["rr_id"]
			}
		}
		if id == "" {
			return nil
		}
	}
	_, err := c.call(ctx, http.MethodDelete, "dns_rr_delete", url.Values{"rr_id": {id}})
	if refused(err) {
		if gone, gerr := c.gone(ctx, "dns_rr_list", "rr_id", id); gerr == nil && gone {
			return nil
		}
	}
	return err
}

// NameExists reports whether DNS has an A, AAAA or CNAME record for the
// name, on any server.
func (c *HTTPClient) NameExists(ctx context.Context, name string) (bool, error) {
	rows, err := c.call(ctx, http.MethodGet, "dns_rr_list",
		list("rr_full_name="+quote(fqdn(name))+" and (rr_type='A' or rr_type='AAAA' or rr_type='CNAME')", 1))
	return len(rows) > 0, err
}

// pages calls a list service page after page.
func (c *HTTPClient) pages(ctx context.Context, service, where string) ([]row, error) {
	const size = 500
	var out []row
	for offset := 0; ; offset += size {
		q := list(where, size)
		q.Set("offset", strconv.Itoa(offset))
		rows, err := c.call(ctx, http.MethodGet, service, q)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(rows) < size {
			return out, nil
		}
	}
}

func (c *HTTPClient) Subnets(ctx context.Context) ([]SubnetInfo, error) {
	where := "is_terminal='1'"
	if c.opts.Space != "" {
		where += " and site_name=" + quote(c.opts.Space)
	}
	rows, err := c.pages(ctx, "ip_block_subnet_list", where)
	if err != nil {
		return nil, err
	}
	out := make([]SubnetInfo, 0, len(rows))
	for _, r := range rows {
		bits := prefixLen(r)
		if bits < 0 {
			continue
		}
		params, _ := url.ParseQuery(r["subnet_class_parameters"])
		out = append(out, SubnetInfo{ID: r["subnet_id"], Name: r["subnet_name"], Space: r["site_name"],
			CIDR: r["start_hostaddr"] + "/" + strconv.Itoa(bits), Params: params})
	}
	return out, nil
}

func (c *HTTPClient) Zones(ctx context.Context) ([]string, error) {
	where := "dns_name=" + quote(c.opts.DNSServer) + " and dnszone_is_reverse='0'"
	if c.opts.DNSView != "" {
		where += " and dnsview_name=" + quote(c.opts.DNSView)
	}
	rows, err := c.pages(ctx, "dns_zone_list", where)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r["dnszone_name"])
	}
	return out, nil
}
