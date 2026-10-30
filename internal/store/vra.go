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
	_, err := s.Pool.Exec(ctx, `insert into vra_snapshots (id, read_at, source, url, estate, counts, created_by)
		values ($1, $2, $3, $4, $5, $6, $7)`, v.ID, v.ReadAt, v.Source, v.URL, v.Estate, v.Counts, v.CreatedBy)
	return err
}

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

// VRAProjectMap is the vRA projects administrators mapped: vRA ID → rvav ID.
func (s *Store) VRAProjectMap(ctx context.Context) (map[string]string, error) {
	rows, _ := s.Pool.Query(ctx, `select vra_project_id, rvav_project_id from vra_project_map`)
	out := map[string]string{}
	var k, v string
	_, err := pgx.ForEachRow(rows, []any{&k, &v}, func() error { out[k] = v; return nil })
	return out, err
}

// SetVRAProjectMap maps a vRA project, or clears its mapping (rvavID "").
func (s *Store) SetVRAProjectMap(ctx context.Context, vraID, rvavID, by string) error {
	if rvavID == "" {
		_, err := s.Pool.Exec(ctx, `delete from vra_project_map where vra_project_id = $1`, vraID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `insert into vra_project_map (vra_project_id, rvav_project_id, updated_by) values ($1, $2, $3)
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

// DeleteVRALink forgets a link (its rvav object is gone).
func (s *Store) DeleteVRALink(ctx context.Context, kind, vraID, rvavID string) error {
	_, err := s.Pool.Exec(ctx, `delete from vra_links where kind = $1 and vra_id = $2 and rvav_id = $3`, kind, vraID, rvavID)
	return err
}

// ItemImportState is what the import needs of a catalog item: whether it
// exists, whether it has a draft, who it is shared with.
type ItemImportState struct {
	ID          string   `db:"id"`
	Slug        string   `db:"slug"`
	HasDraft    bool     `db:"has_draft"`
	AllProjects bool     `db:"all_projects"`
	Projects    []string `db:"projects"`
}

// ItemImportStates are the catalog items' states by ID.
func (s *Store) ItemImportStates(ctx context.Context) (map[string]ItemImportState, error) {
	rows, _ := s.Pool.Query(ctx, `select i.id, i.slug,
		exists (select 1 from catalog_item_versions v where v.item_id = i.id and v.state = 'draft') as has_draft,
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
