-- +goose Up
-- rvav's processes and the settings revision each runs with (ADR-0030): a
-- process that sees newer connection settings drains, exits and is started
-- again by its supervisor; the UI shows a change applying until every
-- process runs with it.
create table processes (
    id         text primary key,
    role       text not null check (role in ('api', 'worker', 'sync', 'dev')),
    host       text not null,
    version    text not null default '',
    revision   bigint not null,
    state      text not null default 'running' check (state in ('running', 'draining')),
    started_at timestamptz not null default now(),
    seen_at    timestamptz not null default now()
);

-- +goose Down
drop table processes;
