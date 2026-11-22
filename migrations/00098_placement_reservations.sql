-- +goose Up
-- What a VM a request places holds on its cluster until it exists
-- (ADR-0036), so that requests placed meanwhile count it. A row counts
-- while its request waits or runs, until expires_at, and until its VM is
-- in the inventory or recorded as its deployment's resource.
create table placement_reservations (
    request_id     text not null references requests on delete cascade,
    vm_name        text not null,
    deployment_id  text not null,
    vcenter        text not null,
    cluster        text not null,
    resource_pool  text not null default '',
    memory_mb      bigint not null,
    vcpu           integer not null,
    expires_at     timestamptz not null,
    primary key (request_id, vm_name)
);
create index placement_reservations_cluster on placement_reservations (vcenter, cluster);

-- +goose Down
drop table placement_reservations;
