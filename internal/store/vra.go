package store

import (
	"context"

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
