-- +goose Up
-- C2.2 (ADR-0048): lease rules, tried by priority before the environment's
-- lease and a project's override, for the deployments their condition
-- matches; how long a deployment lives in all; and the rule that set a
-- deployment's lease.
create table lease_rules (
    id                text primary key,
    name              text not null,
    condition         text not null,
    environment       text references environments on delete cascade,
    project_id        text references projects on delete cascade,
    priority          int not null default 100,
    default_days      int check (default_days >= 1),
    max_days          int check (max_days >= 1),
    max_extensions    int not null default 0 check (max_extensions >= 0),
    grace_days        int not null default 7 check (grace_days >= 0),
    expiry_action     text not null default 'notify_only',
    enforcement       text not null default 'hard' check (enforcement in ('hard', 'soft')),
    max_lifetime_days int check (max_lifetime_days >= 1),
    set_by_admin      boolean not null default true,
    created_at        timestamptz not null default now()
);
alter table lease_policies add column max_lifetime_days int check (max_lifetime_days >= 1);
alter table deployments add column lease_rule_id text references lease_rules on delete set null;
create index deployments_lease_rule_idx on deployments (lease_rule_id) where lease_rule_id is not null;

-- +goose Down
drop index deployments_lease_rule_idx;
alter table deployments drop column lease_rule_id;
alter table lease_policies drop column max_lifetime_days;
drop table lease_rules;
