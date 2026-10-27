package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const deploymentRowSelect = `
select d.id, d.name, d.status, d.project_id, d.environment, d.item_id, d.version_id, d.owner_id,
       d.created_by_request_id, d.created_at, d.lease_expires_at, d.lease_extensions_used,
       d.lease_extensions_max, d.expiry_action, d.sync_state, d.inputs,
       p.code as project_code, p.name as project_name, i.slug as item_slug, i.name as item_name,
       v.number as version_number, u.name as owner_name, u.email as owner_email,
       (select count(*) from resources r where r.deployment_id = d.id and r.type = 'vsphere.vm')::int as vm_count,
       (select count(*) from resources r where r.deployment_id = d.id and r.type = 'vsphere.vm'
          and r.properties->'vm'->>'power_state' = 'on')::int as vms_powered_on,
       pa.ip as primary_ip, pa.fqdn as primary_fqdn
from deployments d
join projects p on p.id = d.project_id
join catalog_items i on i.id = d.item_id
join catalog_item_versions v on v.id = d.version_id
join users u on u.id = d.owner_id
left join lateral (select r.properties->>'address' as ip, r.properties->>'fqdn' as fqdn
                   from resources r where r.deployment_id = d.id and r.type = 'ipam.address'
                   order by r.created_at limit 1) pa on true`

