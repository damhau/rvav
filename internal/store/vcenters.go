package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// VCenter is a vCenter added besides the vCenter integration's (ADR-0024);
// its password is sealed like a secret's.
type VCenter struct {
	Name       string    `db:"name"`
	URL        string    `db:"url"`
	Username   string    `db:"username"`
	Insecure   bool      `db:"insecure"`
	KeyID      string    `db:"key_id"`
	Nonce      []byte    `db:"nonce"`
	Ciphertext []byte    `db:"ciphertext"`
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
}

const vcenterCols = `name, url, username, insecure, key_id, nonce, ciphertext, created_at, updated_at`

func (s *Store) VCenters(ctx context.Context) ([]VCenter, error) {
	rows, _ := s.Pool.Query(ctx, `select `+vcenterCols+` from vcenters order by name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[VCenter])
}

func (s *Store) GetVCenter(ctx context.Context, name string) (VCenter, error) {
	rows, _ := s.Pool.Query(ctx, `select `+vcenterCols+` from vcenters where name = $1`, name)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[VCenter])
	return v, notFound(err)
}

// SaveVCenter adds the vCenter (create: ErrInUse when the name is taken)
// or changes it; a nil ciphertext keeps the password.
func (s *Store) SaveVCenter(ctx context.Context, v VCenter, create bool) error {
	if create {
		_, err := s.Pool.Exec(ctx, `insert into vcenters (name, url, username, insecure, key_id, nonce, ciphertext)
			values ($1,$2,$3,$4,$5,$6,$7)`, v.Name, v.URL, v.Username, v.Insecure, v.KeyID, v.Nonce, v.Ciphertext)
		if IsUniqueViolation(err) {
			return ErrInUse
		}
		return err
	}
	tag, err := s.Pool.Exec(ctx, `update vcenters set url = $2, username = $3, insecure = $4,
		key_id = case when $6::bytea is null then key_id else $5 end,
		nonce = coalesce($6, nonce), ciphertext = coalesce($7, ciphertext), updated_at = now() where name = $1`,
		v.Name, v.URL, v.Username, v.Insecure, v.KeyID, v.Nonce, v.Ciphertext)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteVCenter removes the vCenter and its inventory; ErrInUse while a
// deployment not deleted has a VM there.
func (s *Store) DeleteVCenter(ctx context.Context, name string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var used bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from resources r join deployments d on d.id = r.deployment_id
			where r.type = 'vsphere.vm' and r.properties->'vm'->>'vcenter' = $1 and d.status <> 'deleted')`, name).Scan(&used); err != nil {
			return err
		}
		if used {
			return ErrInUse
		}
		tag, err := tx.Exec(ctx, `delete from vcenters where name = $1`, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		for _, t := range []string{"inv_clusters", "inv_datastores", "inv_networks", "inv_templates", "inv_vms", "inv_storage_policies"} {
			if _, err := tx.Exec(ctx, `delete from `+t+` where vcenter = $1`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneInventory forgets the inventory of every vCenter but those named.
func (s *Store) PruneInventory(ctx context.Context, keep []string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		for _, t := range []string{"inv_clusters", "inv_datastores", "inv_networks", "inv_templates", "inv_vms", "inv_storage_policies"} {
			if _, err := tx.Exec(ctx, `delete from `+t+` where vcenter <> all($1::text[])`, keep); err != nil {
				return err
			}
		}
		return nil
	})
}

// PlacementRulesNaming counts the placement rules naming a vCenter.
func (s *Store) PlacementRulesNaming(ctx context.Context, vcenter string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from placement_rules where vcenter = $1`, vcenter).Scan(&n)
	return n, err
}

// VCenterInventory is what the inventory sync last read from a vCenter.
type VCenterInventory struct {
	Clusters int
	VMs      int
	SyncedAt *time.Time
}

func (s *Store) VCenterInventories(ctx context.Context) (map[string]VCenterInventory, error) {
	rows, err := s.Pool.Query(ctx, `select v.vcenter, coalesce(c.n, 0), coalesce(m.n, 0), greatest(c.at, m.at)
		from (select vcenter from inv_clusters union select vcenter from inv_vms) v
		left join (select vcenter, count(*) as n, max(synced_at) as at from inv_clusters group by vcenter) c using (vcenter)
		left join (select vcenter, count(*) as n, max(synced_at) as at from inv_vms group by vcenter) m using (vcenter)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]VCenterInventory{}
	for rows.Next() {
		var name string
		var inv VCenterInventory
		if err := rows.Scan(&name, &inv.Clusters, &inv.VMs, &inv.SyncedAt); err != nil {
			return nil, err
		}
		out[name] = inv
	}
	return out, rows.Err()
}
