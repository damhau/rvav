package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// UnmanagedVM is a VM of the inventory that no deployment has (S13.3).
type UnmanagedVM struct {
	VCenter      string   `db:"vcenter"`
	Moref        string   `db:"moref"`
	InstanceUUID string   `db:"instance_uuid"`
	Name         string   `db:"name"`
	PowerState   string   `db:"power_state"`
	GuestOS      string   `db:"guest_os"`
	CPUCount     int      `db:"cpu_count"`
	MemoryMB     int      `db:"memory_mb"`
	Cluster      string   `db:"cluster"`
	Host         string   `db:"host"`
	IPAddress    string   `db:"ip_address"`
	Hostname     string   `db:"hostname"`
	Folder       string   `db:"folder"`
	Tags         []string `db:"tags"`
}

// InvFilter picks VMs of the inventory: every criterion given must match.
type InvFilter struct {
	VCenter, Cluster, PowerState, Search string
	// Folder is a folder's path: VMs in it and in the folders below.
	Folder string
	// Tag is a vSphere tag, category/tag.
	Tag string
}

func (f InvFilter) args() []any {
	return []any{f.VCenter, f.Cluster, f.PowerState, f.Search, f.Folder, f.Tag}
}

type UnmanagedFilter struct {
	InvFilter
	Limit int
	After *Cursor
}

const unmanagedCols = `v.vcenter, v.moref, v.instance_uuid, v.name, v.power_state, v.guest_os, v.cpu_count,
	v.memory_mb, v.cluster, v.host, v.ip_address, v.hostname, v.folder, v.tags`

// vmHolder is the deployment that has inventory VM v, if any.
const vmHolder = `(select d.id, d.name from resources r join deployments d on d.id = r.deployment_id
	where r.type = 'vsphere.vm' and d.status <> 'deleted'
	  and (r.external_id = v.instance_uuid or r.external_id = v.moref and v.instance_uuid = '')
	order by d.created_at limit 1)`

// vmsBuilding are the names of the VMs requests are building: in vCenter
// before rvav records them.
const vmsBuilding = `(select u->>'name' from requests q,
	    jsonb_array_elements(case when jsonb_typeof(q.plan->'vms') = 'array' then q.plan->'vms' else '[]' end) u
	    where q.status in ('pending_approval', 'scheduled', 'queued', 'running', 'cancelling')
	  union select u->>'name' from requests q,
	    jsonb_array_elements(case when jsonb_typeof(q.plan->'composite') = 'array' then q.plan->'composite' else '[]' end) st,
	    jsonb_array_elements(case when jsonb_typeof(st->'vms') = 'array' then st->'vms' else '[]' end) u
	    where q.status in ('pending_approval', 'scheduled', 'queued', 'running', 'cancelling'))`

// vmMatches applies a InvFilter's six arguments, $1 to $6.
const vmMatches = `($1 = '' or v.vcenter = $1) and ($2 = '' or v.cluster = $2) and ($3 = '' or v.power_state = $3)
	  and ($4 = '' or v.name ilike '%' || $4 || '%' or v.ip_address like $4 || '%' or v.hostname ilike '%' || $4 || '%')
	  and ($5 = '' or v.folder = $5 or starts_with(v.folder, rtrim($5, '/') || '/'))
	  and ($6 = '' or $6 = any(v.tags))`

const unmanagedWhere = `
	from inv_vms v
	where not exists ` + vmHolder + `
	  and v.name not in ` + vmsBuilding + `
	  and ` + vmMatches

// UnmanagedVMs lists them by name, and says how many match.
func (s *Store) UnmanagedVMs(ctx context.Context, f UnmanagedFilter) (Page[UnmanagedVM], int, error) {
	total, err := s.CountUnmanagedVMs(ctx, f.InvFilter)
	if err != nil {
		return Page[UnmanagedVM]{}, 0, err
	}
	args := append(f.args(), f.Limit+1, "", "")
	if f.After != nil {
		args[7], args[8] = f.After.Value, f.After.ID
	}
	rows, _ := s.Pool.Query(ctx, `select `+unmanagedCols+unmanagedWhere+`
		  and ($8 = '' or (v.name, v.vcenter || '/' || v.moref) > ($8, $9))
		order by v.name, v.vcenter || '/' || v.moref limit $7`, args...)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[UnmanagedVM])
	if err != nil {
		return Page[UnmanagedVM]{}, 0, err
	}
	return paginate(list, f.Limit, func(v UnmanagedVM) Cursor { return Cursor{Value: v.Name, ID: v.VCenter + "/" + v.Moref} }),
		total, nil
}

// CountUnmanagedVMs says how many VMs no deployment has match a filter.
func (s *Store) CountUnmanagedVMs(ctx context.Context, f InvFilter) (int, error) {
	var total int
	err := s.Pool.QueryRow(ctx, `select count(*)`+unmanagedWhere, f.args()...).Scan(&total)
	return total, err
}

// GetUnmanagedVM finds one of them; ErrNotFound when the inventory has no
// such VM, or a deployment has it.
func (s *Store) GetUnmanagedVM(ctx context.Context, vcenter, moref string) (UnmanagedVM, error) {
	rows, _ := s.Pool.Query(ctx, `select `+unmanagedCols+unmanagedWhere+` and v.vcenter = $7 and v.moref = $8`,
		append(InvFilter{}.args(), vcenter, moref)...)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[UnmanagedVM])
	return v, notFound(err)
}

// MatchedVM is a VM of the inventory a plan's filter matches, with the
// deployment that has it, or whether a request is building it.
type MatchedVM struct {
	UnmanagedVM
	DeploymentID   string `db:"deployment_id"`
	DeploymentName string `db:"deployment_name"`
	Building       bool   `db:"building"`
}

// MatchingVMs lists every VM of the inventory a filter matches, by name,
// held or not.
func (s *Store) MatchingVMs(ctx context.Context, f InvFilter) ([]MatchedVM, error) {
	rows, _ := s.Pool.Query(ctx, `select `+unmanagedCols+`, coalesce(h.id, '') as deployment_id, coalesce(h.name, '') as deployment_name,
		  coalesce(v.name in `+vmsBuilding+`, false) as building
		from inv_vms v left join lateral `+vmHolder+` h on true
		where `+vmMatches+`
		order by v.name, v.vcenter, v.moref`, f.args()...)
	return pgx.CollectRows(rows, pgx.RowToStructByName[MatchedVM])
}
