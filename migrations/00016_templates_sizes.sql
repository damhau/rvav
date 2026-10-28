-- +goose Up
-- Templates and size presets offered to projects and environments (vRA
-- image and flavour mappings; docs/adr/0020-administration.md), and where
-- they and naming policies are offered: a null project or environment means
-- all of them.
create table vm_templates (
    key              text primary key check (key ~ '^[a-z][a-z0-9-]{1,40}$'),
    label            text not null,
    vcenter_template text not null,
    os_family        text not null check (os_family in ('linux', 'windows')),
    customization    text not null default '',
    description      text not null default '',
    created_at       timestamptz not null default now()
);

create table size_presets (
    key        text primary key,
    label      text not null,
    vcpu       int not null check (vcpu between 1 and 128),
    memory_gb  int not null check (memory_gb between 1 and 2048),
    disk_gb    int not null check (disk_gb between 1 and 65536),
    created_at timestamptz not null default now()
);

create table offers (
    kind        text not null check (kind in ('template', 'size', 'naming')),
    key         text not null,
    project_id  text references projects on delete cascade,
    environment text references environments on delete cascade,
    unique nulls not distinct (kind, key, project_id, environment)
);
create index offers_kind_key on offers (kind, key);

-- +goose Down
drop table offers;
drop table size_presets;
drop table vm_templates;
