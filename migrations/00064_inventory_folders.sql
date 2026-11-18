-- +goose Up
-- The VM folders the inventory sync reads, by path from the root such as
-- /GVA/vm/rvav/web: a vsphere.folders field offers those of the
-- datacenters its project's placement allows.
create table inv_folders (
    vcenter     text not null,
    moref       text not null,
    path        text not null,
    datacenter  text not null,
    synced_at   timestamptz not null default now(),
    primary key (vcenter, moref)
);

-- +goose Down
drop table inv_folders;
