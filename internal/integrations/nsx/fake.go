package nsx

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FakeServer answers the Policy API calls the client makes, from memory,
// as NSX does: basic authentication or a client certificate, lists by
// cursor, PATCH creating or updating by ID, references checked (a segment's
// Tier-1, a rule's groups and services), segments realised after a few
// reads of their state, NSX's error answers.
type FakeServer struct {
	Username, Password string
	// RealizeAfter: how many reads of a segment's state say in_progress
	// before success.
	RealizeAfter int
	// OnSegment is called when a segment is made or deleted, as NSX makes
	// or removes its port group in vCenter.
	OnSegment func(s Segment, deleted bool)
	// AnyVM: any VM looked up by its instance UUID is known, after SeeAfter
	// lookups, and so is one tags are set on (the compose stack, whose
	// vcsim isn't NSX's compute manager).
	AnyVM    bool
	SeeAfter int
	// NewVMTags are the tags such a VM has when NSX sees it: another
	// tool's.
	NewVMTags []Tag

	mu       sync.Mutex
	tier1s   map[string]Tier1
	segments map[string]Segment
	groups   map[string]Group
	policies map[string]SecurityPolicy
	services map[string]Service
	zones    map[string]TransportZone
	vms      map[string]VM
	reads    map[string]int // segment ID → state reads; "vm:" + UUID → lookups
	// Calls counts the requests by method and path prefix, for tests.
	Calls map[string]int
}

// FakeUser and FakePassword are what the fake accepts.
const (
	FakeUser     = "rvav-nsx"
	FakePassword = "nsx"
)

// NewFakeServer is an NSX with the sample infrastructure.
func NewFakeServer() *FakeServer {
	f := &FakeServer{Username: FakeUser, Password: FakePassword, tier1s: map[string]Tier1{}, segments: map[string]Segment{},
		groups: map[string]Group{}, policies: map[string]SecurityPolicy{}, services: map[string]Service{}, zones: map[string]TransportZone{},
		vms: map[string]VM{}, reads: map[string]int{}, Calls: map[string]int{}}
	s := SampleInfra()
	for _, t := range s.Tier1s {
		f.tier1s[t.ID] = t
	}
	for _, z := range s.Zones {
		f.zones[z.ID] = z
	}
	for _, x := range s.Services {
		f.services[x.ID] = x
	}
	for _, x := range s.Segments {
		f.segments[x.ID] = x
	}
	for _, g := range s.Groups {
		f.groups[g.ID] = g
	}
	return f
}

// Infra is what an NSX has before rvav: gateways, zones, services, and
// some segments and groups.
type Infra struct {
	Tier1s   []Tier1
	Zones    []TransportZone
	Services []Service
	Segments []Segment
	Groups   []Group
}

// SampleInfra is an NSX like the organisation's: a Tier-1 per site and
// environment, an overlay zone, the usual services, an application segment
// per environment and two groups selected by tag.
func SampleInfra() Infra {
	t1 := func(id, name string) Tier1 { return Tier1{ID: id, DisplayName: name, Path: Tier1Path + id} }
	svc := func(id string) Service { return Service{ID: id, DisplayName: id, Path: ServicePath + id} }
	zone := TransportZone{ID: "tz-overlay", DisplayName: "TZ-OVERLAY", Type: "OVERLAY_STANDARD",
		Path: "/infra/sites/default/enforcement-points/default/transport-zones/tz-overlay"}
	seg := func(id, t1, gw string) Segment {
		return Segment{ID: id, DisplayName: id, Path: SegmentPath + id, ConnectivityPath: Tier1Path + t1, TransportZonePath: zone.Path,
			Subnets: []SegmentSubnet{{GatewayAddress: gw}}}
	}
	grp := func(id, scope, tag string) Group {
		return Group{ID: id, DisplayName: id, Path: GroupPath + id, Expression: []Condition{TagCondition(Tag{Scope: scope, Tag: tag})}}
	}
	return Infra{
		Tier1s:   []Tier1{t1("t1-gva-dev", "T1-GVA-DEV"), t1("t1-gva-prd", "T1-GVA-PRD"), t1("t1-zrh-prd", "T1-ZRH-PRD")},
		Zones:    []TransportZone{zone},
		Services: []Service{svc("HTTP"), svc("HTTPS"), svc("SSH"), svc("ICMP-ALL"), svc("MySQL"), svc("PostgreSQL"), svc("MS-SQL-S"), svc("RDP")},
		Segments: []Segment{seg("seg-gva-dev-app", "t1-gva-dev", "10.31.40.1/24"), seg("seg-gva-prd-app", "t1-gva-prd", "10.32.40.1/24")},
		Groups:   []Group{grp("grp-web-servers", "role", "web"), grp("grp-backup-clients", "backup", "daily")},
	}
}

