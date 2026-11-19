-- +goose Up
-- Editing a VM's vSphere tags (X1.3): an environment names the tag
-- categories its VMs' users may change; the inventory sync reads every
-- vCenter's categories for VMs with their tags, which the Edit tags form
-- offers. Single categories take one tag per VM.
alter table environments add column tag_categories text[] not null default '{}';
create table inv_tag_categories (
    vcenter    text not null,
    name       text not null,
    single     boolean not null,
    tags       text[] not null,
    synced_at  timestamptz not null default now(),
    primary key (vcenter, name)
);

-- +goose Down
drop table inv_tag_categories;
alter table environments drop column tag_categories;
