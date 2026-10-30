-- +goose Up
-- The subnet of an NSX segment rvav made for a deployment (ADR-0028),
-- mapped to its port group by rvav itself: the EfficientIP sync leaves
-- these alone, and deleting the segment removes its mapping.
alter table network_subnets drop constraint network_subnets_source_check;
alter table network_subnets add constraint network_subnets_source_check check (source in ('manual', 'efficientip', 'nsx'));
-- The segment and its subnet are resources of the deployment.
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule', 'nsx.segment', 'ipam.subnet'));
-- Its steps in a request's timeline are NSX's.
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx'));

-- +goose Down
delete from resources where type in ('nsx.segment', 'ipam.subnet');
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule'));
update request_steps set system = 'rvav' where system = 'nsx';
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx'));
delete from network_subnets where source = 'nsx';
alter table network_subnets drop constraint network_subnets_source_check;
alter table network_subnets add constraint network_subnets_source_check check (source in ('manual', 'efficientip'));
