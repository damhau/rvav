package store

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/iprange"
)

// IPRange is a range of a network's subnet rvav hands addresses from
// itself, for networks EfficientIP doesn't manage (ADR-0063).
type IPRange struct {
	ID               string    `db:"id"`
	Name             string    `db:"name"`
	Description      string    `db:"description"`
	VCenter          string    `db:"vcenter"`
	Network          string    `db:"network"`
	CIDR             string    `db:"cidr"`
	StartAddress     string    `db:"start_address"`
	EndAddress       string    `db:"end_address"`
	Gateway          string    `db:"gateway"`
	DNSServers       []string  `db:"dns_servers"`
	DNSSearchDomains []string  `db:"dns_search_domains"`
	DNSDomain        string    `db:"dns_domain"`
	Excluded         []string  `db:"excluded"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
	// Used is how many of its addresses VMs hold.
	Used int `db:"used"`
}

// Range is its arithmetic; the store keeps only ranges that parse.
func (r IPRange) Range() iprange.Range {
	out, _ := iprange.Parse(r.CIDR, r.StartAddress, r.EndAddress, r.Gateway, r.Excluded)
	return out
}

// IPAllocation is an address a range gave a VM's adapter ("" for its first).
type IPAllocation struct {
	ID             string    `db:"id"`
	RangeID        string    `db:"range_id"`
	Address        string    `db:"address"`
	RequestID      string    `db:"request_id"`
	DeploymentID   string    `db:"deployment_id"`
	Hostname       string    `db:"hostname"`
	NIC            string    `db:"nic"`
	FQDN           string    `db:"fqdn"`
	CreatedAt      time.Time `db:"created_at"`
	DeploymentName *string   `db:"deployment_name"`
}

var (
	// ErrNoRange is a network with no IP range in its vCenter.
	ErrNoRange = errors.New("the network has no IP range")
	// ErrRangeFull is a network whose ranges have no address left.
	ErrRangeFull = errors.New("the network's IP ranges have no free address")
	// ErrAddressOutside is an address the network's ranges don't give.
	ErrAddressOutside = errors.New("the address is not one the network's IP ranges give")
	// ErrAddressHeld is an address another VM holds.
	ErrAddressHeld = errors.New("another VM holds the address")
)

const ipRangeSelect = `select r.id, r.name, r.description, r.vcenter, r.network, r.cidr, r.start_address, r.end_address,
	r.gateway, r.dns_servers, r.dns_search_domains, r.dns_domain, r.excluded, r.created_at, r.updated_at,
	(select count(*) from ip_allocations a where a.range_id = r.id)::int as used
	from ip_ranges r`

const ipRangeOrder = ` order by r.vcenter, r.network, r.created_at, r.id`

// IPRanges lists every range, by vCenter and network.
func (s *Store) IPRanges(ctx context.Context) ([]IPRange, error) {
	rows, _ := s.Pool.Query(ctx, ipRangeSelect+ipRangeOrder)
	return pgx.CollectRows(rows, pgx.RowToStructByName[IPRange])
}

func (s *Store) IPRange(ctx context.Context, id string) (IPRange, error) {
	rows, _ := s.Pool.Query(ctx, ipRangeSelect+` where r.id = $1`, id)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[IPRange])
	return r, notFound(err)
}

// SaveIPRange creates the range (no ID) or replaces it, once check, given
// the range as it was (nil for a new one), every other range and the
// addresses the range gave, accepts it. Ranges
// stay locked meanwhile, so that no address is given while it checks.
func (s *Store) SaveIPRange(ctx context.Context, r IPRange, check func(old *IPRange, others []IPRange, given []string) error) (IPRange, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, ipRangeSelect+ipRangeOrder+` for update of r`)
		all, err := pgx.CollectRows(rows, pgx.RowToStructByName[IPRange])
		if err != nil {
			return err
		}
		var others []IPRange
		var old *IPRange
		for _, o := range all {
			if o.ID == r.ID {
				old = &o
				continue
			}
			others = append(others, o)
		}
		if r.ID != "" && old == nil {
			return ErrNotFound
		}
		var given []string
		if old != nil {
			rows, _ := tx.Query(ctx, `select address from ip_allocations where range_id = $1`, r.ID)
			if given, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
				return err
			}
		}
		if err := check(old, others, given); err != nil {
			return err
		}
		if r.ID == "" {
			r.ID = ids.New(ids.IPRange)
		}
		return tx.QueryRow(ctx, `insert into ip_ranges (id, name, description, vcenter, network, cidr, start_address,
			end_address, gateway, dns_servers, dns_search_domains, dns_domain, excluded)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			on conflict (id) do update set name = excluded.name, description = excluded.description, vcenter = excluded.vcenter,
			network = excluded.network, cidr = excluded.cidr, start_address = excluded.start_address,
			end_address = excluded.end_address, gateway = excluded.gateway, dns_servers = excluded.dns_servers,
			dns_search_domains = excluded.dns_search_domains, dns_domain = excluded.dns_domain, excluded = excluded.excluded,
			updated_at = now()
			returning created_at, updated_at`,
			r.ID, r.Name, r.Description, r.VCenter, r.Network, r.CIDR, r.StartAddress, r.EndAddress, r.Gateway,
			nonNil(r.DNSServers), nonNil(r.DNSSearchDomains), r.DNSDomain, nonNil(r.Excluded)).Scan(&r.CreatedAt, &r.UpdatedAt)
	})
	if err != nil {
		return IPRange{}, err
	}
	return s.IPRange(ctx, r.ID)
}

// DeleteIPRange removes a range; ErrInUse while it gives an address.
func (s *Store) DeleteIPRange(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from ip_ranges where id = $1`, id)
	switch {
	case IsForeignKeyViolation(err):
		return ErrInUse
	case err == nil && tag.RowsAffected() == 0:
		return ErrNotFound
	}
	return err
}

