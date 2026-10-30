package efficientip

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FakeServer is an in-memory SOLIDserver answering the REST services
// HTTPClient calls, with the same parameters, mandatory checks, add_flag
// rules and result shapes (every field a string, 204 when empty, errors as
// errno and errmsg). Subnets and DNS zones are added with AddSubnet and
// AddZone. Addresses below FirstHost and those InUse reports are never
// offered as free; like SOLIDserver, it knows nothing of the gateway class
// parameter. A deleted DNS record stays listed, with delayed_delete_time
// set, for DeleteDelay, as while SOLIDserver waits for the DNS server.
type FakeServer struct {
	// Credentials accepted, as native headers or basic authentication; or
	// an API token.
	Username, Password   string
	TokenID, TokenSecret string
	FirstHost            int
	InUse                func(ctx context.Context, cidr string) ([]string, error)
	DeleteDelay          time.Duration
	// Latency is added to every answer, as a real SOLIDserver's.
	Latency time.Duration

	mu        sync.Mutex
	idBase    int
	seq       int
	sites     map[string]string // IPAM space name -> ID
	servers   map[string]string // DNS server name -> ID
	subnets   []row
	addresses []row
	zones     []row
	records   []row
	deleting  map[string]time.Time // record ID -> when it was deleted
}

// What the development stack's fake uses.
const (
	FakeUser      = "rvav"
	FakePassword  = "rvav"
	FakeSpace     = "rvav-lab"
	FakeDNSServer = "ns1.corp.example"
)

// The subnet class parameters rvav reads: the vSphere network the subnet
// is on, its gateway, DNS zone and DNS servers (see ADR-0015).
const (
	ParamNetwork    = "vsphere_network"
	ParamGateway    = "gateway"
	ParamZone       = "domain"
	ParamDNSServers = "dns_servers"
)

func NewFakeServer() *FakeServer {
	return &FakeServer{Username: FakeUser, Password: FakePassword, FirstHost: 20, DeleteDelay: 2 * time.Second,
		idBase: 100, sites: map[string]string{}, servers: map[string]string{}, deleting: map[string]time.Time{}}
}

// UniqueIDs makes the fake's object IDs start from the time, so a fake
// started again (it keeps its state in memory) never reuses an ID rvav
// has recorded, as SOLIDserver never does. Call it before adding anything.
func (f *FakeServer) UniqueIDs() {
	f.idBase = int(time.Now().Unix()) * 1000
}

func (f *FakeServer) id() string {
	f.seq++
	return strconv.Itoa(f.idBase + f.seq)
}

func (f *FakeServer) site(name string) string {
	if id, ok := f.sites[name]; ok {
		return id
	}
	id := f.id()
	f.sites[name] = id
	return id
}

// AddSubnet adds a terminal subnet to an IPAM space, with its class
// parameters, and returns its ID.
func (f *FakeServer) AddSubnet(space, cidr, name string, params map[string]string) (string, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p = p.Masked()
	start := p.Addr().String()
	for _, s := range f.subnets {
		if s["start_hostaddr"] == start && s["site_name"] == space {
			return s["subnet_id"], nil
		}
	}
	end := p.Addr()
	for next := end.Next(); p.Contains(next); next = next.Next() {
		end = next
	}
	cp := url.Values{}
	for k, v := range params {
		cp.Set(k, v)
	}
	id := f.id()
	f.subnets = append(f.subnets, row{"subnet_id": id, "subnet_name": name, "start_hostaddr": start,
		"end_hostaddr": end.String(), "subnet_size": strconv.Itoa(1 << (32 - p.Bits())), "subnet_prefix": strconv.Itoa(p.Bits()),
		"is_terminal": "1", "subnet_level": "1", "site_id": f.site(space), "site_name": space,
		"subnet_class_name": "", "subnet_class_parameters": cp.Encode()})
	return id, nil
}

