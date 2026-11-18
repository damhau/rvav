-- +goose Up
-- A network's IPv6 subnet in EfficientIP, for VMs given an IPv6 address
-- beside their IPv4 one (ADR-0039).
alter table network_subnets add column subnet6 text not null default '';
alter table network_subnets add column gateway6 text not null default '';
alter table network_subnets add column efficientip_subnet6_id text not null default '';

-- +goose Down
alter table network_subnets drop column efficientip_subnet6_id;
alter table network_subnets drop column gateway6;
alter table network_subnets drop column subnet6;
