-- +goose Up
-- Notifications (docs/adr/0023-notifications.md): kept for the app's
-- notification centre, sent on the channels each user chose.
create table notifications (
    id            text primary key,
    user_id       text not null references users on delete cascade,
    kind          text not null,
    title         text not null,
    body          text not null default '',
    level         text not null default 'info',
    link          text not null default '',
    request_id    text references requests on delete set null,
    deployment_id text references deployments on delete set null,
    approval_id   text references approvals on delete set null,
    -- What the templates need beyond the request and deployment.
    data          jsonb not null default '{}',
    -- One notification per occasion: retries and reruns make no second.
    dedupe_key    text,
    created_at    timestamptz not null default now(),
    read_at       timestamptz,
    unique (user_id, dedupe_key)
);
create index notifications_user_idx on notifications (user_id, created_at desc, id desc);
create index notifications_unread_idx on notifications (user_id) where read_at is null;

-- One delivery per notification and channel, sent by the notifier.
create table notification_deliveries (
    id              bigserial primary key,
    notification_id text not null references notifications on delete cascade,
    channel         text not null check (channel in ('email', 'teams', 'slack')),
    status          text not null default 'queued' check (status in ('queued', 'sending', 'sent', 'failed')),
    attempts        int not null default 0,
    next_attempt_at timestamptz not null default now(),
    lease_until     timestamptz,
    error           text,
    -- Where it went: the address, or the Teams or Slack message.
    sent_to         text,
    external_id     text,
    created_at      timestamptz not null default now(),
    sent_at         timestamptz,
    unique (notification_id, channel)
);
create index notification_deliveries_due_idx on notification_deliveries (next_attempt_at) where status in ('queued', 'sending');

-- Each user's channels per kind and language; none: the defaults.
create table notification_preferences (
    user_id    text primary key references users on delete cascade,
    locale     text not null default 'en' check (locale in ('en', 'fr')),
    -- {"<kind>": ["email", ...]}: kinds not listed keep the defaults.
    rules      jsonb not null default '{}',
    updated_at timestamptz not null default now()
);

-- +goose Down
drop table notification_preferences;
drop table notification_deliveries;
drop table notifications;
