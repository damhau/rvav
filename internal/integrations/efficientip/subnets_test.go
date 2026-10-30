package efficientip

import (
	"context"
	"errors"
	"testing"
)

// TestAllocateSubnet: subnets carved from a block for NSX segments
// (ADR-0028): by the block's name or range, the next free range each time,
// the same one asked again by its name, a full block, the class parameters
// the network sync reads, and deletion only once its addresses are gone.
func TestAllocateSubnet(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	if _, err := l.fake.AddBlock("lab", "10.40.0.0/24", "nsx-segments"); err != nil {
		t.Fatal(err)
	}
	c := l.client(t, AuthNative, "rvav", "s3cret")
	params := map[string]string{ParamNetwork: "rvav-dep1-app", ParamGateway: "10.40.0.1", ParamZone: "corp.example"}
	app, err := c.AllocateSubnet(ctx, "nsx-segments", 26, "rvav-dep1-app", params)
	if err != nil || app.CIDR != "10.40.0.0/26" || app.ID == "" {
		t.Fatalf("first = %+v, %v", app, err)
	}
	again, err := c.AllocateSubnet(ctx, "10.40.0.0/24", 26, "rvav-dep1-app", params)
	if err != nil || again.ID != app.ID {
		t.Fatalf("asked again = %+v, %v", again, err)
	}
	db, err := c.AllocateSubnet(ctx, "nsx-segments", 26, "rvav-dep1-db", nil)
	if err != nil || db.CIDR != "10.40.0.64/26" {
		t.Fatalf("second = %+v, %v", db, err)
	}
	for _, name := range []string{"rvav-dep2-a", "rvav-dep2-b"} {
		if _, err := c.AllocateSubnet(ctx, "nsx-segments", 26, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.AllocateSubnet(ctx, "nsx-segments", 26, "rvav-dep3-a", nil); !errors.Is(err, ErrBlockFull) {
		t.Fatalf("a full block: %v", err)
	}
	if _, err := c.AllocateSubnet(ctx, "no-such-block", 26, "x", nil); err == nil {
		t.Fatal("an unknown block")
	}

	// The network sync reads its class parameters, as any subnet's.
	subnets, err := c.Subnets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range subnets {
		if s.ID == app.ID && s.Params.Get(ParamNetwork) == "rvav-dep1-app" && s.Params.Get(ParamGateway) == "10.40.0.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("subnets = %+v", subnets)
	}

	// Deleted once its addresses are released.
	addr, err := c.Reserve(ctx, Subnet{ID: app.ID, CIDR: app.CIDR, Gateway: "10.40.0.1"}, "web-dev-001", "dep1")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSubnet(ctx, app.ID); err == nil {
		t.Fatal("a subnet with an address deleted")
	}
	if err := c.Release(ctx, addr); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSubnet(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSubnet(ctx, app.ID); err != nil {
		t.Fatalf("deleted twice: %v", err)
	}
	if again, err := c.AllocateSubnet(ctx, "nsx-segments", 26, "rvav-dep3-a", nil); err != nil || again.CIDR != "10.40.0.0/26" {
		t.Fatalf("the freed range = %+v, %v", again, err)
	}
}
