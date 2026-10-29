-- +goose Up
-- Content library templates and storage policies
-- (docs/adr/0024-multi-vm-blueprints.md): the inventory sync reads the
-- libraries' VM template and OVF items (as templates of their library)
-- and the storage policies; placement rules may name a storage policy and
-- the templates catalogue a content library.
alter table inv_templates add column library text not null default '';
create table inv_storage_policies (
    vcenter    text not null,
    id         text not null,
    name       text not null,
    synced_at  timestamptz not null default now(),
    primary key (vcenter, id)
);
alter table placement_rules add column storage_policy text not null default '';
alter table vm_templates add column content_library text not null default '';

-- +goose Down
alter table vm_templates drop column content_library;
alter table placement_rules drop column storage_policy;
drop table inv_storage_policies;
alter table inv_templates drop column library;
