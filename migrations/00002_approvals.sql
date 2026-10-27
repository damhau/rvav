-- +goose Up
-- Approvals (docs/adr/0011-approvals.md): one row per level of a request,
-- the votes on it, per-level delegates, the groups seen in users' tokens,
-- and the audit log.

-- CEL: when true, the policy is satisfied without anyone approving.
alter table approval_policies add column auto_approve text;

create table approvals (
    id              text primary key,
    request_id      text not null references requests on delete cascade,
    seq             int not null,
    name            text not null,
    group_name      text,
    approver_ids    text[] not null default '{}',
    mode            text not null default 'any' check (mode in ('any', 'all')),
    policy_ids      text[] not null default '{}',
    reasons         text[] not null default '{}',
    rules           text[] not null default '{}',
    status          text not null check (status in ('waiting', 'pending', 'approved', 'rejected', 'cancelled')),
    -- Seconds the level may stay pending; null for no timeout.
    timeout_seconds int,
    on_timeout      text check (on_timeout in ('approve', 'reject', 'escalate')),
    escalate_group  text,
    escalate_ids    text[] not null default '{}',
    escalated       boolean not null default false,
    created_at      timestamptz not null default now(),
    opened_at       timestamptz,
    due_at          timestamptz,
    decided_at      timestamptz,
    -- How it ended, for people: "Approved by Marc Keller".
    outcome         text,
    unique (request_id, seq)
);
create index approvals_status_idx on approvals (status, opened_at);

create table approval_votes (
    approval_id  text not null references approvals on delete cascade,
    user_id      text not null references users,
    decision     text not null check (decision in ('approve', 'reject')),
    comment      text not null default '',
    -- [{field, label, from, to}] for inputs the approver changed.
    changes      jsonb not null default '[]',
    decided_at   timestamptz not null default now(),
    primary key (approval_id, user_id)
);

create table approval_delegates (
    approval_id   text not null references approvals on delete cascade,
    user_id       text not null references users,
    delegated_by  text not null references users,
    comment       text not null default '',
    created_at    timestamptz not null default now(),
    primary key (approval_id, user_id)
);

-- OIDC groups from each user's last token, so approvers in a group are known
-- before they sign in again.
create table user_groups (
    user_id     text not null references users on delete cascade,
    group_name  text not null,
    seen_at     timestamptz not null default now(),
    primary key (user_id, group_name)
);
create index user_groups_group_idx on user_groups (group_name);

-- Append-only: who changed what. Read through the API in S9.
create table audit_events (
    id           bigserial primary key,
    at           timestamptz not null default now(),
    -- Null when rvav itself acted (a timeout, an auto-approval).
    actor_id     text references users,
    action       text not null,
    target_type  text not null,
    target_id    text not null,
    project_id   text,
    detail       jsonb not null default '{}'
);
create index audit_events_target_idx on audit_events (target_type, target_id, at);

-- +goose Down
drop table if exists audit_events, user_groups, approval_delegates, approval_votes, approvals;
alter table approval_policies drop column if exists auto_approve;
