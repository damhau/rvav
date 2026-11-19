-- +goose Up
-- Shared variables (ADR-0057), as vRO's configuration elements: named,
-- non-secret values catalog admins keep, which composite steps and
-- conditions read as vars.<group>.<name> each time they run.
create table variables (
    group_key    text not null,
    name         text not null,
    value        jsonb not null,
    description  text not null default '',
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now(),
    updated_by   text references users on delete set null,
    primary key (group_key, name)
);

-- +goose Down
drop table variables;
