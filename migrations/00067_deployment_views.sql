-- +goose Up
-- The deployments list filters by catalog item, owner, lease and VM
-- template; the Resources page by a VM's vCenter, cluster and power state.
create index deployments_item_idx on deployments (item_id);
create index deployments_owner_idx on deployments (owner_id);
create index deployments_lease_idx on deployments (lease_expires_at) where lease_expires_at is not null;
create index resources_vm_template_idx on resources ((properties->'vm'->>'template')) where type = 'vsphere.vm';
create index resources_vm_placement_idx on resources ((properties->'vm'->>'vcenter'), (properties->'vm'->>'cluster'))
    where type = 'vsphere.vm';
create index resources_vm_power_idx on resources ((properties->'vm'->>'power_state')) where type = 'vsphere.vm';

-- A list's filters, sort and layout saved under a name: its owner's only
-- (shared by sending the list's address instead).
create table saved_views (
    id          text primary key,
    user_id     text not null references users on delete cascade,
    list        text not null check (list in ('deployments', 'resources')),
    name        text not null,
    query       jsonb not null default '{}',
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now(),
    unique (user_id, list, name)
);

-- +goose Down
drop table saved_views;
drop index resources_vm_power_idx;
drop index resources_vm_placement_idx;
drop index resources_vm_template_idx;
drop index deployments_lease_idx;
drop index deployments_owner_idx;
drop index deployments_item_idx;
