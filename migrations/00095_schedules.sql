-- +goose Up
-- Recurring schedules (ADR-0057): an item that makes no deployment, run as
-- its owner's request on a cron expression or every so many minutes. A
-- Temporal schedule fires each run; schedule_runs keeps what each firing
-- did, its fire_id making a firing tried again submit once.
create table schedules (
    id             text primary key,
    name           text not null,
    item_id        text not null references catalog_items on delete cascade,
    version_id     text not null references catalog_item_versions on delete cascade,
    project_id     text not null references projects on delete cascade,
    environment    text not null references environments on delete cascade,
    owner_id       text not null references users,
    inputs         jsonb not null default '{}',
    justification  text not null default '',
    accept_warnings boolean not null default false,
    cron           text,
    every_minutes  int check (every_minutes between 5 and 44640),
    timezone       text not null default 'UTC',
    paused         boolean not null default false,
    paused_note    text not null default '',
    created_at     timestamptz not null default now(),
    updated_at     timestamptz not null default now(),
    deleted_at     timestamptz,
    check ((cron is null) <> (every_minutes is null))
);
create index schedules_by_project on schedules (project_id) where deleted_at is null;

create table schedule_runs (
    fire_id      text primary key,
    schedule_id  text not null references schedules on delete cascade,
    at           timestamptz not null,
    request_id   text references requests on delete cascade,
    outcome      text not null check (outcome in ('started', 'refused', 'skipped', 'failed')),
    reason       text not null default ''
);
create index schedule_runs_by_schedule on schedule_runs (schedule_id, at desc);

alter table requests add column schedule_id text references schedules;

-- +goose Down
alter table requests drop column schedule_id;
drop table schedule_runs;
drop table schedules;
