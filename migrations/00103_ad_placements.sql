-- +goose Up
-- Where VMs' computer objects go in Active Directory
-- (docs/adr/0064-active-directory-computer-objects.md): a row for a
-- project, an environment, both or neither; the most specific applies.
-- ou and groups are DNs as given, maybe relative to the base DN.
create table ad_placements (
    id                  text primary key,
    project_id          text references projects on delete cascade,
    environment         text references environments on delete cascade,
    ou                  text not null,
    groups              text[] not null default '{}',
    allow_blueprint_ou  boolean not null default false,
    updated_at          timestamptz not null default now()
);
create unique index ad_placements_scope_idx on ad_placements (coalesce(project_id, ''), coalesce(environment, ''));

-- A VM's computer object, and the steps that make and delete it.
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule', 'nsx.segment', 'ipam.subnet',
                    'nsx.group', 'nsx.policy', 'nsx.membership', 'guest.access', 'custom', 'ad.computer'));
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check
    check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx', 'rest', 'person', 'wait', 'branch', 'ad'));

-- +goose Down
delete from request_steps where system = 'ad';
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check
    check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx', 'rest', 'person', 'wait', 'branch'));
delete from resources where type = 'ad.computer';
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule', 'nsx.segment', 'ipam.subnet',
                    'nsx.group', 'nsx.policy', 'nsx.membership', 'guest.access', 'custom'));
drop table ad_placements;