func (s *Store) InsertDeployment(ctx context.Context, db DB, d Deployment) error {
	_, err := db.Exec(ctx, `insert into deployments (id, name, status, project_id, environment, item_id, version_id,
		owner_id, created_by_request_id, lease_expires_at, lease_extensions_max, expiry_action, inputs)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		d.ID, d.Name, d.Status, d.ProjectID, d.Environment, d.ItemID, d.VersionID, d.OwnerID,
		d.CreatedByRequestID, d.LeaseExpiresAt, d.LeaseExtensionsMax, d.ExpiryAction, jsonOr(d.Inputs, "{}"))
	return err
}

func (s *Store) GetDeployment(ctx context.Context, id string) (DeploymentRow, error) {
	rows, _ := s.Pool.Query(ctx, deploymentRowSelect+` where d.id = $1`, id)
	d, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[DeploymentRow])
	return d, notFound(err)
}

// DeploymentsForRequest lists the deployments a request created, or the one
// a day-2 request acts on.
func (s *Store) DeploymentsForRequest(ctx context.Context, requestID string) ([]DeploymentRow, error) {
	rows, _ := s.Pool.Query(ctx, deploymentRowSelect+` where d.created_by_request_id = $1
		or d.id = (select deployment_id from requests where id = $1) order by d.created_at, d.id`, requestID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[DeploymentRow])
}

type DeploymentFilter struct {
	Projects           []string // nil means all projects
	ProjectID          string
	Statuses           []string
	Environments       []string
	OwnerID            string
	LeaseExpiresBefore *time.Time
	Search             string
	Sort               string // name, -name, created_at, -created_at, lease_expires_at, -lease_expires_at
	IncludeDeleted     bool
	Limit              int
	After              *Cursor
}

var deploymentSorts = map[string]struct {
	expr string
	desc bool
}{
	"name":              {"d.name", false},
	"-name":             {"d.name", true},
	"created_at":        {"to_char(d.created_at at time zone 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US')", false},
	"-created_at":       {"to_char(d.created_at at time zone 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US')", true},
	"lease_expires_at":  {"coalesce(to_char(d.lease_expires_at at time zone 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US'), '9999')", false},
	"-lease_expires_at": {"coalesce(to_char(d.lease_expires_at at time zone 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US'), '9999')", true},
}

// ListDeployments filters and sorts deployments with keyset pagination. The
// sort key is compared as text so one cursor format covers every sort.
func (s *Store) ListDeployments(ctx context.Context, f DeploymentFilter) (Page[DeploymentRow], error) {
	sort, ok := deploymentSorts[f.Sort]
	if !ok {
		sort = deploymentSorts["name"]
	}
	cmp, dir := ">", "asc"
	if sort.desc {
		cmp, dir = "<", "desc"
	}
	after := Cursor{}
	if f.After != nil {
		after = *f.After
	}
	q := fmt.Sprintf(`select * from (%s
		where ($1 or d.project_id = any($2))
		  and ($3 = '' or d.project_id = $3)
		  and (cardinality($4::text[]) = 0 or d.status = any($4))
		  and (cardinality($5::text[]) = 0 or d.environment = any($5))
		  and ($6 = '' or d.owner_id = $6)
		  and ($7::timestamptz is null or d.lease_expires_at < $7)
		  and ($8 = '' or d.name ilike $8 or u.name ilike $8 or exists (
		        select 1 from resources r where r.deployment_id = d.id
		        and (r.name ilike $8 or r.properties->>'address' ilike $8 or r.properties->'vm'->>'fqdn' ilike $8)))
		  and ($9 or d.status <> 'deleted')
		) d0, lateral (select %s as sort_key from deployments d where d.id = d0.id) k
		where ($10 = '' or (k.sort_key, d0.id) %s ($10, $11))
		order by k.sort_key %s, d0.id %s limit $12`,
		deploymentRowSelect, sort.expr, cmp, dir, dir)
	rows, err := s.Pool.Query(ctx, q, f.Projects == nil, f.Projects, f.ProjectID, nonNil(f.Statuses),
		nonNil(f.Environments), f.OwnerID, f.LeaseExpiresBefore, SearchText(f.Search), f.IncludeDeleted,
		after.Value, after.ID, f.Limit+1)
	if err != nil {
		return Page[DeploymentRow]{}, err
	}
	type withKey struct {
		DeploymentRow
		SortKey string `db:"sort_key"`
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[withKey])
	if err != nil {
		return Page[DeploymentRow]{}, err
	}
	page := paginate(list, f.Limit, func(w withKey) Cursor { return Cursor{Value: w.SortKey, ID: w.ID} })
	out := Page[DeploymentRow]{Next: page.Next, Items: make([]DeploymentRow, len(page.Items))}
	for i, w := range page.Items {
		out.Items[i] = w.DeploymentRow
	}
	return out, nil
}

func (s *Store) SetDeploymentStatus(ctx context.Context, db DB, id, status string) error {
	_, err := db.Exec(ctx, `update deployments set status = $2,
		deleted_at = case when $2 = 'deleted' then now() else deleted_at end where id = $1`, id, status)
	return err
}

func (s *Store) SetDeploymentsStatusForRequest(ctx context.Context, db DB, requestID, status string) error {
	_, err := db.Exec(ctx, `update deployments set status = $2,
		deleted_at = case when $2 = 'deleted' then now() else deleted_at end
		where created_by_request_id = $1 and status <> 'deleted'`, requestID, status)
	return err
}

const resourceCols = `id, deployment_id, request_id, type, name, external_id, sync_state, properties`

// UpsertResource creates a resource or replaces its properties, keyed by type
// and external ID.
func (s *Store) UpsertResource(ctx context.Context, db DB, r Resource) (Resource, error) {
	rows, _ := db.Query(ctx, `insert into resources (id, deployment_id, request_id, type, name, external_id, sync_state, properties)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		on conflict (type, external_id) do update set name = excluded.name, properties = excluded.properties,
		       sync_state = excluded.sync_state, updated_at = now()
		returning `+resourceCols,
		r.ID, r.DeploymentID, r.RequestID, r.Type, r.Name, r.ExternalID, orDefault(r.SyncState, "in_sync"), jsonOr(r.Properties, "{}"))
	res, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Resource])
	return res, err
}

func (s *Store) GetResource(ctx context.Context, id string) (Resource, error) {
	rows, _ := s.Pool.Query(ctx, `select `+resourceCols+` from resources where id = $1`, id)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Resource])
	return r, notFound(err)
}

func (s *Store) ListResources(ctx context.Context, deploymentID string) ([]Resource, error) {
	rows, _ := s.Pool.Query(ctx, `select `+resourceCols+` from resources where deployment_id = $1
		order by case type when 'vsphere.vm' then 0 when 'ipam.address' then 1 else 2 end, name, id`, deploymentID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Resource])
}

func (s *Store) ResourcesForRequest(ctx context.Context, requestID string) ([]Resource, error) {
	rows, _ := s.Pool.Query(ctx, `select `+resourceCols+` from resources where request_id = $1 order by created_at, id`, requestID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Resource])
}

func (s *Store) FindResource(ctx context.Context, typ, externalID string) (Resource, error) {
	rows, _ := s.Pool.Query(ctx, `select `+resourceCols+` from resources where type = $1 and external_id = $2`, typ, externalID)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Resource])
	return r, notFound(err)
}

func (s *Store) DeleteResource(ctx context.Context, db DB, id string) error {
	_, err := db.Exec(ctx, `delete from resources where id = $1`, id)
	return err
}

// VMResources returns every VM resource of live deployments, for inventory sync.
func (s *Store) VMResources(ctx context.Context) ([]Resource, error) {
	rows, _ := s.Pool.Query(ctx, `select r.id, r.deployment_id, r.request_id, r.type, r.name, r.external_id, r.sync_state, r.properties
		from resources r join deployments d on d.id = r.deployment_id
		where r.type = 'vsphere.vm' and d.status <> 'deleted'`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Resource])
}

func (s *Store) UpdateResourceState(ctx context.Context, db DB, id, syncState string, props json.RawMessage) error {
	_, err := db.Exec(ctx, `update resources set sync_state = $2, properties = $3, updated_at = now() where id = $1`, id, syncState, props)
	return err
}

// RefreshDeploymentSyncState sets a deployment's sync state from its worst resource.
func (s *Store) RefreshDeploymentSyncState(ctx context.Context, db DB, id string) error {
	_, err := db.Exec(ctx, `update deployments d set sync_state = coalesce((
		select case when bool_or(sync_state = 'missing') then 'missing'
		            when bool_or(sync_state = 'drifted') then 'drifted' else 'in_sync' end
		from resources r where r.deployment_id = d.id), 'in_sync') where id = $1`, id)
	return err
}

// IPAddressesInUse lists addresses held by ipam.address resources in a subnet.
func (s *Store) IPAddressesInUse(ctx context.Context, subnet string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select properties->>'address' from resources
		where type = 'ipam.address' and properties->>'subnet' = $1`, subnet)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
