// Package efficientip reserves IP addresses and manages DNS records in
// EfficientIP SOLIDserver through its REST API. FakeServer speaks the same
// API, for local development and tests (RVAV_FAKE_EFFICIENTIP=1).
package efficientip

import (
	"context"
	"errors"
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

var ErrSubnetFull = errors.New("no free address in subnet")

// Client is what rvav needs from IPAM and DNS. Reserve and CreateRecord are
// idempotent: calling them again for the same hostname returns what exists.
// Release and DeleteRecord succeed when the object is already gone.
type Client interface {
	NextFree(ctx context.Context, s Subnet) (string, error)
	Reserve(ctx context.Context, s Subnet, hostname string) (Address, error)
	Release(ctx context.Context, a Address) error
	CreateRecord(ctx context.Context, r Record) (Record, error)
	DeleteRecord(ctx context.Context, r Record) error
	NameExists(ctx context.Context, fqdn string) (bool, error)
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
