package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const deploymentRowSelect = `
select d.id, d.name, d.description, d.status, d.project_id, d.environment, d.item_id, d.version_id, d.owner_id,
       d.created_by_request_id, d.created_at, d.lease_expires_at, d.lease_extensions_used,
       d.lease_extensions_max, d.expiry_action, d.sync_state, d.inputs, d.lease_reminded, d.lease_expired_at,
       p.code as project_code, p.name as project_name, p.deployment_sharing as project_sharing, i.slug as item_slug, i.name as item_name, i.type as item_type,
       v.number as version_number, u.name as owner_name, u.email as owner_email,
       (select count(*) from resources r where r.deployment_id = d.id and r.type = 'vsphere.vm')::int as vm_count,
       (select count(*) from resources r where r.deployment_id = d.id and r.type = 'vsphere.vm'
          and r.properties->'vm'->>'power_state' = 'on')::int as vms_powered_on,
       pa.ip as primary_ip, pa.fqdn as primary_fqdn,
       lp.max_days as lease_max_days, lp.max_extensions as lease_max_extensions, lp.grace_days as lease_grace_days,
       lp.enforcement as lease_enforcement
from deployments d
join projects p on p.id = d.project_id
join catalog_items i on i.id = d.item_id
join catalog_item_versions v on v.id = d.version_id
join users u on u.id = d.owner_id
left join lateral (select r.properties->>'address' as ip, r.properties->>'fqdn' as fqdn
                   from resources r where r.deployment_id = d.id and r.type = 'ipam.address'
                   and strpos(r.name, ':') = 0
                   order by r.created_at limit 1) pa on true
left join lateral (select max_days, max_extensions, grace_days, enforcement from lease_policies lp
                   where lp.environment = d.environment and (lp.project_id = d.project_id or lp.project_id is null)
                   order by lp.project_id nulls last limit 1) lp on true`

func (s *Store) InsertDeployment(ctx context.Context, db DB, d Deployment) error {
	_, err := db.Exec(ctx, `insert into deployments (id, name, status, project_id, environment, item_id, version_id,
		owner_id, created_by_request_id, lease_expires_at, lease_extensions_max, expiry_action, inputs, description)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		d.ID, d.Name, d.Status, d.ProjectID, d.Environment, d.ItemID, d.VersionID, d.OwnerID,
		d.CreatedByRequestID, d.LeaseExpiresAt, d.LeaseExtensionsMax, d.ExpiryAction, jsonOr(d.Inputs, "{}"), d.Description)
	return err
}

// LockDeploymentNames holds these names in a project until the
// transaction ends, so two requests can't both take one. They are held
// whatever their case, in one order, so two holders never deadlock.
func (s *Store) LockDeploymentNames(ctx context.Context, tx pgx.Tx, projectID string, names []string) error {
	keys := lowered(names)
	slices.Sort(keys)
	for _, k := range slices.Compact(keys) {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('rvav.deployment:' || $1 || ':' || $2))`, projectID, k); err != nil {
			return err
		}
	}
	return nil
}

// TakenDeploymentNames are those of the names that a deployment of the
// project other than except, not deleted, holds whatever the case.
func (s *Store) TakenDeploymentNames(ctx context.Context, db DB, projectID, except string, names []string) ([]string, error) {
	rows, _ := db.Query(ctx, `select name from deployments where project_id = $1 and status <> 'deleted'
		and lower(name) = any($2) and id <> $3 order by name`, projectID, lowered(names), except)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func lowered(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.ToLower(n)
	}
	return out
}

func (s *Store) GetDeployment(ctx context.Context, id string) (DeploymentRow, error) {
	return s.GetDeploymentIn(ctx, s.Pool, id)
}

