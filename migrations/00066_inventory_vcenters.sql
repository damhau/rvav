-- +goose Up
-- Each synced vCenter's host and instance UUID
-- (ServiceContent.About.InstanceUuid): a VM's link into the vSphere Client
-- names both, so the API gives the link once the sync has read them.
create table inv_vcenters (
    vcenter        text primary key,
    host           text not null,
    instance_uuid  text not null,
    synced_at      timestamptz not null default now()
);

-- +goose Down
drop table inv_vcenters;
