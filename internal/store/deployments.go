package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const deploymentRowSelect = `
select d.id, d.name, d.status, d.project_id, d.environment, d.item_id, d.version_id, d.owner_id,
       d.created_by_request_id, d.created_at, d.lease_expires_at, d.lease_extensions_used,
       d.lease_extensions_max, d.expiry_action, d.sync_state, d.inputs, d.lease_reminded, d.lease_expired_at,
       p.code as project_code, p.name as project_name, i.slug as item_slug, i.name as item_name,
       v.number as version_number, u.name as owner_name, u.email as owner_email,
       (select count(*) from resources r where r.deployment_id = d.id and r.type = 'vsphere.vm')::int as vm_count,
       (select count(*) from resources r where r.deployment_id = d.id and r.type = 'vsphere.vm'
          and r.properties->'vm'->>'power_state' = 'on')::int as vms_powered_on,
       pa.ip as primary_ip, pa.fqdn as primary_fqdn,
       lp.max_days as lease_max_days, lp.max_extensions as lease_max_extensions, lp.grace_days as lease_grace_days
from deployments d
join projects p on p.id = d.project_id
join catalog_items i on i.id = d.item_id
join catalog_item_versions v on v.id = d.version_id
join users u on u.id = d.owner_id
left join lateral (select r.properties->>'address' as ip, r.properties->>'fqdn' as fqdn
                   from resources r where r.deployment_id = d.id and r.type = 'ipam.address'
                   order by r.created_at limit 1) pa on true
left join lateral (select max_days, max_extensions, grace_days from lease_policies lp
                   where lp.environment = d.environment and (lp.project_id = d.project_id or lp.project_id is null)
                   order by lp.project_id nulls last limit 1) lp on true`

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
// UpsertResource records an external object, or updates it when the
// deployment already has it. An object another deployment has is ErrTaken:
// it is never moved.
func (s *Store) UpsertResource(ctx context.Context, db DB, r Resource) (Resource, error) {
	rows, _ := db.Query(ctx, `insert into resources (id, deployment_id, request_id, type, name, external_id, sync_state, properties)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		on conflict (type, external_id) do update set name = excluded.name, properties = excluded.properties,
		       sync_state = excluded.sync_state, updated_at = now()
		where resources.deployment_id = excluded.deployment_id
		returning `+resourceCols,
		r.ID, r.DeploymentID, r.RequestID, r.Type, r.Name, r.ExternalID, orDefault(r.SyncState, "in_sync"), jsonOr(r.Properties, "{}"))
	res, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Resource])
	if errors.Is(err, pgx.ErrNoRows) {
		return res, fmt.Errorf("%s %s: %w", r.Type, r.Name, ErrTaken)
	}
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

