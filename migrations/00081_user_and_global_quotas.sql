-- +goose Up
-- C2.3 (ADR-0047): what each user may own in a project, or across rvav
-- when project_id is null, and what all of rvav may hold.
create table user_quotas (
    project_id    text references projects on delete cascade,
    resource      text not null check (resource in ('vcpu', 'memory_gb', 'storage_gb', 'vm_count')),
    limit_value   int,
    enforcement   text not null default 'hard' check (enforcement in ('hard', 'soft')),
    set_by_admin  boolean not null default true,
    unique nulls not distinct (project_id, resource)
);

create table global_quotas (
    resource     text primary key check (resource in ('vcpu', 'memory_gb', 'storage_gb', 'vm_count')),
    limit_value  int,
    enforcement  text not null default 'hard' check (enforcement in ('hard', 'soft'))
);

-- +goose Down
drop table global_quotas;
drop table user_quotas;
