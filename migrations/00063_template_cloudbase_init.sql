-- +goose Up
-- A Windows template with cloudbase-init: its VMs get their name,
-- addresses, user and user data through guestinfo (ADR-0039).
alter table vm_templates add column cloudbase_init boolean not null default false;

-- +goose Down
alter table vm_templates drop column cloudbase_init;
