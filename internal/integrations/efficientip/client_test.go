package efficientip

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type call struct {
	Method, Path string
	Query        url.Values
}

// lab is a fake SOLIDserver with one subnet and its zones, recording each
// call made to it; before, when set, runs ahead of the fake.
type lab struct {
	fake   *FakeServer
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []call
	before func(w http.ResponseWriter, r *http.Request) bool
}

func newLab(t *testing.T) *lab {
	t.Helper()
	l := &lab{fake: NewFakeServer()}
	l.fake.Username, l.fake.Password = "rvav", "s3cret"
	l.fake.TokenID, l.fake.TokenSecret = "key01", "token-secret"
	if _, err := l.fake.AddSubnet("lab", "10.31.10.0/24", "app-dev", map[string]string{"gateway": "10.31.10.1",
		"vsphere_network": "dvpg-app-dev-vl310"}); err != nil {
		t.Fatal(err)
	}
	l.fake.AddZone("ns1.corp.example", "corp.example")
	l.fake.AddZone("ns1.corp.example", "10.31.10.in-addr.arpa")
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		l.calls = append(l.calls, call{r.Method, r.URL.Path, r.URL.Query()})
		before := l.before
		l.mu.Unlock()
		if before != nil && before(w, r) {
			return
		}
		l.fake.ServeHTTP(w, r)
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *lab) client(t *testing.T, auth Auth, user, pass string) *HTTPClient {
	t.Helper()
	c, err := New(Options{URL: l.srv.URL, Auth: auth, Username: user, Password: pass, DNSServer: "ns1.corp.example"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (l *lab) setBefore(fn func(w http.ResponseWriter, r *http.Request) bool) {
	l.mu.Lock()
	l.before = fn
	l.mu.Unlock()
}

func (l *lab) last(method, path string) call {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.calls) - 1; i >= 0; i-- {
		if l.calls[i].Method == method && l.calls[i].Path == path {
			return l.calls[i]
		}
	}
	return call{}
}

var subnet = Subnet{CIDR: "10.31.10.0/24", Gateway: "10.31.10.1"}

func TestAddresses(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	l.fake.InUse = func(context.Context, string) ([]string, error) { return []string{"10.31.10.20"}, nil }
	c := l.client(t, AuthNative, "rvav", "s3cret")

	next, err := c.NextFree(ctx, subnet)
	if err != nil || next != "10.31.10.21" {
		t.Fatalf("next free = %q, %v (want .21, .20 is in use)", next, err)
	}
	if got := l.last("OPTIONS", "/rpc/ip_find_free_address").Query; got.Get("subnet_id") == "" || got.Get("max_find") != "1" {
		t.Fatalf("free address search = %v", got)
	}
	a, err := c.Reserve(ctx, subnet, "web-dev-app001.corp.example")
	if err != nil || a.IP != "10.31.10.21" || a.ID == "" {
		t.Fatalf("reserve = %+v, %v", a, err)
	}
	add := l.last("POST", "/rest/ip_add").Query
	want := url.Values{"site_id": {add.Get("site_id")}, "hostaddr": {"10.31.10.21"}, "name": {"web-dev-app001.corp.example"},
		"ip_class_parameters": {"rvav_managed=1"}, "add_flag": {"new_only"}}
	if add.Encode() != want.Encode() || add.Get("site_id") == "" {
		t.Fatalf("ip_add = %v", add)
	}
	again, err := c.Reserve(ctx, subnet, "web-dev-app001.corp.example")
	if err != nil || again != a {
		t.Fatalf("reserving the same host twice gave %+v then %+v (%v)", a, again, err)
	}

	// An address taken between the search and the add is skipped.
	l.setBefore(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && r.URL.Path == "/rest/ip_add" && r.URL.Query().Get("hostaddr") == "10.31.10.22" {
			l.setBefore(nil)
			other := l.client(t, AuthNative, "rvav", "s3cret")
			if _, err := other.call(r.Context(), "POST", "ip_add", url.Values{"site_id": {r.URL.Query().Get("site_id")},
				"hostaddr": {"10.31.10.22"}, "name": {"someone-else"}}); err != nil {
				t.Error(err)
			}
		}
		return false
	})
	b, err := c.Reserve(ctx, subnet, "web-dev-app002.corp.example")
	if err != nil || b.IP != "10.31.10.23" {
		t.Fatalf("reserve after a race = %+v, %v", b, err)
	}

	if err := c.Release(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got := l.last("DELETE", "/rest/ip_delete").Query; got.Get("ip_id") != a.ID {
		t.Fatalf("ip_delete = %v", got)
	}
	if err := c.Release(ctx, a); err != nil {
		t.Fatalf("releasing twice: %v", err)
	}
	if err := c.Release(ctx, Address{IP: b.IP, Subnet: subnet.CIDR}); err != nil {
		t.Fatalf("release without an ID: %v", err)
	}
	if next, _ := c.NextFree(ctx, subnet); next != "10.31.10.21" {
		t.Fatalf("after releasing, next free = %s", next)
	}
	if err := c.Release(ctx, Address{IP: "10.99.0.5", Subnet: "10.99.0.0/24"}); err != nil {
		t.Fatalf("release in a subnet EfficientIP no longer has: %v", err)
	}
}

func TestSubnetLookup(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.client(t, AuthNative, "rvav", "s3cret")
	if _, err := c.NextFree(ctx, Subnet{CIDR: "10.31.10.0/25"}); !errors.Is(err, errUnknownSubnet) {
		t.Fatalf("a /25 where EfficientIP has a /24 = %v", err)
	}
	if got := l.last("GET", "/rest/ip_block_subnet_list").Query.Get("WHERE"); got != "start_hostaddr='10.31.10.0' and is_terminal='1'" {
		t.Fatalf("subnet lookup WHERE = %s", got)
	}
	id, _ := l.fake.AddSubnet("other", "10.31.10.0/24", "copy", nil)
	if _, err := c.NextFree(ctx, subnet); err == nil || !strings.Contains(err.Error(), "several IPAM spaces") {
		t.Fatalf("same CIDR in two spaces = %v", err)
	}
	if ip, err := c.NextFree(ctx, Subnet{ID: id, CIDR: subnet.CIDR}); err != nil || ip != "10.31.10.20" {
		t.Fatalf("by ID = %s, %v", ip, err)
	}
	spaced, _ := New(Options{URL: l.srv.URL, Username: "rvav", Password: "s3cret", DNSServer: "ns1.corp.example", Space: "lab"})
	if _, err := spaced.NextFree(ctx, subnet); err != nil {
		t.Fatalf("with the space set: %v", err)
	}

	small, _ := l.fake.AddSubnet("lab", "10.0.0.0/30", "tiny", map[string]string{"gateway": "10.0.0.1"})
	l.fake.FirstHost = 1
	if _, err := c.Reserve(ctx, Subnet{ID: small}, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Reserve(ctx, Subnet{ID: small}, "b"); !errors.Is(err, ErrSubnetFull) {
		t.Fatalf("full subnet = %v", err)
	}
}

func TestRecords(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.client(t, AuthNative, "rvav", "s3cret")
	a, err := c.CreateRecord(ctx, Record{FQDN: "web-dev-app001.corp.example", Type: "A", Value: "10.31.10.21", Zone: "corp.example"})
	if err != nil || a.ID == "" {
		t.Fatalf("A = %+v, %v", a, err)
	}
	add := l.last("POST", "/rest/dns_rr_add").Query
	want := url.Values{"rr_name": {"web-dev-app001.corp.example"}, "rr_type": {"A"}, "value1": {"10.31.10.21"},
		"dns_name": {"ns1.corp.example"}, "dnszone_name": {"corp.example"}, "rr_class_parameters": {"rvav_managed=1"},
		"add_flag": {"new_only"}}
	if add.Encode() != want.Encode() {
		t.Fatalf("dns_rr_add = %v", add)
	}
	if again, err := c.CreateRecord(ctx, Record{FQDN: "WEB-dev-app001.corp.example.", Type: "A", Value: "10.31.10.21"}); err != nil || again.ID != a.ID {
		t.Fatalf("the same record again = %+v, %v", again, err)
	}
	if _, err := c.CreateRecord(ctx, Record{FQDN: "web-dev-app001.corp.example", Type: "A", Value: "10.0.0.1"}); err == nil ||
		!strings.Contains(err.Error(), "already points to 10.31.10.21") {
		t.Fatalf("a conflicting A record = %v", err)
	}
	ptr, err := c.CreateRecord(ctx, Record{FQDN: PTRName("10.31.10.21"), Type: "PTR", Value: "web-dev-app001.corp.example", Zone: "in-addr.arpa"})
	if err != nil {
		t.Fatalf("PTR: %v", err)
	}
	if got := l.last("POST", "/rest/dns_rr_add").Query; got.Has("dnszone_name") {
		t.Fatalf("a PTR names its zone: %v", got)
	}
	if ok, err := c.NameExists(ctx, "web-dev-app001.corp.example"); !ok || err != nil {
		t.Fatalf("name exists = %v, %v", ok, err)
	}
	if ok, err := c.NameExists(ctx, "web-dev-app009.corp.example"); ok || err != nil {
		t.Fatalf("a free name exists = %v, %v", ok, err)
	}
	if err := c.DeleteRecord(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRecord(ctx, a); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
	if err := c.DeleteRecord(ctx, Record{FQDN: ptr.FQDN, Type: "PTR", Value: ptr.Value}); err != nil {
		t.Fatalf("delete without an ID: %v", err)
	}
	if ok, _ := c.NameExists(ctx, "web-dev-app001.corp.example"); ok {
		t.Fatal("the name still exists after deleting its record")
	}
	if _, err := c.CreateRecord(ctx, Record{FQDN: "x.other.example", Type: "A", Value: "10.0.0.1"}); err == nil ||
		!strings.Contains(err.Error(), "no zone on ns1.corp.example holds x.other.example") {
		t.Fatalf("a record in no zone = %v", err)
	}
}

func TestAuthAndErrors(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	for _, c := range []*HTTPClient{l.client(t, AuthBasic, "rvav", "s3cret"), l.client(t, AuthToken, "key01", "token-secret")} {
		if _, err := c.NextFree(ctx, subnet); err != nil {
			t.Fatalf("%s: %v", c.opts.Auth, err)
		}
	}
	tok := l.client(t, AuthToken, "key01", "token-secret")
	tok.now = func() time.Time { return time.Unix(1700000000, 0) }
	if _, err := tok.NextFree(ctx, Subnet{ID: "101"}); err != nil {
		t.Fatalf("token at a fixed time: %v", err)
	}

	var apiErr *APIError
	if _, err := l.client(t, AuthNative, "rvav", "wrong").NextFree(ctx, subnet); !errors.As(err, &apiErr) || apiErr.Status != 401 ||
		!strings.Contains(err.Error(), "refused rvav's credentials") {
		t.Fatalf("wrong password = %v", err)
	}
	if _, err := New(Options{URL: l.srv.URL, Auth: "kerberos", DNSServer: "ns1"}); err == nil {
		t.Fatal("unknown authentication accepted")
	}

	// 429 is retried after Retry-After.
	c := l.client(t, AuthNative, "rvav", "s3cret")
	var limited atomic.Int32
	l.setBefore(func(w http.ResponseWriter, r *http.Request) bool {
		if limited.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	})
	if _, err := c.NextFree(ctx, subnet); err != nil || limited.Load() < 2 {
		t.Fatalf("after a 429: %v (%d calls)", err, limited.Load())
	}
	l.setBefore(nil)

	// SOLIDserver's errmsg is in the error.
	_, err := c.call(ctx, "POST", "ip_add", url.Values{"hostaddr": {"10.31.10.50"}})
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || !strings.Contains(err.Error(), "ip_add: mandatory parameters") || !refused(err) {
		t.Fatalf("missing parameters = %v", err)
	}
}

func TestWhere(t *testing.T) {
	r := row{"rr_full_name": "o'hara.corp.example", "rr_type": "CNAME", "dns_name": "ns1"}
	for where, want := range map[string]bool{
		"rr_full_name='o''hara.corp.example'":                                     true,
		"rr_full_name='O''HARA.corp.example' and dns_name=ns1":                    true,
		"rr_full_name like '%.corp.example' and (rr_type='A' or rr_type='CNAME')": true,
		"rr_type='A' or rr_type='AAAA'":                                           false,
		"(rr_type='A' or rr_type='CNAME') and dns_name!='ns1'":                    false,
		"origin='x' or rr_type='CNAME'":                                           true,
	} {
		match, err := parseWhere(where)
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		if match(r) != want {
			t.Errorf("%s = %v", where, !want)
		}
	}
	for _, bad := range []string{"rr_type=", "rr_type='A", "(rr_type='A'", "rr_type ~ 'A'"} {
		if _, err := parseWhere(bad); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
}
