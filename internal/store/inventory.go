package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// inventoryTables hold what the inventory sync reads of each vCenter.
var inventoryTables = []string{"inv_clusters", "inv_datastores", "inv_networks", "inv_templates", "inv_vms", "inv_storage_policies",
	"inv_hosts", "inv_resource_pools", "inv_datastore_clusters", "inv_folders", "inv_vcenters", "inv_tag_categories"}

// ReplaceInventory swaps the cached inventory of one vCenter for a new snapshot.
func (s *Store) ReplaceInventory(ctx context.Context, inv Inventory) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		for _, t := range inventoryTables {
			if _, err := tx.Exec(ctx, `delete from `+t+` where vcenter = $1`, inv.VCenter); err != nil {
				return err
			}
		}
		b := &pgx.Batch{}
		if inv.InstanceUUID != "" {
			b.Queue(`insert into inv_vcenters (vcenter, host, instance_uuid) values ($1,$2,$3)`, inv.VCenter, inv.Host, inv.InstanceUUID)
		}
		for _, c := range inv.Clusters {
			b.Queue(`insert into inv_clusters (vcenter, moref, name, datacenter, host_count, cpu_total_mhz, cpu_used_mhz,
				memory_total_mb, memory_used_mb, tags) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				inv.VCenter, c.Moref, c.Name, c.Datacenter, c.HostCount, c.CPUTotalMHz, c.CPUUsedMHz, c.MemoryTotalMB, c.MemoryUsedMB,
				nonNil(c.Tags))
		}
		for _, d := range inv.Datastores {
			b.Queue(`insert into inv_datastores (vcenter, moref, name, capacity_gb, free_gb, clusters, tags) values ($1,$2,$3,$4,$5,$6,$7)`,
				inv.VCenter, d.Moref, d.Name, d.CapacityGB, d.FreeGB, nonNil(d.Clusters), nonNil(d.Tags))
		}
		for _, n := range inv.Networks {
			b.Queue(`insert into inv_networks (vcenter, moref, name, kind, clusters, tags) values ($1,$2,$3,$4,$5,$6)`,
				inv.VCenter, n.Moref, n.Name, n.Kind, nonNil(n.Clusters), nonNil(n.Tags))
		}
		for _, h := range inv.Hosts {
			b.Queue(`insert into inv_hosts (vcenter, moref, name, cluster, state, cpu_total_mhz, cpu_used_mhz, memory_total_mb,
				memory_used_mb, tags) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				inv.VCenter, h.Moref, h.Name, h.Cluster, h.State, h.CPUTotalMHz, h.CPUUsedMHz, h.MemoryTotalMB, h.MemoryUsedMB, nonNil(h.Tags))
		}
		for _, p := range inv.ResourcePools {
			b.Queue(`insert into inv_resource_pools (vcenter, moref, name, cluster, path, cpu_limit_mhz, memory_limit_mb, tags)
				values ($1,$2,$3,$4,$5,$6,$7,$8)`,
				inv.VCenter, p.Moref, p.Name, p.Cluster, p.Path, p.CPULimitMHz, p.MemoryLimitMB, nonNil(p.Tags))
		}
		for _, d := range inv.DatastoreClusters {
			b.Queue(`insert into inv_datastore_clusters (vcenter, moref, name, capacity_gb, free_gb, datastores, sdrs, tags)
				values ($1,$2,$3,$4,$5,$6,$7,$8)`,
				inv.VCenter, d.Moref, d.Name, d.CapacityGB, d.FreeGB, nonNil(d.Datastores), d.SDRS, nonNil(d.Tags))
		}
		for _, f := range inv.Folders {
			b.Queue(`insert into inv_folders (vcenter, moref, path, datacenter) values ($1,$2,$3,$4)`,
				inv.VCenter, f.Moref, f.Path, f.Datacenter)
		}
		for _, t := range inv.Templates {
			b.Queue(`insert into inv_templates (vcenter, moref, name, guest_os, library) values ($1,$2,$3,$4,$5)`,
				inv.VCenter, t.Moref, t.Name, t.GuestOS, t.Library)
		}
		for _, p := range inv.StoragePolicies {
			b.Queue(`insert into inv_storage_policies (vcenter, id, name) values ($1,$2,$3)`, inv.VCenter, p.ID, p.Name)
		}
		for _, c := range inv.TagCategories {
			b.Queue(`insert into inv_tag_categories (vcenter, name, single, tags) values ($1,$2,$3,$4)`,
				inv.VCenter, c.Name, c.Single, nonNil(c.Tags))
		}
		for _, v := range inv.VMs {
			b.Queue(`insert into inv_vms (vcenter, moref, instance_uuid, name, power_state, guest_state, ip_address, hostname,
				guest_os, cpu_count, memory_mb, cluster, host, cpu_pct, memory_pct, snapshot_count, folder, tags)
				values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
				inv.VCenter, v.Moref, v.InstanceUUID, v.Name, v.PowerState, v.GuestState, v.IPAddress, v.Hostname,
				v.GuestOS, v.CPUCount, v.MemoryMB, v.Cluster, v.Host, v.CPUPct, v.MemoryPct, v.SnapshotCount, v.Folder, nonNil(v.Tags))
		}
		return tx.SendBatch(ctx, b).Close()
	})
}

func (s *Store) InventoryClusters(ctx context.Context) ([]InvCluster, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, moref, name, datacenter, host_count, cpu_total_mhz, cpu_used_mhz,
		memory_total_mb, memory_used_mb, tags from inv_clusters order by name, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvCluster])
}

func (s *Store) InventoryDatastores(ctx context.Context) ([]InvDatastore, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, moref, name, capacity_gb, free_gb, clusters, tags from inv_datastores order by name, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvDatastore])
}

func (s *Store) InventoryNetworks(ctx context.Context) ([]InvNetwork, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, moref, name, kind, clusters, tags from inv_networks order by name, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvNetwork])
}

func (s *Store) InventoryESXiHosts(ctx context.Context) ([]InvHost, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, moref, name, cluster, state, cpu_total_mhz, cpu_used_mhz, memory_total_mb,
		memory_used_mb, tags from inv_hosts order by cluster, name, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvHost])
}

func (s *Store) InventoryResourcePools(ctx context.Context) ([]InvResourcePool, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, moref, name, cluster, path, cpu_limit_mhz, memory_limit_mb, tags
		from inv_resource_pools order by cluster, path, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvResourcePool])
}

// InventoryFolders are the VM folders of every vCenter, by path.
func (s *Store) InventoryFolders(ctx context.Context) ([]InvFolder, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, moref, path, datacenter from inv_folders order by path, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvFolder])
}

func (s *Store) InventoryDatastoreClusters(ctx context.Context) ([]InvDatastoreCluster, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, moref, name, capacity_gb, free_gb, datastores, sdrs, tags
		from inv_datastore_clusters order by name, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvDatastoreCluster])
}

// RvavVMsPerCluster counts the VMs of deployments not deleted, by
// "vcenter/cluster" (ADR-0036).
func (s *Store) RvavVMsPerCluster(ctx context.Context) (map[string]int, error) {
	rows, err := s.Pool.Query(ctx, `select coalesce(r.properties->'vm'->>'vcenter', '') || '/' || coalesce(r.properties->'vm'->>'cluster', ''),
		count(*) from resources r join deployments d on d.id = r.deployment_id
		where r.type = 'vsphere.vm' and d.status not in ('deleting', 'deleted') group by 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, rows.Err()
}

func (s *Store) InventoryTemplates(ctx context.Context) ([]InvTemplate, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, moref, name, guest_os, library from inv_templates order by library, name, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvTemplate])
}

