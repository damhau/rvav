-- +goose Up
-- Network mappings found in EfficientIP (docs/adr/0015-efficientip-client.md):
-- the sync service writes rows from subnet class parameters; rows set by an
-- administrator ('manual') win, and only get the subnet's ID filled in.
alter table network_subnets add column source text not null default 'manual' check (source in ('manual', 'efficientip'));
alter table network_subnets add column subnet_name text not null default '';
alter table network_subnets add column synced_at timestamptz;

-- +goose Down
delete from network_subnets where source = 'efficientip';
alter table network_subnets drop column if exists synced_at, drop column if exists subnet_name, drop column if exists source;
