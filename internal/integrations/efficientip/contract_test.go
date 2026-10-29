package efficientip

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"testing"
	"time"
)

// The contract tests check what rvav relies on SOLIDserver for (S14.9):
// addresses reserved, named, renamed and released, A and PTR records
// created, changed and deleted, names found, rvav's objects listed. They run
// against the fake in every build, and against a real SOLIDserver when
// RVAV_EFFICIENTIP_TEST_URL is set: the same assertions passing on both
// keeps the fake honest.
//
//	RVAV_EFFICIENTIP_TEST_URL, _USERNAME, _PASSWORD, _AUTH (native), _INSECURE
//	RVAV_EFFICIENTIP_TEST_DNS_SERVER, _SPACE, _VIEW
//	RVAV_EFFICIENTIP_TEST_SUBNET        a terminal subnet the tests may use (CIDR)
//	RVAV_EFFICIENTIP_TEST_ZONE          a forward zone on the DNS server
//	RVAV_EFFICIENTIP_TEST_REVERSE_ZONE  the subnet's reverse zone, if PTR records are to be tried
func contractEnv(t *testing.T) (c *HTTPClient, s Subnet, zone, reverse string) {
	t.Helper()
	env := func(k string) string { return os.Getenv("RVAV_EFFICIENTIP_TEST_" + k) }
	if env("URL") == "" {
		l := newLab(t)
		return l.client(t, AuthNative, "rvav", "s3cret"), subnet, "corp.example", "10.31.10.in-addr.arpa"
	}
	c, err := New(Options{URL: env("URL"), Auth: Auth(env("AUTH")), Username: env("USERNAME"), Password: env("PASSWORD"),
		Insecure: env("INSECURE") == "1", DNSServer: env("DNS_SERVER"), Space: env("SPACE"), DNSView: env("VIEW"),
		Instance: "rvav-contract"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	subnets, err := c.Subnets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(subnets, func(s SubnetInfo) bool { return s.CIDR == env("SUBNET") })
	if i < 0 {
		t.Fatalf("subnet %s is not among SOLIDserver's terminal subnets", env("SUBNET"))
	}
	gw := subnets[i].Params.Get(ParamGateway)
	return c, Subnet{ID: subnets[i].ID, CIDR: subnets[i].CIDR, Gateway: gw}, env("ZONE"), env("REVERSE_ZONE")
}

func TestContract(t *testing.T) {
	c, s, zone, reverse := contractEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	prefix := netip.MustParsePrefix(s.CIDR)
	in := func(ip string) bool { a, err := netip.ParseAddr(ip); return err == nil && prefix.Contains(a) }
	name := fmt.Sprintf("rvav-contract-%d.%s", time.Now().UnixNano()%1_000_000_000, zone)
	owner := "dep_contract"

	// The subnet and the zone are listed.
	zones, err := c.Zones(ctx)
	if err != nil || !slices.Contains(zones, zone) {
		t.Fatalf("zones = %v, %v; want %s among them", zones, err, zone)
	}
	if taken, err := c.NameExists(ctx, name); err != nil || taken {
		t.Fatalf("a new name exists = %v, %v", taken, err)
	}

	// An address: found free, reserved under the name, again for the same
	// owner, refused to another.
	free, err := c.NextFree(ctx, s)
	if err != nil || !in(free) {
		t.Fatalf("next free = %q, %v", free, err)
	}
	a, err := c.Reserve(ctx, s, name, owner)
	if err != nil || !in(a.IP) || a.ID == "" {
		t.Fatalf("reserve = %+v, %v", a, err)
	}
	t.Cleanup(func() { _ = c.Release(context.Background(), a) })
	if again, err := c.Reserve(ctx, s, name, owner); err != nil || again.ID != a.ID || again.IP != a.IP {
		t.Fatalf("reserved again = %+v, %v; want %+v", again, err, a)
	}
	if _, err := c.Reserve(ctx, s, name, "dep_other"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("the name reserved for another owner = %v", err)
	}
	if taken, err := c.NameExists(ctx, name); err != nil || !taken {
		t.Fatalf("a reserved name exists = %v, %v", taken, err)
	}

	// Its A record, created once; and its PTR record.
	r, err := c.CreateRecord(ctx, Record{FQDN: name, Type: "A", Value: a.IP})
	if err != nil || r.ID == "" {
		t.Fatalf("A record = %+v, %v", r, err)
	}
	t.Cleanup(func() { _ = c.DeleteRecord(context.Background(), r) })
	if again, err := c.CreateRecord(ctx, Record{FQDN: name, Type: "A", Value: a.IP}); err != nil || again.ID != r.ID {
		t.Fatalf("A record again = %+v, %v; want %s", again, err, r.ID)
	}
	var ptr Record
	if reverse != "" {
		if ptr, err = c.CreateRecord(ctx, Record{FQDN: PTRName(a.IP), Type: "PTR", Value: name}); err != nil || ptr.ID == "" {
			t.Fatalf("PTR record = %+v, %v", ptr, err)
		}
		t.Cleanup(func() { _ = c.DeleteRecord(context.Background(), ptr) })
	}

	// rvav finds its objects: tagged, by ID, and by the VM's address.
	addrs, recs, err := c.Managed(ctx)
	if err != nil || !slices.ContainsFunc(addrs, func(x Address) bool { return x.ID == a.ID }) ||
		!slices.ContainsFunc(recs, func(x Record) bool { return x.ID == r.ID }) {
		t.Fatalf("managed: %d addresses, %d records, %v; ours missing", len(addrs), len(recs), err)
	}
	ea, er, err := c.Existing(ctx, []string{a.ID}, []string{r.ID})
	if err != nil || !ea[a.ID] || !er[r.ID] {
		t.Fatalf("existing = %v %v %v", ea, er, err)
	}
	fa, fr, err := c.Find(ctx, []string{a.IP}, nil)
	if err != nil || !slices.ContainsFunc(fa, func(x Address) bool { return x.ID == a.ID }) ||
		!slices.ContainsFunc(fr, func(x Record) bool { return x.ID == r.ID }) {
		t.Fatalf("find %s = %+v %+v %v", a.IP, fa, fr, err)
	}

	// The VM renamed: its address and PTR record follow.
	renamed := "rvav-contract-renamed-" + name
	if err := c.RenameAddress(ctx, a, renamed); err != nil {
		t.Fatalf("rename address: %v", err)
	}
	a.Hostname = renamed
	if ptr.ID != "" {
		if moved, err := c.UpdateRecord(ctx, ptr, renamed); err != nil || moved.Value != renamed {
			t.Fatalf("PTR record moved = %+v, %v", moved, err)
		}
	}

	// Deleted and released, twice each: gone is fine.
	for range 2 {
		if err := c.DeleteRecord(ctx, r); err != nil {
			t.Fatalf("delete A record: %v", err)
		}
		if ptr.ID != "" {
			if err := c.DeleteRecord(ctx, ptr); err != nil {
				t.Fatalf("delete PTR record: %v", err)
			}
		}
		if err := c.Release(ctx, a); err != nil {
			t.Fatalf("release: %v", err)
		}
	}
	deadline := time.Now().Add(time.Minute)
	for {
		ea, er, err := c.Existing(ctx, []string{a.ID}, []string{r.ID})
		if err != nil {
			t.Fatal(err)
		}
		if !ea[a.ID] && !er[r.ID] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still there a minute after deleting: address %v, record %v", ea[a.ID], er[r.ID])
		}
		time.Sleep(2 * time.Second)
	}
}
