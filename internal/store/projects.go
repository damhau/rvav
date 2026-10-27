package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s *Store) ListEnvironments(ctx context.Context) ([]Environment, error) {
	rows, _ := s.Pool.Query(ctx, `select key, name, production, sort_order, snapshot_max_count, snapshot_max_age_days from environments order by sort_order, key`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Environment])
}

func (s *Store) GetEnvironment(ctx context.Context, key string) (Environment, error) {
	rows, _ := s.Pool.Query(ctx, `select key, name, production, sort_order, snapshot_max_count, snapshot_max_age_days from environments where key = $1`, key)
	e, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Environment])
	return e, notFound(err)
}

const projectCols = `id, code, name, description, cost_center, environments`

// ListProjects returns projects ordered by name. A nil only list means all projects.
func (s *Store) ListProjects(ctx context.Context, only []string, limit int, after *Cursor) (Page[Project], error) {
	args := []any{only == nil, only, limit + 1, "", ""}
	if after != nil {
		args[3], args[4] = after.Value, after.ID
	}
	rows, _ := s.Pool.Query(ctx, `select `+projectCols+` from projects
		where ($1 or id = any($2))
		  and ($4 = '' or (name, id) > ($4, $5))
		order by name, id limit $3`, args...)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[Project])
	if err != nil {
		return Page[Project]{}, err
	}
	return paginate(list, limit, func(p Project) Cursor { return Cursor{Value: p.Name, ID: p.ID} }), nil
}

// GetProject finds a project by ID or code.
func (s *Store) GetProject(ctx context.Context, idOrCode string) (Project, error) {
	rows, _ := s.Pool.Query(ctx, `select `+projectCols+` from projects where id = $1 or code = $1`, idOrCode)
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Project])
	return p, notFound(err)
}

func (s *Store) ProjectsByID(ctx context.Context, idList []string) (map[string]Project, error) {
	rows, _ := s.Pool.Query(ctx, `select `+projectCols+` from projects where id = any($1)`, idList)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[Project])
	if err != nil {
		return nil, err
	}
	out := make(map[string]Project, len(list))
	for _, p := range list {
		out[p.ID] = p
	}
	return out, nil
}

// QuotaLimits returns the limits for one environment (” for project-wide).
func (s *Store) QuotaLimits(ctx context.Context, projectID, environment string) ([]QuotaLimit, error) {
	rows, _ := s.Pool.Query(ctx, `select environment, resource, limit_value, enforcement
		from quotas where project_id = $1 and environment = $2 order by resource`, projectID, environment)
	return pgx.CollectRows(rows, pgx.RowToStructByName[QuotaLimit])
}

// QuotaUsed sums what live deployments allocate. Deployments still being
// provisioned are counted as reserved by their request instead.
func (s *Store) QuotaUsed(ctx context.Context, projectID, environment string) (QuotaAmounts, error) {
	var vcpu, memMB, storage, vms int
	err := s.Pool.QueryRow(ctx, `
		select coalesce(sum((r.properties->'vm'->>'cpu_count')::int), 0),
		       coalesce(sum((r.properties->'vm'->>'memory_mb')::int), 0),
		       coalesce(sum((select coalesce(sum((d->>'capacity_gb')::int), 0)
		                     from jsonb_array_elements(r.properties->'vm'->'disks') d)), 0),
		       count(*)
		from resources r join deployments dp on dp.id = r.deployment_id
		where r.type = 'vsphere.vm' and dp.project_id = $1
		  and ($2 = '' or dp.environment = $2)
		  and dp.status not in ('provisioning', 'deleted')`, projectID, environment).
		Scan(&vcpu, &memMB, &storage, &vms)
	if err != nil {
		return nil, err
	}
	return QuotaAmounts{"vcpu": vcpu, "memory_gb": memMB / 1024, "storage_gb": storage, "vm_count": vms}, nil
}

