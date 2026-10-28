-- +goose Up
-- Custom Temporal workflows (docs/adr/0022-custom-workflows-and-composites.md).
create table workflow_registrations (
    key             text primary key,
    title           text not null,
    description     text not null default '',
    -- The Temporal workflow type, ending with its major version: decommission.v1.
    workflow_type   text not null,
    task_queue      text not null,
    input_schema    jsonb not null default '{"type": "object"}',
    timeout_seconds int not null default 2592000 check (timeout_seconds >= 60),
    -- The projects that may use it; empty for all.
    project_ids     text[] not null default '{}',
    usages          text[] not null,
    -- How it is offered as the day-2 action wf.<key>: name, types, condition, offered.
    day2            jsonb,
    enabled         boolean not null default true,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

-- Each run rvav started: what the contract's activities report into.
create table workflow_runs (
    workflow_id      text primary key,
    registration_key text not null references workflow_registrations,
    request_id       text references requests on delete cascade,
    -- The request step it runs as, and reports its steps under.
    step_key         text,
    status           text not null default 'running' check (status in ('running', 'succeeded', 'failed', 'cancelled')),
    result           jsonb,
    error            text,
    started_at       timestamptz not null default now(),
    finished_at      timestamptz
);
create index workflow_runs_registration_idx on workflow_runs (registration_key, status);
create index workflow_runs_request_idx on workflow_runs (request_id);

-- Approvals a custom workflow asked for: their decision is signalled to it,
-- not to the request's workflow, and they are not policy levels.
alter table approvals add column workflow_id text;

-- Catalog items that start a custom workflow.
alter table catalog_items drop constraint catalog_items_type_check;
alter table catalog_items add constraint catalog_items_type_check
    check (type in ('vm_blueprint', 'awx_job_template', 'awx_workflow', 'composite', 'workflow'));

-- +goose Down
alter table catalog_items drop constraint catalog_items_type_check;
alter table catalog_items add constraint catalog_items_type_check
    check (type in ('vm_blueprint', 'awx_job_template', 'awx_workflow', 'composite'));
alter table approvals drop column workflow_id;
drop table workflow_runs;
drop table workflow_registrations;