// AddBlock adds a block (a non-terminal subnet) to an IPAM space, for the
// subnets of NSX segments to be carved from, and returns its ID.
func (f *FakeServer) AddBlock(space, cidr, name string) (string, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p = p.Masked()
	id := f.id()
	f.subnets = append(f.subnets, row{"subnet_id": id, "subnet_name": name, "start_hostaddr": p.Addr().String(),
		"end_hostaddr": lastAddr(p).String(), "subnet_size": strconv.Itoa(1 << (32 - p.Bits())), "subnet_prefix": strconv.Itoa(p.Bits()),
		"is_terminal": "0", "subnet_level": "0", "parent_subnet_id": "0", "site_id": f.site(space), "site_name": space,
		"subnet_class_name": "", "subnet_class_parameters": ""})
	return id, nil
}

func lastAddr(p netip.Prefix) netip.Addr {
	end := p.Addr()
	for next := end.Next(); p.Contains(next); next = next.Next() {
		end = next
	}
	return end
}

func prefixOf(r row) netip.Prefix {
	p, _ := netip.ParsePrefix(cidrOf(r))
	return p
}

// findFreeSubnet offers the first ranges of the prefix asked inside a
// block that overlap no subnet in it, as ip_find_free_subnet does.
func (f *FakeServer) findFreeSubnet(q url.Values) ([]row, *fakeError) {
	b := f.subnetByID(q.Get("block_id"))
	if b == nil || b["is_terminal"] != "0" {
		return nil, failf(http.StatusBadRequest, "1101", "block %s not found", q.Get("block_id"))
	}
	bits, err := strconv.Atoi(q.Get("prefix"))
	block := prefixOf(b)
	if err != nil || bits < block.Bits() || bits > 32 {
		return nil, failf(http.StatusBadRequest, "1102", "prefix %q does not fit in %s", q.Get("prefix"), cidrOf(b))
	}
	most, _ := strconv.Atoi(q.Get("max_find"))
	var out []row
	for cand := netip.PrefixFrom(block.Addr(), bits); block.Contains(cand.Addr()) && (most <= 0 || len(out) < most); {
		free := true
		for _, s := range f.subnets {
			if s["parent_subnet_id"] == b["subnet_id"] && prefixOf(s).Overlaps(cand) {
				free = false
				break
			}
		}
		if free {
			out = append(out, row{"start_hostaddr": cand.Addr().String(), "subnet_prefix": strconv.Itoa(bits)})
		}
		next := lastAddr(cand).Next()
		if !next.IsValid() {
			break
		}
		cand = netip.PrefixFrom(next, bits)
	}
	return out, nil
}

// addSubnet adds a terminal subnet inside a block, refusing one that
// overlaps another, as ip_subnet_add does.
func (f *FakeServer) addSubnet(q url.Values) ([]row, *fakeError) {
	b := f.subnetByID(q.Get("parent_subnet_id"))
	if b == nil {
		return nil, failf(http.StatusBadRequest, "1101", "parent subnet %s not found", q.Get("parent_subnet_id"))
	}
	p, err := netip.ParsePrefix(q.Get("subnet_addr") + "/" + q.Get("subnet_prefix"))
	if err != nil || !prefixOf(b).Contains(p.Addr()) || p.Bits() < prefixOf(b).Bits() {
		return nil, failf(http.StatusBadRequest, "1103", "subnet %s/%s is not inside %s", q.Get("subnet_addr"), q.Get("subnet_prefix"), cidrOf(b))
	}
	p = p.Masked()
	for _, s := range f.subnets {
		if s["parent_subnet_id"] == b["subnet_id"] && prefixOf(s).Overlaps(p) {
			return nil, failf(http.StatusBadRequest, "1104", "subnet %s overlaps %s", p, cidrOf(s))
		}
	}
	id := f.id()
	f.subnets = append(f.subnets, row{"subnet_id": id, "subnet_name": q.Get("subnet_name"), "start_hostaddr": p.Addr().String(),
		"end_hostaddr": lastAddr(p).String(), "subnet_size": strconv.Itoa(1 << (32 - p.Bits())), "subnet_prefix": strconv.Itoa(p.Bits()),
		"is_terminal": cmp.Or(q.Get("is_terminal"), "1"), "subnet_level": "1", "parent_subnet_id": b["subnet_id"], "site_id": b["site_id"],
		"site_name": b["site_name"], "subnet_class_name": "", "subnet_class_parameters": q.Get("subnet_class_parameters")})
	return []row{{"ret_oid": id}}, nil
}