const ipAllocationSelect = `select a.id, a.range_id, a.address, a.request_id, a.deployment_id, a.hostname, a.nic, a.fqdn,
	a.created_at, d.name as deployment_name
	from ip_allocations a left join deployments d on d.id = a.deployment_id`

// IPAllocations lists the addresses a range gave, or every range's ("" for
// all), in the order they were given.
func (s *Store) IPAllocations(ctx context.Context, rangeID string) ([]IPAllocation, error) {
	rows, _ := s.Pool.Query(ctx, ipAllocationSelect+` where $1 = '' or a.range_id = $1 order by a.created_at, a.id`, rangeID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[IPAllocation])
}

// AllocationRequest is an adapter of a VM asking its network's ranges for
// an address: the next free one, or Address when one is asked for.
type AllocationRequest struct {
	VCenter, Network                       string
	RequestID, DeploymentID, Hostname, NIC string
	FQDN, Address                          string
}

// AllocateAddress gives the adapter an address from its network's ranges,
// in their order, or the one it was given before. The network's ranges
// stay locked until the address is recorded: two requests at once never
// get the same address.
func (s *Store) AllocateAddress(ctx context.Context, q AllocationRequest) (IPAllocation, IPRange, error) {
	var out IPAllocation
	var from IPRange
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, ipRangeSelect+` where r.vcenter = $1 and r.network = $2 order by r.created_at, r.id for update of r`,
			q.VCenter, q.Network)
		ranges, err := pgx.CollectRows(rows, pgx.RowToStructByName[IPRange])
		if err != nil {
			return err
		}
		if len(ranges) == 0 {
			return ErrNoRange
		}
		rangeIDs := make([]string, len(ranges))
		for i, r := range ranges {
			rangeIDs[i] = r.ID
		}
		rows, _ = tx.Query(ctx, ipAllocationSelect+` where a.request_id = $1 and a.hostname = $2 and a.nic = $3`,
			q.RequestID, q.Hostname, q.NIC)
		had, err := pgx.CollectRows(rows, pgx.RowToStructByName[IPAllocation])
		if err != nil {
			return err
		}
		for _, h := range had {
			i := indexOfRange(ranges, h.RangeID)
			if i >= 0 && (q.Address == "" || q.Address == h.Address) {
				out, from = h, ranges[i]
				return nil
			}
			// Asked again for another network or address: the old one goes.
			if _, err := tx.Exec(ctx, `delete from ip_allocations where id = $1`, h.ID); err != nil {
				return err
			}
		}
		rows, _ = tx.Query(ctx, `select address from ip_allocations where range_id = any($1)`, rangeIDs)
		given, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		held := map[netip.Addr]bool{}
		for _, g := range given {
			if a, err := netip.ParseAddr(g); err == nil {
				held[a] = true
			}
		}
		var ip netip.Addr
		if q.Address != "" {
			a, err := netip.ParseAddr(q.Address)
			if err != nil {
				return ErrAddressOutside
			}
			i := -1
			for j, r := range ranges {
				if r.Range().Usable(a) {
					i = j
					break
				}
			}
			switch {
			case i < 0:
				return ErrAddressOutside
			case held[a]:
				return ErrAddressHeld
			}
			ip, from = a, ranges[i]
		} else {
			for _, r := range ranges {
				if a, ok := r.Range().First(func(a netip.Addr) bool { return held[a] }); ok {
					ip, from = a, r
					break
				}
			}
			if !ip.IsValid() {
				return ErrRangeFull
			}
		}
		out = IPAllocation{ID: ids.New(ids.IPAllocation), RangeID: from.ID, Address: ip.String(), RequestID: q.RequestID,
			DeploymentID: q.DeploymentID, Hostname: q.Hostname, NIC: q.NIC, FQDN: q.FQDN}
		return tx.QueryRow(ctx, `insert into ip_allocations (id, range_id, address, request_id, deployment_id, hostname, nic, fqdn)
			values ($1,$2,$3,$4,$5,$6,$7,$8) returning created_at`,
			out.ID, out.RangeID, out.Address, out.RequestID, out.DeploymentID, out.Hostname, out.NIC, out.FQDN).Scan(&out.CreatedAt)
	})
	return out, from, err
}

