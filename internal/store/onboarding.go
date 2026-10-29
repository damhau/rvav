package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// UnmanagedVM is a VM of the inventory that no deployment has (S13.3).
type UnmanagedVM struct {
	VCenter      string `db:"vcenter"`
	Moref        string `db:"moref"`
	InstanceUUID string `db:"instance_uuid"`
	Name         string `db:"name"`
	PowerState   string `db:"power_state"`
	GuestOS      string `db:"guest_os"`
	CPUCount     int    `db:"cpu_count"`
	MemoryMB     int    `db:"memory_mb"`
	Cluster      string `db:"cluster"`
	Host         string `db:"host"`
	IPAddress    string `db:"ip_address"`
	Hostname     string `db:"hostname"`
}

type UnmanagedFilter struct {
	VCenter, Cluster, PowerState, Search string
	Limit                                int
	After                                *Cursor
}

const unmanagedWhere = `
	from inv_vms v
	where not exists (select 1 from resources r join deployments d on d.id = r.deployment_id
	                  where r.type = 'vsphere.vm' and d.status <> 'deleted'
	                    and (r.external_id = v.instance_uuid or r.external_id = v.moref and v.instance_uuid = ''))
	  and ($1 = '' or v.vcenter = $1) and ($2 = '' or v.cluster = $2) and ($3 = '' or v.power_state = $3)
	  and ($4 = '' or v.name ilike '%' || $4 || '%' or v.ip_address like $4 || '%' or v.hostname ilike '%' || $4 || '%')`

// UnmanagedVMs lists them by name, and says how many match.
func (s *Store) UnmanagedVMs(ctx context.Context, f UnmanagedFilter) (Page[UnmanagedVM], int, error) {
	args := []any{f.VCenter, f.Cluster, f.PowerState, f.Search, f.Limit + 1, "", ""}
	if f.After != nil {
		args[5], args[6] = f.After.Value, f.After.ID
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `select count(*)`+unmanagedWhere, args[:4]...).Scan(&total); err != nil {
		return Page[UnmanagedVM]{}, 0, err
	}
	rows, _ := s.Pool.Query(ctx, `select v.vcenter, v.moref, v.instance_uuid, v.name, v.power_state, v.guest_os, v.cpu_count,
		v.memory_mb, v.cluster, v.host, v.ip_address, v.hostname`+unmanagedWhere+`
		  and ($6 = '' or (v.name, v.vcenter || '/' || v.moref) > ($6, $7))
		order by v.name, v.vcenter || '/' || v.moref limit $5`, args...)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[UnmanagedVM])
	if err != nil {
		return Page[UnmanagedVM]{}, 0, err
	}
	return paginate(list, f.Limit, func(v UnmanagedVM) Cursor { return Cursor{Value: v.Name, ID: v.VCenter + "/" + v.Moref} }),
		total, nil
}

// GetUnmanagedVM finds one of them; ErrNotFound when the inventory has no
// such VM, or a deployment has it.
func (s *Store) GetUnmanagedVM(ctx context.Context, vcenter, moref string) (UnmanagedVM, error) {
	rows, _ := s.Pool.Query(ctx, `select v.vcenter, v.moref, v.instance_uuid, v.name, v.power_state, v.guest_os, v.cpu_count,
		v.memory_mb, v.cluster, v.host, v.ip_address, v.hostname`+unmanagedWhere+` and v.vcenter = $5 and v.moref = $6`,
		"", "", "", "", vcenter, moref)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[UnmanagedVM])
	return v, notFound(err)
}