// deleteSubnet deletes a subnet; SOLIDserver refuses one that still has
// addresses.
func (f *FakeServer) deleteSubnet(q url.Values) ([]row, *fakeError) {
	id := q.Get("subnet_id")
	if f.subnetByID(id) == nil {
		return nil, failf(http.StatusBadRequest, "1101", "subnet %s not found", id)
	}
	for _, a := range f.addresses {
		if a["subnet_id"] == id {
			return nil, failf(http.StatusBadRequest, "1105", "subnet %s still has address %s", id, a["hostaddr"])
		}
	}
	f.subnets = slices.DeleteFunc(f.subnets, func(s row) bool { return s["subnet_id"] == id })
	return []row{{"ret_oid": id}}, nil
}

// SetSubnetParams replaces a subnet's class parameters.
func (f *FakeServer) SetSubnetParams(id string, params map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := url.Values{}
	for k, v := range params {
		cp.Set(k, v)
	}
	if s := f.subnetByID(id); s != nil {
		s["subnet_class_parameters"] = cp.Encode()
	}
}

// AddZone adds a DNS zone on a server; zones ending in in-addr.arpa are
// reverse zones.
func (f *FakeServer) AddZone(server, zone string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	zone = fqdn(zone)
	for _, z := range f.zones {
		if z["dns_name"] == server && z["dnszone_name"] == zone {
			return
		}
	}
	if f.servers[server] == "" {
		f.servers[server] = f.id()
	}
	reverse := "0"
	if strings.HasSuffix(zone, "in-addr.arpa") {
		reverse = "1"
	}
	f.zones = append(f.zones, row{"dnszone_id": f.id(), "dnszone_name": zone, "dnszone_name_utf": zone, "dns_name": server,
		"dns_id": f.servers[server], "dnszone_type": "master", "dnszone_is_reverse": reverse, "dnsview_name": ""})
}

// FakeNetwork is a vSphere network and the subnet it is on.
type FakeNetwork struct {
	Network, CIDR, Gateway, Zone string
	DNSServers                   []string
}

// AddNetwork adds the network's subnet to FakeSpace, with the class
// parameters rvav reads, and its forward and reverse zones on
// FakeDNSServer.
func (f *FakeServer) AddNetwork(n FakeNetwork) error {
	if _, err := f.AddSubnet(FakeSpace, n.CIDR, n.Network, map[string]string{ParamNetwork: n.Network,
		ParamGateway: n.Gateway, ParamZone: n.Zone, ParamDNSServers: strings.Join(n.DNSServers, ",")}); err != nil {
		return err
	}
	f.AddZone(FakeDNSServer, n.Zone)
	f.AddZone(FakeDNSServer, "in-addr.arpa")
	return nil
}

// Start serves the fake on addr ("127.0.0.1:0" for any free port) until
// ctx ends, and returns its URL.
func (f *FakeServer) Start(ctx context.Context, addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	return "http://" + ln.Addr().String(), nil
}

// Client returns a client signed in to the fake at url.
func (f *FakeServer) Client(url string) (*HTTPClient, error) {
	return New(Options{URL: url, Username: f.Username, Password: f.Password, DNSServer: FakeDNSServer})
}

type fakeError struct {
	status int
	errno  string
	msg    string
}

func failf(status int, errno, format string, args ...any) *fakeError {
	return &fakeError{status: status, errno: errno, msg: fmt.Sprintf(format, args...)}
}

func (f *FakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/readyz" { // for the container probe; not a SOLIDserver service
		w.WriteHeader(http.StatusOK)
		return
	}
	time.Sleep(f.Latency)
	w.Header().Set("Content-Type", "application/json")
	if !f.authenticated(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`[{"errno":"-1","errmsg":"authentication failed"}]`))
		return
	}
	q := r.URL.Query()
	f.mu.Lock()
	rows, ferr := f.serve(r.Context(), r.Method, r.URL.Path, q)
	f.mu.Unlock()
	if ferr != nil {
		w.WriteHeader(ferr.status)
		_ = json.NewEncoder(w).Encode([]row{{"errno": ferr.errno, "errmsg": ferr.msg, "severity": "error"}})
		return
	}
	if len(rows) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = json.NewEncoder(w).Encode(rows)
}