// InventoryTemplatesOf and InventoryStoragePoliciesOf are one vCenter's.
func (s *Store) InventoryTemplatesOf(ctx context.Context, vcenter string) ([]InvTemplate, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, moref, name, guest_os, library from inv_templates where vcenter = $1`, vcenter)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvTemplate])
}

func (s *Store) InventoryStoragePoliciesOf(ctx context.Context, vcenter string) ([]InvStoragePolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, id, name from inv_storage_policies where vcenter = $1`, vcenter)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvStoragePolicy])
}

// InventoryTagCategories are every vCenter's tag categories for VMs, and
// InventoryTagCategoriesOf one vCenter's.
func (s *Store) InventoryTagCategories(ctx context.Context) ([]InvTagCategory, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, name, single, tags from inv_tag_categories order by name, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvTagCategory])
}

func (s *Store) InventoryTagCategoriesOf(ctx context.Context, vcenter string) ([]InvTagCategory, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, name, single, tags from inv_tag_categories where vcenter = $1 order by name`, vcenter)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvTagCategory])
}

func (s *Store) InventoryStoragePolicies(ctx context.Context) ([]InvStoragePolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select vcenter, id, name from inv_storage_policies order by name, vcenter`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvStoragePolicy])
}

const invVMCols = `moref, instance_uuid, name, power_state, guest_state, ip_address, hostname, guest_os,
	cpu_count, memory_mb, cluster, host, cpu_pct, memory_pct, snapshot_count, folder, tags`

