package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reads for the vRA importer (P2, ADR-0027).

// InventoryVM is a VM of the inventory by name, and whether an rvav
// deployment has it.
type InventoryVM struct {
	VCenter string `db:"vcenter"`
	Moref   string `db:"moref"`
	Name    string `db:"name"`
	Managed bool   `db:"managed"`
}

// InventoryVMsNamed lists the inventory's VMs of these names.
func (s *Store) InventoryVMsNamed(ctx context.Context, names []string) ([]InventoryVM, error) {
	rows, _ := s.Pool.Query(ctx, `select v.vcenter, v.moref, v.name,
		exists (select 1 from resources r join deployments d on d.id = r.deployment_id
		        where r.type = 'vsphere.vm' and d.status <> 'deleted'
		          and (r.external_id = v.instance_uuid or r.external_id = v.moref and v.instance_uuid = '')) as managed
		from inv_vms v where v.name = any($1) order by v.name, v.vcenter`, nonNil(names))
	return pgx.CollectRows(rows, pgx.RowToStructByName[InventoryVM])
}

// UsersByEmail are the users' IDs by lower-case email.
func (s *Store) UsersByEmail(ctx context.Context) (map[string]string, error) {
	rows, _ := s.Pool.Query(ctx, `select lower(email), id from users where email <> ''`)
	out := map[string]string{}
	var email, id string
	_, err := pgx.ForEachRow(rows, []any{&email, &id}, func() error { out[email] = id; return nil })
	return out, err
}

// ItemsBySlug are the catalog items' IDs by slug.
func (s *Store) ItemsBySlug(ctx context.Context) (map[string]string, error) {
	rows, _ := s.Pool.Query(ctx, `select slug, id from catalog_items`)
	out := map[string]string{}
	var slug, id string
	_, err := pgx.ForEachRow(rows, []any{&slug, &id}, func() error { out[slug] = id; return nil })
	return out, err
}

// VRASnapshot is an estate read from vRA.
type VRASnapshot struct {
	ID        string          `db:"id"`
	ReadAt    time.Time       `db:"read_at"`
	Source    string          `db:"source"`
	URL       string          `db:"url"`
	Estate    json.RawMessage `db:"estate"`
	Counts    json.RawMessage `db:"counts"`
	CreatedAt time.Time       `db:"created_at"`
	CreatedBy *string         `db:"created_by"`
	// CreatedByName: who read it.
	CreatedByName *string `db:"created_by_name"`
}