func (f *FakeServer) authenticated(r *http.Request) bool {
	if user, err := base64.StdEncoding.DecodeString(r.Header.Get("X-IPM-Username")); err == nil && len(user) > 0 {
		pass, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-IPM-Password"))
		return string(user) == f.Username && string(pass) == f.Password
	}
	if user, pass, ok := r.BasicAuth(); ok {
		return user == f.Username && pass == f.Password
	}
	if keyID, sig, ok := strings.Cut(strings.TrimPrefix(r.Header.Get("Authorization"), "SDS "), ":"); ok && f.TokenID != "" {
		target := "http://" + r.Host + r.URL.RequestURI()
		return keyID == f.TokenID && sig == tokenSignature(f.TokenSecret, r.Header.Get("X-SDS-TS"), r.Method, target)
	}
	return false
}

func (f *FakeServer) serve(ctx context.Context, method, path string, q url.Values) ([]row, *fakeError) {
	f.purge()
	switch method + " " + path {
	case "OPTIONS /rpc/ip_find_free_address":
		return f.findFree(ctx, q)
	case "GET /rest/ip_block_subnet_list":
		return listRows(f.subnets, q)
	case "GET /rest/ip_address_list":
		return listRows(f.addresses, q)
	case "POST /rest/ip_add", "PUT /rest/ip_add":
		return f.addAddress(method, q)
	case "DELETE /rest/ip_delete":
		return f.deleteAddress(q)
	case "OPTIONS /rpc/ip_find_free_subnet":
		return f.findFreeSubnet(q)
	case "POST /rest/ip_subnet_add":
		return f.addSubnet(q)
	case "DELETE /rest/ip_subnet_delete":
		return f.deleteSubnet(q)
	case "GET /rest/dns_zone_list":
		return listRows(f.zones, q)
	case "GET /rest/dns_rr_list":
		return listRows(f.records, q)
	case "POST /rest/dns_rr_add":
		return f.addRecord(q)
	case "PUT /rest/dns_rr_add":
		return f.editRecord(q)
	case "DELETE /rest/dns_rr_delete":
		return f.deleteRecord(q)
	}
	return nil, failf(http.StatusNotFound, "-1", "unknown service %s %s", method, path)
}

