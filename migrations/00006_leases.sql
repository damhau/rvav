-- +goose Up
-- Leases (docs/adr/0014-leases-and-quotas.md): a policy per environment,
-- which a project may override, with a grace period before deletion; and
-- what housekeeping has done about a deployment's lease.
alter table lease_policies drop constraint lease_policies_pkey;
alter table lease_policies add column project_id text references projects on delete cascade;
alter table lease_policies add column grace_days int not null default 7 check (grace_days >= 0);
create unique index lease_policies_scope_idx on lease_policies (environment, (coalesce(project_id, '')));

-- Reminders sent for the current lease ('7d', '1d'), and when its expiry
-- powered the VMs off (the grace period counts from then).
alter table deployments add column lease_reminded text[] not null default '{}';
alter table deployments add column lease_expired_at timestamptz;

-- +goose Down
alter table deployments drop column if exists lease_expired_at, drop column if exists lease_reminded;
drop index if exists lease_policies_scope_idx;
delete from lease_policies where project_id is not null;
alter table lease_policies drop column if exists grace_days, drop column if exists project_id;
alter table lease_policies add primary key (environment);
