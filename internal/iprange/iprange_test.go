package iprange

import (
	"net/netip"
	"testing"
)

func mustParse(t *testing.T, cidr, start, end, gw string, excluded ...string) Range {
	t.Helper()
	r, err := Parse(cidr, start, end, gw, excluded)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSizeAndUsable(t *testing.T) {
	r := mustParse(t, "10.51.0.0/24", "10.51.0.0", "10.51.0.255", "10.51.0.1", "10.51.0.10-10.51.0.19", "10.51.0.15", "10.51.0.200")
	// 256, less network, broadcast, gateway and 11 excluded.
	if got := r.Size(); got != 256-3-11 {
		t.Errorf("size = %d", got)
	}
	for ip, want := range map[string]bool{"10.51.0.0": false, "10.51.0.1": false, "10.51.0.2": true, "10.51.0.12": false,
		"10.51.0.20": true, "10.51.0.200": false, "10.51.0.255": false, "10.51.1.2": false} {
		if got := r.Usable(netip.MustParseAddr(ip)); got != want {
			t.Errorf("usable(%s) = %v", ip, got)
		}
	}
	small := mustParse(t, "10.51.0.0/24", "10.51.0.10", "10.51.0.12", "10.51.0.1", "10.51.0.11")
	if got := small.Size(); got != 2 {
		t.Errorf("size = %d", got)
	}
	if got := mustParse(t, "10.51.0.0/31", "10.51.0.0", "10.51.0.1", "10.51.0.0").Size(); got != 1 {
		t.Errorf("/31 size = %d", got)
	}
}

func TestFirst(t *testing.T) {
	r := mustParse(t, "10.51.0.0/24", "10.51.0.1", "10.51.0.30", "10.51.0.1", "10.51.0.2-10.51.0.9", "10.51.0.11")
	taken := map[netip.Addr]bool{}
	var got []string
	for {
		a, ok := r.First(func(a netip.Addr) bool { return taken[a] })
		if !ok {
			break
		}
		taken[a] = true
		got = append(got, a.String())
	}
	if len(got) != r.Size() || got[0] != "10.51.0.10" || got[1] != "10.51.0.12" || got[len(got)-1] != "10.51.0.30" {
		t.Errorf("handed %v, size %d", got, r.Size())
	}
}

func TestParse(t *testing.T) {
	for _, bad := range [][]string{{"10.51.0.1/24", "10.51.0.2", "10.51.0.3", "10.51.0.1"}, {"2001:db8::/64", "10.51.0.2", "10.51.0.3", "10.51.0.1"},
		{"10.51.0.0/24", "x", "10.51.0.3", "10.51.0.1"}} {
		if _, err := Parse(bad[0], bad[1], bad[2], bad[3], nil); err == nil {
			t.Errorf("%v parsed", bad)
		}
	}
	if _, err := ParseSpan("10.51.0.9-10.51.0.2"); err == nil {
		t.Error("a span backwards parsed")
	}
	a := mustParse(t, "10.51.0.0/24", "10.51.0.10", "10.51.0.20", "10.51.0.1")
	b := mustParse(t, "10.51.0.0/24", "10.51.0.20", "10.51.0.30", "10.51.0.1")
	c := mustParse(t, "10.51.0.0/24", "10.51.0.21", "10.51.0.30", "10.51.0.1")
	if !a.Overlaps(b) || a.Overlaps(c) {
		t.Error("overlaps")
	}
}