// GetDeploymentIn reads a deployment in a transaction (one that holds a
// lock must not wait for another connection, S14.7).
func (s *Store) GetDeploymentIn(ctx context.Context, db DB, id string) (DeploymentRow, error) {
	rows, _ := db.Query(ctx, deploymentRowSelect+` where d.id = $1`, id)
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
	Projects []string // nil means all projects
	// OwnOnly and Viewer narrow Projects to what a person sees (Scope).
	OwnOnly      []string
	Viewer       string
	ProjectID    string
	Statuses     []string
	Expired      bool // or its lease has ended, whatever its status
	Environments []string
	// Items are catalog items by ID or slug; Templates VM templates by name.
	Items              []string
	Templates          []string
	OwnerID            string
	LeaseExpiresBefore *time.Time
	Search             string
	Sort               string // a key of deploymentSorts
	IncludeDeleted     bool
	Limit              int
	After              *Cursor
}

const isoTime = `'YYYY-MM-DD"T"HH24:MI:SS.US'`

// deploymentSorts are each sort's key over the listed row d0; all but the
// name sort break ties by name. A descending sort reverses both.
var deploymentSorts = map[string]string{
	"name":             "d0.name",
	"created_at":       "to_char(d0.created_at at time zone 'UTC', " + isoTime + ")",
	"lease_expires_at": "coalesce(to_char(d0.lease_expires_at at time zone 'UTC', " + isoTime + "), '9999')",
	"status":           "d0.status",
	"environment":      "d0.environment",
	"owner":            "d0.owner_name",
	"item":             "d0.item_name",
	"project":          "d0.project_name",
	"vm_count":         "lpad(d0.vm_count::text, 6, '0')",
}

// sortOf is a sort's key expression and direction; an unknown sort is
// the default's.
func sortOf(sorts map[string]string, sort, fallback string) (key string, then bool, desc bool) {
	desc = strings.HasPrefix(sort, "-")
	field := strings.TrimPrefix(sort, "-")
	key, ok := sorts[field]
	if !ok {
		field, desc, key = fallback, false, sorts[fallback]
	}
	return key, field != fallback, desc
}

// keyset is the ordering and the "after the cursor" condition of a list
// sorted by sort_key, then then_key, then id, all in one direction.
func keyset(desc bool, from int) (where, order string) {
	cmp, dir := ">", "asc"
	if desc {
		cmp, dir = "<", "desc"
	}
	return fmt.Sprintf(`($%d = '' or (sort_key, then_key, id) %s ($%d, $%d, $%d))`, from, cmp, from, from+1, from+2),
		fmt.Sprintf(`order by sort_key %s, then_key %s, id %s`, dir, dir, dir)
}

