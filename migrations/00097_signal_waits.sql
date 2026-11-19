-- +goose Up
-- A composite step waiting for a signal (ADR-0057): a call to the request's
-- signal/<name>, or an event of a topic. What came is kept until the
-- workflow reads it, so a lost Temporal signal costs only a recheck.
create table signal_waits (
    request_id   text not null references requests on delete cascade,
    step_key     text not null,
    name         text,
    topic        text,
    condition    text not null default '',
    waiting      jsonb not null default '{}',
    after_seq    bigint not null default 0,
    due_at       timestamptz not null,
    payload      jsonb,
    received_at  timestamptz,
    closed_at    timestamptz,
    created_at   timestamptz not null default now(),
    primary key (request_id, step_key),
    check ((name is null) <> (topic is null))
);
create unique index signal_waits_open_name on signal_waits (request_id, name)
    where name is not null and received_at is null and closed_at is null;
create index signal_waits_open_topic on signal_waits (topic)
    where topic is not null and received_at is null and closed_at is null;

-- +goose Down
drop table signal_waits;