// QuotaReserved sums the planned amounts of requests not finished yet,
// including those waiting for approval: approving them must not take a
// project over a limit that others counted on.
func (s *Store) QuotaReserved(ctx context.Context, projectID, environment string) (QuotaAmounts, error) {
	out := QuotaAmounts{}
	rows, err := s.Pool.Query(ctx, `
		select q.key, coalesce(sum(q.value::int), 0)
		from requests r, jsonb_each_text(case when jsonb_typeof(r.plan->'quota') = 'object'
		                                      then r.plan->'quota' else '{}' end) q
		where r.project_id = $1 and ($2 = '' or r.environment = $2)
		  and r.status in ('pending_approval', 'queued', 'scheduled', 'running')
		group by q.key`, projectID, environment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) PlacementRules(ctx context.Context, projectID, environment string) ([]PlacementRule, error) {
	rows, _ := s.Pool.Query(ctx, `select id, project_id, environment, priority, cluster_patterns,
		datastore_patterns, network_patterns, folder
		from placement_rules where project_id = $1 and environment = $2 order by priority, id`, projectID, environment)
	return pgx.CollectRows(rows, pgx.RowToStructByName[PlacementRule])
}

const subnetColumns = `network, subnet, gateway, dns_zone, dns_servers, efficientip_subnet_id, subnet_name, source`

// SyncNetworkSubnets replaces the mappings found in EfficientIP with these:
// rows set by hand are kept, and get the subnet's ID when their CIDR is
// the one found; synced rows neither found nor held are removed.
func (s *Store) SyncNetworkSubnets(ctx context.Context, found []NetworkSubnet, held []string) (synced, removed int, err error) {
	err = s.Tx(ctx, func(tx pgx.Tx) error {
		networks := make([]string, len(found))
		for i, n := range found {
			networks[i] = n.Network
			tag, err := tx.Exec(ctx, `insert into network_subnets (network, subnet, gateway, dns_zone, dns_servers,
				efficientip_subnet_id, subnet_name, source, synced_at) values ($1, $2, $3, $4, $5, $6, $7, 'efficientip', now())
				on conflict (network) do update set subnet = excluded.subnet, gateway = excluded.gateway, dns_zone = excluded.dns_zone,
					dns_servers = excluded.dns_servers, efficientip_subnet_id = excluded.efficientip_subnet_id,
					subnet_name = excluded.subnet_name, synced_at = now()
				where network_subnets.source = 'efficientip'`, n.Network, n.Subnet, n.Gateway, n.DNSZone, n.DNSServers, n.EfficientIPSubnetID, n.SubnetName)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				if _, err := tx.Exec(ctx, `update network_subnets set efficientip_subnet_id = $2, subnet_name = $3, synced_at = now()
					where network = $1 and source = 'manual' and subnet = $4`, n.Network, n.EfficientIPSubnetID, n.SubnetName, n.Subnet); err != nil {
					return err
				}
			}
		}
		tag, err := tx.Exec(ctx, `delete from network_subnets where source = 'efficientip' and not (network = any($1))
			and not (network = any($2))`, networks, append([]string{}, held...))
		removed = int(tag.RowsAffected())
		return err
	})
	return len(found), removed, err
}

func (s *Store) NetworkSubnet(ctx context.Context, network string) (NetworkSubnet, error) {
	rows, _ := s.Pool.Query(ctx, `select `+subnetColumns+` from network_subnets where network = $1`, network)
	n, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[NetworkSubnet])
	return n, notFound(err)
}

func (s *Store) NetworkSubnets(ctx context.Context) (map[string]NetworkSubnet, error) {
	rows, _ := s.Pool.Query(ctx, `select `+subnetColumns+` from network_subnets`)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[NetworkSubnet])
	if err != nil {
		return nil, err
	}
	out := make(map[string]NetworkSubnet, len(list))
	for _, n := range list {
		out[n.Network] = n
	}
	return out, nil
}

// LeasePolicy returns the project's lease policy for the environment, or
// the environment's.
func (s *Store) LeasePolicy(ctx context.Context, projectID, environment string) (LeasePolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select environment, project_id, default_days, max_days, max_extensions, grace_days, expiry_action
		from lease_policies where environment = $1 and (project_id = $2 or project_id is null)
		order by project_id nulls last limit 1`, environment, projectID)
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[LeasePolicy])
	if err == pgx.ErrNoRows {
		return LeasePolicy{Environment: environment, ExpiryAction: "notify_only"}, nil
	}
	return p, err
}

// NextName reserves the next counter value for a naming prefix, skipping
// values for which taken returns true.
func (s *Store) NextName(ctx context.Context, db DB, prefix string, taken func(int) bool) (int, error) {
	for {
		var n int
		err := db.QueryRow(ctx, `insert into name_counters (prefix, last_value) values ($1, 1)
			on conflict (prefix) do update set last_value = name_counters.last_value + 1
			returning last_value`, prefix).Scan(&n)
		if err != nil {
			return 0, err
		}
		if !taken(n) {
			return n, nil
		}
	}
}

// PeekName returns the counter value NextName would try next.
func (s *Store) PeekName(ctx context.Context, prefix string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select coalesce((select last_value from name_counters where prefix = $1), 0) + 1`, prefix).Scan(&n)
	return n, err
}

// ProjectAdmins returns the project's admins: users with the role, and the
// OIDC groups mapped to it.
func (s *Store) ProjectAdmins(ctx context.Context, projectID string) (ids, groups []string, err error) {
	rows, _ := s.Pool.Query(ctx, `select user_id from project_members where project_id = $1 and role = 'project-admin' order by user_id`, projectID)
	if ids, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return nil, nil, err
	}
	rows, _ = s.Pool.Query(ctx, `select oidc_group from group_mappings where project_id = $1 and role = 'project-admin' order by oidc_group`, projectID)
	groups, err = pgx.CollectRows(rows, pgx.RowTo[string])
	return ids, groups, err
}

// ProjectMembers lists the users with a role in the project, directly or
// through a group seen in their token, by name.
func (s *Store) ProjectMembers(ctx context.Context, projectID string) ([]User, error) {
	rows, _ := s.Pool.Query(ctx, `select u.id, u.subject, u.name, u.email, u.title, u.global_roles from users u
		where exists (select 1 from project_members m where m.user_id = u.id and m.project_id = $1)
		   or exists (select 1 from user_groups g join group_mappings gm on gm.oidc_group = g.group_name
		              where g.user_id = u.id and gm.project_id = $1)
		order by u.name, u.id`, projectID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[User])
}
