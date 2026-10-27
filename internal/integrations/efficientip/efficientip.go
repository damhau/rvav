// Package efficientip reserves IP addresses and manages DNS records in
// EfficientIP SOLIDserver through its REST API. FakeServer speaks the same
// API, for local development and tests (RVAV_FAKE_EFFICIENTIP=1).
package efficientip

import (
	"context"
	"errors"
	"net/url"
)

// Subnet identifies an IPAM subnet. ID is the SOLIDserver subnet ID when
// known; CIDR and Gateway are always set.
type Subnet struct {
	ID      string
	CIDR    string
	Gateway string
}

type Address struct {
	ID       string
	IP       string
	Subnet   string
	Hostname string
}

type Record struct {
	ID    string
	FQDN  string
	Type  string // A or PTR
	Value string
	Zone  string
}

// SubnetInfo is a terminal subnet and its class parameters.
type SubnetInfo struct {
	ID, Name, CIDR, Space string
	Params                url.Values
}

var ErrSubnetFull = errors.New("no free address in subnet")

// Client is what rvav needs from IPAM and DNS. Reserve and CreateRecord are
// idempotent: calling them again for the same hostname returns what exists.
// Release and DeleteRecord succeed when the object is already gone.
type Client interface {
	NextFree(ctx context.Context, s Subnet) (string, error)
	Reserve(ctx context.Context, s Subnet, hostname string) (Address, error)
	Release(ctx context.Context, a Address) error
	RenameAddress(ctx context.Context, a Address, hostname string) error
	CreateRecord(ctx context.Context, r Record) (Record, error)
	DeleteRecord(ctx context.Context, r Record) error
	NameExists(ctx context.Context, fqdn string) (bool, error)
	// Subnets lists the terminal subnets of rvav's space (all spaces when
	// none is set).
	Subnets(ctx context.Context) ([]SubnetInfo, error)
	// Zones lists the forward zones of rvav's DNS server and view.
	Zones(ctx context.Context) ([]string, error)
	// Managed lists the addresses and records tagged rvav_managed=1.
	Managed(ctx context.Context) ([]Address, []Record, error)
	// Existing reports which of these address and record IDs EfficientIP has.
	Existing(ctx context.Context, addressIDs, recordIDs []string) (addresses, records map[string]bool, err error)
}

// PTRName returns the reverse lookup name for an IPv4 address.
func PTRName(ip string) string {
	parts := splitDots(ip)
	if len(parts) != 4 {
		return ""
	}
	return parts[3] + "." + parts[2] + "." + parts[1] + "." + parts[0] + ".in-addr.arpa"
}

func splitDots(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
