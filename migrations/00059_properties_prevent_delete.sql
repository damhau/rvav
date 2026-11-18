-- +goose Up
-- A project's custom properties, written to its VMs as vSphere custom
-- attributes and read by bindings as project.properties; a VM whose
-- blueprint says prevent_delete isn't deleted until an administrator
-- allows it (ADR-0038).
alter table projects add column properties jsonb not null default '{}';
alter table resources add column prevent_delete boolean not null default false;

-- +goose Down
alter table resources drop column prevent_delete;
alter table projects drop column properties;
