package auth

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// X-Forwarded-For is believed from trusted proxies only, and only as far
// as the first address that isn't one of them, from the right.
func TestClientOf(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.10/32")}
	for _, c := range []struct {
		peer    string
		xff     []string
		trusted []netip.Prefix
		want    string
	}{
		{"203.0.113.5:4000", nil, proxies, "203.0.113.5"},
		{"203.0.113.5:4000", []string{"198.51.100.1"}, proxies, "203.0.113.5"},
		{"10.1.2.3:4000", []string{"198.51.100.1"}, nil, "10.1.2.3"},
		{"10.1.2.3:4000", []string{"198.51.100.1"}, proxies, "198.51.100.1"},
		{"10.1.2.3:4000", []string{"6.6.6.6, 198.51.100.1, 10.9.9.9"}, proxies, "198.51.100.1"},
		{"10.1.2.3:4000", []string{"6.6.6.6", "198.51.100.1, 192.168.1.10"}, proxies, "198.51.100.1"},
		{"10.1.2.3:4000", []string{"bogus, 10.9.9.9"}, proxies, "10.9.9.9"},
		{"10.1.2.3:4000", []string{"10.4.4.4"}, proxies, "10.4.4.4"},
		{"10.1.2.3:4000", nil, proxies, "10.1.2.3"},
		{"[::ffff:10.1.2.3]:4000", []string{"2001:db8::1"}, proxies, "2001:db8::1"},
	} {
		r := httptest.NewRequest("GET", "/api/v1/me", nil)
		r.RemoteAddr = c.peer
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		r.Header.Set("User-Agent", strings.Repeat("a", 300))
		got := ClientOf(r, c.trusted)
		if got.IP != c.want || len(got.UserAgent) != 256 {
			t.Errorf("%s %q: %q (%d), want %s", c.peer, c.xff, got.IP, len(got.UserAgent), c.want)
		}
	}
}