// Start serves on addr until ctx is done and returns its URL.
func (f *FakeServer) Start(ctx context.Context, addr string) (string, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	go func() { _ = srv.Serve(l) }()
	return "http://" + l.Addr().String(), nil
}

// AddVM makes a VM known, as NSX learns it from vCenter.
func (f *FakeServer) AddVM(externalID, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vms[externalID] = VM{ExternalID: externalID, DisplayName: name}
}

// VM is a VM's state, for tests.
func (f *FakeServer) VM(externalID string) (VM, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vms[externalID]
	return v, ok
}

// SetVMTagsDirectly changes a VM's tags as someone would in NSX.
func (f *FakeServer) SetVMTagsDirectly(externalID string, tags []Tag) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.vms[externalID]
	v.Tags = tags
	f.vms[externalID] = v
}

func nsxError(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"httpStatus": http.StatusText(status), "error_code": code, "module_name": "Policy",
		"error_message": msg})
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// page answers a list by cursor (the next offset) and page_size.
func page[T any](w http.ResponseWriter, q url.Values, all []T) {
	size, _ := strconv.Atoi(q.Get("page_size"))
	if size <= 0 || size > 1000 {
		size = 1000
	}
	from, _ := strconv.Atoi(q.Get("cursor"))
	if from > len(all) {
		from = len(all)
	}
	to := min(from+size, len(all))
	out := map[string]any{"results": all[from:to], "result_count": len(all)}
	if to < len(all) {
		out["cursor"] = strconv.Itoa(to)
	}
	reply(w, out)
}

func sorted[T any](m map[string]T) []T {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]T, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

func (f *FakeServer) authorised(r *http.Request) bool {
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		return true
	}
	u, p, ok := r.BasicAuth()
	return ok && subtle.ConstantTimeCompare([]byte(u), []byte(f.Username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(p), []byte(f.Password)) == 1
}

