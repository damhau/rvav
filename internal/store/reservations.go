package store

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReservationTTL is how long a reservation counts after it was made or its
// build started (ADR-0036). It is the longest one step of a build may take
// (an AWX job's six hours), so a build that is still running keeps it, and
// one whose worker or workflow is gone stops counting within the day.
var ReservationTTL = 6 * time.Hour

// Reservation is the memory and vCPU one VM a request places holds on its
// cluster until the VM exists (ADR-0036).
type Reservation struct {
	VMName, DeploymentID, VCenter, Cluster, ResourcePool string
	MemoryMB                                             int64
	VCPU                                                 int
}

// Reserved is what a request's reservations that count hold in one
// resource pool of a cluster ("" for its root pool).
type Reserved struct {
	RequestID    string `db:"request_id"`
	VCenter      string `db:"vcenter"`
	Cluster      string `db:"cluster"`
	ResourcePool string `db:"resource_pool"`
	MemoryMB     int64  `db:"memory_mb"`
	VCPU         int    `db:"vcpu"`
	VMs          int    `db:"vms"`
}

// liveReservation is a reservation r that counts: its request waits or
// runs (a request being cancelled may still build its VM), it hasn't
// expired, and its VM is neither in the inventory nor recorded as its
// deployment's resource.
const liveReservation = `r.expires_at > now()
	and exists (select 1 from requests q where q.id = r.request_id
		and q.status in ('pending_approval', 'scheduled', 'queued', 'running', 'cancelling'))
	and not exists (select 1 from inv_vms v where v.vcenter = r.vcenter and v.name = r.vm_name)
	and not exists (select 1 from resources x where x.deployment_id = r.deployment_id and x.type = 'vsphere.vm'
		and x.name = r.vm_name)`

// Reserve replaces a request's reservations with these, each counting for
// ReservationTTL from now.
func (s *Store) Reserve(ctx context.Context, db DB, requestID string, rows []Reservation) error {
	if _, err := db.Exec(ctx, `delete from placement_reservations where request_id = $1`, requestID); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, r := range rows {
		b.Queue(`insert into placement_reservations (request_id, vm_name, deployment_id, vcenter, cluster, resource_pool,
			memory_mb, vcpu, expires_at) values ($1,$2,$3,$4,$5,$6,$7,$8, now() + make_interval(secs => $9))
			on conflict (request_id, vm_name) do update set deployment_id = excluded.deployment_id, vcenter = excluded.vcenter,
			cluster = excluded.cluster, resource_pool = excluded.resource_pool, memory_mb = excluded.memory_mb,
			vcpu = excluded.vcpu, expires_at = excluded.expires_at`,
			requestID, r.VMName, r.DeploymentID, r.VCenter, r.Cluster, r.ResourcePool, r.MemoryMB, r.VCPU, ReservationTTL.Seconds())
	}
	return db.SendBatch(ctx, b).Close()
}

// Reservations are the reservations that count, by request, cluster and
// resource pool, but those of the request except.
func (s *Store) Reservations(ctx context.Context, db DB, except string) ([]Reserved, error) {
	rows, _ := db.Query(ctx, `select r.request_id, r.vcenter, r.cluster, r.resource_pool, sum(r.memory_mb)::bigint as memory_mb,
		sum(r.vcpu)::int as vcpu, count(*)::int as vms
		from placement_reservations r where r.request_id <> $1 and `+liveReservation+`
		group by 1, 2, 3, 4 order by 2, 3, 4, 1`, except)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Reserved])
}

// LockClusters holds placement on these clusters ("vcenter/cluster")
// until the transaction ends, so that two requests placed at the same time
// can't both take the last of their room. Keys are taken in one order, so
// two holders never deadlock.
func (s *Store) LockClusters(ctx context.Context, tx pgx.Tx, keys []string) error {
	for _, k := range slices.Compact(slices.Sorted(slices.Values(keys))) {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('rvav.placement'), hashtext($1))`, k); err != nil {
			return err
		}
	}
	return nil
}

// SweepReservations deletes the reservations that no longer count.
func (s *Store) SweepReservations(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `delete from placement_reservations r where not (`+liveReservation+`)`)
	return tag.RowsAffected(), err
}