// VMResourcesOf are the VM resources of a VM, by its instance UUID, in
// deployments not deleted.
func (s *Store) VMResourcesOf(ctx context.Context, instanceUUID string) ([]Resource, error) {
	rows, _ := s.Pool.Query(ctx, `select r.id, r.deployment_id, r.request_id, r.type, r.name, r.external_id, r.sync_state, r.properties
		from resources r join deployments d on d.id = r.deployment_id
		where r.type = 'vsphere.vm' and d.status <> 'deleted' and r.external_id = $1`, instanceUUID)
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

// Day-2 changes to a deployment and its resources.

func (s *Store) RenameDeployment(ctx context.Context, db DB, id, name string) error {
	_, err := db.Exec(ctx, `update deployments set name = $2 where id = $1`, id, name)
	return err
}

// RenameResource sets a resource's name and properties; the external ID is
// kept unless ext is not nil.
func (s *Store) RenameResource(ctx context.Context, db DB, id, name string, ext *string, props json.RawMessage) error {
	_, err := db.Exec(ctx, `update resources set name = $2, external_id = coalesce($3, external_id), properties = $4,
		updated_at = now() where id = $1`, id, name, ext, props)
	return err
}

func (s *Store) SetDeploymentOwner(ctx context.Context, db DB, id, ownerID string) error {
	_, err := db.Exec(ctx, `update deployments set owner_id = $2 where id = $1`, id, ownerID)
	return err
}

func (s *Store) SetDeploymentProject(ctx context.Context, db DB, id, projectID string) error {
	_, err := db.Exec(ctx, `update deployments set project_id = $2 where id = $1`, id, projectID)
	return err
}

// ReviveDeploymentsForRequest puts the deployments a request created back
// to provisioning, rolled back ones included, for a retry.
func (s *Store) ReviveDeploymentsForRequest(ctx context.Context, db DB, requestID string) error {
	_, err := db.Exec(ctx, `update deployments set status = 'provisioning', deleted_at = null
		where created_by_request_id = $1`, requestID)
	return err
}

// ExtendLease sets a new end to the deployment's lease and counts the
// extension; reminders start again and a pending deletion is called off.
func (s *Store) ExtendLease(ctx context.Context, db DB, id string, until time.Time) error {
	_, err := db.Exec(ctx, `update deployments set lease_expires_at = $2, lease_extensions_used = lease_extensions_used + 1,
		lease_reminded = '{}', lease_expired_at = null where id = $1`, id, until)
	return err
}

// SetLeaseExpired records when the lease's expiry powered the VMs off.
func (s *Store) SetLeaseExpired(ctx context.Context, db DB, id string, at time.Time) error {
	_, err := db.Exec(ctx, `update deployments set lease_expired_at = $2 where id = $1`, id, at)
	return err
}

// SetLeaseReminded records the reminders sent for the lease ending at
// expires; an extension since then (another end) is left alone.
func (s *Store) SetLeaseReminded(ctx context.Context, db DB, id string, expires time.Time, reminded []string) error {
	_, err := db.Exec(ctx, `update deployments set lease_reminded = $2 where id = $1 and lease_expires_at = $3`, id, reminded, expires)
	return err
}

// PostponeLease moves the end of a lease that ended unannounced, so its
// owner gets the 1-day reminder first.
func (s *Store) PostponeLease(ctx context.Context, db DB, id string, expires, until time.Time) error {
	_, err := db.Exec(ctx, `update deployments set lease_expires_at = $3 where id = $1 and lease_expires_at = $2`, id, expires, until)
	return err
}

// LeasedDeployments lists the deployments with a lease that are not being
// created or deleted.
func (s *Store) LeasedDeployments(ctx context.Context) ([]DeploymentRow, error) {
	rows, _ := s.Pool.Query(ctx, deploymentRowSelect+` where d.lease_expires_at is not null
		and d.status in ('ready', 'updating', 'failed') order by d.lease_expires_at, d.id`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[DeploymentRow])
}

// IPAMResource is an address or DNS record a deployment owns.
type IPAMResource struct {
	Resource
	DeploymentName string `db:"deployment_name"`
}

// IPAMResources lists the addresses and DNS records deployments own.
func (s *Store) IPAMResources(ctx context.Context) ([]IPAMResource, error) {
	rows, _ := s.Pool.Query(ctx, `select r.id, r.deployment_id, r.request_id, r.type, r.name, r.external_id, r.sync_state,
		r.properties, d.name as deployment_name
		from resources r join deployments d on d.id = r.deployment_id
		where r.type in ('ipam.address', 'dns.record') order by d.name, r.type, r.name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[IPAMResource])
}

// InventoryHost is a VM for the Ansible inventory, with its deployment's
// context.
type InventoryHost struct {
	Name           string          `db:"name"`
	VM             json.RawMessage `db:"vm"`
	Address        *string         `db:"address"`
	DeploymentID   string          `db:"deployment_id"`
	DeploymentName string          `db:"deployment_name"`
	Status         string          `db:"status"`
	Environment    string          `db:"environment"`
	Project        string          `db:"project"`
	Item           string          `db:"item"`
	Tags           []string        `db:"tags"`
	Owner          string          `db:"owner"`
	OSFamily       string          `db:"os_family"`
	Inputs         json.RawMessage `db:"inputs"`
	UISchema       json.RawMessage `db:"ui_schema"`
}

// InventoryHosts lists the VMs of deployments that are not being deleted,
// each with the address rvav reserved for it.
func (s *Store) InventoryHosts(ctx context.Context) ([]InventoryHost, error) {
	rows, _ := s.Pool.Query(ctx, `select r.name, r.properties->'vm' as vm,
		(select a.properties->>'address' from resources a where a.deployment_id = d.id and a.type = 'ipam.address'
			and (a.properties->>'hostname' = r.name or a.properties->>'fqdn' = r.properties->'vm'->>'fqdn')
			and coalesce(a.properties->>'nic', '') = ''
			order by a.name limit 1) as address,
		d.id as deployment_id, d.name as deployment_name, d.status, d.environment, p.code as project, i.slug as item, i.tags,
		coalesce(u.email, '') as owner,
		coalesce((select c->'guest'->>'os_family' from jsonb_array_elements(case when jsonb_typeof(v.blueprint->'components') = 'array'
			then v.blueprint->'components' else '[]' end) c where c->>'key' = r.properties->'vm'->>'component'),
			v.blueprint->'guest'->>'os_family', '') as os_family,
		d.inputs, coalesce(v.ui_schema, '{}') as ui_schema
		from resources r join deployments d on d.id = r.deployment_id join projects p on p.id = d.project_id
		join catalog_items i on i.id = d.item_id left join catalog_item_versions v on v.id = d.version_id
		left join users u on u.id = d.owner_id
		where r.type = 'vsphere.vm' and d.status not in ('deleting', 'deleted') order by r.name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InventoryHost])
}
