package store

import (
	"context"
	"net/netip"
	"slices"

	"github.com/jackc/pgx/v5"
)

// HeldAddress is an address a range's span has that it didn't give: a
// VM holds it some other way, or it is a DNS server (ADR-0063).
type HeldAddress struct {
	Address netip.Addr
	// Holder is the VM, or the DNS server; From is how rvav knows: "ipam"
	// for a deployment's address (EfficientIP's, adopted, or another
	// range's), "vcenter" for one vCenter reports, "dns" for a DNS server.
	Holder, From string
}

type heldRow struct {
	VCenter string `db:"vcenter"`
	Address string `db:"address"`
	Holder  string `db:"holder"`
	From    string `db:"source"`
}

// heldAddresses are the addresses VMs hold, whatever gave them: the
// ipam.address resources of deployments not deleted, in any vCenter
// (EfficientIP's address space is one) but for a range's, in its own, and
// those vCenter reports for the VMs of these vCenters.
func heldAddresses(ctx context.Context, db DB, vcenters []string) ([]heldRow, error) {
	rows, _ := db.Query(ctx, `select coalesce((select g.vcenter from ip_ranges g where r.properties->>'source' = 'rvav'
				and g.id = r.properties->>'range_id'), '') as vcenter, r.properties->>'address' as address,
			coalesce(nullif(r.properties->>'hostname', ''), r.name) as holder, 'ipam' as source
		from resources r join deployments d on d.id = r.deployment_id
		where r.type = 'ipam.address' and d.status <> 'deleted' and coalesce(r.properties->>'address', '') <> ''
		union all
		select v.vcenter, v.ip_address, v.name, 'vcenter' from inv_vms v where v.vcenter = any($1) and v.ip_address <> ''`,
		nonNil(vcenters))
	return pgx.CollectRows(rows, pgx.RowToStructByName[heldRow])
}

// takenIn are the addresses the range would give that it didn't: held by
// VMs of its vCenter, or by a deployment, or its DNS servers.
func takenIn(r IPRange, held []heldRow, given map[string]bool) []HeldAddress {
	rg := r.Range()
	var out []HeldAddress
	add := func(a, holder, from string) {
		ip, err := netip.ParseAddr(a)
		if err != nil || given[ip.String()] || !rg.Usable(ip) ||
			slices.ContainsFunc(out, func(h HeldAddress) bool { return h.Address == ip }) {
			return
		}
		out = append(out, HeldAddress{Address: ip, Holder: holder, From: from})
	}
	for _, d := range r.DNSServers {
		add(d, d, "dns")
	}
	for _, h := range held {
		if h.VCenter == "" || h.VCenter == r.VCenter {
			add(h.Address, h.Holder, h.From)
		}
	}
	slices.SortFunc(out, func(a, b HeldAddress) int { return a.Address.Compare(b.Address) })
	return out
}

// withTaken works out each range's Taken.
func withTaken(ctx context.Context, db DB, ranges []IPRange) error {
	if len(ranges) == 0 {
		return nil
	}
	var vcs, rangeIDs []string
	for _, r := range ranges {
		if !slices.Contains(vcs, r.VCenter) {
			vcs = append(vcs, r.VCenter)
		}
		rangeIDs = append(rangeIDs, r.ID)
	}
	held, err := heldAddresses(ctx, db, vcs)
	if err != nil {
		return err
	}
	rows, _ := db.Query(ctx, `select range_id, address from ip_allocations where range_id = any($1)`, rangeIDs)
	given := map[string]map[string]bool{}
	var rangeID, address string
	if _, err := pgx.ForEachRow(rows, []any{&rangeID, &address}, func() error {
		if given[rangeID] == nil {
			given[rangeID] = map[string]bool{}
		}
		given[rangeID][address] = true
		return nil
	}); err != nil {
		return err
	}
	for i := range ranges {
		ranges[i].Taken = takenIn(ranges[i], held, given[ranges[i].ID])
	}
	return nil
}
