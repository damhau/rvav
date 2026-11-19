-- +goose Up
-- Audit events sent to syslog servers and webhooks (docs/adr/0058-audit-forwarding-and-sign-ins.md).
create table audit_destinations (
    id              text primary key,
    name            text not null unique,
    enabled         boolean not null default true,
    kind            text not null check (kind in ('syslog', 'webhook')),
    -- Empty: every category.
    categories      text[] not null default '{}',
    -- The api.AuditSyslog or api.AuditWebhook it was saved with.
    settings        jsonb not null,
    failures        integer not null default 0,
    next_attempt_at timestamptz not null default now(),
    lease_until     timestamptz,
    delivered       bigint not null default 0,
    last_success_at timestamptz,
    last_error      text,
    last_error_at   timestamptz,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

-- What waits to be sent, written with the event in the audited action's
-- transaction: nothing is lost, and sending never holds the action up.
create table audit_deliveries (
    destination_id text not null references audit_destinations on delete cascade,
    event_id       bigint not null references audit_events on delete cascade,
    queued_at      timestamptz not null default now(),
    primary key (destination_id, event_id)
);

-- An expired API token's use is audited once a day, as its first use is.
alter table api_tokens add column last_refused_at timestamptz;

-- +goose Down
alter table api_tokens drop column last_refused_at;
drop table audit_deliveries;
drop table audit_destinations;
