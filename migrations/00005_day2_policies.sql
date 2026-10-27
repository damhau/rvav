-- +goose Up
-- Day-2 policies (docs/adr/0013-day2-actions.md): by priority, the first
-- policy whose actions, roles and condition match decides: allow, deny
-- (reason shown to the user) or approval (levels as approval policies).
-- Nothing matching denies.
create table day2_policies (
    id          text primary key,
    name        text not null,
    reason      text not null default '',
    priority    int not null default 100,
    -- Action key patterns: delete, snapshot_*, *.
    actions     text[] not null default '{*}',
    -- owner, viewer, member, project-admin, admin; empty: anyone.
    roles       text[] not null default '{}',
    -- CEL over action, resource, deployment, environment, user and inputs.
    condition   text not null default 'true',
    effect      text not null check (effect in ('allow', 'deny', 'approval')),
    levels      jsonb not null default '[]',
    project_id  text references projects on delete cascade,
    enabled     boolean not null default true
);

-- +goose Down
drop table if exists day2_policies;
