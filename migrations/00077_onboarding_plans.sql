-- +goose Up
-- Onboarding plans (X1.5): a saved filter of the inventory's VMs and where
-- what it matches goes, run by hand or on a schedule; each run says what
-- it did to each VM it matched.
alter table inv_vms add column folder text not null default '';
alter table inv_vms add column tags text[] not null default '{}';

create table onboarding_plans (
    id            text primary key,
    name          text not null unique,
    description   text not null default '',
    filter        jsonb not null default '{}',
    target        jsonb not null default '{}',
    every_hours   integer check (every_hours between 1 and 168),
    next_run_at   timestamptz,
    -- The deployment its VMs go into: the existing one it names, or the
    -- one its first run made for a deployment name.
    deployment_id text references deployments on delete set null,
    created_by    text references users on delete set null,
    updated_by    text references users on delete set null,
    created_at    timestamptz not null default now(),
    updated_at    timestamptz not null default now()
);

create table onboarding_runs (
    id          text primary key,
    plan_id     text not null references onboarding_plans on delete cascade,
    trigger     text not null check (trigger in ('manual', 'schedule')),
    started_by  text references users on delete set null,
    status      text not null default 'running' check (status in ('running', 'succeeded', 'partial', 'failed')),
    error       text not null default '',
    started_at  timestamptz not null default now(),
    finished_at timestamptz
);
create index onboarding_runs_plan_idx on onboarding_runs (plan_id, started_at desc);
-- A plan runs once at a time.
create unique index onboarding_runs_one_running on onboarding_runs (plan_id) where status = 'running';

create table onboarding_run_vms (
    run_id        text not null references onboarding_runs on delete cascade,
    vcenter       text not null,
    moref         text not null,
    name          text not null,
    outcome       text not null check (outcome in ('adopted', 'skipped', 'failed')),
    code          text not null default '',
    reason        text not null default '',
    deployment_id text references deployments on delete set null,
    primary key (run_id, vcenter, moref)
);

-- +goose Down
drop table if exists onboarding_run_vms;
drop table if exists onboarding_runs;
drop table if exists onboarding_plans;
alter table inv_vms drop column if exists tags;
alter table inv_vms drop column if exists folder;
