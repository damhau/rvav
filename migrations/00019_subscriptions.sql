-- +goose Up
-- Events and subscriptions (docs/adr/0021-event-subscriptions.md). Events
-- are written in the transaction of the change they describe; the
-- dispatcher makes one run per matching subscription and delivers it at
-- least once.
create table integration_events (
    id            text primary key,
    seq           bigserial not null unique,
    topic         text not null,
    request_id    text references requests on delete set null,
    deployment_id text references deployments on delete set null,
    -- A retried write of the same event (an activity run twice) is ignored.
    dedupe_key    text not null unique,
    payload       jsonb not null,
    created_at    timestamptz not null default now(),
    dispatched_at timestamptz
);
create index integration_events_undispatched_idx on integration_events (seq) where dispatched_at is null;
create index integration_events_created_idx on integration_events (created_at);

create table subscriptions (
    id              text primary key,
    name            text not null,
    description     text not null default '',
    topic           text not null,
    condition       text not null default '',
    blocking        boolean not null default false,
    on_failure      text not null default 'stop_request' check (on_failure in ('stop_request', 'continue_request')),
    priority        int not null default 100,
    timeout_seconds int not null default 30 check (timeout_seconds between 1 and 3600),
    enabled         boolean not null default true,
    -- One of {"webhook": {url, secret}}, {"awx": {template_id, job_template}}, {"workflow": {key}}.
    target          jsonb not null,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now(),
    check (not blocking or topic like '%.pre')
);
create index subscriptions_topic_idx on subscriptions (topic, priority);

create table subscription_runs (
    id              text primary key,
    subscription_id text not null references subscriptions on delete cascade,
    event_id        text not null references integration_events on delete cascade,
    status          text not null check (status in ('queued', 'delivering', 'delivered', 'failed', 'blocked')),
    blocking        boolean not null default false,
    test            boolean not null default false,
    request_id      text references requests on delete set null,
    step_id         text,
    attempts        int not null default 0,
    next_attempt_at timestamptz not null default now(),
    -- A run being delivered is the deliverer's until then; after, another
    -- worker takes it again (the first one died).
    lease_until     timestamptz,
    outcome         text,
    reason          text,
    properties      jsonb,
    created_at      timestamptz not null default now(),
    finished_at     timestamptz,
    unique (subscription_id, event_id)
);
create index subscription_runs_due_idx on subscription_runs (next_attempt_at) where status in ('queued', 'delivering');
create index subscription_runs_subscription_idx on subscription_runs (subscription_id, created_at desc, id desc);

create table subscription_attempts (
    id          bigserial primary key,
    run_id      text not null references subscription_runs on delete cascade,
    at          timestamptz not null default now(),
    ok          boolean not null,
    status_code int,
    job_id      int,
    workflow_id text,
    response    text,
    error       text,
    duration_ms int not null default 0
);
create index subscription_attempts_run_idx on subscription_attempts (run_id, id);

-- Set by blocking subscriptions on provisioning.pre (a CMDB ID, a cost
-- centre); shown on the deployment and passed to AWX jobs.
alter table deployments add column properties jsonb not null default '{}';

-- +goose Down
alter table deployments drop column properties;
drop table subscription_attempts;
drop table subscription_runs;
drop table subscriptions;
drop table integration_events;
