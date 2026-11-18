-- +goose Up
-- Placement targets and strategies (docs/adr/0036-placement-targets.md):
-- the hosts, resource pools and datastore clusters the inventory sync
-- reads, with their vSphere tags, and what a placement rule names of them.
create table inv_hosts (
    vcenter          text not null,
    moref            text not null,
    name             text not null,
    cluster          text not null default '',
    state            text not null default 'connected',
    cpu_total_mhz    bigint not null default 0,
    cpu_used_mhz     bigint not null default 0,
    memory_total_mb  bigint not null default 0,
    memory_used_mb   bigint not null default 0,
    tags             text[] not null default '{}',
    synced_at        timestamptz not null default now(),
    primary key (vcenter, moref)
);

create table inv_resource_pools (
    vcenter          text not null,
    moref            text not null,
    name             text not null,
    cluster          text not null,
    path             text not null,
    cpu_limit_mhz    bigint not null default -1,
    memory_limit_mb  bigint not null default -1,
    tags             text[] not null default '{}',
    synced_at        timestamptz not null default now(),
    primary key (vcenter, moref)
);

create table inv_datastore_clusters (
    vcenter      text not null,
    moref        text not null,
    name         text not null,
    capacity_gb  bigint not null default 0,
    free_gb      bigint not null default 0,
    datastores   text[] not null default '{}',
    sdrs         boolean not null default false,
    tags         text[] not null default '{}',
    synced_at    timestamptz not null default now(),
    primary key (vcenter, moref)
);

alter table placement_rules
    add column resource_pool text not null default '',
    add column host_patterns text[] not null default '{}',
    add column datastore_cluster text not null default '',
    add column strategy text not null default 'most_free_memory'
        check (strategy in ('most_free_memory', 'binpack', 'spread'));

-- +goose Down
alter table placement_rules drop column strategy, drop column datastore_cluster, drop column host_patterns,
    drop column resource_pool;
drop table inv_datastore_clusters;
drop table inv_resource_pools;
drop table inv_hosts;