// ListDeployments filters and sorts deployments with keyset pagination. The
// sort keys are compared as text so one cursor format covers every sort.
func (s *Store) ListDeployments(ctx context.Context, f DeploymentFilter) (Page[DeploymentRow], error) {
	key, byName, desc := sortOf(deploymentSorts, f.Sort, "name")
	then := "''"
	if byName {
		then = "d0.name"
	}
	after := Cursor{}
	if f.After != nil {
		after = *f.After
	}
	where, order := keyset(desc, 13)
	q := fmt.Sprintf(`select * from (select d0.*, %s as sort_key, %s as then_key from (%s
		where ((($1 or d.project_id = any($2)) and d.project_id <> all($17::text[])) or d.owner_id = $18)
		  and ($3 = '' or d.project_id = $3)
		  and ((cardinality($4::text[]) = 0 and not $5) or d.status = any($4) or ($5 and d.lease_expires_at <= now()))
		  and (cardinality($6::text[]) = 0 or d.environment = any($6))
		  and ($7 = '' or d.owner_id = $7)
		  and ($8::timestamptz is null or d.lease_expires_at < $8)
		  and ($9 = '' or d.name ilike $9 or u.name ilike $9 or exists (
		        select 1 from resources r where r.deployment_id = d.id
		        and (r.name ilike $9 or r.properties->>'address' ilike $9 or r.properties->'vm'->>'fqdn' ilike $9)))
		  and ($10 or d.status <> 'deleted')
		  and (cardinality($11::text[]) = 0 or d.item_id = any($11) or i.slug = any($11))
		  and (cardinality($12::text[]) = 0 or exists (select 1 from resources r where r.deployment_id = d.id
		        and r.type = 'vsphere.vm' and r.properties->'vm'->>'template' = any($12)))
		) d0) d1
		where %s %s limit $16`,
		key, then, deploymentRowSelect, where, order)
	rows, err := s.Pool.Query(ctx, q, f.Projects == nil, f.Projects, f.ProjectID, nonNil(f.Statuses), f.Expired,
		nonNil(f.Environments), f.OwnerID, f.LeaseExpiresBefore, SearchText(f.Search), f.IncludeDeleted,
		nonNil(f.Items), nonNil(f.Templates), after.Value, after.Then, after.ID, f.Limit+1, nonNil(f.OwnOnly), f.Viewer)
	if err != nil {
		return Page[DeploymentRow]{}, err
	}
	type withKey struct {
		DeploymentRow
		SortKey string `db:"sort_key"`
		ThenKey string `db:"then_key"`
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[withKey])
	if err != nil {
		return Page[DeploymentRow]{}, err
	}
	page := paginate(list, f.Limit, func(w withKey) Cursor { return Cursor{Value: w.SortKey, Then: w.ThenKey, ID: w.ID} })
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

// SetDeploymentInputs records the deployment's inputs, its count changed
// by VMs that joined or left it.
func (s *Store) SetDeploymentInputs(ctx context.Context, db DB, id string, inputs json.RawMessage) error {
	_, err := db.Exec(ctx, `update deployments set inputs = $2 where id = $1`, id, inputs)
	return err
}

// SetDeploymentVersion records the version and inputs an update left the
// deployment with (ADR-0042).
func (s *Store) SetDeploymentVersion(ctx context.Context, db DB, id, versionID string, inputs json.RawMessage) error {
	_, err := db.Exec(ctx, `update deployments set version_id = $2, inputs = $3 where id = $1`, id, versionID, inputs)
	return err
}

func (s *Store) SetDeploymentsStatusForRequest(ctx context.Context, db DB, requestID, status string) error {
	_, err := db.Exec(ctx, `update deployments set status = $2,
		deleted_at = case when $2 = 'deleted' then now() else deleted_at end
		where created_by_request_id = $1 and status <> 'deleted'`, requestID, status)
	return err
}

const resourceCols = `id, deployment_id, request_id, type, name, external_id, sync_state, properties, prevent_delete`

// UpsertResource creates a resource or replaces its properties, keyed by type
// and external ID.
// UpsertResource records an external object, or updates it when the
// deployment already has it. An object another deployment has is ErrTaken:
// it is never moved.
func (s *Store) UpsertResource(ctx context.Context, db DB, r Resource) (Resource, error) {
	rows, _ := db.Query(ctx, `insert into resources (id, deployment_id, request_id, type, name, external_id, sync_state, properties,
			prevent_delete)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		on conflict (type, external_id) do update set name = excluded.name, properties = excluded.properties,
		       sync_state = excluded.sync_state, updated_at = now()
		where resources.deployment_id = excluded.deployment_id
		returning `+resourceCols,
		r.ID, r.DeploymentID, r.RequestID, r.Type, r.Name, r.ExternalID, orDefault(r.SyncState, "in_sync"), jsonOr(r.Properties, "{}"),
		r.PreventDelete)
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

// ResourcesOfType lists every deployment's resources of a type.
func (s *Store) ResourcesOfType(ctx context.Context, typ string) ([]Resource, error) {
	rows, _ := s.Pool.Query(ctx, `select `+resourceCols+` from resources where type = $1 order by id`, typ)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Resource])
}

