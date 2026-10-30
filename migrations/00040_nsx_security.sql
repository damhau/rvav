-- +goose Up
-- A deployment's NSX security groups, the security policy of its firewall
-- rules, and what each VM was given to join its groups, are resources of
-- the deployment (docs/adr/0028-nsx-networking.md).
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule', 'nsx.segment', 'ipam.subnet',
                    'nsx.group', 'nsx.policy', 'nsx.membership'));

-- +goose Down
delete from resources where type in ('nsx.group', 'nsx.policy', 'nsx.membership');
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule', 'nsx.segment', 'ipam.subnet'));