// listRows filters with WHERE, sorts with ORDERBY (one field) and pages
// with offset and limit.
func listRows(all []row, q url.Values) ([]row, *fakeError) {
	match, err := parseWhere(q.Get("WHERE"))
	if err != nil {
		return nil, failf(http.StatusBadRequest, "-1", "%v", err)
	}
	var out []row
	for _, r := range all {
		if match(r) {
			out = append(out, withErrno(r))
		}
	}
	if by := q.Get("ORDERBY"); by != "" {
		slices.SortStableFunc(out, func(a, b row) int { return cmp.Compare(a[by], b[by]) })
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, err2 := strconv.Atoi(q.Get("limit"))
	if offset > len(out) {
		offset = len(out)
	}
	out = out[offset:]
	if err2 == nil && limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func withErrno(r row) row {
	c := make(row, len(r)+1)
	for k, v := range r {
		c[k] = v
	}
	c["errno"] = "0"
	return c
}

func (f *FakeServer) subnetByID(id string) row {
	for _, s := range f.subnets {
		if s["subnet_id"] == id {
			return s
		}
	}
	return nil
}

func cidrOf(s row) string { return s["start_hostaddr"] + "/" + s["subnet_prefix"] }

func (f *FakeServer) findFree(ctx context.Context, q url.Values) ([]row, *fakeError) {
	s := f.subnetByID(q.Get("subnet_id"))
	if s == nil {
		return nil, failf(http.StatusBadRequest, "-1", "subnet_id %q not found", q.Get("subnet_id"))
	}
	n, err := strconv.Atoi(q.Get("max_find"))
	if err != nil || n < 1 {
		n = 1
	}
	taken := map[string]bool{}
	for _, a := range f.addresses {
		if a["subnet_id"] == s["subnet_id"] {
			taken[a["hostaddr"]] = true
		}
	}
	if f.InUse != nil {
		used, err := f.InUse(ctx, cidrOf(s))
		if err != nil {
			return nil, failf(http.StatusInternalServerError, "-1", "%v", err)
		}
		for _, ip := range used {
			taken[ip] = true
		}
	}
	p := netip.MustParsePrefix(cidrOf(s))
	var out []row
	addr := p.Addr()
	for i := 0; p.Contains(addr) && len(out) < n; i++ {
		next := addr.Next()
		if i >= max(f.FirstHost, 1) && p.Contains(next) && !taken[addr.String()] { // not the network or broadcast address
			out = append(out, row{"hostaddr": addr.String(), "subnet_id": s["subnet_id"], "site_id": s["site_id"]})
		}
		addr = next
	}
	return out, nil
}

func (f *FakeServer) siteID(q url.Values) string {
	if id := q.Get("site_id"); id != "" {
		return id
	}
	return f.sites[q.Get("site_name")]
}

func (f *FakeServer) addAddress(method string, q url.Values) ([]row, *fakeError) {
	flag := cmp.Or(q.Get("add_flag"), "new_edit")
	var existing row
	if id := q.Get("ip_id"); id != "" {
		for _, a := range f.addresses {
			if a["ip_id"] == id {
				existing = a
			}
		}
		if existing == nil {
			return nil, failf(http.StatusBadRequest, "-1", "ip_id %s not found", id)
		}
	} else {
		if q.Get("hostaddr") == "" || f.siteID(q) == "" {
			return nil, failf(http.StatusBadRequest, "-1", "mandatory parameters: hostaddr and site_id or site_name")
		}
		for _, a := range f.addresses {
			if a["hostaddr"] == q.Get("hostaddr") && a["site_id"] == f.siteID(q) {
				existing = a
			}
		}
	}
	switch {
	case existing != nil && flag == "new_only":
		return nil, failf(http.StatusBadRequest, "-1", "address %s already exists", existing["hostaddr"])
	case existing == nil && flag == "edit_only":
		return nil, failf(http.StatusBadRequest, "-1", "address %s not found", q.Get("hostaddr"))
	case existing != nil:
		if q.Has("name") {
			existing["name"] = q.Get("name")
		}
		if q.Has("ip_class_parameters") {
			existing["ip_class_parameters"] = q.Get("ip_class_parameters")
		}
		return []row{{"ret_oid": existing["ip_id"]}}, nil
	}
	ip, err := netip.ParseAddr(q.Get("hostaddr"))
	if err != nil {
		return nil, failf(http.StatusBadRequest, "-1", "hostaddr %q is not an IPv4 address", q.Get("hostaddr"))
	}
	var subnet row
	for _, s := range f.subnets {
		if s["site_id"] == f.siteID(q) && netip.MustParsePrefix(cidrOf(s)).Contains(ip) {
			subnet = s
		}
	}
	if subnet == nil {
		return nil, failf(http.StatusBadRequest, "-1", "no subnet holds %s in this space", ip)
	}
	a := row{"ip_id": f.id(), "hostaddr": ip.String(), "name": q.Get("name"), "subnet_id": subnet["subnet_id"],
		"subnet_name": subnet["subnet_name"], "subnet_start_hostaddr": subnet["start_hostaddr"], "subnet_size": subnet["subnet_size"],
		"site_id": subnet["site_id"], "site_name": subnet["site_name"],
		"ip_class_parameters": q.Get("ip_class_parameters"), "mac_addr": q.Get("mac_addr")}
	f.addresses = append(f.addresses, a)
	return []row{{"ret_oid": a["ip_id"]}}, nil
}

func (f *FakeServer) deleteAddress(q url.Values) ([]row, *fakeError) {
	for i, a := range f.addresses {
		if a["ip_id"] == q.Get("ip_id") || q.Get("ip_id") == "" && a["hostaddr"] == q.Get("hostaddr") && a["site_id"] == f.siteID(q) {
			f.addresses = slices.Delete(f.addresses, i, i+1)
			return []row{{"ret_oid": a["ip_id"]}}, nil
		}
	}
	return nil, failf(http.StatusBadRequest, "-1", "address not found")
}

func (f *FakeServer) addRecord(q url.Values) ([]row, *fakeError) {
	name, typ, value, server := fqdn(q.Get("rr_name")), q.Get("rr_type"), q.Get("value1"), q.Get("dns_name")
	if name == "" || typ == "" || value == "" || server == "" && q.Get("dns_id") == "" {
		return nil, failf(http.StatusBadRequest, "-1", "mandatory parameters: rr_name, rr_type, value1 and dns_id or dns_name")
	}
	var zone row
	for _, z := range f.zones {
		if z["dns_name"] != server && z["dns_id"] != q.Get("dns_id") {
			continue
		}
		zn := z["dnszone_name"]
		if want := q.Get("dnszone_name"); want != "" && fqdn(want) != zn {
			continue
		}
		if (name == zn || strings.HasSuffix(name, "."+zn)) && (zone == nil || len(zn) > len(zone["dnszone_name"])) {
			zone = z
		}
	}
	if zone == nil {
		return nil, failf(http.StatusBadRequest, "-1", "no zone on %s holds %s", server, name)
	}
	for _, r := range f.records {
		if r["rr_full_name"] == name && r["rr_type"] == typ && r["dns_name"] == zone["dns_name"] && sameValue(r["value1"], value) && !pendingDelete(r) {
			if q.Get("add_flag") == "new_only" {
				return nil, failf(http.StatusBadRequest, "-1", "%s record %s already exists", typ, name)
			}
			return []row{{"ret_oid": r["rr_id"]}}, nil
		}
	}
	r := row{"rr_id": f.id(), "rr_full_name": name, "rr_full_name_utf": name, "rr_type": typ, "value1": value,
		"ttl": cmp.Or(q.Get("rr_ttl"), "3600"), "dns_name": zone["dns_name"], "dns_id": zone["dns_id"],
		"dnszone_name": zone["dnszone_name"], "dnszone_id": zone["dnszone_id"], "dnsview_name": q.Get("dnsview_name"),
		"rr_class_parameters": q.Get("rr_class_parameters")}
	f.records = append(f.records, r)
	return []row{{"ret_oid": r["rr_id"]}}, nil
}

func (f *FakeServer) record(id string) row {
	for _, r := range f.records {
		if r["rr_id"] == id && !pendingDelete(r) {
			return r
		}
	}
	return nil
}

// editRecord changes a record's value (add_flag edit_only, by rr_id).
func (f *FakeServer) editRecord(q url.Values) ([]row, *fakeError) {
	r := f.record(q.Get("rr_id"))
	if r == nil {
		return nil, failf(http.StatusBadRequest, "-1", "record %s not found", q.Get("rr_id"))
	}
	if v := q.Get("value1"); v != "" {
		r["value1"] = v
	}
	if q.Has("rr_class_parameters") {
		r["rr_class_parameters"] = q.Get("rr_class_parameters")
	}
	return []row{{"ret_oid": r["rr_id"]}}, nil
}

// deleteRecord marks the record deleted; purge drops it after DeleteDelay.
func (f *FakeServer) deleteRecord(q url.Values) ([]row, *fakeError) {
	r := f.record(q.Get("rr_id"))
	if r == nil {
		return nil, failf(http.StatusBadRequest, "-1", "record not found")
	}
	now := time.Now()
	r["delayed_delete_time"] = strconv.FormatInt(now.Unix(), 10)
	f.deleting[r["rr_id"]] = now
	return []row{{"ret_oid": r["rr_id"]}}, nil
}

func (f *FakeServer) purge() {
	for id, at := range f.deleting {
		if time.Since(at) >= f.DeleteDelay {
			f.records = slices.DeleteFunc(f.records, func(r row) bool { return r["rr_id"] == id })
			delete(f.deleting, id)
		}
	}
}
