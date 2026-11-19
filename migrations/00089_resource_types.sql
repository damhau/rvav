-- +goose Up
-- Custom resource types (ADR-0056): a custom workflow or an AWX template
-- creates a type's resources and another deletes them; one may update
-- them and one read them again. lifecycle holds them by step:
-- {"create": {"kind": "workflow", "name": "lb-create"}, "delete": ...}.
create table resource_types (
    key              text primary key,
    title            text not null,
    description      text not null default '',
    lifecycle        jsonb not null,
    property_schema  jsonb not null default '{"type": "object"}',
    actions          text[] not null default '{}',
    enabled          boolean not null default true,
    created_at       timestamptz not null default now(),
    updated_at       timestamptz not null default now()
);

-- A custom resource's properties: {"resource_type": {"key", "title"},
-- "key": its blueprint key, "inputs", "properties"}.
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule', 'nsx.segment', 'ipam.subnet',
                    'nsx.group', 'nsx.policy', 'nsx.membership', 'guest.access', 'custom'));

-- +goose Down
delete from resources where type = 'custom';
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule', 'nsx.segment', 'ipam.subnet',
                    'nsx.group', 'nsx.policy', 'nsx.membership', 'guest.access'));
drop table resource_types;
