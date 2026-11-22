-- +goose Up
-- ServiceNow's CMDB (docs/adr/0065-servicenow.md): the CI rvav keeps for
-- each VM, the deployments to look at again, and the last reconcile.
create table servicenow_cis (
    instance_uuid  text primary key,
    resource_id    text not null,
    deployment_id  text not null,
    sys_id         text not null,
    class          text not null,
    -- The values last sent: a VM whose values are the same isn't sent again.
    sent           jsonb not null default '{}',
    retired_at     timestamptz,
    synced_at      timestamptz not null default now()
);
create index servicenow_cis_deployment_idx on servicenow_cis (deployment_id);

-- Written in the transaction that marks events dispatched; gen moves when
-- an event queues a deployment already queued, so a pass that read it
-- before leaves it for the next.
create table servicenow_cmdb_queue (
    deployment_id  text primary key,
    gen            bigint not null default 0,
    due_at         timestamptz not null default now(),
    attempts       int not null default 0,
    last_error     text,
    queued_at      timestamptz not null default now()
);

create table servicenow_state (
    id             boolean primary key default true check (id),
    -- Whether the CMDB is kept: the dispatcher queues nothing otherwise.
    cmdb           boolean not null default false,
    reconciled_at  timestamptz,
    report         jsonb
);
insert into servicenow_state default values;

-- +goose Down
drop table servicenow_state;
drop table servicenow_cmdb_queue;
drop table servicenow_cis;
