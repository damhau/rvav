-- +goose Up
-- Property groups (ADR-0038): reusable inputs and constants an item's
-- blueprint names at a version. A version never changes; saving one adds
-- the next.
create table property_groups (
    key          text not null,
    version      integer not null check (version >= 1),
    name         text not null,
    description  text not null default '',
    inputs       jsonb not null default '[]',
    constants    jsonb not null default '{}',
    created_by   text references users on delete set null,
    created_at   timestamptz not null default now(),
    primary key (key, version)
);

-- +goose Down
drop table if exists property_groups;
