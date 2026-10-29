-- +goose Up
-- DRS rules keeping a blueprint component's VMs on separate hosts are
-- resources of their deployment (docs/adr/0024-multi-vm-blueprints.md).
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule'));

-- +goose Down
delete from resources where type = 'vsphere.drs_rule';
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record'));
