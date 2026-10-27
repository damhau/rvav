-- +goose Up
-- Day-2 actions backed by AWX templates (S6, docs/adr/0018-awx-day2-actions.md):
-- the sync service keeps the template, its form and warnings as AWX has
-- them; administrators choose the resource types, the condition, the name
-- and whether it is offered.
create table awx_actions (
    key          text primary key,
    awx_id       int not null,
    workflow     boolean not null,
    template     text not null,
    name         text not null,
    description  text not null default '',
    types        text[] not null default '{vsphere.vm}',
    condition    text not null default '',
    enabled      boolean not null default true,
    form_schema  jsonb not null,
    ui_schema    jsonb not null,
    initial      jsonb not null default '{}',
    warnings     text[] not null default '{}',
    gone         boolean not null default false,
    synced_at    timestamptz not null default now(),
    updated_at   timestamptz not null default now(),
    unique (awx_id, workflow)
);

-- +goose Down
drop table if exists awx_actions;
