// Package iprange is the arithmetic of the IPv4 ranges rvav hands
// addresses from (ADR-0063): which addresses a range may give, how many,
// and the first one free.
package iprange

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Range is a range of a subnet, its gateway and the spans it never gives.
type Range struct {
	Prefix     netip.Prefix
	Start, End netip.Addr
	Gateway    netip.Addr
	Excluded   []Span
}

// Span is the addresses from From to To, both included.
type Span struct{ From, To netip.Addr }

func (s Span) String() string {
	if s.From == s.To {
		return s.From.String()
	}
	return s.From.String() + "-" + s.To.String()
}

// ErrNotIPv4 is an address or subnet that isn't IPv4: ranges are IPv4 only.
var ErrNotIPv4 = errors.New("not an IPv4 address")

// ParseAddr is an IPv4 address.
func ParseAddr(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, err
	}
	if !a.Is4() {
		return netip.Addr{}, ErrNotIPv4
	}
	return a, nil
}

// ParsePrefix is an IPv4 subnet such as 10.51.0.0/24, its host bits zero.
func ParsePrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, err
	}
	if !p.Addr().Is4() {
		return netip.Prefix{}, ErrNotIPv4
	}
	if p.Masked() != p {
		return netip.Prefix{}, fmt.Errorf("%s has host bits set: the subnet is %s", s, p.Masked())
	}
	return p, nil
}

// ParseSpan is one address or two joined by a dash, the first not after
// the second.
func ParseSpan(s string) (Span, error) {
	from, to, ok := strings.Cut(s, "-")
	if !ok {
		a, err := ParseAddr(from)
		return Span{a, a}, err
	}
	a, err := ParseAddr(from)
	if err != nil {
		return Span{}, err
	}
	b, err := ParseAddr(to)
	if err != nil {
		return Span{}, err
	}
	if b.Less(a) {
		return Span{}, fmt.Errorf("%s comes after %s", a, b)
	}
	return Span{a, b}, nil
}

// Parse reads a range as the store keeps it, already checked.
func Parse(cidr, start, end, gateway string, excluded []string) (Range, error) {
	var r Range
	var err error
	if r.Prefix, err = ParsePrefix(cidr); err != nil {
		return r, err
	}
	if r.Start, err = ParseAddr(start); err != nil {
		return r, err
	}
	if r.End, err = ParseAddr(end); err != nil {
		return r, err
	}
	if r.Gateway, err = ParseAddr(gateway); err != nil {
		return r, err
	}
	for _, e := range excluded {
		s, err := ParseSpan(e)
		if err != nil {
			return r, err
		}
		r.Excluded = append(r.Excluded, s)
	}
	return r, nil
}

func u32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func addr(n uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

// reserved are the subnet's own addresses a VM can't take: its network
// and broadcast addresses (a /31 or /32 has none) and the gateway.
func (r Range) reserved() []uint32 {
	out := []uint32{u32(r.Gateway)}
	if r.Prefix.Bits() <= 30 {
		first := u32(r.Prefix.Addr())
		out = append(out, first, first|(1<<(32-r.Prefix.Bits())-1))
	}
	return out
}

// Usable says whether the range may give the address.
func (r Range) Usable(a netip.Addr) bool {
	if !a.Is4() || !r.Prefix.Contains(a) || a.Less(r.Start) || r.End.Less(a) {
		return false
	}
	n := u32(a)
	if slices.Contains(r.reserved(), n) {
		return false
	}
	return !slices.ContainsFunc(r.Excluded, func(s Span) bool { return u32(s.From) <= n && n <= u32(s.To) })
}

// Size is how many addresses the range may give.
func (r Range) Size() int {
	lo, hi := u32(r.Start), u32(r.End)
	if hi < lo {
		return 0
	}
	spans := r.merged(lo, hi)
	size := int(hi - lo + 1)
	for _, s := range spans {
		size -= int(s[1] - s[0] + 1)
	}
	for _, n := range slices.Compact(slices.Sorted(slices.Values(r.reserved()))) {
		if lo <= n && n <= hi && !slices.ContainsFunc(spans, func(s [2]uint32) bool { return s[0] <= n && n <= s[1] }) {
			size--
		}
	}
	return size
}

// merged is the exclusions clipped to lo..hi, sorted and joined.
func (r Range) merged(lo, hi uint32) [][2]uint32 {
	var spans [][2]uint32
	for _, s := range r.Excluded {
		a, b := max(u32(s.From), lo), min(u32(s.To), hi)
		if a <= b {
			spans = append(spans, [2]uint32{a, b})
		}
	}
	slices.SortFunc(spans, func(x, y [2]uint32) int { return int(int64(x[0]) - int64(y[0])) })
	var out [][2]uint32
	for _, s := range spans {
		if n := len(out); n > 0 && s[0] <= out[n-1][1]+1 {
			out[n-1][1] = max(out[n-1][1], s[1])
			continue
		}
		out = append(out, s)
	}
	return out
}

// First is the first address of the range, in order, that it may give and
// taken doesn't hold.
func (r Range) First(taken func(netip.Addr) bool) (netip.Addr, bool) {
	lo, hi := u32(r.Start), u32(r.End)
	if hi < lo {
		return netip.Addr{}, false
	}
	spans := r.merged(lo, hi)
	reserved := r.reserved()
	next := 0
	for n := lo; ; n++ {
		if next < len(spans) && n == spans[next][0] {
			n = spans[next][1]
			next++
		} else if a := addr(n); !slices.Contains(reserved, n) && r.Prefix.Contains(a) && !taken(a) {
			return a, true
		}
		if n >= hi {
			return netip.Addr{}, false
		}
	}
}

// Overlaps says whether two ranges share an address between their start
// and end.
func (r Range) Overlaps(o Range) bool {
	return !r.End.Less(o.Start) && !o.End.Less(r.Start)
}
