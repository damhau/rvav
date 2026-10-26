package efficientip

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
)

// Fake is an in-memory IPAM and DNS. Addresses below FirstHost and the
// gateway are never handed out. InUse, when set, reports addresses already
// taken elsewhere (for example recorded in the rvav database) so a restarted
// fake does not hand them out again.
type Fake struct {
	FirstHost int
	InUse     func(ctx context.Context, subnet string) ([]string, error)

	mu      sync.Mutex
	seq     int
	byIP    map[string]Address // ip -> address
	records map[string]Record  // type/fqdn -> record
}

func NewFake() *Fake {
	return &Fake{FirstHost: 20, byIP: map[string]Address{}, records: map[string]Record{}}
}

func (f *Fake) candidates(ctx context.Context, s Subnet) ([]string, map[string]bool, error) {
	prefix, err := netip.ParsePrefix(s.CIDR)
	if err != nil {
		return nil, nil, fmt.Errorf("subnet %q: %w", s.CIDR, err)
	}
	taken := map[string]bool{s.Gateway: true}
	if f.InUse != nil {
		used, err := f.InUse(ctx, s.CIDR)
		if err != nil {
			return nil, nil, err
		}
		for _, ip := range used {
			taken[ip] = true
		}
	}
	for ip := range f.byIP {
		taken[ip] = true
	}
	var out []string
	addr := prefix.Masked().Addr()
	for i := 0; prefix.Contains(addr); i++ {
		next := addr.Next()
		if i >= f.FirstHost && prefix.Contains(next) { // skip network, low range and broadcast
			out = append(out, addr.String())
		}
		addr = next
	}
	return out, taken, nil
}

func (f *Fake) NextFree(ctx context.Context, s Subnet) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list, taken, err := f.candidates(ctx, s)
	if err != nil {
		return "", err
	}
	for _, ip := range list {
		if !taken[ip] {
			return ip, nil
		}
	}
	return "", ErrSubnetFull
}

func (f *Fake) Reserve(ctx context.Context, s Subnet, hostname string) (Address, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.byIP {
		if a.Subnet == s.CIDR && a.Hostname == hostname {
			return a, nil
		}
	}
	list, taken, err := f.candidates(ctx, s)
	if err != nil {
		return Address{}, err
	}
	for _, ip := range list {
		if taken[ip] {
			continue
		}
		f.seq++
		a := Address{ID: fmt.Sprintf("ip-%d", 7000+f.seq), IP: ip, Subnet: s.CIDR, Hostname: hostname}
		f.byIP[ip] = a
		return a, nil
	}
	return Address{}, ErrSubnetFull
}

func (f *Fake) Release(_ context.Context, a Address) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byIP, a.IP)
	return nil
}

func recordKey(r Record) string { return r.Type + "/" + strings.ToLower(r.FQDN) }

func (f *Fake) CreateRecord(_ context.Context, r Record) (Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.records[recordKey(r)]; ok {
		if existing.Value != r.Value {
			return Record{}, fmt.Errorf("%s record %s already points to %s", r.Type, r.FQDN, existing.Value)
		}
		return existing, nil
	}
	f.seq++
	r.ID = fmt.Sprintf("rr-%d", 9000+f.seq)
	f.records[recordKey(r)] = r
	return r, nil
}

func (f *Fake) DeleteRecord(_ context.Context, r Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.records, recordKey(r))
	return nil
}

func (f *Fake) NameExists(_ context.Context, fqdn string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.records[recordKey(Record{Type: "A", FQDN: fqdn})]
	return ok, nil
}