// InventoryVMsByName reports which of the given VM names exist in vCenter.
func (s *Store) InventoryVMNames(ctx context.Context, db DB, prefix string) (map[string]bool, error) {
	rows, _ := db.Query(ctx, `select name from inv_vms where name like $1 || '%'
		union select name from resources where type = 'vsphere.vm' and name like $1 || '%'`, prefix)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out, nil
}

func (s *Store) InventoryVMs(ctx context.Context, vcenter string) ([]InvVM, error) {
	rows, _ := s.Pool.Query(ctx, `select `+invVMCols+` from inv_vms where vcenter = $1`, vcenter)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvVM])
}

// UpsertInventoryVM stores one VM of a vCenter's inventory as a change
// notification says it is now; its quick stats and tags stay those of the
// last full sync.
func (s *Store) UpsertInventoryVM(ctx context.Context, vcenter string, v InvVM) error {
	_, err := s.Pool.Exec(ctx, `insert into inv_vms (vcenter, moref, instance_uuid, name, power_state, guest_state, ip_address,
		hostname, guest_os, cpu_count, memory_mb, cluster, host, snapshot_count, folder, synced_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15, now())
		on conflict (vcenter, moref) do update set instance_uuid = excluded.instance_uuid, name = excluded.name,
		power_state = excluded.power_state, guest_state = excluded.guest_state, ip_address = excluded.ip_address,
		hostname = excluded.hostname, guest_os = excluded.guest_os, cpu_count = excluded.cpu_count, memory_mb = excluded.memory_mb,
		cluster = excluded.cluster, host = excluded.host, snapshot_count = excluded.snapshot_count, folder = excluded.folder,
		synced_at = now()`,
		vcenter, v.Moref, v.InstanceUUID, v.Name, v.PowerState, v.GuestState, v.IPAddress, v.Hostname, v.GuestOS, v.CPUCount,
		v.MemoryMB, v.Cluster, v.Host, v.SnapshotCount, v.Folder)
	return err
}

// DeleteInventoryVM forgets a VM vCenter no longer has, and says which it
// was ("" when it wasn't known).
func (s *Store) DeleteInventoryVM(ctx context.Context, vcenter, moref string) (instanceUUID string, err error) {
	err = s.Pool.QueryRow(ctx, `delete from inv_vms where vcenter = $1 and moref = $2 returning instance_uuid`, vcenter, moref).
		Scan(&instanceUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return instanceUUID, err
}
