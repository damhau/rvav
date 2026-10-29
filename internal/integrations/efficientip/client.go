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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/observe"
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
	// Instance tells this rvav's objects from another's on a shared
	// SOLIDserver (InstanceParam); empty is "rvav".
	Instance string
}

// TagParam is the class parameter set on every address and record rvav
// creates, so the reconcile report can tell them apart.
const TagParam = "rvav_managed"

// OwnerParam is the class parameter naming what an address was reserved
// for (rvav's deployment), so a retried reservation takes back its own
// address and never one of the same name reserved by anyone else.
const OwnerParam = "rvav_owner"

// InstanceParam names the rvav instance that made an object (ADR-0020), so
// two instances sharing a SOLIDserver each report only their own.
const InstanceParam = "rvav_instance"

// tags are the class parameters rvav sets on what it creates.
func (c *HTTPClient) tags(extra url.Values) string {
	v := url.Values{TagParam: {"1"}, InstanceParam: {c.instance()}}
	for k, list := range extra {
		v[k] = list
	}
	return v.Encode()
}

func (c *HTTPClient) instance() string {
	if c.opts.Instance == "" {
		return "rvav"
	}
	return c.opts.Instance
}

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
	// Redirects are not followed: the native sign-in headers would go along
	// to whatever host the redirect names.
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPClient{opts: o, base: u, http: &http.Client{Transport: observe.Transport("efficientip", tr), Timeout: 30 * time.Second, CheckRedirect: noRedirect},
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

// subnet finds the subnet's ID and IPAM space: by ID when it is known and
// still has the subnet's range, else by its start address and size among
// terminal subnets.
func (c *HTTPClient) subnet(ctx context.Context, s Subnet) (subnetRef, error) {
	key := s.ID + " " + s.CIDR
	c.mu.Lock()
	ref, ok := c.subnets[key]
	c.mu.Unlock()
	if ok {
		return ref, nil
	}
	var prefix netip.Prefix
	if s.CIDR != "" {
		var err error
		if prefix, err = netip.ParsePrefix(s.CIDR); err != nil {
			return subnetRef{}, fmt.Errorf("subnet %q: %w", s.CIDR, err)
		}
		prefix = prefix.Masked()
	}
	inRange := func(r row) bool {
		return r["start_hostaddr"] == prefix.Addr().String() && prefixLen(r) == prefix.Bits()
	}
	var found []subnetRef
	if s.ID != "" {
		rows, err := c.call(ctx, http.MethodGet, "ip_block_subnet_list", list("subnet_id="+quote(s.ID), 1))
		if err != nil {
			return subnetRef{}, err
		}
		if len(rows) > 0 && (s.CIDR == "" || inRange(rows[0])) {
			found = append(found, subnetRef{ID: rows[0]["subnet_id"], Site: rows[0]["site_id"]})
		}
	}
	if len(found) == 0 && s.CIDR != "" {
		where := "start_hostaddr=" + quote(prefix.Addr().String()) + " and is_terminal='1'"
		if c.opts.Space != "" {
			where += " and site_name=" + quote(c.opts.Space)
		}
		rows, err := c.call(ctx, http.MethodGet, "ip_block_subnet_list", list(where, 10))
		if err != nil {
			return subnetRef{}, err
		}
		for _, r := range rows {
			if inRange(r) {
				found = append(found, subnetRef{ID: r["subnet_id"], Site: r["site_id"]})
			}
		}
	}
	switch {
	case len(found) == 0:
		return subnetRef{}, fmt.Errorf("%s: %w", strings.TrimSpace(key), errUnknownSubnet)
	case len(found) > 1:
		return subnetRef{}, fmt.Errorf("subnet %s is in several IPAM spaces; set the space rvav uses", s.CIDR)
	}
	c.mu.Lock()
	c.subnets[key] = found[0]
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

// free asks for up to n free addresses. SOLIDserver knows nothing of the
// gateway rvav reads from the subnet's class parameters, so it may offer
// it (and the network and broadcast addresses) when they are not
// registered: they are left out.
func (c *HTTPClient) free(ctx context.Context, s Subnet, ref subnetRef, n int) ([]string, error) {
	rows, err := c.call(ctx, http.MethodOptions, "ip_find_free_address",
		url.Values{"subnet_id": {ref.ID}, "max_find": {strconv.Itoa(n + 3)}})
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{s.Gateway: true}
	if p, err := netip.ParsePrefix(s.CIDR); err == nil && p.Bits() < 31 {
		p = p.Masked()
		last := p.Addr()
		for next := last.Next(); p.Contains(next); next = next.Next() {
			last = next
		}
		skip[p.Addr().String()], skip[last.String()] = true, true
	}
	var out []string
	for _, r := range rows {
		if ip := r["hostaddr"]; ip != "" && !skip[ip] && len(out) < n {
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
	free, err := c.free(ctx, s, ref, 1)
	if err != nil {
		return "", err
	}
	return free[0], nil
}

// Reserve returns the address named hostname in the subnet that was
// reserved for owner, or adds the first free one. An address of that name
// reserved by anyone else is ErrNameTaken. An address taken between the
// search and the add is skipped.
func (c *HTTPClient) Reserve(ctx context.Context, s Subnet, hostname, owner string) (Address, error) {
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
		params, _ := url.ParseQuery(existing[0]["ip_class_parameters"])
		if params.Get(TagParam) != "1" || params.Get(OwnerParam) != owner {
			return Address{}, fmt.Errorf("%s (%s): %w", hostname, existing[0]["hostaddr"], ErrNameTaken)
		}
		return Address{ID: existing[0]["ip_id"], IP: existing[0]["hostaddr"], Subnet: s.CIDR, Hostname: hostname}, nil
	}
	classParams := c.tags(url.Values{OwnerParam: {owner}})
	var last error
	for range 2 {
		free, err := c.free(ctx, s, ref, 5)
		if err != nil {
			return Address{}, err
		}
		for _, ip := range free {
			res, err := c.call(ctx, http.MethodPost, "ip_add", url.Values{"site_id": {ref.Site}, "hostaddr": {ip},
				"name": {hostname}, "ip_class_parameters": {classParams}, "add_flag": {"new_only"}})
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

// pendingDelete reports whether SOLIDserver has the DNS record queued for
// deletion: it stays listed, with delayed_delete_time set, until the DNS
// server has dropped it.
func pendingDelete(r row) bool {
	t := r["delayed_delete_time"]
	return t != "" && t != "0"
}

func live(rows []row) []row {
	return slices.DeleteFunc(rows, pendingDelete)
}

// gone reports whether the object with this ID no longer exists (or is
// being deleted).
func (c *HTTPClient) gone(ctx context.Context, service, key, id string) (bool, error) {
	rows, err := c.call(ctx, http.MethodGet, service, list(key+"="+quote(id), 1))
	return len(live(rows)) == 0, err
}

// addressID is the address's ID: its own, or found by subnet and IP when
// it still has one of the names ("" when EfficientIP doesn't have it, or
// the IP now belongs to another host).
func (c *HTTPClient) addressID(ctx context.Context, a Address, names ...string) (string, error) {
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
	if err != nil || len(rows) == 0 {
		return "", err
	}
	if !slices.ContainsFunc(append(names, a.Hostname), func(n string) bool { return n != "" && sameValue(n, rows[0]["name"]) }) {
		return "", nil
	}
	return rows[0]["ip_id"], nil
}

// Release deletes the address. Without its ID, it is found by IP and
// released only when it still has the name rvav gave it.
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
	id, err := c.addressID(ctx, a, hostname)
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
	rows, err := c.call(ctx, http.MethodGet, "dns_rr_list", list(where, 20))
	return live(rows), err
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
		"dns_name": {c.opts.DNSServer}, "rr_class_parameters": {c.tags(nil)}, "add_flag": {"new_only"}}
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

// UpdateRecord points the record (found by ID, or else by its value) to a
// new value in place. A record of the name and type that already has the
// value is adopted; when there is none to change, it is created.
func (c *HTTPClient) UpdateRecord(ctx context.Context, r Record, value string) (Record, error) {
	rows, err := c.records(ctx, r.FQDN, r.Type)
	if err != nil {
		return Record{}, err
	}
	var current row
	for _, existing := range rows {
		if sameValue(existing["value1"], value) {
			r.ID, r.Value = existing["rr_id"], value
			return r, nil
		}
		if r.ID != "" && existing["rr_id"] == r.ID || r.ID == "" && sameValue(existing["value1"], r.Value) {
			current = existing
		}
	}
	if current == nil {
		r.ID, r.Value = "", value
		return c.CreateRecord(ctx, r)
	}
	if _, err := c.call(ctx, http.MethodPut, "dns_rr_add",
		url.Values{"rr_id": {current["rr_id"]}, "value1": {value}, "add_flag": {"edit_only"}}); err != nil {
		return Record{}, err
	}
	r.ID, r.Value = current["rr_id"], value
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
// name, on any server, or an IPAM address has it as its name.
func (c *HTTPClient) NameExists(ctx context.Context, name string) (bool, error) {
	rows, err := c.call(ctx, http.MethodGet, "dns_rr_list",
		list("rr_full_name="+quote(fqdn(name))+" and (rr_type='A' or rr_type='AAAA' or rr_type='CNAME')", 20))
	if err != nil {
		return false, err
	}
	if len(live(rows)) > 0 {
		return true, nil
	}
	rows, err = c.call(ctx, http.MethodGet, "ip_address_list", list("name="+quote(fqdn(name)), 1))
	return len(rows) > 0, err
}

// pages calls a list service page after page, until a page is empty (the
// server may cap a page below the limit asked for).
func (c *HTTPClient) pages(ctx context.Context, service, where string) ([]row, error) {
	const size, most = 500, 200_000
	var out []row
	for {
		q := list(where, size)
		q.Set("offset", strconv.Itoa(len(out)))
		rows, err := c.call(ctx, http.MethodGet, service, q)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return out, nil
		}
		if out = append(out, rows...); len(out) > most {
			return nil, fmt.Errorf("%s: more than %d results", service, most)
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

var tagged = TagParam + "=1"

// ours says whether rvav made an object, this instance of it: those made
// before instances were recorded count as this instance's.
func (c *HTTPClient) ours(classParams string) bool {
	params, _ := url.ParseQuery(classParams)
	if params.Get(TagParam) != "1" {
		return false
	}
	instance := params.Get(InstanceParam)
	return instance == "" || instance == c.instance()
}

func (c *HTTPClient) Managed(ctx context.Context) ([]Address, []Record, error) {
	rows, err := c.pages(ctx, "ip_address_list", "ip_class_parameters like "+quote("%"+tagged+"%"))
	if err != nil {
		return nil, nil, err
	}
	var addresses []Address
	for _, r := range rows {
		if !c.ours(r["ip_class_parameters"]) {
			continue
		}
		subnet := r["subnet_name"]
		if bits := prefixLen(r); r["subnet_start_hostaddr"] != "" && bits >= 0 {
			subnet = r["subnet_start_hostaddr"] + "/" + strconv.Itoa(bits)
		}
		addresses = append(addresses, Address{ID: r["ip_id"], IP: r["hostaddr"], Subnet: subnet, Hostname: r["name"]})
	}
	rows, err = c.pages(ctx, "dns_rr_list", "rr_class_parameters like "+quote("%"+tagged+"%")+" and dns_name="+quote(c.opts.DNSServer))
	if err != nil {
		return nil, nil, err
	}
	var records []Record
	for _, r := range live(rows) {
		if c.ours(r["rr_class_parameters"]) {
			records = append(records, Record{ID: r["rr_id"], FQDN: r["rr_full_name"], Type: r["rr_type"], Value: r["value1"], Zone: r["dnszone_name"]})
		}
	}
	return addresses, records, nil
}

func (c *HTTPClient) Existing(ctx context.Context, addressIDs, recordIDs []string) (map[string]bool, map[string]bool, error) {
	addresses, err := c.existing(ctx, "ip_address_list", "ip_id", addressIDs)
	if err != nil {
		return nil, nil, err
	}
	records, err := c.existing(ctx, "dns_rr_list", "rr_id", recordIDs)
	return addresses, records, err
}

// existing asks for the IDs fifty at a time.
func (c *HTTPClient) existing(ctx context.Context, service, key string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(ids); start += 50 {
		chunk := ids[start:min(start+50, len(ids))]
		terms := make([]string, len(chunk))
		for i, id := range chunk {
			terms[i] = key + "=" + quote(id)
		}
		rows, err := c.call(ctx, http.MethodGet, service, list(strings.Join(terms, " or "), len(chunk)))
		if err != nil {
			return nil, err
		}
		for _, r := range live(rows) {
			out[r[key]] = true
		}
	}
	return out, nil
}

// Find looks up an existing VM's addresses (in rvav's space when it has
// one) and DNS records. It changes nothing.
func (c *HTTPClient) Find(ctx context.Context, ips, names []string) ([]Address, []Record, error) {
	var addresses []Address
	var records []Record
	candidates := append([]string{}, names...)
	for _, ip := range ips {
		where := "hostaddr=" + quote(ip)
		if c.opts.Space != "" {
			where += " and site_name=" + quote(c.opts.Space)
		}
		rows, err := c.call(ctx, http.MethodGet, "ip_address_list", list(where, 10))
		if err != nil {
			return nil, nil, err
		}
		if rows = live(rows); len(rows) > 0 {
			r := rows[0]
			subnet := r["subnet_name"]
			if bits := prefixLen(r); r["subnet_start_hostaddr"] != "" && bits >= 0 {
				subnet = r["subnet_start_hostaddr"] + "/" + strconv.Itoa(bits)
			}
			addresses = append(addresses, Address{ID: r["ip_id"], IP: r["hostaddr"], Subnet: subnet, Hostname: r["name"]})
			candidates = append(candidates, r["name"])
		}
		ptr, err := c.records(ctx, PTRName(ip), "PTR")
		if err != nil {
			return nil, nil, err
		}
		for _, r := range ptr {
			records = append(records, Record{ID: r["rr_id"], FQDN: r["rr_full_name"], Type: "PTR", Value: r["value1"], Zone: r["dnszone_name"]})
			candidates = append(candidates, r["value1"])
		}
	}
	seen := map[string]bool{}
	for _, n := range candidates {
		n = fqdn(n)
		if !strings.Contains(n, ".") || seen[n] {
			continue
		}
		seen[n] = true
		rows, err := c.records(ctx, n, "A")
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rows {
			if slices.Contains(ips, r["value1"]) {
				records = append(records, Record{ID: r["rr_id"], FQDN: r["rr_full_name"], Type: "A", Value: r["value1"], Zone: r["dnszone_name"]})
			}
		}
	}
	return addresses, records, nil
}
