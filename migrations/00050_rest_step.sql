-- +goose Up
-- A composite's REST calls are steps of their own (ADR-0033).
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx', 'rest'));

-- +goose Down
update request_steps set system = 'rvav' where system = 'rest';
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx'));