// ProjectVMs lists the VMs of a project's deployments that aren't deleted,
// by name; of one deployment when deploymentID isn't "".
func (s *Store) ProjectVMs(ctx context.Context, projectID, deploymentID string) ([]ProjectVM, error) {
	rows, _ := s.Pool.Query(ctx, `select r.name, d.id as deployment_id, d.name as deployment_name, d.owner_id
		from resources r join deployments d on d.id = r.deployment_id
		where d.project_id = $1 and d.status <> 'deleted' and ($2 = '' or d.id = $2) and r.type = 'vsphere.vm'
		order by r.name, d.name, d.id`, projectID, deploymentID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ProjectVM])
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

// AllowDelete lets a deployment's VMs be deleted, and names those that
// weren't (ADR-0038).
func (s *Store) AllowDelete(ctx context.Context, db DB, deploymentID string) ([]string, error) {
	rows, _ := db.Query(ctx, `update resources set prevent_delete = false, updated_at = now()
		where deployment_id = $1 and prevent_delete returning name`, deploymentID)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	slices.Sort(names)
	return names, err
}

func (s *Store) DeleteResource(ctx context.Context, db DB, id string) error {
	_, err := db.Exec(ctx, `delete from resources where id = $1`, id)
	return err
}

// VMResources returns every VM resource of live deployments, for inventory sync.
func (s *Store) VMResources(ctx context.Context) ([]Resource, error) {
	rows, _ := s.Pool.Query(ctx, `select r.id, r.deployment_id, r.request_id, r.type, r.name, r.external_id, r.sync_state, r.properties,
			r.prevent_delete
		from resources r join deployments d on d.id = r.deployment_id
		where r.type = 'vsphere.vm' and d.status <> 'deleted'`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Resource])
}