func (f *FakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/readyz" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !f.authorised(r) {
		nsxError(w, http.StatusForbidden, 403, "The credentials were incorrect or the account specified has been locked.")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, q := r.URL.Path, r.URL.Query()
	prefix := p
	if i := strings.Index(strings.TrimPrefix(p, policy+"/infra/"), "/"); i >= 0 && strings.HasPrefix(p, policy+"/infra/") {
		prefix = p[:len(policy+"/infra/")+i]
	}
	f.Calls[r.Method+" "+prefix]++
	var body map[string]json.RawMessage
	if r.Method == http.MethodPatch || r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	decode := func(v any) bool {
		b, _ := json.Marshal(body)
		if err := json.Unmarshal(b, v); err != nil {
			nsxError(w, http.StatusBadRequest, 255, "The request body is not valid: "+err.Error())
			return false
		}
		return true
	}
	id := func(base string) string { s, _ := url.PathUnescape(strings.TrimPrefix(p, policy+base)); return s }

	switch {
	case p == "/api/v1/node/version":
		reply(w, map[string]string{"product_version": "4.1.2.0.0.22589037", "node_version": "4.1.2.0.0.22589037"})
	case p == policy+"/infra/tier-1s":
		page(w, q, sorted(f.tier1s))
	case p == policy+"/infra/sites/default/enforcement-points/default/transport-zones":
		page(w, q, sorted(f.zones))
	case p == policy+"/infra/services":
		page(w, q, sorted(f.services))
	case p == policy+"/infra/segments":
		page(w, q, sorted(f.segments))
	case strings.HasPrefix(p, policy+SegmentPath) && strings.HasSuffix(p, "/state"):
		sid := strings.TrimSuffix(id(SegmentPath), "/state")
		if _, ok := f.segments[sid]; !ok {
			nsxError(w, http.StatusNotFound, 500090, "Segment "+sid+" was not found.")
			return
		}
		f.reads[sid]++
		state := "success"
		if f.reads[sid] <= f.RealizeAfter {
			state = "in_progress"
		}
		reply(w, map[string]string{"state": state})
	case strings.HasPrefix(p, policy+SegmentPath):
		sid := id(SegmentPath)
		switch r.Method {
		case http.MethodGet:
			s, ok := f.segments[sid]
			if !ok {
				nsxError(w, http.StatusNotFound, 500090, "Segment "+sid+" was not found.")
				return
			}
			reply(w, s)
		case http.MethodPatch:
			// PATCH keeps what it doesn't give, as NSX does.
			s := f.segments[sid]
			s.Subnets, s.Tags = slices.Clone(s.Subnets), slices.Clone(s.Tags)
			if !decode(&s) {
				return
			}
			if s.ConnectivityPath != "" {
				if _, ok := f.tier1s[strings.TrimPrefix(s.ConnectivityPath, Tier1Path)]; !ok {
					nsxError(w, http.StatusBadRequest, 503040, "Invalid connectivity path "+s.ConnectivityPath+".")
					return
				}
			}
			for _, sub := range s.Subnets {
				if !strings.Contains(sub.GatewayAddress, "/") {
					nsxError(w, http.StatusBadRequest, 503063, "Gateway address "+sub.GatewayAddress+" is not in CIDR form.")
					return
				}
			}
			s.ID, s.Path = sid, SegmentPath+sid
			if s.DisplayName == "" {
				s.DisplayName = sid
			}
			_, existed := f.segments[sid]
			f.segments[sid] = s
			if !existed {
				f.reads[sid] = 0
				if f.OnSegment != nil {
					f.OnSegment(s, false)
				}
			}
		case http.MethodDelete:
			if s, ok := f.segments[sid]; ok {
				delete(f.segments, sid)
				if f.OnSegment != nil {
					f.OnSegment(s, true)
				}
			}
		}
	case p == policy+"/infra/domains/default/groups":
		page(w, q, sorted(f.groups))
	case strings.HasPrefix(p, policy+GroupPath):
		gid := id(GroupPath)
		switch r.Method {
		case http.MethodGet:
			g, ok := f.groups[gid]
			if !ok {
				nsxError(w, http.StatusNotFound, 500090, "Group "+gid+" was not found.")
				return
			}
			reply(w, g)
		case http.MethodPatch:
			g := f.groups[gid]
			g.Expression, g.Tags = slices.Clone(g.Expression), slices.Clone(g.Tags)
			if !decode(&g) {
				return
			}
			for _, c := range g.Expression {
				if c.ResourceType == "Condition" && (c.Value == "" || c.Key == "") {
					nsxError(w, http.StatusBadRequest, 500191, "A condition needs a key and a value.")
					return
				}
			}
			g.ID, g.Path = gid, GroupPath+gid
			if g.DisplayName == "" {
				g.DisplayName = gid
			}
			f.groups[gid] = g
		case http.MethodDelete:
			// NSX refuses to delete a group a rule still uses.
			for _, pol := range f.policies {
				for _, rule := range pol.Rules {
					if slices.Contains(rule.SourceGroups, GroupPath+gid) || slices.Contains(rule.DestinationGroups, GroupPath+gid) ||
						slices.Contains(rule.Scope, GroupPath+gid) {
						nsxError(w, http.StatusBadRequest, 500030, "Group "+gid+" is used by rule "+rule.ID+" of policy "+pol.ID+".")
						return
					}
				}
			}
			delete(f.groups, gid)
		}
	case p == policy+"/infra/domains/default/security-policies":
		page(w, q, sorted(f.policies))
	case strings.HasPrefix(p, policy+PolicyPath):
		pid := id(PolicyPath)
		switch r.Method {
		case http.MethodGet:
			pol, ok := f.policies[pid]
			if !ok {
				nsxError(w, http.StatusNotFound, 500090, "Security policy "+pid+" was not found.")
				return
			}
			reply(w, pol)
		case http.MethodPatch:
			pol := f.policies[pid]
			pol.Rules, pol.Scope, pol.Tags = nil, slices.Clone(pol.Scope), slices.Clone(pol.Tags)
			if !decode(&pol) {
				return
			}
			for _, rule := range pol.Rules {
				if msg := f.checkRule(rule); msg != "" {
					nsxError(w, http.StatusBadRequest, 500105, msg)
					return
				}
			}
			// PATCH: the rules given are made or updated, the others kept.
			old := f.policies[pid]
			rules := slices.Clone(old.Rules)
			for _, rule := range pol.Rules {
				if i := slices.IndexFunc(rules, func(x Rule) bool { return x.ID == rule.ID }); i >= 0 {
					rules[i] = rule
				} else {
					rules = append(rules, rule)
				}
			}
			pol.ID, pol.Path, pol.Rules = pid, PolicyPath+pid, rules
			if pol.DisplayName == "" {
				pol.DisplayName = pid
			}
			f.policies[pid] = pol
		case http.MethodDelete:
			delete(f.policies, pid)
		}
	case p == vms && r.Method == http.MethodGet:
		all := sorted(f.vms)
		if want, ok := strings.CutPrefix(q.Get("query"), "external_id:"); ok {
			if _, known := f.vms[want]; !known && f.AnyVM {
				if f.reads["vm:"+want]++; f.reads["vm:"+want] > f.SeeAfter {
					f.vms[want] = VM{ExternalID: want, DisplayName: want, Tags: slices.Clone(f.NewVMTags)}
				}
			}
			all = slices.DeleteFunc(sorted(f.vms), func(v VM) bool { return v.ExternalID != want })
		}
		page(w, q, all)
	case p == vms && r.Method == http.MethodPost && q.Get("action") == "update_tags":
		var in struct {
			ID   string `json:"virtual_machine_id"`
			Tags []Tag  `json:"tags"`
		}
		if !decode(&in) {
			return
		}
		v, ok := f.vms[in.ID]
		if !ok && !f.AnyVM {
			nsxError(w, http.StatusNotFound, 600, "Virtual machine "+in.ID+" was not found: NSX hasn't seen it in vCenter yet.")
			return
		}
		v.ExternalID, v.Tags = in.ID, in.Tags
		f.vms[in.ID] = v
	default:
		nsxError(w, http.StatusNotFound, 500090, fmt.Sprintf("%s %s is not something the fake NSX answers.", r.Method, p))
	}
}

// checkRule says what NSX would refuse in a rule: groups and services
// that don't exist, an action it doesn't know.
func (f *FakeServer) checkRule(r Rule) string {
	for _, g := range slices.Concat(r.SourceGroups, r.DestinationGroups, r.Scope) {
		if g != "ANY" {
			if _, ok := f.groups[strings.TrimPrefix(g, GroupPath)]; !ok {
				return "Rule " + r.ID + ": group " + g + " does not exist."
			}
		}
	}
	for _, s := range r.Services {
		if s != "ANY" {
			if _, ok := f.services[strings.TrimPrefix(s, ServicePath)]; !ok {
				return "Rule " + r.ID + ": service " + s + " does not exist."
			}
		}
	}
	if !slices.Contains([]string{"ALLOW", "DROP", "REJECT"}, r.Action) {
		return "Rule " + r.ID + ": action " + r.Action + " is not ALLOW, DROP or REJECT."
	}
	return ""
}