func indexOfRange(ranges []IPRange, id string) int {
	for i, r := range ranges {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// ReleaseAddress gives an address back to its range; one already given
// back is no error.
func (s *Store) ReleaseAddress(ctx context.Context, db DB, id string) error {
	_, err := db.Exec(ctx, `delete from ip_allocations where id = $1`, id)
	return err
}

// RenameAllocation follows a VM's new name.
func (s *Store) RenameAllocation(ctx context.Context, db DB, id, hostname, fqdn string) error {
	_, err := db.Exec(ctx, `update ip_allocations set hostname = $2, fqdn = $3 where id = $1`, id, hostname, fqdn)
	return err
}

// ReleaseRequestAddresses gives back the addresses a request's VMs got
// from ranges, but for the VMs in keep.
func (s *Store) ReleaseRequestAddresses(ctx context.Context, requestID string, keep []string) ([]IPAllocation, error) {
	rows, _ := s.Pool.Query(ctx, `delete from ip_allocations where request_id = $1 and not hostname = any($2)
		returning id, range_id, address, request_id, deployment_id, hostname, nic, fqdn, created_at, null::text as deployment_name`,
		requestID, nonNil(keep))
	return pgx.CollectRows(rows, pgx.RowToStructByName[IPAllocation])
}

// orphanAllocation: no deployment has the allocation's VM, and the request
// that took it has ended.
const orphanAllocation = ` not exists (select 1 from resources r join deployments rd on rd.id = r.deployment_id
		where r.type = 'vsphere.vm' and r.deployment_id = a.deployment_id and r.name = a.hostname and rd.status <> 'deleted')
	and not exists (select 1 from requests q where q.id = a.request_id
		and q.status in ('pending_approval', 'scheduled', 'queued', 'running', 'cancelling'))`

// OrphanIPAllocations are the addresses ranges gave to VMs no deployment
// has any more (ADR-0063), as an unregistered VM leaves them.
func (s *Store) OrphanIPAllocations(ctx context.Context) ([]IPAllocation, error) {
	rows, _ := s.Pool.Query(ctx, ipAllocationSelect+` where`+orphanAllocation+` order by a.created_at, a.id`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[IPAllocation])
}

// ReleaseOrphanAllocation gives back the address a range gave, when it is
// an orphan (ErrInUse otherwise), with the resource that records it.
func (s *Store) ReleaseOrphanAllocation(ctx context.Context, rangeID, address string) (IPAllocation, error) {
	var out IPAllocation
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, ipAllocationSelect+` where a.range_id = $1 and a.address = $2 for update of a`, rangeID, address)
		al, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[IPAllocation])
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = al
		tag, err := tx.Exec(ctx, `delete from ip_allocations a where a.id = $1 and`+orphanAllocation, al.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrInUse
		}
		_, err = tx.Exec(ctx, `delete from resources where type = 'ipam.address' and external_id = $1`, "rvav:"+al.ID)
		return err
	})
	return out, err
}

// AddressedVM is a VM of the inventory with the address vCenter reports
// for it, and the deployment that has it, if any.
type AddressedVM struct {
	VCenter        string  `db:"vcenter"`
	Name           string  `db:"name"`
	IPAddress      string  `db:"ip_address"`
	DeploymentID   *string `db:"deployment_id"`
	DeploymentName *string `db:"deployment_name"`
}

// AddressedVMs lists the VMs of these vCenters that have an address.
func (s *Store) AddressedVMs(ctx context.Context, vcenters []string) ([]AddressedVM, error) {
	rows, _ := s.Pool.Query(ctx, `select v.vcenter, v.name, v.ip_address, m.id as deployment_id, m.name as deployment_name
		from inv_vms v left join lateral (
			select d.id, d.name from resources r join deployments d on d.id = r.deployment_id
			where r.type = 'vsphere.vm' and d.status <> 'deleted'
			  and (r.external_id = v.instance_uuid or r.external_id = v.moref and v.instance_uuid = '') limit 1) m on true
		where v.vcenter = any($1) and v.ip_address <> '' order by v.vcenter, v.name`, nonNil(vcenters))
	return pgx.CollectRows(rows, pgx.RowToStructByName[AddressedVM])
}