// VMResourcesOf are the VM resources of a VM, by its instance UUID, in
// deployments not deleted.
func (s *Store) VMResourcesOf(ctx context.Context, instanceUUID string) ([]Resource, error) {
	rows, _ := s.Pool.Query(ctx, `select r.id, r.deployment_id, r.request_id, r.type, r.name, r.external_id, r.sync_state, r.properties,
			r.prevent_delete
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

// SetDeploymentDetails gives the deployment a name and, unless nil, a
// description, unless another live deployment of its project holds the
// name: then it changes nothing and returns that one's name. The name is
// held as a request's are (ADR-0046).
func (s *Store) SetDeploymentDetails(ctx context.Context, projectID, id, name string, description *string) (string, error) {
	var held string
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		if held, err = s.heldName(ctx, tx, projectID, id, name); err != nil || held != "" {
			return err
		}
		_, err = tx.Exec(ctx, `update deployments set name = $2, description = coalesce($3, description) where id = $1`,
			id, name, description)
		return err
	})
	return held, err
}

// MoveDeploymentNamed moves the deployment to the project unless a live
// deployment there holds its name: then it returns that one's name.
func (s *Store) MoveDeploymentNamed(ctx context.Context, id, name, projectID string) (string, error) {
	var held string
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		if held, err = s.heldName(ctx, tx, projectID, id, name); err != nil || held != "" {
			return err
		}
		return s.SetDeploymentProject(ctx, tx, id, projectID)
	})
	return held, err
}

// heldName is the name of the project's live deployment but id that holds
// name, whatever the case, the name locked until tx ends; "" when none does.
func (s *Store) heldName(ctx context.Context, tx pgx.Tx, projectID, id, name string) (string, error) {
	if err := s.LockDeploymentNames(ctx, tx, projectID, []string{name}); err != nil {
		return "", err
	}
	taken, err := s.TakenDeploymentNames(ctx, tx, projectID, id, []string{name})
	if err != nil || len(taken) == 0 {
		return "", err
	}
	return taken[0], nil
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
		r.properties, r.prevent_delete, d.name as deployment_name
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
	// UISchema is the item version's, with its property groups' fields.
	UISchema  json.RawMessage `db:"ui_schema"`
	VersionID string          `db:"version_id"`
	Groups    json.RawMessage `db:"groups"`
}

// InventoryHosts lists the VMs of deployments that are not being deleted,
// each with the address rvav reserved for it.
func (s *Store) InventoryHosts(ctx context.Context) ([]InventoryHost, error) {
	rows, _ := s.Pool.Query(ctx, `select r.name, r.properties->'vm' as vm,
		coalesce((select a.properties->>'address' from resources a where a.deployment_id = d.id and a.type = 'ipam.address'
			and (a.properties->>'hostname' = r.name or a.properties->>'fqdn' = r.properties->'vm'->>'fqdn')
			and coalesce(a.properties->>'nic', '') = '' and strpos(a.name, ':') = 0
			order by a.name limit 1),
			case when (r.properties->'vm'->>'dhcp')::boolean then r.properties->'vm'->'nics'->0->'ip_addresses'->>0 end) as address,
		d.id as deployment_id, d.name as deployment_name, d.status, d.environment, p.code as project, i.slug as item, i.tags,
		coalesce(u.email, '') as owner,
		coalesce((select c->'guest'->>'os_family' from jsonb_array_elements(case when jsonb_typeof(v.blueprint->'components') = 'array'
			then v.blueprint->'components' else '[]' end) c where c->>'key' = r.properties->'vm'->>'component'),
			v.blueprint->'guest'->>'os_family', '') as os_family,
		d.inputs, coalesce(v.ui_schema, '{}') as ui_schema, coalesce(v.id, '') as version_id,
		jsonb_build_object('property_groups', v.blueprint->'property_groups') as groups
		from resources r join deployments d on d.id = r.deployment_id join projects p on p.id = d.project_id
		join catalog_items i on i.id = d.item_id left join catalog_item_versions v on v.id = d.version_id
		left join users u on u.id = d.owner_id
		where r.type = 'vsphere.vm' and d.status not in ('deleting', 'deleted') order by r.name`)
	hosts, err := pgx.CollectRows(rows, pgx.RowToStructByName[InventoryHost])
	if err != nil {
		return nil, err
	}
	merged := map[string]json.RawMessage{}
	for i, h := range hosts {
		ui, ok := merged[h.VersionID]
		if !ok {
			if ui, err = UISchemaWithGroups(ctx, s.Pool, h.UISchema, h.Groups); err != nil {
				return nil, err
			}
			merged[h.VersionID] = ui
		}
		hosts[i].UISchema = ui
	}
	return hosts, nil
}

// UpdateResourceStateIf stores a resource's new state and properties only
// if its properties are still prev, and says whether it did: a writer that
// computed them from what it read then doesn't overwrite a newer write
// (the sync and the worker both write a VM's).
func (s *Store) UpdateResourceStateIf(ctx context.Context, id string, prev json.RawMessage, syncState string, props json.RawMessage) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update resources set sync_state = $3, properties = $4, updated_at = now()
		where id = $1 and properties = $2::jsonb`, id, prev, syncState, props)
	return tag.RowsAffected() == 1, err
}

// PutConsoleToken keeps a console token's ticket until it expires, and
// forgets the expired ones.
func (s *Store) PutConsoleToken(ctx context.Context, hash []byte, ticket json.RawMessage, expires time.Time) error {
	if _, err := s.Pool.Exec(ctx, `delete from console_tokens where expires_at < now()`); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `insert into console_tokens (token_hash, ticket, expires_at) values ($1, $2, $3)`, hash, ticket, expires)
	return err
}

// TakeConsoleToken returns a console token's ticket once (ErrNotFound when
// it was never given or is already used), with when it expires.
func (s *Store) TakeConsoleToken(ctx context.Context, hash []byte) (json.RawMessage, time.Time, error) {
	var ticket json.RawMessage
	var expires time.Time
	err := s.Pool.QueryRow(ctx, `delete from console_tokens where token_hash = $1 returning ticket, expires_at`, hash).
		Scan(&ticket, &expires)
	return ticket, expires, notFound(err)
}