func (s *Store) SaveVRASnapshot(ctx context.Context, v VRASnapshot) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into vra_snapshots (id, read_at, source, url, estate, counts, created_by)
			values ($1, $2, $3, $4, $5, $6, $7)`, v.ID, v.ReadAt, v.Source, v.URL, v.Estate, v.Counts, v.CreatedBy); err != nil {
			return err
		}
		// Only the latest reads are kept: each holds a whole estate.
		_, err := tx.Exec(ctx, `delete from vra_snapshots where id in (select id from vra_snapshots
			order by created_at desc, id desc offset $1)`, keepSnapshots)
		return err
	})
}

// keepSnapshots is how many reads of vRA are kept.
const keepSnapshots = 5

// LatestVRASnapshot is the estate read last; ErrNotFound when none was.
func (s *Store) LatestVRASnapshot(ctx context.Context, withEstate bool) (VRASnapshot, error) {
	estate := `'null'::jsonb`
	if withEstate {
		estate = `v.estate`
	}
	rows, _ := s.Pool.Query(ctx, `select v.id, v.read_at, v.source, v.url, `+estate+` as estate, v.counts, v.created_at, v.created_by,
		u.name as created_by_name from vra_snapshots v left join users u on u.id = v.created_by order by v.created_at desc, v.id desc limit 1`)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[VRASnapshot])
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// VRAProjectMap is the vRA projects administrators mapped: vRA ID → rvav
// ID, "" for one kept in vRA.
func (s *Store) VRAProjectMap(ctx context.Context) (map[string]string, error) {
	rows, _ := s.Pool.Query(ctx, `select vra_project_id, coalesce(rvav_project_id, '') from vra_project_map`)
	out := map[string]string{}
	var k, v string
	_, err := pgx.ForEachRow(rows, []any{&k, &v}, func() error { out[k] = v; return nil })
	return out, err
}

// SetVRAProjectMap maps a vRA project to an rvav project, keeps it in vRA
// (stay), or clears its mapping (neither).
func (s *Store) SetVRAProjectMap(ctx context.Context, vraID, rvavID string, stay bool, by string) error {
	if rvavID == "" && !stay {
		_, err := s.Pool.Exec(ctx, `delete from vra_project_map where vra_project_id = $1`, vraID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `insert into vra_project_map (vra_project_id, rvav_project_id, updated_by) values ($1, nullif($2, ''), $3)
		on conflict (vra_project_id) do update set rvav_project_id = excluded.rvav_project_id, updated_at = now(), updated_by = excluded.updated_by`,
		vraID, rvavID, by)
	return err
}

// VRALink is what a vRA object became.
type VRALink struct {
	Kind       string    `db:"kind"`
	VRAID      string    `db:"vra_id"`
	RvavID     string    `db:"rvav_id"`
	VRAVersion string    `db:"vra_version"`
	SourceHash string    `db:"source_hash"`
	ImportedAt time.Time `db:"imported_at"`
}

// VRALinks lists the links of a kind.
func (s *Store) VRALinks(ctx context.Context, kind string) ([]VRALink, error) {
	rows, _ := s.Pool.Query(ctx, `select kind, vra_id, rvav_id, vra_version, source_hash, imported_at from vra_links
		where kind = $1 order by vra_id, imported_at`, kind)
	return pgx.CollectRows(rows, pgx.RowToStructByName[VRALink])
}

// SaveVRALink records what a vRA object became, or what it maps to now.
func (s *Store) SaveVRALink(ctx context.Context, l VRALink, by string) error {
	_, err := s.Pool.Exec(ctx, `insert into vra_links (kind, vra_id, rvav_id, vra_version, source_hash, imported_by)
		values ($1, $2, $3, $4, $5, $6) on conflict (kind, vra_id, rvav_id) do update set vra_version = excluded.vra_version,
		source_hash = excluded.source_hash, imported_at = now(), imported_by = excluded.imported_by`,
		l.Kind, l.VRAID, l.RvavID, l.VRAVersion, l.SourceHash, by)
	return err
}

// AdoptedVRADeployments are the rvav deployments, not deleted, each vRA
// deployment became.
func (s *Store) AdoptedVRADeployments(ctx context.Context) (map[string][]string, error) {
	rows, _ := s.Pool.Query(ctx, `select l.vra_id, l.rvav_id from vra_links l join deployments d on d.id = l.rvav_id
		where l.kind = 'deployment' and d.status <> 'deleted' order by l.vra_id, l.rvav_id`)
	out := map[string][]string{}
	var k, v string
	_, err := pgx.ForEachRow(rows, []any{&k, &v}, func() error { out[k] = append(out[k], v); return nil })
	return out, err
}

// AddEntitlements offers an item to more projects, leaving those it has.
func (s *Store) AddEntitlements(ctx context.Context, itemID string, projectIDs []string) error {
	_, err := s.Pool.Exec(ctx, `insert into catalog_entitlements (item_id, project_id) select $1, unnest($2::text[])
		on conflict do nothing`, itemID, projectIDs)
	return err
}

// ItemImportState is what the import needs of a catalog item: whether it
// exists, whether it has a draft, who it is shared with.
type ItemImportState struct {
	ID          string   `db:"id"`
	Slug        string   `db:"slug"`
	HasDraft    bool     `db:"has_draft"`
	Published   bool     `db:"published"`
	AllProjects bool     `db:"all_projects"`
	Projects    []string `db:"projects"`
}

// ItemImportStates are the catalog items' states by ID.
func (s *Store) ItemImportStates(ctx context.Context) (map[string]ItemImportState, error) {
	rows, _ := s.Pool.Query(ctx, `select i.id, i.slug,
		exists (select 1 from catalog_item_versions v where v.item_id = i.id and v.state = 'draft') as has_draft,
		i.published_version_id is not null as published,
		i.all_projects,
		coalesce((select array_agg(e.project_id order by e.project_id) from catalog_entitlements e where e.item_id = i.id), '{}') as projects
		from catalog_items i`)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[ItemImportState])
	out := map[string]ItemImportState{}
	for _, it := range list {
		out[it.ID] = it
	}
	return out, err
}

// VRAMove is a vRA project an administrator marked moved to rvav.
type VRAMove struct {
	VRAProjectID string    `db:"vra_project_id"`
	MovedAt      time.Time `db:"moved_at"`
	MovedBy      *string   `db:"moved_by"`
	Note         string    `db:"note"`
}

// VRAMoves are the moved projects by vRA project ID; MovedBy is the name.
func (s *Store) VRAMoves(ctx context.Context) (map[string]VRAMove, error) {
	rows, _ := s.Pool.Query(ctx, `select c.vra_project_id, c.moved_at, u.name as moved_by, c.note
		from vra_cutover c left join users u on u.id = c.moved_by`)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[VRAMove])
	out := map[string]VRAMove{}
	for _, m := range list {
		out[m.VRAProjectID] = m
	}
	return out, err
}

// SetVRAMoved marks a vRA project moved, or not.
func (s *Store) SetVRAMoved(ctx context.Context, vraID string, moved bool, note, by string) error {
	if !moved {
		_, err := s.Pool.Exec(ctx, `delete from vra_cutover where vra_project_id = $1`, vraID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `insert into vra_cutover (vra_project_id, moved_by, note) values ($1, $2, $3)
		on conflict (vra_project_id) do update set note = excluded.note`, vraID, by, note)
	return err
}

// TryLock takes a session advisory lock, if it is free: it holds one
// connection until unlock.
func (s *Store) TryLock(ctx context.Context, key int64) (unlock func(), ok bool, err error) {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := conn.QueryRow(ctx, `select pg_try_advisory_lock($1)`, key).Scan(&ok); err != nil || !ok {
		conn.Release()
		return nil, false, err
	}
	return func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `select pg_advisory_unlock($1)`, key)
		conn.Release()
	}, true, nil
}

// SaveSegmentSubnet maps an NSX segment rvav made to its subnet (ADR-0028):
// the EfficientIP sync leaves the mapping alone.
func (s *Store) SaveSegmentSubnet(ctx context.Context, n NetworkSubnet) error {
	_, err := s.Pool.Exec(ctx, `insert into network_subnets (network, subnet, gateway, dns_zone, dns_servers, efficientip_subnet_id,
		subnet_name, source, synced_at) values ($1, $2, $3, $4, $5, $6, $7, 'nsx', now())
		on conflict (network) do update set subnet = excluded.subnet, gateway = excluded.gateway, dns_zone = excluded.dns_zone,
		dns_servers = excluded.dns_servers, efficientip_subnet_id = excluded.efficientip_subnet_id, subnet_name = excluded.subnet_name,
		source = 'nsx', synced_at = now()`,
		n.Network, n.Subnet, n.Gateway, n.DNSZone, nonNil(n.DNSServers), n.EfficientIPSubnetID, n.SubnetName)
	return err
}

// DeleteSegmentSubnet removes a segment's mapping, once the segment is gone.
func (s *Store) DeleteSegmentSubnet(ctx context.Context, network string) error {
	_, err := s.Pool.Exec(ctx, `delete from network_subnets where network = $1 and source = 'nsx'`, network)
	return err
}
