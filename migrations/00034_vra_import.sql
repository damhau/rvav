-- +goose Up
-- The vRA importer (ADR-0027, P2.5): what was read from vRA, the vRA
-- projects an administrator mapped to rvav's, and what each vRA object
-- became, so the import can run again.
create table vra_snapshots (
    id          text primary key,
    read_at     timestamptz not null,
    source      text not null check (source in ('vra', 'file')),
    url         text not null default '',
    estate      jsonb not null,
    counts      jsonb not null default '{}',
    created_at  timestamptz not null default now(),
    created_by  text references users
);

create table vra_project_map (
    vra_project_id   text primary key,
    rvav_project_id  text not null references projects on delete cascade,
    updated_at       timestamptz not null default now(),
    updated_by       text references users
);

-- kind: template (a cloud template → a catalog item), deployment (→ the
-- deployments it became). source_hash: what the vRA object mapped to when
-- it was last imported.
create table vra_links (
    kind         text not null check (kind in ('template', 'deployment')),
    vra_id       text not null,
    rvav_id      text not null,
    vra_version  text not null default '',
    source_hash  text not null default '',
    imported_at  timestamptz not null default now(),
    imported_by  text references users,
    primary key (kind, vra_id, rvav_id)
);

-- +goose Down
drop table vra_links;
drop table vra_project_map;
drop table vra_snapshots;
