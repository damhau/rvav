package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ReplaceInventory swaps the cached inventory of one vCenter for a new snapshot.
func (s *Store) ReplaceInventory(ctx context.Context, inv Inventory) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		for _, t := range []string{"inv_clusters", "inv_datastores", "inv_networks", "inv_templates", "inv_vms"} {
			if _, err := tx.Exec(ctx, `delete from `+t+` where vcenter = $1`, inv.VCenter); err != nil {
				return err
			}
		}
		b := &pgx.Batch{}
		for _, c := range inv.Clusters {
			b.Queue(`insert into inv_clusters (vcenter, moref, name, datacenter, host_count, cpu_total_mhz, cpu_used_mhz,
				memory_total_mb, memory_used_mb) values ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				inv.VCenter, c.Moref, c.Name, c.Datacenter, c.HostCount, c.CPUTotalMHz, c.CPUUsedMHz, c.MemoryTotalMB, c.MemoryUsedMB)
		}
		for _, d := range inv.Datastores {
			b.Queue(`insert into inv_datastores (vcenter, moref, name, capacity_gb, free_gb, clusters) values ($1,$2,$3,$4,$5,$6)`,
				inv.VCenter, d.Moref, d.Name, d.CapacityGB, d.FreeGB, nonNil(d.Clusters))
		}
		for _, n := range inv.Networks {
			b.Queue(`insert into inv_networks (vcenter, moref, name, kind, clusters) values ($1,$2,$3,$4,$5)`,
				inv.VCenter, n.Moref, n.Name, n.Kind, nonNil(n.Clusters))
		}
		for _, t := range inv.Templates {
			b.Queue(`insert into inv_templates (vcenter, moref, name, guest_os) values ($1,$2,$3,$4)`,
				inv.VCenter, t.Moref, t.Name, t.GuestOS)
		}
		for _, v := range inv.VMs {
			b.Queue(`insert into inv_vms (vcenter, moref, instance_uuid, name, power_state, guest_state, ip_address, hostname,
				guest_os, cpu_count, memory_mb, cluster, host, cpu_pct, memory_pct, snapshot_count)
				values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
				inv.VCenter, v.Moref, v.InstanceUUID, v.Name, v.PowerState, v.GuestState, v.IPAddress, v.Hostname,
				v.GuestOS, v.CPUCount, v.MemoryMB, v.Cluster, v.Host, v.CPUPct, v.MemoryPct, v.SnapshotCount)
		}
		return tx.SendBatch(ctx, b).Close()
	})
}

func (s *Store) InventoryClusters(ctx context.Context) ([]InvCluster, error) {
	rows, _ := s.Pool.Query(ctx, `select moref, name, datacenter, host_count, cpu_total_mhz, cpu_used_mhz,
		memory_total_mb, memory_used_mb from inv_clusters order by name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvCluster])
}

func (s *Store) InventoryDatastores(ctx context.Context) ([]InvDatastore, error) {
	rows, _ := s.Pool.Query(ctx, `select moref, name, capacity_gb, free_gb, clusters from inv_datastores order by name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvDatastore])
}

func (s *Store) InventoryNetworks(ctx context.Context) ([]InvNetwork, error) {
	rows, _ := s.Pool.Query(ctx, `select moref, name, kind, clusters from inv_networks order by name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvNetwork])
}

func (s *Store) InventoryTemplates(ctx context.Context) ([]InvTemplate, error) {
	rows, _ := s.Pool.Query(ctx, `select moref, name, guest_os from inv_templates order by name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[InvTemplate])
}

const invVMCols = `moref, instance_uuid, name, power_state, guest_state, ip_address, hostname, guest_os,
	cpu_count, memory_mb, cluster, host, cpu_pct, memory_pct, snapshot_count`

// InventoryVMsByName reports which of the given VM names exist in vCenter.
func (s *Store) InventoryVMNames(ctx context.Context, prefix string) (map[string]bool, error) {
	rows, _ := s.Pool.Query(ctx, `select name from inv_vms where name like $1 || '%'
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
